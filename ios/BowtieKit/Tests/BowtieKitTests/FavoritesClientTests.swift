import XCTest
@testable import BowtieKit

/// Favorites / Recents wire contract (`/api/v1/me/favorites`, `/api/v1/me/recents`).
final class FavoritesClientTests: XCTestCase {
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

    // MARK: - Models

    func testChannelDecodesFavorite() throws {
        let decoded = try JSONDecoder().decode(
            [Channel].self,
            from: TestFixtures.favoriteChannelJSON([(7, "9.1", "FOX9", true), (8, "11.1", "KARE", false)])
        )
        XCTAssertEqual(decoded[0].favorite, true)
        XCTAssertEqual(decoded[1].favorite, false)
    }

    func testChannelWithoutFavoriteIsNil() throws {
        // Older servers omit the field.
        let decoded = try JSONDecoder().decode(
            [Channel].self,
            from: TestFixtures.channelJSON([(7, "9.1", "FOX9")])
        )
        XCTAssertNil(decoded[0].favorite)
    }

    func testGuideChannelDecodesFavorite() throws {
        let json = """
        [{"channelId":7,"guideNumber":"9.1","name":"FOX9","logoUrl":"","favorite":true,"programs":[]},
         {"channelId":8,"guideNumber":"11.1","name":"KARE","logoUrl":"","programs":[]}]
        """.data(using: .utf8)!
        let decoded = try JSONDecoder().decode([GuideChannel].self, from: json)
        XCTAssertEqual(decoded[0].favorite, true)
        XCTAssertNil(decoded[1].favorite)
    }

    // MARK: - setFavorite

    func testSetFavoriteOnSendsPUT() async throws {
        StubURLProtocol.handler = { _ in (204, Data(), [:]) }
        let client = await makeClient()

        try await client.setFavorite(channelId: 7, on: true)

        let request = try XCTUnwrap(StubURLProtocol.recorded.first)
        XCTAssertEqual(request.httpMethod, "PUT")
        XCTAssertEqual(request.url?.path, "/api/v1/me/favorites/7")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer access-1")
    }

    func testSetFavoriteOffSendsDELETE() async throws {
        StubURLProtocol.handler = { _ in (204, Data(), [:]) }
        let client = await makeClient()

        try await client.setFavorite(channelId: 7, on: false)

        let request = try XCTUnwrap(StubURLProtocol.recorded.first)
        XCTAssertEqual(request.httpMethod, "DELETE")
        XCTAssertEqual(request.url?.path, "/api/v1/me/favorites/7")
    }

    func testSetFavoriteUnknownChannelThrowsNotFound() async throws {
        StubURLProtocol.handler = { _ in (404, #"{"error":"channel not found"}"#.data(using: .utf8)!, [:]) }
        let client = await makeClient()

        do {
            try await client.setFavorite(channelId: 99, on: true)
            XCTFail("expected notFound")
        } catch BowtieError.notFound {
            // expected
        }
    }

    // MARK: - recents

    func testRecentsRequestAndDecode() async throws {
        StubURLProtocol.handler = { _ in
            (200, TestFixtures.recentsJSON([
                (7, "9.1", "FOX9", "2026-10-03T19:42:10Z"),
                (8, "11.1", "KARE", "2026-10-03T18:00:00Z"),
            ]), [:])
        }
        let client = await makeClient()

        let recents = try await client.recents(limit: 8)

        let request = try XCTUnwrap(StubURLProtocol.recorded.first)
        XCTAssertEqual(request.httpMethod, "GET")
        XCTAssertEqual(request.url?.path, "/api/v1/me/recents")
        let items = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems ?? []
        XCTAssertEqual(items, [URLQueryItem(name: "limit", value: "8")])

        XCTAssertEqual(recents.map(\.channelId), [7, 8])
        XCTAssertEqual(recents[0].guideNumber, "9.1")
        XCTAssertEqual(recents[0].name, "FOX9")
        XCTAssertEqual(recents[0].watchedAt, TestFixtures.iso("2026-10-03T19:42:10Z"))
        XCTAssertEqual(recents[0].id, 7)
    }

    func testRecentsToleratesFractionalSeconds() async throws {
        StubURLProtocol.handler = { _ in
            (200, TestFixtures.recentsJSON([(7, "9.1", "FOX9", "2026-10-03T19:42:10.123456Z")]), [:])
        }
        let client = await makeClient()

        let recents = try await client.recents(limit: 8)
        XCTAssertEqual(
            recents[0].watchedAt.timeIntervalSince1970,
            TestFixtures.iso("2026-10-03T19:42:10Z").timeIntervalSince1970 + 0.123456,
            accuracy: 0.001
        )
    }

    func testRecentsOnOlderServerThrowsNotFound() async throws {
        StubURLProtocol.handler = { _ in (404, Data(), [:]) }
        let client = await makeClient()

        do {
            _ = try await client.recents(limit: 8)
            XCTFail("expected notFound")
        } catch BowtieError.notFound {
            // expected
        }
    }

    func testClearRecentsSendsDELETE() async throws {
        StubURLProtocol.handler = { _ in (204, Data(), [:]) }
        let client = await makeClient()

        try await client.clearRecents()

        let request = try XCTUnwrap(StubURLProtocol.recorded.first)
        XCTAssertEqual(request.httpMethod, "DELETE")
        XCTAssertEqual(request.url?.path, "/api/v1/me/recents")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer access-1")
    }
}
