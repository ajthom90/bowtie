import AVFoundation
import XCTest
import BowtieKit
@testable import Bowtie

/// Stand-in for a SharePlay `GroupSession` (which tests can't create).
@MainActor
final class FakeWatchGroup: WatchGroup {
    var activity: WatchChannelActivity
    var onActivityChange: (@MainActor (WatchChannelActivity) -> Void)?
    var onEnd: (@MainActor () -> Void)?
    private(set) var joinCount = 0
    private(set) var leaveCount = 0
    private(set) var updates: [WatchChannelActivity] = []

    init(_ activity: WatchChannelActivity) {
        self.activity = activity
    }

    func join() { joinCount += 1 }
    func leave() { leaveCount += 1 }
    func update(_ activity: WatchChannelActivity) {
        updates.append(activity)
        self.activity = activity
    }
    func coordinate(_ player: AVPlayer) {}

    /// Another member changed the activity.
    func remoteUpdate(_ activity: WatchChannelActivity) {
        self.activity = activity
        onActivityChange?(activity)
    }
}

@MainActor
final class PlayerModelSharePlayTests: XCTestCase {
    private var store: InMemorySessionStore!
    private var clock: ManualClock!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
        store = InMemorySessionStore()
        clock = ManualClock()
    }

    override func tearDown() {
        StubURLProtocol.reset()
        super.tearDown()
    }

    private func makeModel() -> PlayerModel {
        PlayerModel(
            client: BowtieClient(
                server: SharedFixtures.baseURL,
                store: store,
                urlSession: SharedFixtures.makeStubSession()
            ),
            caps: SharedFixtures.sampleCaps,
            debounce: .milliseconds(400),
            clock: clock,
            heartbeatInterval: .zero
        )
    }

    /// Run `work`, advancing the clock whenever it parks on the debounce.
    private func runThroughDebounce(_ work: @escaping @MainActor () async -> Void) async {
        var done = false
        let task = Task {
            await work()
            done = true
        }
        while !done {
            if clock.pendingWaiterCount > 0 {
                clock.advance(by: .milliseconds(400))
            }
            await Task.yield()
        }
        await task.value
    }

    /// Server stub: creates return `sessionId(channelId, joinSessionId)`;
    /// version says "srv-1" / "Living Room"; channel list has both samples.
    private func stubServer(
        sessionId: @escaping @Sendable (Int, String?) -> String = { channelId, join in join ?? "sess-\(channelId)" }
    ) {
        StubURLProtocol.handler = { [self] request in
            switch (request.httpMethod, request.url?.path) {
            case ("POST", "/api/v1/sessions"):
                let body = try jsonBody(of: request)
                let channelId = (body["channelId"] as? NSNumber)?.intValue ?? 0
                let id = sessionId(channelId, body["joinSessionId"] as? String)
                return (200, SharedFixtures.createdSessionJSON(viewerId: "v-\(id)", sessionId: id), [:])
            case ("DELETE", _):
                return (204, Data(), [:])
            case ("GET", "/api/v1/version"):
                return (200, #"{"version":"0.7.0","serverId":"srv-1","serverName":"Living Room"}"#.data(using: .utf8)!, [:])
            case ("GET", "/api/v1/channels"):
                return (200, #"[{"id":10,"guideNumber":"4.1","name":"WABC","logoUrl":""},{"id":20,"guideNumber":"7.1","name":"WXYZ","logoUrl":""}]"#.data(using: .utf8)!, [:])
            default:
                return (500, Data(), [:])
            }
        }
    }

    private func activity(channelId: Int64 = 10, channelName: String = "WABC", sessionId: String = "sess-shared") -> WatchChannelActivity {
        WatchChannelActivity(
            serverId: "srv-1",
            serverName: "Living Room",
            channelId: channelId,
            channelName: channelName,
            sessionId: sessionId
        )
    }

    // MARK: - Sharing

    func testMakeWatchActivityDescribesCurrentSession() async {
        stubServer()
        let model = makeModel()
        await runThroughDebounce { await model.play(channel: SharedFixtures.sampleChannel) }

        let made = await model.makeWatchActivity()

        XCTAssertEqual(made, activity(sessionId: "sess-10"))
    }

    func testMakeWatchActivityNeedsServerSessionId() async {
        // Older servers return no session.id: nothing to join, so no sharing.
        StubURLProtocol.handler = { request in
            if request.httpMethod == "POST" {
                return (200, SharedFixtures.createdSessionJSON(), [:])
            }
            return (200, #"{"version":"0.7.0","serverId":"srv-1","serverName":"Den"}"#.data(using: .utf8)!, [:])
        }
        let model = makeModel()
        await runThroughDebounce { await model.play(channel: SharedFixtures.sampleChannel) }

        let made = await model.makeWatchActivity()
        XCTAssertNil(made)
    }

    func testSharerReceivingOwnGroupJoinsWithoutRestarting() async {
        stubServer()
        let model = makeModel()
        await runThroughDebounce { await model.play(channel: SharedFixtures.sampleChannel) }
        let shared = await model.makeWatchActivity()!
        let group = FakeWatchGroup(shared)

        await model.receiveGroup(group)

        XCTAssertEqual(group.joinCount, 1)
        XCTAssertEqual(model.groupRole, .sharer)
        XCTAssertEqual(sessionCreateRequests().count, 1, "the sharer keeps its session")
        XCTAssertNil(model.groupPresentation)
    }

    func testSharerZapUpdatesActivity() async throws {
        stubServer()
        let model = makeModel()
        await runThroughDebounce { await model.play(channel: SharedFixtures.sampleChannel) }
        let group = FakeWatchGroup(await model.makeWatchActivity()!)
        await model.receiveGroup(group)

        await runThroughDebounce { await model.play(channel: SharedFixtures.sampleChannel2) }

        XCTAssertEqual(group.updates, [activity(channelId: 20, channelName: "WXYZ", sessionId: "sess-20")])
        let body = try jsonBody(of: sessionCreateRequests().last!)
        XCTAssertNil(body["joinSessionId"], "the sharer starts its own session")
        XCTAssertEqual(model.groupRole, .sharer)

        // Our own update echoes back: the sharer doesn't restart for it.
        group.remoteUpdate(group.activity)
        await Task.yield()
        XCTAssertEqual(sessionCreateRequests().count, 2)
    }

    // MARK: - Joining

    func testParticipantJoinsSharersSession() async throws {
        stubServer()
        let model = makeModel()
        let group = FakeWatchGroup(activity())

        await runThroughDebounce { await model.receiveGroup(group) }

        XCTAssertEqual(group.joinCount, 1)
        XCTAssertEqual(model.groupRole, .participant)
        let body = try jsonBody(of: sessionCreateRequests().last!)
        XCTAssertEqual(body["joinSessionId"] as? String, "sess-shared")
        XCTAssertEqual((body["channelId"] as? NSNumber)?.intValue, 10)
        XCTAssertEqual(model.currentChannel, SharedFixtures.sampleChannel, "full channel looked up for the chrome")
        XCTAssertEqual(model.groupPresentation?.channel.id, 10)
        guard case .playing(let session) = model.state else {
            return XCTFail("expected playing, got \(model.state)")
        }
        XCTAssertEqual(session.session?.id, "sess-shared")
    }

    func testParticipantRetryRejoinsGroupSession() async throws {
        stubServer()
        let model = makeModel()
        await runThroughDebounce { await model.receiveGroup(FakeWatchGroup(self.activity())) }

        await runThroughDebounce { await model.setProfile("low") }

        let body = try jsonBody(of: sessionCreateRequests().last!)
        XCTAssertEqual(body["joinSessionId"] as? String, "sess-shared")
    }

    func testParticipantFollowsSharersChannelChange() async throws {
        stubServer()
        let model = makeModel()
        let group = FakeWatchGroup(activity())
        await runThroughDebounce { await model.receiveGroup(group) }

        await runThroughDebounce {
            group.remoteUpdate(self.activity(channelId: 20, channelName: "WXYZ", sessionId: "sess-next"))
            // The follow runs on its own task; wait for its create.
            while model.currentChannel?.id != 20 || model.state == .starting {
                await Task.yield()
            }
        }

        let body = try jsonBody(of: sessionCreateRequests().last!)
        XCTAssertEqual(body["joinSessionId"] as? String, "sess-next")
        XCTAssertEqual((body["channelId"] as? NSNumber)?.intValue, 20)
        XCTAssertEqual(group.leaveCount, 0)
    }

    func testParticipantZapAsksBeforeLeavingGroup() async {
        stubServer()
        let model = makeModel()
        let group = FakeWatchGroup(activity())
        await runThroughDebounce { await model.receiveGroup(group) }

        await model.play(channel: SharedFixtures.sampleChannel2)

        XCTAssertEqual(model.pendingGroupZap, SharedFixtures.sampleChannel2)
        XCTAssertEqual(model.currentChannel?.id, 10, "still on the group's channel")
        XCTAssertEqual(sessionCreateRequests().count, 1)
        XCTAssertEqual(group.leaveCount, 0)
    }

    func testConfirmingZapLeavesGroupAndPlays() async throws {
        stubServer()
        let model = makeModel()
        let group = FakeWatchGroup(activity())
        await runThroughDebounce { await model.receiveGroup(group) }
        await model.play(channel: SharedFixtures.sampleChannel2)

        await runThroughDebounce { await model.confirmGroupZap() }

        XCTAssertEqual(group.leaveCount, 1)
        XCTAssertNil(model.groupRole)
        XCTAssertNil(model.pendingGroupZap)
        XCTAssertEqual(model.currentChannel?.id, 20)
        let body = try jsonBody(of: sessionCreateRequests().last!)
        XCTAssertNil(body["joinSessionId"])
    }

    func testCancellingZapStaysInGroup() async {
        stubServer()
        let model = makeModel()
        let group = FakeWatchGroup(activity())
        await runThroughDebounce { await model.receiveGroup(group) }
        await model.play(channel: SharedFixtures.sampleChannel2)

        model.cancelGroupZap()

        XCTAssertNil(model.pendingGroupZap)
        XCTAssertEqual(model.groupRole, .participant)
        XCTAssertEqual(model.currentChannel?.id, 10)
        XCTAssertEqual(group.leaveCount, 0)
    }

    // MARK: - Leaving

    func testStopLeavesGroup() async {
        stubServer()
        let model = makeModel()
        let group = FakeWatchGroup(activity())
        await runThroughDebounce { await model.receiveGroup(group) }

        await model.stop()

        XCTAssertEqual(group.leaveCount, 1)
        XCTAssertNil(model.groupRole)
    }

    func testGroupEndingKeepsPlaying() async {
        stubServer()
        let model = makeModel()
        let group = FakeWatchGroup(activity())
        await runThroughDebounce { await model.receiveGroup(group) }

        group.onEnd?()

        XCTAssertNil(model.groupRole)
        guard case .playing = model.state else {
            return XCTFail("expected playing, got \(model.state)")
        }
    }
}
