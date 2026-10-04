import XCTest
@testable import BowtieKit

final class ContinueWatchingTests: XCTestCase {
    private func rec(
        id: Int64,
        state: String = "ready",
        durationSec: Int = 3600,
        positionSec: Int = 600,
        locked: Bool = false,
        start: String = "2026-10-05T00:00:00Z",
        updated: String? = nil
    ) -> Recording {
        RecordingFixtures.recording(
            id: id,
            state: state,
            durationSec: durationSec,
            positionSec: positionSec,
            locked: locked,
            start: start,
            positionUpdatedAt: updated.map(TestFixtures.iso)
        )
    }

    private func decode(_ json: String) throws -> Recording {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return try decoder.decode(Recording.self, from: Data(json.utf8))
    }

    // MARK: - Decoding

    func testDecodesPositionUpdatedAtWhenPresent() throws {
        let rec = try decode(RecordingFixtures.json(positionSec: 412, extra: #""positionUpdatedAt": "2026-10-06T01:02:03Z""#))
        XCTAssertEqual(rec.positionUpdatedAt, TestFixtures.iso("2026-10-06T01:02:03Z"))
    }

    func testPositionUpdatedAtIsNilWhenOmitted() throws {
        XCTAssertNil(try decode(RecordingFixtures.json()).positionUpdatedAt)
    }

    func testPositionUpdatedAtIsNilWhenNull() throws {
        XCTAssertNil(try decode(RecordingFixtures.json(extra: #""positionUpdatedAt": null"#)).positionUpdatedAt)
    }

    // MARK: - Eligibility

    func testNeedsAtLeastAMinuteWatched() {
        XCTAssertFalse(ContinueWatching.isEligible(rec(id: 1, positionSec: 59)))
        XCTAssertTrue(ContinueWatching.isEligible(rec(id: 1, positionSec: 60)))
    }

    func testExcludesTheLastTwoMinutes() {
        XCTAssertTrue(ContinueWatching.isEligible(rec(id: 1, durationSec: 3600, positionSec: 3479)))
        XCTAssertFalse(ContinueWatching.isEligible(rec(id: 1, durationSec: 3600, positionSec: 3480)))
        XCTAssertFalse(ContinueWatching.isEligible(rec(id: 1, durationSec: 3600, positionSec: 3600)))
    }

    func testExcludesUnknownDuration() {
        XCTAssertFalse(ContinueWatching.isEligible(rec(id: 1, durationSec: 0, positionSec: 600)))
    }

    func testOnlyReadyAndUnlocked() {
        for state in ["scheduled", "waiting", "recording", "converting", "failed"] {
            XCTAssertFalse(ContinueWatching.isEligible(rec(id: 1, state: state)), state)
        }
        XCTAssertFalse(ContinueWatching.isEligible(rec(id: 1, locked: true)))
        XCTAssertTrue(ContinueWatching.isEligible(rec(id: 1)))
    }

    // MARK: - Ordering

    func testSortsByLastSavedNewestFirstThenUnsavedByStart() {
        let rows = [
            rec(id: 1, start: "2026-10-01T00:00:00Z"),
            rec(id: 2, start: "2026-10-03T00:00:00Z", updated: "2026-10-04T10:00:00Z"),
            rec(id: 3, start: "2026-10-02T00:00:00Z"),
            rec(id: 4, start: "2026-10-01T00:00:00Z", updated: "2026-10-04T12:00:00Z"),
            rec(id: 5, positionSec: 0, updated: "2026-10-04T13:00:00Z"),
            rec(id: 6, locked: true, updated: "2026-10-04T14:00:00Z"),
        ]
        XCTAssertEqual(ContinueWatching.items(from: rows).map(\.id), [4, 2, 3, 1])
    }

    func testSameSaveTimeFallsBackToStart() {
        let rows = [
            rec(id: 1, start: "2026-10-01T00:00:00Z", updated: "2026-10-04T10:00:00Z"),
            rec(id: 2, start: "2026-10-02T00:00:00Z", updated: "2026-10-04T10:00:00Z"),
        ]
        XCTAssertEqual(ContinueWatching.items(from: rows).map(\.id), [2, 1])
    }

    func testCapsAtTen() {
        let rows = (1...15).map { i in
            rec(id: Int64(i), updated: String(format: "2026-10-04T10:%02d:00Z", i))
        }
        let items = ContinueWatching.items(from: rows)
        XCTAssertEqual(ContinueWatching.maxItems, 10)
        XCTAssertEqual(items.count, 10)
        XCTAssertEqual(items.first?.id, 15)
        XCTAssertEqual(items.last?.id, 6)
    }

    func testNothingEligibleIsEmpty() {
        XCTAssertEqual(ContinueWatching.items(from: [rec(id: 1, positionSec: 0)]), [])
        XCTAssertEqual(ContinueWatching.items(from: []), [])
    }

    // MARK: - Copy

    func testRemainingText() {
        XCTAssertEqual(ContinueWatching.remainingText(rec(id: 1, durationSec: 3600, positionSec: 3541)), "less than a minute left")
        XCTAssertEqual(ContinueWatching.remainingText(rec(id: 1, durationSec: 3600, positionSec: 3540)), "1 min left")
        XCTAssertEqual(ContinueWatching.remainingText(rec(id: 1, durationSec: 1980, positionSec: 600)), "23 min left")
        XCTAssertEqual(ContinueWatching.remainingText(rec(id: 1, durationSec: 3600, positionSec: 1)), "59 min left")
        XCTAssertEqual(ContinueWatching.remainingText(rec(id: 1, durationSec: 3660, positionSec: 60)), "1 hr left")
        XCTAssertEqual(ContinueWatching.remainingText(rec(id: 1, durationSec: 7200, positionSec: 3300)), "1 hr 5 min left")
        XCTAssertEqual(ContinueWatching.remainingText(rec(id: 1, durationSec: 600, positionSec: 900)), "less than a minute left")
    }

    func testProgress() {
        XCTAssertEqual(ContinueWatching.progress(rec(id: 1, durationSec: 3600, positionSec: 900)), 0.25, accuracy: 0.0001)
        XCTAssertEqual(ContinueWatching.progress(rec(id: 1, durationSec: 0, positionSec: 900)), 0)
        XCTAssertEqual(ContinueWatching.progress(rec(id: 1, durationSec: 600, positionSec: 900)), 1)
    }
}

@MainActor
final class ContinueWatchingModelTests: XCTestCase {
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

    func testLoadAsksForRecordedAndKeepsOnlyInProgress() async {
        StubURLProtocol.handler = { _ in
            (200, RecordingFixtures.listJSON([
                RecordingFixtures.json(id: 1, positionSec: 0),
                RecordingFixtures.json(id: 2, positionSec: 600, extra: #""positionUpdatedAt": "2026-10-06T01:00:00Z""#),
                RecordingFixtures.json(id: 3, positionSec: 300, extra: #""positionUpdatedAt": "2026-10-06T02:00:00.5Z""#),
                RecordingFixtures.json(id: 4, state: "recording", positionSec: 300),
            ]), [:])
        }
        let model = ContinueWatchingModel(client: await makeClient())
        XCTAssertEqual(model.items, [])

        await model.load()

        XCTAssertEqual(model.items.map(\.id), [3, 2])
        XCTAssertEqual(StubURLProtocol.recorded.first?.url?.query, "state=recorded")
    }

    func testLoadFailureKeepsWhatWasShown() async {
        StubURLProtocol.handler = { _ in
            (200, RecordingFixtures.listJSON([RecordingFixtures.json(id: 2, positionSec: 600)]), [:])
        }
        let model = ContinueWatchingModel(client: await makeClient())
        await model.load()
        StubURLProtocol.handler = { _ in (503, Data(#"{"error":"recording is not available"}"#.utf8), [:]) }

        await model.load()

        XCTAssertEqual(model.items.map(\.id), [2])
    }

    func testRemoveResetsPositionToZeroAndDropsIt() async throws {
        StubURLProtocol.handler = { request in
            if request.httpMethod == "PUT" {
                return (204, Data(), [:])
            }
            return (200, RecordingFixtures.listJSON([
                RecordingFixtures.json(id: 2, positionSec: 600),
                RecordingFixtures.json(id: 5, positionSec: 700),
            ]), [:])
        }
        let model = ContinueWatchingModel(client: await makeClient())
        await model.load()
        let target = try XCTUnwrap(model.items.first { $0.id == 2 })

        await model.remove(target)

        XCTAssertEqual(model.items.map(\.id), [5])
        XCTAssertNil(model.actionError)
        let put = try XCTUnwrap(StubURLProtocol.recorded.last)
        XCTAssertEqual(put.httpMethod, "PUT")
        XCTAssertEqual(put.url?.path, "/api/v1/recordings/2/position")
        let body = try XCTUnwrap(put.httpBody ?? put.httpBodyStream.map(Self.read))
        let json = try JSONSerialization.jsonObject(with: body) as? [String: Any]
        XCTAssertEqual(json?["positionSec"] as? Int, 0)
    }

    func testRemoveFailureKeepsItAndExplains() async throws {
        StubURLProtocol.handler = { request in
            if request.httpMethod == "PUT" {
                return (404, Data(#"{"error":"not found"}"#.utf8), [:])
            }
            return (200, RecordingFixtures.listJSON([RecordingFixtures.json(id: 2, positionSec: 600)]), [:])
        }
        let model = ContinueWatchingModel(client: await makeClient())
        await model.load()

        await model.remove(try XCTUnwrap(model.items.first))

        XCTAssertEqual(model.items.map(\.id), [2])
        XCTAssertEqual(model.actionError, "That recording is gone.")
    }

    private static func read(_ stream: InputStream) -> Data {
        stream.open()
        defer { stream.close() }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 1024)
        while stream.hasBytesAvailable {
            let n = stream.read(&buffer, maxLength: buffer.count)
            if n <= 0 { break }
            data.append(buffer, count: n)
        }
        return data
    }
}
