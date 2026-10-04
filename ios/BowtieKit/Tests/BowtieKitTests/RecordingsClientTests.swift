import XCTest
@testable import BowtieKit

final class RecordingsClientTests: XCTestCase {
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

    // MARK: - List

    func testListSendsStateFilterWithBearer() async throws {
        StubURLProtocol.handler = { _ in
            (200, RecordingFixtures.listJSON([RecordingFixtures.json()]), [:])
        }
        let client = await makeClient()

        let recs = try await client.recordings(filter: .failed)

        XCTAssertEqual(recs.count, 1)
        XCTAssertEqual(recs.first?.title, "Jeopardy!")
        let req = try XCTUnwrap(StubURLProtocol.recorded.first)
        XCTAssertEqual(req.httpMethod, "GET")
        XCTAssertEqual(req.url?.path, "/api/v1/recordings")
        XCTAssertEqual(req.url?.query, "state=failed")
        XCTAssertEqual(req.value(forHTTPHeaderField: "Authorization"), "Bearer access-1")
    }

    func testListWithoutFilterHasNoQuery() async throws {
        StubURLProtocol.handler = { _ in (200, Data("[]".utf8), [:]) }
        let client = await makeClient()

        let recs = try await client.recordings(filter: nil)

        XCTAssertTrue(recs.isEmpty)
        XCTAssertNil(StubURLProtocol.recorded.first?.url?.query)
    }

    // MARK: - Schedule

    func testScheduleSendsChannelAndISOProgramStart() async throws {
        let body = """
        {"recording":\(RecordingFixtures.json(state: "scheduled")),
         "warnings":[{"code":"usesAllTuners","message":"If Plex is using a tuner then, this may not record."}]}
        """
        StubURLProtocol.handler = { _ in (201, Data(body.utf8), [:]) }
        let client = await makeClient()

        let result = try await client.scheduleRecording(
            channelId: 3,
            programStart: TestFixtures.iso("2026-10-05T00:00:00Z")
        )

        XCTAssertEqual(result.recording.status, .scheduled)
        XCTAssertEqual(result.warnings, [
            RecordingWarning(code: "usesAllTuners", message: "If Plex is using a tuner then, this may not record."),
        ])
        let req = try XCTUnwrap(StubURLProtocol.recorded.first)
        XCTAssertEqual(req.httpMethod, "POST")
        XCTAssertEqual(req.url?.path, "/api/v1/recordings")
        let json = try jsonBody(of: req)
        XCTAssertEqual(json["channelId"] as? Int, 3)
        XCTAssertEqual(json["programStart"] as? String, "2026-10-05T00:00:00Z")
        XCTAssertNil(json["force"], "force is omitted unless asked for")
    }

    func testScheduleForceSendsTrue() async throws {
        let body = #"{"recording":\#(RecordingFixtures.json(state: "scheduled")),"warnings":[]}"#
        StubURLProtocol.handler = { _ in (201, Data(body.utf8), [:]) }
        let client = await makeClient()

        _ = try await client.scheduleRecording(
            channelId: 3,
            programStart: TestFixtures.iso("2026-10-05T00:00:00Z"),
            force: true
        )

        let json = try jsonBody(of: try XCTUnwrap(StubURLProtocol.recorded.first))
        XCTAssertEqual(json["force"] as? Bool, true)
    }

    func testScheduleConflictMapsTo409Error() async throws {
        let body = """
        {"error":"Only 2 tuners: other recordings already need them then.","tunerCount":2,
         "conflicts":[\(RecordingFixtures.json(id: 1, title: "A", state: "scheduled")),
                      \(RecordingFixtures.json(id: 2, title: "B", state: "scheduled"))]}
        """
        StubURLProtocol.handler = { _ in (409, Data(body.utf8), [:]) }
        let client = await makeClient()

        do {
            _ = try await client.scheduleRecording(channelId: 3, programStart: TestFixtures.iso("2026-10-05T00:00:00Z"))
            XCTFail("expected conflict")
        } catch let BowtieError.recordingConflict(tunerCount, conflicts, message) {
            XCTAssertEqual(tunerCount, 2)
            XCTAssertEqual(conflicts.map(\.title), ["A", "B"])
            XCTAssertEqual(message, "Only 2 tuners: other recordings already need them then.")
        }
    }

    func testScheduleUnavailable503IsServerError() async throws {
        StubURLProtocol.handler = { _ in (503, Data(#"{"error":"recording is not available"}"#.utf8), [:]) }
        let client = await makeClient()

        do {
            _ = try await client.scheduleRecording(channelId: 3, programStart: Date())
            XCTFail("expected error")
        } catch {
            XCTAssertEqual(error as? BowtieError, .server(status: 503, message: "recording is not available"))
        }
    }

    // MARK: - Manage

    func testDeleteStopAndProtect() async throws {
        StubURLProtocol.handler = { request in
            if request.httpMethod == "PATCH" {
                return (200, Data(RecordingFixtures.json(protected: true).utf8), [:])
            }
            return (204, Data(), [:])
        }
        let client = await makeClient()

        try await client.deleteRecording(id: 7)
        try await client.stopRecording(id: 7)
        let updated = try await client.setRecordingProtected(id: 7, protected: true)

        XCTAssertTrue(updated.protected)
        let reqs = StubURLProtocol.recorded
        XCTAssertEqual(reqs.count, 3)
        XCTAssertEqual(reqs[0].httpMethod, "DELETE")
        XCTAssertEqual(reqs[0].url?.path, "/api/v1/recordings/7")
        XCTAssertEqual(reqs[1].httpMethod, "POST")
        XCTAssertEqual(reqs[1].url?.path, "/api/v1/recordings/7/stop")
        XCTAssertEqual(reqs[2].httpMethod, "PATCH")
        XCTAssertEqual(reqs[2].url?.path, "/api/v1/recordings/7")
        let json = try jsonBody(of: reqs[2])
        XCTAssertEqual(json["protected"] as? Bool, true)
        XCTAssertEqual(Set(json.keys), ["protected"])
    }

    func testForbiddenIsServer403() async throws {
        StubURLProtocol.handler = { _ in
            (403, Data(#"{"error":"only the person who scheduled it (or an admin) can change this recording"}"#.utf8), [:])
        }
        let client = await makeClient()

        do {
            try await client.deleteRecording(id: 7)
            XCTFail("expected 403")
        } catch let BowtieError.server(status, _) {
            XCTAssertEqual(status, 403)
        }
    }

    // MARK: - Play + position

    func testPlayDecodesPlaybackAndExposesServer() async throws {
        let body = #"{"playlistUrl":"/api/v1/recordings/7/hls/index.m3u8?token=abc","positionSec":412,"durationSec":1980}"#
        StubURLProtocol.handler = { _ in (200, Data(body.utf8), [:]) }
        let client = await makeClient()

        let play = try await client.playRecording(id: 7)

        XCTAssertEqual(play, RecordingPlayback(
            playlistUrl: "/api/v1/recordings/7/hls/index.m3u8?token=abc",
            positionSec: 412,
            durationSec: 1980
        ))
        let req = try XCTUnwrap(StubURLProtocol.recorded.first)
        XCTAssertEqual(req.httpMethod, "POST")
        XCTAssertEqual(req.url?.path, "/api/v1/recordings/7/play")
        XCTAssertEqual(client.serverURL, TestFixtures.baseURL)
    }

    func testPlayNotReady409IsPlainServerError() async throws {
        StubURLProtocol.handler = { _ in (409, Data(#"{"error":"recording is not ready yet"}"#.utf8), [:]) }
        let client = await makeClient()

        do {
            _ = try await client.playRecording(id: 7)
            XCTFail("expected 409")
        } catch {
            XCTAssertEqual(error as? BowtieError, .server(status: 409, message: "recording is not ready yet"))
        }
    }

    func testSavePositionPutsSeconds() async throws {
        StubURLProtocol.handler = { _ in (204, Data(), [:]) }
        let client = await makeClient()

        try await client.saveRecordingPosition(id: 7, positionSec: 615)

        let req = try XCTUnwrap(StubURLProtocol.recorded.first)
        XCTAssertEqual(req.httpMethod, "PUT")
        XCTAssertEqual(req.url?.path, "/api/v1/recordings/7/position")
        let json = try jsonBody(of: req)
        XCTAssertEqual(json["positionSec"] as? Int, 615)
    }
}
