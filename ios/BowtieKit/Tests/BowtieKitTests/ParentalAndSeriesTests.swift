import XCTest
@testable import BowtieKit

/// Parental-control locks, series marks and the `skipped` failure on the
/// shared models, plus the 403 `code: "parental"` error.
final class ParentalAndSeriesTests: XCTestCase {
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

    private func decodeRecording(_ json: String) async throws -> Recording {
        StubURLProtocol.handler = { _ in (200, RecordingFixtures.listJSON([json]), [:]) }
        let client = await makeClient()
        let rows = try await client.recordings(filter: nil)
        return try XCTUnwrap(rows.first)
    }

    // MARK: - Recording fields

    func testRecordingFromOlderServerDefaultsNewFields() async throws {
        let recording = try await decodeRecording(RecordingFixtures.json())

        XCTAssertEqual(recording.rating, "")
        XCTAssertEqual(recording.ruleId, 0)
        XCTAssertFalse(recording.locked)
        XCTAssertFalse(recording.isSeries)
    }

    func testRecordingDecodesRatingRuleAndLock() async throws {
        let recording = try await decodeRecording(
            RecordingFixtures.json(extra: #""rating": "TV-MA", "ruleId": 12, "locked": true"#)
        )

        XCTAssertEqual(recording.rating, "TV-MA")
        XCTAssertEqual(recording.ruleId, 12)
        XCTAssertTrue(recording.locked)
        XCTAssertTrue(recording.isSeries)
    }

    func testLockedRecordingIsNotPlayable() {
        XCTAssertTrue(RecordingFixtures.recording(state: "ready").isPlayable)
        XCTAssertFalse(RecordingFixtures.recording(state: "ready", locked: true).isPlayable)
    }

    // MARK: - Skipped

    func testSkippedReadsSkipped() {
        let skipped = RecordingFixtures.recording(state: "failed", failure: "skipped")

        XCTAssertEqual(RecordingLogic.stateLabel(skipped), "Skipped")
        XCTAssertEqual(RecordingLogic.failureLabel("skipped"), "Skipped")
        XCTAssertEqual(RecordingLogic.badgeTone(skipped), .neutral)
        let detail = try? XCTUnwrap(RecordingLogic.detailLine(skipped))
        XCTAssertFalse(detail?.hasPrefix("Missed") ?? true, "a skipped episode wasn't missed")
    }

    func testOtherFailuresStillReadMissed() {
        let missed = RecordingFixtures.recording(state: "failed", failure: "noTuner")

        XCTAssertEqual(RecordingLogic.stateLabel(missed), "Missed")
        XCTAssertEqual(RecordingLogic.detailLine(missed), "Missed: no tuner was free")
    }

    // MARK: - Guide program lock

    func testGuideProgramDecodesRatingAndLock() async throws {
        let json = """
        [{"channelId":3,"guideNumber":"5.1","name":"KSTP","logoUrl":"","programs":[
          {"start":"2026-10-05T00:00:00Z","stop":"2026-10-05T01:00:00Z","title":"Late Show",
           "subtitle":"","description":"","category":"","rating":"TV-MA","locked":true,
           "seriesId":"SH01","programId":"EP01","isNew":true},
          {"start":"2026-10-05T01:00:00Z","stop":"2026-10-05T02:00:00Z","title":"News",
           "subtitle":"","description":"Local news.","category":""}
        ]}]
        """
        StubURLProtocol.handler = { _ in (200, Data(json.utf8), [:]) }
        let client = await makeClient()

        let programs = try await client.guide(start: Date(), stop: Date()).first?.programs ?? []

        XCTAssertEqual(programs.count, 2)
        XCTAssertEqual(programs[0].rating, "TV-MA")
        XCTAssertTrue(programs[0].isLocked)
        XCTAssertEqual(programs[0].isNew, true)
        XCTAssertEqual(programs[0].seriesId, "SH01")
        XCTAssertNil(programs[1].rating)
        XCTAssertFalse(programs[1].isLocked)
    }

    // MARK: - 403 parental

    func testParental403MapsToParentalError() async throws {
        StubURLProtocol.handler = { _ in
            (403, Data(#"{"error":"Blocked by parental controls (rated TV-MA)","code":"parental"}"#.utf8), [:])
        }
        let client = await makeClient()

        do {
            _ = try await client.createSession(
                channelId: 3,
                caps: ClientCaps(videoCodecs: ["h264"], audioCodecs: ["aac"], maxHeight: 1080, profile: "")
            )
            XCTFail("expected parental error")
        } catch {
            XCTAssertEqual(error as? BowtieError, .parental("Blocked by parental controls (rated TV-MA)"))
        }
    }

    func testOther403StaysServerError() async throws {
        StubURLProtocol.handler = { _ in (403, Data(#"{"error":"forbidden"}"#.utf8), [:]) }
        let client = await makeClient()

        do {
            try await client.deleteRecording(id: 1)
            XCTFail("expected an error")
        } catch {
            XCTAssertEqual(error as? BowtieError, .server(status: 403, message: "forbidden"))
        }
    }

    func testHeartbeatReportsParentalStop() async {
        StubURLProtocol.handler = { _ in
            (403, Data(#"{"error":"Blocked by parental controls (rated TV-14)","code":"parental"}"#.utf8), [:])
        }
        let client = await makeClient()

        let error = await client.heartbeat(viewerId: "v1", token: "tok")

        XCTAssertEqual(error, .parental("Blocked by parental controls (rated TV-14)"))
    }

    func testHeartbeatSuccessReportsNoError() async {
        StubURLProtocol.handler = { _ in (204, Data(), [:]) }
        let client = await makeClient()

        let error = await client.heartbeat(viewerId: "v1", token: "tok")

        XCTAssertNil(error)
    }

    @MainActor
    func testParentalErrorCopyIsTheServerMessage() {
        XCTAssertEqual(
            RecordingErrorCopy.message(for: BowtieError.parental("Blocked by parental controls (rated TV-MA)")),
            "Blocked by parental controls (rated TV-MA)"
        )
    }
}
