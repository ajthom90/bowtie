import XCTest
@testable import BowtieKit

/// `GET /api/v1/guide/search` and the search screen's model.
@MainActor
final class GuideSearchTests: XCTestCase {
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

    nonisolated static func hitJSON(
        channelId: Int64 = 3,
        title: String = "Jeopardy!",
        start: String = "2026-10-05T00:00:00Z",
        stop: String = "2026-10-05T00:30:00Z",
        extra: String = ""
    ) -> String {
        """
        {"channelId":\(channelId),"guideNumber":"5.1","channelName":"KSTP","logoUrl":"",
         "start":"\(start)","stop":"\(stop)","title":"\(title)","subtitle":"Teachers Tournament",
         "description":"Quiz show.","category":"Game show"\(extra.isEmpty ? "" : "," + extra)}
        """
    }

    nonisolated private static func list(_ items: [String]) -> Data {
        "[\(items.joined(separator: ","))]".data(using: .utf8)!
    }

    // MARK: - Client

    func testSearchSendsQueryAndDecodesHits() async throws {
        StubURLProtocol.handler = { _ in
            (200, Self.list([
                Self.hitJSON(extra: #""rating":"TV-PG","locked":false,"recording":{"id":9,"state":"scheduled"}"#),
                Self.hitJSON(channelId: 4, title: "Late Show", extra: #""rating":"TV-MA","locked":true"#),
            ]), [:])
        }
        let client = await makeClient()

        let hits = try await client.searchGuide(query: "jeop & co", limit: 25)

        XCTAssertEqual(hits.count, 2)
        XCTAssertEqual(hits[0].title, "Jeopardy!")
        XCTAssertEqual(hits[0].channelName, "KSTP")
        XCTAssertEqual(hits[0].recording, RecordingMark(id: 9, state: "scheduled"))
        XCTAssertFalse(hits[0].isLocked)
        XCTAssertTrue(hits[1].isLocked)
        XCTAssertEqual(hits[1].rating, "TV-MA")

        let request = try XCTUnwrap(StubURLProtocol.recorded.first)
        XCTAssertEqual(request.url?.path, "/api/v1/guide/search")
        let items = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems ?? []
        XCTAssertEqual(items.first { $0.name == "q" }?.value, "jeop & co")
        XCTAssertEqual(items.first { $0.name == "limit" }?.value, "25")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer access-1")
    }

    func testHitBuildsChannelAndProgramForWatchAndRecord() throws {
        let data = Data(Self.hitJSON(extra: #""rating":"TV-PG","locked":true"#).utf8)
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        let hit = try decoder.decode(GuideSearchResult.self, from: data)

        XCTAssertEqual(hit.channel.id, 3)
        XCTAssertEqual(hit.channel.guideNumber, "5.1")
        XCTAssertEqual(hit.channel.name, "KSTP")
        XCTAssertEqual(hit.program.start, TestFixtures.iso("2026-10-05T00:00:00Z"))
        XCTAssertEqual(hit.program.title, "Jeopardy!")
        XCTAssertEqual(hit.program.rating, "TV-PG")
        XCTAssertTrue(hit.program.isLocked)

        XCTAssertFalse(hit.isOnNow(at: TestFixtures.iso("2026-10-04T23:59:59Z")))
        XCTAssertTrue(hit.isOnNow(at: TestFixtures.iso("2026-10-05T00:00:00Z")))
        XCTAssertTrue(hit.isOnNow(at: TestFixtures.iso("2026-10-05T00:29:59Z")))
        XCTAssertFalse(hit.isOnNow(at: TestFixtures.iso("2026-10-05T00:30:00Z")))
    }

    func testCanWatchOnlyWhenOnNowAndTheChannelCanStart() throws {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        let onNow = TestFixtures.iso("2026-10-05T00:10:00Z")
        // Older servers send no watchable: an on-now result stays watchable.
        let noField = try decoder.decode(GuideSearchResult.self, from: Data(Self.hitJSON().utf8))
        let busy = try decoder.decode(GuideSearchResult.self, from: Data(Self.hitJSON(extra: "\"watchable\":false").utf8))
        let free = try decoder.decode(GuideSearchResult.self, from: Data(Self.hitJSON(extra: "\"watchable\":true").utf8))
        XCTAssertTrue(noField.canWatch(at: onNow))
        XCTAssertFalse(busy.canWatch(at: onNow), "all tuners busy: no Watch")
        XCTAssertTrue(free.canWatch(at: onNow))
        XCTAssertFalse(free.canWatch(at: TestFixtures.iso("2026-10-05T01:00:00Z")), "over: no Watch")
    }

    func testResultIdsAreUniquePerChannelAndStart() throws {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        let a = try decoder.decode(GuideSearchResult.self, from: Data(Self.hitJSON(channelId: 3).utf8))
        let b = try decoder.decode(GuideSearchResult.self, from: Data(Self.hitJSON(channelId: 4).utf8))
        let c = try decoder.decode(
            GuideSearchResult.self,
            from: Data(Self.hitJSON(channelId: 3, start: "2026-10-05T01:00:00Z", stop: "2026-10-05T01:30:00Z").utf8)
        )
        XCTAssertEqual(Set([a.id, b.id, c.id]).count, 3)
    }

    // MARK: - Model

    func testBlankQueryIsIdleWithoutARequest() async {
        let model = GuideSearchModel(client: await makeClient())

        await model.search("   ")

        XCTAssertEqual(model.state, .idle)
        XCTAssertTrue(StubURLProtocol.recorded.isEmpty)
    }

    func testResultsAndEmpty() async {
        StubURLProtocol.handler = { request in
            let q = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?
                .queryItems?.first { $0.name == "q" }?.value
            return (200, q == "news" ? Self.list([Self.hitJSON(title: "News")]) : Data("[]".utf8), [:])
        }
        let model = GuideSearchModel(client: await makeClient())

        await model.search(" news ")
        XCTAssertEqual(model.results.map(\.title), ["News"])
        XCTAssertEqual(
            URLComponents(url: StubURLProtocol.recorded.last!.url!, resolvingAgainstBaseURL: false)?
                .queryItems?.first { $0.name == "q" }?.value,
            "news",
            "the query is trimmed"
        )

        await model.search("zzz")
        XCTAssertEqual(model.state, .empty)
        XCTAssertTrue(model.results.isEmpty)
    }

    func testFailureShowsMessage() async {
        StubURLProtocol.handler = { _ in (500, Data(#"{"error":"search failed"}"#.utf8), [:]) }
        let model = GuideSearchModel(client: await makeClient())

        await model.search("news")

        XCTAssertEqual(model.state, .failed("Search failed"))
    }

    func testStaleResponseDoesNotReplaceNewerResults() async {
        StubURLProtocol.handler = { request in
            let q = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?
                .queryItems?.first { $0.name == "q" }?.value ?? ""
            return (200, Self.list([Self.hitJSON(title: q)]), [:])
        }
        StubURLProtocol.delay = { request in
            request.url?.query?.contains("q=slow") == true ? 0.3 : 0
        }
        let model = GuideSearchModel(client: await makeClient())

        async let slow: Void = model.search("slow")
        try? await Task.sleep(for: .milliseconds(50))
        await model.search("fast")
        await slow

        XCTAssertEqual(model.results.map(\.title), ["fast"])
    }

    func testRefreshRepeatsTheLastQuery() async {
        StubURLProtocol.handler = { _ in (200, Self.list([Self.hitJSON()]), [:]) }
        let model = GuideSearchModel(client: await makeClient())

        await model.search("jeopardy")
        await model.refresh()

        XCTAssertEqual(StubURLProtocol.recorded.count, 2)
        XCTAssertEqual(model.query, "jeopardy")
    }

    func testClearReturnsToIdle() async {
        StubURLProtocol.handler = { _ in (200, Self.list([Self.hitJSON()]), [:]) }
        let model = GuideSearchModel(client: await makeClient())

        await model.search("jeopardy")
        await model.search("")

        XCTAssertEqual(model.state, .idle)
        XCTAssertTrue(model.results.isEmpty)
    }
}
