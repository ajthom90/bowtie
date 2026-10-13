import XCTest
import BowtieKit
@testable import Bowtie

/// The weak-signal note (from the heartbeat) and plain-words player errors.
@MainActor
final class PlayerModelSignalAndErrorTests: XCTestCase {
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

    private func makeModel(heartbeatInterval: Duration = .zero) -> PlayerModel {
        PlayerModel(
            client: BowtieClient(
                server: SharedFixtures.baseURL,
                store: store,
                urlSession: SharedFixtures.makeStubSession()
            ),
            caps: SharedFixtures.sampleCaps,
            debounce: .milliseconds(400),
            clock: clock,
            heartbeatInterval: heartbeatInterval
        )
    }

    private func waitUntil(timeout: Duration = .seconds(5), _ condition: @escaping () -> Bool) async {
        let deadline = ContinuousClock.now.advanced(by: timeout)
        while !condition() && ContinuousClock.now < deadline {
            try? await Task.sleep(for: .milliseconds(5))
        }
    }

    private func advanceClock(_ duration: Duration) async {
        await waitUntil { self.clock.pendingWaiterCount > 0 }
        clock.advance(by: duration)
        await Task.yield()
        await Task.yield()
    }

    /// Play `channel` through the debounce. Waits for `.starting` first: an
    /// earlier session's heartbeat sleeper must not be mistaken for the
    /// debounce one (the replace cancels it before `.starting`).
    private func play(_ model: PlayerModel, channel: Channel = SharedFixtures.sampleChannel) async {
        let task = Task { await model.play(channel: channel) }
        await waitUntil { model.state == .starting && self.clock.pendingWaiterCount > 0 }
        clock.advance(by: .milliseconds(400))
        await task.value
    }

    private func heartbeatCount() -> Int {
        StubURLProtocol.recorded.filter { $0.url?.path.contains("/heartbeat") == true }.count
    }

    /// Advance one heartbeat interval and wait for that beat's answer to land.
    private func beat() async {
        let before = heartbeatCount()
        await advanceClock(.seconds(15))
        await waitUntil { self.heartbeatCount() > before }
        // Let the reply reach the main actor.
        for _ in 0..<20 { await Task.yield() }
        try? await Task.sleep(for: .milliseconds(20))
    }

    /// Heartbeat answers, one per beat in order; the last one repeats.
    private final class Beats: @unchecked Sendable {
        var replies: [(Int, String)]
        init(_ replies: [(Int, String)]) { self.replies = replies }
        func next() -> (Int, String) {
            replies.count > 1 ? replies.removeFirst() : replies[0]
        }
    }

    private static let weak = #"{"signal":{"strength":96,"quality":46,"symbolQuality":0,"weak":true}}"#
    private static let fine = #"{"signal":{"strength":100,"quality":100,"symbolQuality":100,"weak":false}}"#

    private func stub(_ beats: Beats) {
        StubURLProtocol.handler = { request in
            let path = request.url?.path ?? ""
            if request.httpMethod == "POST", path == "/api/v1/sessions" {
                return (200, SharedFixtures.createdSessionJSON(viewerId: "sig-1"), [:])
            }
            if path.contains("/heartbeat") {
                let (status, body) = beats.next()
                return (status, Data(body.utf8), [:])
            }
            return (204, Data(), [:])
        }
    }

    // MARK: - Weak signal

    func testWeakSignalShowsTheNoteAndAStrongOneHidesIt() async {
        stub(Beats([(200, Self.weak), (200, Self.fine)]))
        let model = makeModel(heartbeatInterval: .seconds(15))
        await play(model)
        XCTAssertFalse(model.showsWeakSignalNote, "unknown until the first beat")

        XCTAssertNil(model.signalStatsLine)

        await beat()
        XCTAssertTrue(model.showsWeakSignalNote)
        XCTAssertEqual(model.weakSignalNote, "Weak signal (46%) — the picture may break up.")
        XCTAssertEqual(model.signalStatsLine, "Signal quality 46% · strength 96% · error-free 0%")

        await beat()
        XCTAssertFalse(model.showsWeakSignalNote)
        XCTAssertNil(model.weakSignalNote)
        XCTAssertEqual(
            model.signalStatsLine,
            "Signal quality 100% · strength 100% · error-free 100%",
            "the stats line shows any known reading"
        )
        await model.stop()
    }

    func testUnknownSignalHidesTheNote() async {
        stub(Beats([(200, Self.weak), (200, #"{"signal":null}"#), (200, Self.weak), (204, "")]))
        let model = makeModel(heartbeatInterval: .seconds(15))
        await play(model)

        await beat()
        XCTAssertTrue(model.showsWeakSignalNote)
        await beat()
        XCTAssertFalse(model.showsWeakSignalNote, "null is unknown")
        XCTAssertNil(model.signalStatsLine, "no line for an unknown reading")
        await beat()
        XCTAssertTrue(model.showsWeakSignalNote)
        await beat()
        XCTAssertFalse(model.showsWeakSignalNote, "an older server's 204 is unknown")
        await model.stop()
    }

    func testAMissedBeatDoesNotFlashTheNote() async {
        stub(Beats([(200, Self.weak), (500, #"{"error":"boom"}"#), (200, Self.weak)]))
        let model = makeModel(heartbeatInterval: .seconds(15))
        await play(model)

        await beat()
        XCTAssertTrue(model.showsWeakSignalNote)
        await beat()
        XCTAssertTrue(model.showsWeakSignalNote, "no new reading: keep the last one")
        await model.stop()
    }

    func testNoteClearsOnChannelChangeAndStop() async {
        stub(Beats([(200, Self.weak), (204, "")]))
        let model = makeModel(heartbeatInterval: .seconds(15))
        await play(model)
        await beat()
        XCTAssertTrue(model.showsWeakSignalNote)

        await play(model, channel: SharedFixtures.sampleChannel2)
        XCTAssertFalse(model.showsWeakSignalNote, "a new channel starts unknown")

        await model.stop()
        XCTAssertFalse(model.showsWeakSignalNote)
    }

    func testNoteHiddenOnceFailed() async {
        stub(Beats([(200, Self.weak)]))
        let model = makeModel(heartbeatInterval: .seconds(15))
        await play(model)
        await beat()
        XCTAssertTrue(model.showsWeakSignalNote)

        model.markStalled()
        XCTAssertTrue(model.showsWeakSignalNote, "still the same session while it reconnects")
        model.stallFailed()
        XCTAssertFalse(model.showsWeakSignalNote)
        XCTAssertNil(model.signalStatsLine)
        await model.stop()
    }

    // MARK: - Plain-words errors

    private func failedMessage(createStatus: Int, body: String) async -> String? {
        StubURLProtocol.handler = { request in
            if request.httpMethod == "POST", request.url?.path == "/api/v1/sessions" {
                return (createStatus, Data(body.utf8), [:])
            }
            return (204, Data(), [:])
        }
        let model = makeModel()
        await play(model)
        guard case .failed(let message) = model.state else { return nil }
        return message
    }

    func testUnreachableServerIsPlain() async {
        StubURLProtocol.handler = { _ in throw URLError(.cannotConnectToHost) }
        let model = makeModel()
        await play(model)
        XCTAssertEqual(
            model.state,
            .failed("Can't reach your Bowtie server. Check your connection and try again.")
        )
    }

    func testUnreadableReplyIsPlain() async {
        let message = await failedMessage(createStatus: 200, body: "<html>not json</html>")
        XCTAssertEqual(message, "Something went wrong. Try again.")
    }

    func testBodylessServerErrorIsPlain() async {
        let message = await failedMessage(createStatus: 500, body: "")
        XCTAssertEqual(message, "Something went wrong. Try again.")
    }

    func testServerMessageIsShownAsIs() async {
        let message = await failedMessage(
            createStatus: 500,
            body: #"{"error":"This channel isn't coming in right now."}"#
        )
        XCTAssertEqual(message, "This channel isn't coming in right now.")
    }

    func testStoppedStreamCopy() {
        XCTAssertEqual(PlayerModel.stallFailedMessage, "The stream stopped. Try again.")
        XCTAssertEqual(PlayerModel.playbackAuthFailedMessage, "The stream stopped. Try again.")
        XCTAssertEqual(PlayerModel.deviceCantPlayMessage, "This channel can't play on this device.")
    }
}
