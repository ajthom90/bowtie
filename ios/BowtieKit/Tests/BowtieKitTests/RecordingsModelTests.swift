import XCTest
@testable import BowtieKit

@MainActor
final class RecordingsModelTests: XCTestCase {
    private var store: InMemorySessionStore!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
        store = InMemorySessionStore()
    }

    override func tearDown() {
        StubURLProtocol.reset()
        super.tearDown()
    }

    private func makeClient() async -> BowtieClient {
        let client = BowtieClient(
            server: TestFixtures.baseURL,
            store: store,
            urlSession: TestFixtures.makeStubSession()
        )
        await client.setAccessTokenForTesting("access-1")
        return client
    }

    private func rows(_ model: RecordingsModel, file: StaticString = #filePath, line: UInt = #line) -> [Recording] {
        guard case .loaded(let rows) = model.state else {
            XCTFail("expected loaded, got \(model.state)", file: file, line: line)
            return []
        }
        return rows
    }

    // MARK: - Loading

    func testDefaultTabIsRecordedAndLoadsWithItsFilter() async {
        StubURLProtocol.handler = { _ in
            (200, RecordingFixtures.listJSON([RecordingFixtures.json(id: 1), RecordingFixtures.json(id: 2)]), [:])
        }
        let model = RecordingsModel(client: await makeClient())

        XCTAssertEqual(model.tab, .recorded)
        await model.load()

        XCTAssertEqual(rows(model).map(\.id), [1, 2])
        XCTAssertEqual(StubURLProtocol.recorded.first?.url?.query, "state=recorded")
    }

    func testSelectMissedQueriesFailed() async {
        StubURLProtocol.handler = { _ in
            (200, RecordingFixtures.listJSON([RecordingFixtures.json(state: "failed", failure: "noTuner")]), [:])
        }
        let model = RecordingsModel(client: await makeClient())

        await model.select(.missed)

        XCTAssertEqual(model.tab, .missed)
        XCTAssertEqual(rows(model).first?.failure, "noTuner")
        XCTAssertEqual(StubURLProtocol.recorded.last?.url?.query, "state=failed")
    }

    func testEmptyListIsEmptyState() async {
        StubURLProtocol.handler = { _ in (200, Data("[]".utf8), [:]) }
        let model = RecordingsModel(client: await makeClient())

        await model.load()

        XCTAssertEqual(model.state, .empty)
    }

    func testServerErrorIsFailedState() async {
        StubURLProtocol.handler = { _ in (503, Data(#"{"error":"recording is not available"}"#.utf8), [:]) }
        let model = RecordingsModel(client: await makeClient())

        await model.load()

        XCTAssertEqual(model.state, .failed("Recording is not available"))
    }

    func testLateResponseForOldTabIsDropped() async throws {
        StubURLProtocol.delay = { $0.url?.query == "state=upcoming" ? 0.3 : 0 }
        StubURLProtocol.handler = { request in
            if request.url?.query == "state=upcoming" {
                return (200, RecordingFixtures.listJSON([RecordingFixtures.json(id: 1, state: "scheduled")]), [:])
            }
            return (200, RecordingFixtures.listJSON([RecordingFixtures.json(id: 2, state: "failed")]), [:])
        }
        let model = RecordingsModel(client: await makeClient())

        let slow = Task { await model.select(.upcoming) }
        for _ in 0..<100 where !StubURLProtocol.recorded.contains(where: { $0.url?.query == "state=upcoming" }) {
            try await Task.sleep(for: .milliseconds(10))
        }
        await model.select(.missed)
        await slow.value

        XCTAssertEqual(model.tab, .missed)
        XCTAssertEqual(rows(model).map(\.id), [2])
    }

    // MARK: - Manage

    func testDeleteRemovesRowAndEmptiesWhenLast() async {
        StubURLProtocol.handler = { request in
            if request.httpMethod == "DELETE" { return (204, Data(), [:]) }
            return (200, RecordingFixtures.listJSON([RecordingFixtures.json(id: 7)]), [:])
        }
        let model = RecordingsModel(client: await makeClient())
        await model.load()

        await model.delete(RecordingFixtures.recording(id: 7))

        XCTAssertEqual(model.state, .empty)
        XCTAssertNil(model.actionError)
        XCTAssertEqual(StubURLProtocol.recorded.last?.url?.path, "/api/v1/recordings/7")
    }

    func testDeleteForbiddenKeepsRowAndExplains() async {
        StubURLProtocol.handler = { request in
            if request.httpMethod == "DELETE" {
                return (403, Data(#"{"error":"only the person who scheduled it (or an admin) can change this recording"}"#.utf8), [:])
            }
            return (200, RecordingFixtures.listJSON([RecordingFixtures.json(id: 7)]), [:])
        }
        let model = RecordingsModel(client: await makeClient())
        await model.load()

        await model.delete(RecordingFixtures.recording(id: 7))

        XCTAssertEqual(rows(model).map(\.id), [7])
        XCTAssertEqual(model.actionError, "Only the person who scheduled it (or an admin) can change this recording")
    }

    func testStopReloadsTheList() async {
        StubURLProtocol.handler = { request in
            if request.url?.path.hasSuffix("/stop") == true { return (204, Data(), [:]) }
            return (200, RecordingFixtures.listJSON([RecordingFixtures.json(id: 7, state: "recording")]), [:])
        }
        let model = RecordingsModel(client: await makeClient())
        await model.select(.upcoming)
        let before = StubURLProtocol.recorded.count

        await model.stop(RecordingFixtures.recording(id: 7, state: "recording"))

        let after = StubURLProtocol.recorded.dropFirst(before)
        XCTAssertEqual(after.map(\.httpMethod), ["POST", "GET"])
        XCTAssertEqual(after.last?.url?.query, "state=upcoming")
    }

    func testProtectReplacesRow() async {
        StubURLProtocol.handler = { request in
            if request.httpMethod == "PATCH" {
                return (200, Data(RecordingFixtures.json(id: 7, protected: true).utf8), [:])
            }
            return (200, RecordingFixtures.listJSON([RecordingFixtures.json(id: 7), RecordingFixtures.json(id: 8)]), [:])
        }
        let model = RecordingsModel(client: await makeClient())
        await model.load()

        await model.setProtected(RecordingFixtures.recording(id: 7), true)

        XCTAssertEqual(rows(model).map(\.protected), [true, false])
    }

    // MARK: - Play

    func testPlayResolvesURLAndOffersResume() async throws {
        StubURLProtocol.handler = { _ in
            (200, Data(#"{"playlistUrl":"/api/v1/recordings/7/hls/index.m3u8?token=abc","positionSec":412,"durationSec":1980}"#.utf8), [:])
        }
        let model = RecordingsModel(client: await makeClient())
        let rec = RecordingFixtures.recording(id: 7, positionSec: 0)

        let maybe = await model.play(rec)
        let playback = try XCTUnwrap(maybe)

        XCTAssertEqual(
            playback.url.absoluteString,
            "http://test.bowtie.local:8400/api/v1/recordings/7/hls/index.m3u8?token=abc"
        )
        XCTAssertEqual(playback.resumeAt, 412, "decides from /play, not the list row")
        XCTAssertEqual(playback.startSec, 0)
        XCTAssertEqual(playback.durationSec, 1980)
        XCTAssertEqual(playback.recording.id, 7)
        XCTAssertEqual(playback.starting(at: 412).startSec, 412)
    }

    func testPlayNearEndStartsOver() async throws {
        StubURLProtocol.handler = { _ in
            (200, Data(#"{"playlistUrl":"/x.m3u8?token=t","positionSec":1960,"durationSec":1980}"#.utf8), [:])
        }
        let model = RecordingsModel(client: await makeClient())

        let maybe = await model.play(RecordingFixtures.recording())
        let playback = try XCTUnwrap(maybe)

        XCTAssertNil(playback.resumeAt)
    }

    func testPlayFailureSetsActionError() async {
        StubURLProtocol.handler = { _ in (409, Data(#"{"error":"recording is not ready yet"}"#.utf8), [:]) }
        let model = RecordingsModel(client: await makeClient())

        let playback = await model.play(RecordingFixtures.recording())

        XCTAssertNil(playback)
        XCTAssertEqual(model.actionError, "Recording is not ready yet")
    }

    func testPlayFailureDoesNotRunBeforeStart() async {
        StubURLProtocol.handler = { _ in (404, Data(#"{"error":"not found"}"#.utf8), [:]) }
        let model = RecordingsModel(client: await makeClient())
        var stoppedLive = false

        let playback = await model.play(RecordingFixtures.recording()) {
            stoppedLive = true
        }

        XCTAssertNil(playback)
        XCTAssertFalse(stoppedLive, "live TV keeps playing when /play fails")
        XCTAssertEqual(model.actionError, "That recording is gone.")
    }

    func testPlaySuccessRunsBeforeStartAfterPlayCall() async throws {
        StubURLProtocol.handler = { _ in
            (200, Data(#"{"playlistUrl":"/x.m3u8?token=t","positionSec":0,"durationSec":1980}"#.utf8), [:])
        }
        let model = RecordingsModel(client: await makeClient())
        var requestsWhenStopped: Int?

        let maybe = await model.play(RecordingFixtures.recording(id: 7)) {
            requestsWhenStopped = StubURLProtocol.recorded.count
        }

        _ = try XCTUnwrap(maybe)
        XCTAssertEqual(requestsWhenStopped, 1, "live stops only after /play answered")
        XCTAssertEqual(StubURLProtocol.recorded.first?.url?.path, "/api/v1/recordings/7/play")
    }

    func testSavePositionFloorsSeconds() async throws {
        StubURLProtocol.handler = { _ in (204, Data(), [:]) }
        let model = RecordingsModel(client: await makeClient())

        await model.savePosition(recordingId: 7, seconds: 615.8)
        await model.savePosition(recordingId: 7, seconds: .nan)

        XCTAssertEqual(StubURLProtocol.recorded.count, 1)
        let req = try XCTUnwrap(StubURLProtocol.recorded.first)
        XCTAssertEqual(req.url?.path, "/api/v1/recordings/7/position")
        XCTAssertEqual(try jsonBody(of: req)["positionSec"] as? Int, 615)
    }

    func testWaitForSavesWaitsForTheClosingPlayersSave() async {
        StubURLProtocol.handler = { _ in (204, Data(), [:]) }
        StubURLProtocol.delay = { _ in 0.3 }
        let model = RecordingsModel(client: await makeClient())
        var saved = false

        // The player queues its last save the way `saveNow` does, then the
        // screen behind it reloads.
        Task {
            await model.savePosition(recordingId: 7, seconds: 900)
            saved = true
        }
        await model.waitForSaves()

        XCTAssertTrue(saved)
    }

    func testWaitForSavesReturnsAtOnceWhenIdle() async {
        let model = RecordingsModel(client: await makeClient())
        let start = ContinuousClock.now

        await model.waitForSaves()

        XCTAssertLessThan(ContinuousClock.now - start, .milliseconds(200))
    }

    // MARK: - Scheduling

    func testScheduleOutcomeCarriesWarnings() async {
        let body = """
        {"recording":\(RecordingFixtures.json(state: "scheduled")),
         "warnings":[{"code":"usesAllTuners","message":"This uses the last free tuner."}]}
        """
        StubURLProtocol.handler = { _ in (201, Data(body.utf8), [:]) }

        let outcome = await RecordingScheduler.schedule(
            client: await makeClient(),
            channelId: 3,
            programStart: TestFixtures.iso("2026-10-05T00:00:00Z")
        )

        XCTAssertEqual(
            outcome,
            .scheduled(RecordingFixtures.recording(state: "scheduled"), warnings: ["This uses the last free tuner."])
        )
    }

    func testScheduleOutcomeConflict() async {
        let body = """
        {"error":"Only 2 tuners.","tunerCount":2,"conflicts":[\(RecordingFixtures.json(id: 1, state: "scheduled"))]}
        """
        StubURLProtocol.handler = { _ in (409, Data(body.utf8), [:]) }

        let outcome = await RecordingScheduler.schedule(
            client: await makeClient(),
            channelId: 3,
            programStart: TestFixtures.iso("2026-10-05T00:00:00Z")
        )

        XCTAssertEqual(
            outcome,
            .conflict(tunerCount: 2, conflicts: [RecordingFixtures.recording(id: 1, state: "scheduled")])
        )
    }

    func testScheduleOutcomeFailure() async {
        StubURLProtocol.handler = { _ in (404, Data(#"{"error":"program not found in the guide"}"#.utf8), [:]) }

        let outcome = await RecordingScheduler.schedule(
            client: await makeClient(),
            channelId: 3,
            programStart: TestFixtures.iso("2026-10-05T00:00:00Z")
        )

        XCTAssertEqual(outcome, .failed("That program is no longer in the guide."))
    }
}
