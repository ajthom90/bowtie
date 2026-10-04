import XCTest
@testable import BowtieKit

/// Favorites-first ordering, optimistic star toggles and the Recent row.
@MainActor
final class ChannelListModelFavoritesTests: XCTestCase {
    private var store: InMemorySessionStore!
    private let clock = TestFixtures.iso("2026-10-03T20:00:00Z")

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
        store = InMemorySessionStore()
    }

    override func tearDown() {
        StubURLProtocol.reset()
        super.tearDown()
    }

    /// Server order is text order on guide number ("11.1" before "4.1").
    private let lineup: [(id: Int64, number: String, name: String, favorite: Bool)] = [
        (1, "11.1", "KARE", true),
        (2, "2.1", "TPT", false),
        (3, "4.1", "WCCO", false),
        (4, "9.1", "FOX9", true),
    ]

    private func makeModel(
        channels: Data,
        recents: @escaping @Sendable () -> (Int, Data) = { (200, Data("[]".utf8)) },
        mutation: @escaping @Sendable (URLRequest) -> Int = { _ in 204 }
    ) async -> ChannelListModel {
        StubURLProtocol.handler = { request in
            switch (request.httpMethod ?? "GET", request.url?.path ?? "") {
            case ("GET", "/api/v1/channels"):
                return (200, channels, [:])
            case ("GET", "/api/v1/guide"):
                return (200, Data("[]".utf8), [:])
            case ("GET", "/api/v1/me/recents"):
                let (status, data) = recents()
                return (status, data, [:])
            default:
                return (mutation(request), Data(), [:])
            }
        }
        let client = BowtieClient(
            server: TestFixtures.baseURL,
            store: store,
            urlSession: TestFixtures.makeStubSession()
        )
        await client.setAccessTokenForTesting("access-1")
        let at = clock
        return ChannelListModel(client: client, now: { at })
    }

    private func loadedIDs(_ model: ChannelListModel) -> [Int64] {
        guard case .loaded(let rows) = model.state else { return [] }
        return rows.map(\.channel.id)
    }

    private func requests(_ method: String, _ path: String) -> [URLRequest] {
        StubURLProtocol.recorded.filter { $0.httpMethod == method && $0.url?.path == path }
    }

    // MARK: - Ordering

    func testFavoritesFirstInGuideNumberOrderThenServerOrder() async {
        let model = await makeModel(channels: TestFixtures.favoriteChannelJSON(lineup))
        await model.load()

        // Favorites 9.1, 11.1 (numeric), then the rest as the server sent them.
        XCTAssertEqual(loadedIDs(model), [4, 1, 2, 3])
        XCTAssertEqual(model.favoriteRows.map(\.channel.id), [4, 1])
        XCTAssertEqual(model.otherRows.map(\.channel.id), [2, 3])
        XCTAssertTrue(model.supportsFavorites)
    }

    func testGuideNumberOrderIsNumeric() {
        XCTAssertTrue(ChannelListModel.guideNumberPrecedes("9.1", "11.1"))
        XCTAssertTrue(ChannelListModel.guideNumberPrecedes("9.1", "9.2"))
        XCTAssertTrue(ChannelListModel.guideNumberPrecedes("9", "9.1"))
        XCTAssertTrue(ChannelListModel.guideNumberPrecedes("9.2", "9.10"))
        XCTAssertFalse(ChannelListModel.guideNumberPrecedes("11.1", "9.1"))
        XCTAssertFalse(ChannelListModel.guideNumberPrecedes("9.1", "9.1"))
    }

    func testOlderServerHasNoFavoritesSupportAndKeepsServerOrder() async {
        let model = await makeModel(
            channels: TestFixtures.channelJSON([(1, "11.1", "KARE"), (2, "2.1", "TPT")]),
            recents: { (404, Data()) }
        )
        await model.load()

        XCTAssertFalse(model.supportsFavorites)
        XCTAssertEqual(loadedIDs(model), [1, 2])
        XCTAssertTrue(model.favoriteRows.isEmpty)
        XCTAssertFalse(model.showsRecents)
    }

    // MARK: - Toggle

    func testToggleFavoriteIsOptimisticAndSendsPUT() async throws {
        let gate = DispatchSemaphore(value: 0)
        let model = await makeModel(
            channels: TestFixtures.favoriteChannelJSON(lineup),
            mutation: { _ in
                _ = gate.wait(timeout: .now() + 5)
                return 204
            }
        )
        await model.load()

        let toggle = Task { await model.toggleFavorite(channelId: 3) }
        try await waitUntil { !self.requests("PUT", "/api/v1/me/favorites/3").isEmpty }

        // Flipped and re-sorted before the server answered.
        XCTAssertEqual(model.favoriteRows.map(\.channel.id), [3, 4, 1])
        XCTAssertEqual(loadedIDs(model), [3, 4, 1, 2])

        gate.signal()
        await toggle.value
        XCTAssertEqual(loadedIDs(model), [3, 4, 1, 2])
        XCTAssertNil(model.actionError)
    }

    func testUnfavoriteSendsDELETEAndReturnsChannelToServerSlot() async throws {
        let model = await makeModel(channels: TestFixtures.favoriteChannelJSON(lineup))
        await model.load()

        await model.toggleFavorite(channelId: 1)

        XCTAssertEqual(requests("DELETE", "/api/v1/me/favorites/1").count, 1)
        XCTAssertEqual(model.favoriteRows.map(\.channel.id), [4])
        // 11.1 goes back to its server position, ahead of 2.1.
        XCTAssertEqual(model.otherRows.map(\.channel.id), [1, 2, 3])
        XCTAssertEqual(model.otherRows.first?.channel.favorite, false)
    }

    func testToggleFailureRevertsAndReportsError() async {
        let model = await makeModel(
            channels: TestFixtures.favoriteChannelJSON(lineup),
            mutation: { _ in 500 }
        )
        await model.load()

        await model.toggleFavorite(channelId: 2)

        XCTAssertEqual(loadedIDs(model), [4, 1, 2, 3], "reverted to the original order")
        XCTAssertEqual(model.otherRows.first?.channel.favorite, false)
        XCTAssertNotNil(model.actionError)

        model.dismissActionError()
        XCTAssertNil(model.actionError)
    }

    func testToggleUnknownChannelDoesNothing() async {
        let model = await makeModel(channels: TestFixtures.favoriteChannelJSON(lineup))
        await model.load()

        await model.toggleFavorite(channelId: 99)

        XCTAssertTrue(requests("PUT", "/api/v1/me/favorites/99").isEmpty)
        XCTAssertEqual(loadedIDs(model), [4, 1, 2, 3])
    }

    // MARK: - Recents

    func testLoadFetchesRecentsAndResolvesChannels() async {
        let model = await makeModel(
            channels: TestFixtures.favoriteChannelJSON(lineup),
            recents: {
                (200, TestFixtures.recentsJSON([
                    (3, "4.1", "WCCO", "2026-10-03T19:00:00Z"),
                    (4, "9.1", "FOX9", "2026-10-03T18:00:00Z"),
                ]))
            }
        )
        await model.load()

        let request = requests("GET", "/api/v1/me/recents").first
        let items = request.flatMap { URLComponents(url: $0.url!, resolvingAgainstBaseURL: false)?.queryItems }
        XCTAssertEqual(items, [URLQueryItem(name: "limit", value: "8")])

        XCTAssertEqual(model.recents.map(\.channelId), [3, 4])
        XCTAssertTrue(model.showsRecents)
        // Resolved through the loaded rows so they carry favorite/reception.
        XCTAssertEqual(model.recentChannels.map(\.id), [3, 4])
        XCTAssertEqual(model.recentChannels[1].favorite, true)
        XCTAssertEqual(model.recentChannels[0].reception, "ok")
    }

    func testRecentNotInListStillResolvesFromItsOwnFields() async {
        let model = await makeModel(
            channels: TestFixtures.favoriteChannelJSON(lineup),
            recents: { (200, TestFixtures.recentsJSON([(42, "5.1", "KSTP", "2026-10-03T19:00:00Z")])) }
        )
        await model.load()

        XCTAssertEqual(model.recentChannels.map(\.id), [42])
        XCTAssertEqual(model.recentChannels.first?.guideNumber, "5.1")
        XCTAssertEqual(model.recentChannels.first?.name, "KSTP")
    }

    func testRecents404IsUnsupportedNotAFailure() async {
        let model = await makeModel(
            channels: TestFixtures.favoriteChannelJSON(lineup),
            recents: { (404, Data()) }
        )
        await model.load()

        XCTAssertEqual(loadedIDs(model), [4, 1, 2, 3])
        XCTAssertFalse(model.recentsSupported)
        XCTAssertTrue(model.recents.isEmpty)
        XCTAssertFalse(model.showsRecents)
    }

    func testRecentsServerErrorKeepsListLoaded() async {
        let model = await makeModel(
            channels: TestFixtures.favoriteChannelJSON(lineup),
            recents: { (500, Data(#"{"error":"boom"}"#.utf8)) }
        )
        await model.load()

        XCTAssertEqual(loadedIDs(model), [4, 1, 2, 3])
        XCTAssertTrue(model.recentsSupported)
        XCTAssertTrue(model.recents.isEmpty)
        XCTAssertFalse(model.showsRecents)
    }

    func testEmptyRecentsHidesRow() async {
        let model = await makeModel(channels: TestFixtures.favoriteChannelJSON(lineup))
        await model.load()
        XCTAssertTrue(model.recentsSupported)
        XCTAssertFalse(model.showsRecents)
    }

    func testOlderServerIgnoresRecentsEvenIfReturned() async {
        let model = await makeModel(
            channels: TestFixtures.channelJSON([(1, "11.1", "KARE")]),
            recents: { (200, TestFixtures.recentsJSON([(1, "11.1", "KARE", "2026-10-03T19:00:00Z")])) }
        )
        await model.load()
        XCTAssertFalse(model.showsRecents)
        XCTAssertTrue(model.recentChannels.isEmpty)
    }

    func testRefreshRecentsReloadsOnlyRecents() async {
        final class Box: @unchecked Sendable {
            var data = TestFixtures.recentsJSON([])
        }
        let box = Box()
        let model = await makeModel(
            channels: TestFixtures.favoriteChannelJSON(lineup),
            recents: { (200, box.data) }
        )
        await model.load()
        XCTAssertFalse(model.showsRecents)
        let channelCalls = requests("GET", "/api/v1/channels").count

        box.data = TestFixtures.recentsJSON([(2, "2.1", "TPT", "2026-10-03T19:59:00Z")])
        await model.refreshRecents()

        XCTAssertEqual(model.recents.map(\.channelId), [2])
        XCTAssertTrue(model.showsRecents)
        XCTAssertEqual(requests("GET", "/api/v1/channels").count, channelCalls)
    }

    func testClearRecentsEmptiesRowAndSendsDELETE() async {
        let model = await makeModel(
            channels: TestFixtures.favoriteChannelJSON(lineup),
            recents: { (200, TestFixtures.recentsJSON([(2, "2.1", "TPT", "2026-10-03T19:59:00Z")])) }
        )
        await model.load()
        XCTAssertTrue(model.showsRecents)

        await model.clearRecents()

        XCTAssertEqual(requests("DELETE", "/api/v1/me/recents").count, 1)
        XCTAssertTrue(model.recents.isEmpty)
        XCTAssertFalse(model.showsRecents)
        XCTAssertNil(model.actionError)
    }

    func testClearRecentsFailureRestoresRowAndReportsError() async {
        let model = await makeModel(
            channels: TestFixtures.favoriteChannelJSON(lineup),
            recents: { (200, TestFixtures.recentsJSON([(2, "2.1", "TPT", "2026-10-03T19:59:00Z")])) },
            mutation: { _ in 500 }
        )
        await model.load()

        await model.clearRecents()

        XCTAssertEqual(model.recents.map(\.channelId), [2])
        XCTAssertNotNil(model.actionError)
    }

    // MARK: - Helpers

    private func waitUntil(
        timeout: TimeInterval = 2,
        _ condition: @escaping () -> Bool
    ) async throws {
        let deadline = Date().addingTimeInterval(timeout)
        while !condition() {
            if Date() > deadline {
                XCTFail("timed out waiting for condition")
                return
            }
            try await Task.sleep(for: .milliseconds(5))
        }
    }
}
