import XCTest
import BowtieKit
@testable import Bowtie

@MainActor
final class PlayerModelTests: XCTestCase {
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

    private func makeClient() -> BowtieClient {
        BowtieClient(
            server: SharedFixtures.baseURL,
            store: store,
            urlSession: SharedFixtures.makeStubSession()
        )
    }

    private func makeModel(
        client: BowtieClient? = nil,
        caps: ClientCaps = SharedFixtures.sampleCaps,
        /// Default `.zero` disables beats so ManualClock debounce tests stay isolated.
        heartbeatInterval: Duration = .zero
    ) -> PlayerModel {
        PlayerModel(
            client: client ?? makeClient(),
            caps: caps,
            debounce: .milliseconds(400),
            clock: clock,
            heartbeatInterval: heartbeatInterval
        )
    }

    /// Wait until ManualClock has a suspended sleeper, then advance past debounce.
    /// Polls waiter registration so advance never races ahead of sleep.
    private func advancePastDebounce() async {
        for _ in 0..<1_000 {
            if clock.pendingWaiterCount > 0 {
                clock.advance(by: .milliseconds(400))
                return
            }
            await Task.yield()
        }
        // Last-chance advance (avoids hard hang if waiter count is wrong).
        clock.advance(by: .milliseconds(400))
    }

    /// Start `work`, wait for debounce sleep, advance, await completion.
    private func runThroughDebounce(_ work: @escaping @MainActor () async -> Void) async {
        let task = Task { await work() }
        await advancePastDebounce()
        await task.value
    }

    /// Run work that parks on the ManualClock, then advance past debounce.
    private func playThroughDebounce(
        _ model: PlayerModel,
        channel: Channel = SharedFixtures.sampleChannel
    ) async {
        await runThroughDebounce { await model.play(channel: channel) }
    }

    // MARK: - Debounced triple-play → one create

    func testRapidTriplePlayCreatesExactlyOneSession() async throws {
        var createCount = 0
        StubURLProtocol.handler = { request in
            if request.httpMethod == "POST", request.url?.path == "/api/v1/sessions" {
                createCount += 1
                return (
                    200,
                    SharedFixtures.createdSessionJSON(viewerId: "v-final"),
                    [:]
                )
            }
            if request.httpMethod == "DELETE" {
                return (204, Data(), [:])
            }
            return (500, Data(), [:])
        }

        let model = makeModel()
        let ch1 = Channel(id: 1, guideNumber: "1", name: "One", logoUrl: "")
        let ch2 = Channel(id: 2, guideNumber: "2", name: "Two", logoUrl: "")
        let ch3 = Channel(id: 3, guideNumber: "3", name: "Three", logoUrl: "")

        let t1 = Task { await model.play(channel: ch1) }
        let t2 = Task { await model.play(channel: ch2) }
        let t3 = Task { await model.play(channel: ch3) }

        // Wait until the latest play (ch3) owns the debounce sleeper, then fire it.
        // Avoid advancing while an intermediate play is still parking — that can
        // resume a soon-to-be-cancelled waiter and leave the final one hung.
        for _ in 0..<2_000 {
            if model.currentChannel?.id == 3, clock.pendingWaiterCount > 0 {
                clock.advance(by: .milliseconds(400))
                break
            }
            await Task.yield()
        }

        await t1.value
        await t2.value
        await t3.value

        XCTAssertEqual(sessionCreateRequests().count, 1, "exactly one create after triple-play debounce")
        XCTAssertEqual(createCount, 1)
        XCTAssertEqual(model.currentChannel?.id, 3)

        // The surviving create must target channel 3 (latest play wins).
        let body = try jsonBody(of: sessionCreateRequests()[0])
        let channelId = (body["channelId"] as? Int)
            ?? (body["channelId"] as? Int64).map(Int.init)
            ?? (body["channelId"] as? NSNumber)?.intValue
        XCTAssertEqual(channelId, 3)

        guard case .playing(let session) = model.state else {
            return XCTFail("expected playing, got \(model.state)")
        }
        XCTAssertEqual(session.viewerId, "v-final")
    }

    // MARK: - Zap: delete old then create new

    func testZapDeletesOldThenCreatesNew() async {
        var createSeq = 0
        StubURLProtocol.handler = { request in
            if request.httpMethod == "POST", request.url?.path == "/api/v1/sessions" {
                createSeq += 1
                return (
                    200,
                    SharedFixtures.createdSessionJSON(viewerId: "viewer-\(createSeq)"),
                    [:]
                )
            }
            if request.httpMethod == "DELETE" {
                return (204, Data(), [:])
            }
            return (500, Data(), [:])
        }

        let model = makeModel()
        await playThroughDebounce(model, channel: SharedFixtures.sampleChannel)

        guard case .playing(let first) = model.state else {
            return XCTFail("expected playing after first play, got \(model.state)")
        }
        XCTAssertEqual(first.viewerId, "viewer-1")
        XCTAssertEqual(sessionCreateRequests().count, 1)
        XCTAssertEqual(sessionDeleteRequests().count, 0)

        // Zap to channel 2.
        await runThroughDebounce {
            await model.play(channel: SharedFixtures.sampleChannel2)
        }

        guard case .playing(let second) = model.state else {
            return XCTFail("expected playing after zap, got \(model.state)")
        }
        XCTAssertEqual(second.viewerId, "viewer-2")
        XCTAssertEqual(sessionCreateRequests().count, 2)

        let deletes = sessionDeleteRequests()
        XCTAssertEqual(deletes.count, 1)
        XCTAssertTrue(
            deletes[0].url?.path.hasSuffix("/viewer-1") == true,
            "zap must DELETE the old viewer before creating the new one: \(deletes[0].url?.path ?? "?")"
        )

        // Ordering: delete of old before second create.
        let recorded = StubURLProtocol.recorded
        let deleteIdx = recorded.firstIndex {
            $0.httpMethod == "DELETE" && ($0.url?.path.contains("viewer-1") ?? false)
        }
        let creates = recorded.indices.filter {
            recorded[$0].httpMethod == "POST" && recorded[$0].url?.path == "/api/v1/sessions"
        }
        XCTAssertEqual(creates.count, 2)
        if let deleteIdx, creates.count == 2 {
            XCTAssertLessThan(deleteIdx, creates[1], "DELETE old before POST new")
        }
    }

    // MARK: - setProfile sends effectiveCaps

    func testSetProfileSendsEffectiveCapsInRequestBody() async throws {
        StubURLProtocol.handler = { request in
            if request.httpMethod == "POST", request.url?.path == "/api/v1/sessions" {
                return (200, SharedFixtures.createdSessionJSON(), [:])
            }
            if request.httpMethod == "DELETE" {
                return (204, Data(), [:])
            }
            return (500, Data(), [:])
        }

        let model = makeModel()
        await playThroughDebounce(model)
        XCTAssertEqual(sessionCreateRequests().count, 1)

        await runThroughDebounce { await model.setProfile("medium") }

        XCTAssertEqual(model.currentChannel?.id, SharedFixtures.sampleChannel.id)
        XCTAssertEqual(model.selectedProfile, "medium")

        let creates = sessionCreateRequests()
        XCTAssertEqual(creates.count, 2)
        let body = try jsonBody(of: creates[1])
        XCTAssertEqual(body["channelId"] as? Int, Int(SharedFixtures.sampleChannel.id))
        let caps = body["caps"] as? [String: Any]
        XCTAssertEqual(caps?["profile"] as? String, "medium")
        XCTAssertEqual(caps?["maxHeight"] as? Int, 1080)
        XCTAssertEqual(caps?["videoCodecs"] as? [String], ["h264", "hevc"])
        XCTAssertEqual(caps?["audioCodecs"] as? [String], ["aac", "ac3", "eac3"])
    }

    // MARK: - 422 fallback

    func testNegotiation422RetriesOnceWithAutoProfile() async throws {
        var createCount = 0
        StubURLProtocol.handler = { request in
            if request.httpMethod == "POST", request.url?.path == "/api/v1/sessions" {
                createCount += 1
                if createCount == 1 {
                    return (
                        422,
                        #"{"error":"profile not available"}"#.data(using: .utf8)!,
                        [:]
                    )
                }
                return (200, SharedFixtures.createdSessionJSON(viewerId: "auto-ok"), [:])
            }
            if request.httpMethod == "DELETE" {
                return (204, Data(), [:])
            }
            return (500, Data(), [:])
        }

        let model = makeModel()
        model.selectedProfile = "high"

        await playThroughDebounce(model)

        XCTAssertEqual(sessionCreateRequests().count, 2)
        let first = try jsonBody(of: sessionCreateRequests()[0])
        let second = try jsonBody(of: sessionCreateRequests()[1])
        XCTAssertEqual((first["caps"] as? [String: Any])?["profile"] as? String, "high")
        XCTAssertEqual((second["caps"] as? [String: Any])?["profile"] as? String, "")
        XCTAssertEqual(model.selectedProfile, "")

        guard case .playing(let session) = model.state else {
            return XCTFail("expected playing after Auto retry, got \(model.state)")
        }
        XCTAssertEqual(session.viewerId, "auto-ok")
    }

    func testNegotiation422TwiceFailsWithDeviceCantPlay() async throws {
        StubURLProtocol.handler = { request in
            if request.httpMethod == "POST", request.url?.path == "/api/v1/sessions" {
                return (
                    422,
                    #"{"error":"cannot negotiate"}"#.data(using: .utf8)!,
                    [:]
                )
            }
            if request.httpMethod == "DELETE" {
                return (204, Data(), [:])
            }
            return (500, Data(), [:])
        }

        let model = makeModel()
        model.selectedProfile = "original"

        await playThroughDebounce(model)

        XCTAssertEqual(sessionCreateRequests().count, 2)
        let first = try jsonBody(of: sessionCreateRequests()[0])
        let second = try jsonBody(of: sessionCreateRequests()[1])
        XCTAssertEqual((first["caps"] as? [String: Any])?["profile"] as? String, "original")
        XCTAssertEqual((second["caps"] as? [String: Any])?["profile"] as? String, "")

        guard case .failed(let message) = model.state else {
            return XCTFail("expected failed, got \(model.state)")
        }
        XCTAssertEqual(message, PlayerModel.deviceCantPlayMessage)
        XCTAssertEqual(model.selectedProfile, "")
    }

    // MARK: - stop

    func testStopDeletesSessionAndReturnsToIdle() async {
        StubURLProtocol.handler = { request in
            if request.httpMethod == "POST", request.url?.path == "/api/v1/sessions" {
                return (200, SharedFixtures.createdSessionJSON(viewerId: "to-stop"), [:])
            }
            if request.httpMethod == "DELETE" {
                return (204, Data(), [:])
            }
            return (500, Data(), [:])
        }

        let model = makeModel()
        await playThroughDebounce(model)
        guard case .playing = model.state else {
            return XCTFail("expected playing, got \(model.state)")
        }

        await model.stop()

        XCTAssertEqual(model.state, .idle)
        XCTAssertNil(model.currentChannel)
        let deletes = sessionDeleteRequests()
        XCTAssertEqual(deletes.count, 1)
        XCTAssertTrue(deletes[0].url?.path.hasSuffix("/to-stop") == true)
    }

    // MARK: - 404 channels-stale

    func testNotFoundBumpsChannelsStaleGeneration() async {
        StubURLProtocol.handler = { request in
            if request.httpMethod == "POST", request.url?.path == "/api/v1/sessions" {
                return (404, #"{"error":"channel not found"}"#.data(using: .utf8)!, [:])
            }
            if request.httpMethod == "DELETE" {
                return (204, Data(), [:])
            }
            return (500, Data(), [:])
        }

        let model = makeModel()
        XCTAssertEqual(model.channelsStaleGeneration, 0)

        await playThroughDebounce(model)

        guard case .failed(let message) = model.state else {
            return XCTFail("expected failed, got \(model.state)")
        }
        XCTAssertEqual(message, "Channel not found")
        XCTAssertEqual(model.channelsStaleGeneration, 1)

        // Second 404 increments again.
        await playThroughDebounce(model)
        XCTAssertEqual(model.channelsStaleGeneration, 2)
    }

    // MARK: - Heartbeats (15s cadence + A6 through-stall)

    private func heartbeatRequests() -> [URLRequest] {
        StubURLProtocol.recorded.filter {
            $0.httpMethod == "POST" && ($0.url?.path.contains("/heartbeat") ?? false)
        }
    }

    /// Wait until ManualClock has a sleeper (heartbeat or debounce), then advance.
    private func advanceClock(_ duration: Duration) async {
        for _ in 0..<1_000 {
            if clock.pendingWaiterCount > 0 {
                clock.advance(by: duration)
                // Yield so resumed tasks can re-park on the next sleep.
                await Task.yield()
                await Task.yield()
                return
            }
            await Task.yield()
        }
        clock.advance(by: duration)
        await Task.yield()
    }

    func testHeartbeatCadenceEvery15s() async {
        StubURLProtocol.handler = { request in
            if request.httpMethod == "POST", request.url?.path == "/api/v1/sessions" {
                return (200, SharedFixtures.createdSessionJSON(viewerId: "hb-1"), [:])
            }
            if request.httpMethod == "POST", request.url?.path.contains("/heartbeat") == true {
                return (204, Data(), [:])
            }
            if request.httpMethod == "DELETE" {
                return (204, Data(), [:])
            }
            return (500, Data(), [:])
        }

        let model = makeModel(heartbeatInterval: .seconds(15))
        await playThroughDebounce(model)
        guard case .playing = model.state else {
            return XCTFail("expected playing, got \(model.state)")
        }
        XCTAssertEqual(heartbeatRequests().count, 0, "no immediate beat on start")

        await advanceClock(.seconds(15))
        // Drain the heartbeat network call.
        for _ in 0..<50 where heartbeatRequests().count < 1 {
            await Task.yield()
        }
        XCTAssertEqual(heartbeatRequests().count, 1)

        await advanceClock(.seconds(15))
        for _ in 0..<50 where heartbeatRequests().count < 2 {
            await Task.yield()
        }
        XCTAssertEqual(heartbeatRequests().count, 2)

        let beat = heartbeatRequests()[0]
        let items = URLComponents(url: beat.url!, resolvingAgainstBaseURL: false)?.queryItems ?? []
        XCTAssertEqual(items.first(where: { $0.name == "token" })?.value, "abc")
        XCTAssertNil(beat.value(forHTTPHeaderField: "Authorization"))
        await model.stop()
    }

    func testHeartbeatContinuesThroughStalled() async {
        StubURLProtocol.handler = { request in
            if request.httpMethod == "POST", request.url?.path == "/api/v1/sessions" {
                return (200, SharedFixtures.createdSessionJSON(viewerId: "hb-stall"), [:])
            }
            if request.httpMethod == "POST", request.url?.path.contains("/heartbeat") == true {
                return (204, Data(), [:])
            }
            if request.httpMethod == "DELETE" {
                return (204, Data(), [:])
            }
            return (500, Data(), [:])
        }

        let model = makeModel(heartbeatInterval: .seconds(15))
        await playThroughDebounce(model)

        await advanceClock(.seconds(15))
        for _ in 0..<50 where heartbeatRequests().count < 1 {
            await Task.yield()
        }
        XCTAssertEqual(heartbeatRequests().count, 1)

        // A6: stall mid-interval; beats must continue.
        model.markStalled()
        XCTAssertEqual(model.state, .stalled)

        await advanceClock(.seconds(15))
        for _ in 0..<50 where heartbeatRequests().count < 2 {
            await Task.yield()
        }
        XCTAssertEqual(heartbeatRequests().count, 2, "beats continue through .stalled")

        await model.stop()
        let afterStop = heartbeatRequests().count
        await advanceClock(.seconds(15))
        await advanceClock(.seconds(15))
        XCTAssertEqual(
            heartbeatRequests().count,
            afterStop,
            "beats stop on real leave"
        )
    }

    func testStreamTokenFromPlaylist() {
        XCTAssertEqual(
            PlayerModel.streamToken(from: "/api/v1/stream/v1/index.m3u8?token=abc"),
            "abc"
        )
        XCTAssertEqual(
            PlayerModel.streamToken(from: "http://host/api/v1/stream/v1/index.m3u8?token=xyz&other=1"),
            "xyz"
        )
        XCTAssertNil(PlayerModel.streamToken(from: "/api/v1/stream/v1/index.m3u8"))
        XCTAssertEqual(PlayerModel.outOfWindowNotice, "Jumped to live — paused longer than the buffer")
    }

    // MARK: - Stall hooks

    func testMarkStalledResumeAndExhaustion() async {
        StubURLProtocol.handler = { request in
            if request.httpMethod == "POST", request.url?.path == "/api/v1/sessions" {
                return (200, SharedFixtures.createdSessionJSON(viewerId: "stall-1"), [:])
            }
            if request.httpMethod == "DELETE" {
                return (204, Data(), [:])
            }
            return (500, Data(), [:])
        }

        let model = makeModel()
        await playThroughDebounce(model)
        guard case .playing(let session) = model.state else {
            return XCTFail("expected playing, got \(model.state)")
        }

        model.markStalled()
        XCTAssertEqual(model.state, .stalled)
        XCTAssertEqual(model.lastSession?.viewerId, session.viewerId)

        model.resumePlaying()
        guard case .playing(let again) = model.state else {
            return XCTFail("expected playing after resume, got \(model.state)")
        }
        XCTAssertEqual(again.viewerId, session.viewerId)

        model.markStalled()
        model.stallFailed()
        guard case .failed(let message) = model.state else {
            return XCTFail("expected failed after stall exhaustion, got \(model.state)")
        }
        XCTAssertEqual(message, PlayerModel.stallFailedMessage)
    }

    func testRetryAfterTunersBusyRecreatesSession() async {
        var createCount = 0
        StubURLProtocol.handler = { request in
            if request.httpMethod == "POST", request.url?.path == "/api/v1/sessions" {
                createCount += 1
                if createCount == 1 {
                    let body = """
                    {
                      "error": "all tuners in use",
                      "sessions": [
                        {
                          "channelName": "WABC",
                          "viewers": [{"username": "bob"}]
                        }
                      ]
                    }
                    """.data(using: .utf8)!
                    return (503, body, [:])
                }
                return (200, SharedFixtures.createdSessionJSON(viewerId: "retry-ok"), [:])
            }
            if request.httpMethod == "DELETE" {
                return (204, Data(), [:])
            }
            return (500, Data(), [:])
        }

        let model = makeModel()
        await playThroughDebounce(model)

        guard case .tunersBusy(let sessions) = model.state else {
            return XCTFail("expected tunersBusy, got \(model.state)")
        }
        XCTAssertEqual(sessions.count, 1)
        XCTAssertEqual(sessions[0].channelName, "WABC")
        XCTAssertEqual(sessions[0].viewers.first?.username, "bob")

        await runThroughDebounce { await model.retry() }

        guard case .playing(let session) = model.state else {
            return XCTFail("expected playing after retry, got \(model.state)")
        }
        XCTAssertEqual(session.viewerId, "retry-ok")
        XCTAssertEqual(sessionCreateRequests().count, 2)
    }

    // MARK: - playbackAuthFailed

    func testPlaybackAuthFailedOnceSilentlyReplaces() async {
        var createCount = 0
        StubURLProtocol.handler = { request in
            if request.httpMethod == "POST", request.url?.path == "/api/v1/sessions" {
                createCount += 1
                return (
                    200,
                    SharedFixtures.createdSessionJSON(viewerId: "auth-\(createCount)"),
                    [:]
                )
            }
            if request.httpMethod == "DELETE" {
                return (204, Data(), [:])
            }
            return (500, Data(), [:])
        }

        let model = makeModel()
        await playThroughDebounce(model)
        guard case .playing(let first) = model.state else {
            return XCTFail("expected playing, got \(model.state)")
        }
        XCTAssertEqual(first.viewerId, "auth-1")

        await runThroughDebounce { await model.playbackAuthFailed() }

        guard case .playing(let second) = model.state else {
            return XCTFail("expected playing after silent replace, got \(model.state)")
        }
        XCTAssertEqual(second.viewerId, "auth-2")
        XCTAssertEqual(sessionCreateRequests().count, 2)
        XCTAssertEqual(sessionDeleteRequests().count, 1)
    }

    func testPlaybackAuthFailedTwiceGoesToFailed() async {
        var createCount = 0
        StubURLProtocol.handler = { request in
            if request.httpMethod == "POST", request.url?.path == "/api/v1/sessions" {
                createCount += 1
                return (
                    200,
                    SharedFixtures.createdSessionJSON(viewerId: "auth-\(createCount)"),
                    [:]
                )
            }
            if request.httpMethod == "DELETE" {
                return (204, Data(), [:])
            }
            return (500, Data(), [:])
        }

        let model = makeModel()
        await playThroughDebounce(model)

        // First 403 → silent replace.
        await runThroughDebounce { await model.playbackAuthFailed() }
        guard case .playing = model.state else {
            return XCTFail("expected playing after first auth failure, got \(model.state)")
        }

        // Second 403 → failed (no further replace).
        await model.playbackAuthFailed()

        guard case .failed(let message) = model.state else {
            return XCTFail("expected failed after second auth failure, got \(model.state)")
        }
        XCTAssertEqual(message, PlayerModel.playbackAuthFailedMessage)
        // Only the initial play + one silent replace.
        XCTAssertEqual(sessionCreateRequests().count, 2)
    }
}
