import XCTest
@testable import BowtieKit

@MainActor
final class ChannelListModelTests: XCTestCase {
    private var store: InMemorySessionStore!
    private var clock: Date!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
        store = InMemorySessionStore()
        clock = TestFixtures.iso("2024-06-15T20:30:00Z")
    }

    override func tearDown() {
        StubURLProtocol.reset()
        super.tearDown()
    }

    private func makeClient() -> BowtieClient {
        let client = BowtieClient(
            server: TestFixtures.baseURL,
            store: store,
            urlSession: TestFixtures.makeStubSession()
        )
        return client
    }

    private func seedAccess(_ client: BowtieClient) async {
        await client.setAccessTokenForTesting("access-1")
    }

    // MARK: - Join logic

    func testLoadJoinsChannelsWithGuideNowNext() async throws {
        let channelsData = TestFixtures.channelJSON([
            (1, "4.1", "WABC"),
            (2, "7.1", "WXYZ"),
        ])
        let guideData = TestFixtures.guideJSON([
            (
                channelId: 1,
                number: "4.1",
                name: "WABC",
                programs: [
                    (start: "2024-06-15T20:00:00Z", stop: "2024-06-15T21:00:00Z", title: "News"),
                    (start: "2024-06-15T21:00:00Z", stop: "2024-06-15T22:00:00Z", title: "Drama"),
                ]
            ),
            // channel 2 has no guide entry
        ])

        StubURLProtocol.handler = { request in
            let path = request.url?.path ?? ""
            if path == "/api/v1/channels" {
                return (200, channelsData, [:])
            }
            if path == "/api/v1/guide" {
                return (200, guideData, [:])
            }
            return (500, Data(), [:])
        }

        let client = makeClient()
        await seedAccess(client)
        let model = ChannelListModel(client: client, now: { [weak self] in self?.clock ?? Date() })

        await model.load()

        guard case .loaded(let rows) = model.state else {
            return XCTFail("expected loaded, got \(model.state)")
        }
        XCTAssertEqual(rows.count, 2)

        XCTAssertEqual(rows[0].channel.id, 1)
        XCTAssertEqual(rows[0].channel.name, "WABC")
        XCTAssertEqual(rows[0].nowNext.now?.title, "News")
        XCTAssertEqual(rows[0].nowNext.next?.title, "Drama")
        XCTAssertEqual(rows[0].id, 1)

        // Channel without guide data → empty NowNext
        XCTAssertEqual(rows[1].channel.id, 2)
        XCTAssertNil(rows[1].nowNext.now)
        XCTAssertNil(rows[1].nowNext.next)
    }

    func testLoadKeepsProgramsAndCategoryFilterIsRememberedPerDevice() async throws {
        let channelsData = TestFixtures.channelJSON([(1, "4.1", "WABC"), (2, "7.1", "WXYZ")])
        let guideData = TestFixtures.guideJSON([
            (
                channelId: 1,
                number: "4.1",
                name: "WABC",
                programs: [
                    (start: "2024-06-15T20:00:00Z", stop: "2024-06-15T21:00:00Z", title: "News"),
                    (start: "2024-06-15T21:00:00Z", stop: "2024-06-15T22:00:00Z", title: "Drama"),
                ]
            ),
        ])
        StubURLProtocol.handler = { request in
            switch request.url?.path ?? "" {
            case "/api/v1/channels": return (200, channelsData, [:])
            case "/api/v1/guide": return (200, guideData, [:])
            default: return (500, Data(), [:])
            }
        }
        let defaults = UserDefaults(suiteName: "ChannelListModelFilterTests")!
        defaults.removePersistentDomain(forName: "ChannelListModelFilterTests")

        let client = makeClient()
        await seedAccess(client)
        let model = ChannelListModel(client: client, now: { [weak self] in self?.clock ?? Date() }, defaults: defaults)
        XCTAssertEqual(model.filter, .all)
        await model.load()

        XCTAssertEqual(model.rows.first { $0.id == 1 }?.programs.map(\.title), ["News", "Drama"])
        XCTAssertEqual(model.rows.first { $0.id == 2 }?.programs, [])
        XCTAssertEqual(model.filteredRows(at: clock).map(\.id), [1, 2])

        // Fixture programs carry no category: nothing is sports.
        model.filter = .sports
        XCTAssertEqual(model.filteredRows(at: clock), [])
        XCTAssertEqual(ChannelListModel(client: client, defaults: defaults).filter, .sports)
    }

    func testLoadClassifiesEachProgramOnceOntoItsRow() async throws {
        let channelsData = TestFixtures.channelJSON([(1, "4.1", "WABC"), (2, "7.1", "WXYZ")])
        let guideData = Data("""
        [{"channelId":1,"guideNumber":"4.1","name":"WABC","logoUrl":"","programs":[
          {"start":"2024-06-15T20:00:00Z","stop":"2024-06-15T21:00:00Z","title":"Six","subtitle":"","description":"","category":"News"},
          {"start":"2024-06-15T21:00:00Z","stop":"2024-06-15T22:00:00Z","title":"Game","subtitle":"","description":"","category":"Sports event; Football"}
        ]}]
        """.utf8)
        StubURLProtocol.handler = { request in
            switch request.url?.path ?? "" {
            case "/api/v1/channels": return (200, channelsData, [:])
            case "/api/v1/guide": return (200, guideData, [:])
            default: return (500, Data(), [:])
            }
        }
        let defaults = UserDefaults(suiteName: "ChannelListModelClassifyTests")!
        defaults.removePersistentDomain(forName: "ChannelListModelClassifyTests")
        let client = makeClient()
        await seedAccess(client)
        let model = ChannelListModel(client: client, now: { [weak self] in self?.clock ?? Date() }, defaults: defaults)
        await model.load()

        // Buckets ride on the row, one set per program, from the load.
        XCTAssertEqual(model.rows.first { $0.id == 1 }?.programBuckets, [[.news], [.sports]])
        XCTAssertEqual(model.rows.first { $0.id == 2 }?.programBuckets, [])

        model.filter = .sports
        XCTAssertEqual(model.filteredRows(at: clock).map(\.id), [1])
        let row = try XCTUnwrap(model.rows.first { $0.id == 1 })
        XCTAssertEqual(
            model.highlight(for: row, at: clock),
            GuideFilter.RowHighlight(nowMatches: false, nextMatches: true, later: nil)
        )

        // The selected channel stays listed (dimmed) under a filter it doesn't match.
        let kept = model.filteredRows(at: clock, keeping: 2)
        XCTAssertEqual(kept.rows.map(\.id), [1, 2])
        XCTAssertEqual(kept.keptId, 2)
    }

    func testLoadRequestsGuideWindowNowToNowPlus4h() async throws {
        let channelsData = TestFixtures.channelJSON([(1, "4.1", "WABC")])
        StubURLProtocol.handler = { request in
            let path = request.url?.path ?? ""
            if path == "/api/v1/channels" {
                return (200, channelsData, [:])
            }
            if path == "/api/v1/guide" {
                let items = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems ?? []
                let start = items.first { $0.name == "start" }?.value
                let stop = items.first { $0.name == "stop" }?.value
                // 20:30 → 00:30 next day
                XCTAssertEqual(start, "2024-06-15T20:30:00Z")
                XCTAssertEqual(stop, "2024-06-16T00:30:00Z")
                return (200, "[]".data(using: .utf8)!, [:])
            }
            return (500, Data(), [:])
        }

        let client = makeClient()
        await seedAccess(client)
        let model = ChannelListModel(client: client, now: { [weak self] in self?.clock ?? Date() })
        await model.load()

        guard case .loaded = model.state else {
            return XCTFail("expected loaded, got \(model.state)")
        }
        let guideCalls = StubURLProtocol.recorded.filter { $0.url?.path == "/api/v1/guide" }
        XCTAssertEqual(guideCalls.count, 1)
    }

    // MARK: - Empty / failure

    func testLoadEmptyChannels() async throws {
        StubURLProtocol.handler = { request in
            let path = request.url?.path ?? ""
            if path == "/api/v1/channels" || path == "/api/v1/guide" {
                return (200, "[]".data(using: .utf8)!, [:])
            }
            return (500, Data(), [:])
        }

        let client = makeClient()
        await seedAccess(client)
        let model = ChannelListModel(client: client, now: { [weak self] in self?.clock ?? Date() })
        await model.load()
        XCTAssertEqual(model.state, .empty)
    }

    func testLoadFailure() async throws {
        StubURLProtocol.handler = { _ in
            (500, #"{"error":"boom"}"#.data(using: .utf8)!, [:])
        }

        let client = makeClient()
        await seedAccess(client)
        let model = ChannelListModel(client: client, now: { [weak self] in self?.clock ?? Date() })
        await model.load()

        guard case .failed(let message) = model.state else {
            return XCTFail("expected failed, got \(model.state)")
        }
        XCTAssertFalse(message.isEmpty)
    }

    func testReloadKeepsRowsOnScreenWhileInFlight() async throws {
        let channelsData = TestFixtures.channelJSON([(1, "4.1", "WABC")])
        StubURLProtocol.handler = { request in
            if request.url?.path == "/api/v1/channels" {
                return (200, channelsData, [:])
            }
            return (200, Data("[]".utf8), [:])
        }
        let client = makeClient()
        await seedAccess(client)
        let model = ChannelListModel(client: client, now: { [weak self] in self?.clock ?? Date() })
        await model.load()

        // Second load (e.g. after scheduling a recording) is slow.
        StubURLProtocol.delay = { _ in 0.3 }
        let reload = Task { await model.load() }
        try await Task.sleep(for: .milliseconds(100))

        guard case .loaded(let rows) = model.state else {
            await reload.value
            return XCTFail("rows should stay while reloading, got \(model.state)")
        }
        XCTAssertEqual(rows.map(\.channel.id), [1])
        await reload.value
    }

    func testInitialStateIsLoading() {
        let client = makeClient()
        let model = ChannelListModel(client: client, now: { Date() })
        XCTAssertEqual(model.state, .loading)
    }

    // MARK: - refreshIfStale window math

    func testRefreshIfStaleSkipsWhenFresh() async throws {
        final class HitCounter: @unchecked Sendable {
            let lock = NSLock()
            var count = 0
            func increment() {
                lock.lock()
                count += 1
                lock.unlock()
            }
            var value: Int {
                lock.lock()
                defer { lock.unlock() }
                return count
            }
        }
        let hits = HitCounter()
        StubURLProtocol.handler = { request in
            let path = request.url?.path ?? ""
            if path == "/api/v1/channels" || path == "/api/v1/guide" {
                hits.increment()
                return (200, "[]".data(using: .utf8)!, [:])
            }
            return (500, Data(), [:])
        }

        let client = makeClient()
        await seedAccess(client)
        let model = ChannelListModel(client: client, now: { [weak self] in self?.clock ?? Date() })

        await model.load()
        let afterLoad = hits.value
        XCTAssertGreaterThan(afterLoad, 0)

        // Advance 1 minute — still fresh (5 min window).
        clock = clock.addingTimeInterval(60)
        await model.refreshIfStale()
        XCTAssertEqual(hits.value, afterLoad, "should not re-fetch within 5 minutes")
    }

    func testRefreshIfStaleReloadsAfter5Minutes() async throws {
        final class HitCounter: @unchecked Sendable {
            let lock = NSLock()
            var count = 0
            func increment() {
                lock.lock()
                count += 1
                lock.unlock()
            }
            var value: Int {
                lock.lock()
                defer { lock.unlock() }
                return count
            }
        }
        let hits = HitCounter()
        StubURLProtocol.handler = { request in
            let path = request.url?.path ?? ""
            if path == "/api/v1/channels" || path == "/api/v1/guide" {
                hits.increment()
                return (200, "[]".data(using: .utf8)!, [:])
            }
            return (500, Data(), [:])
        }

        let client = makeClient()
        await seedAccess(client)
        let model = ChannelListModel(client: client, now: { [weak self] in self?.clock ?? Date() })

        await model.load()
        let afterLoad = hits.value

        // Exactly 5 minutes later → stale.
        clock = clock.addingTimeInterval(5 * 60)
        await model.refreshIfStale()
        XCTAssertGreaterThan(hits.value, afterLoad, "should re-fetch at 5-minute boundary")
    }

    func testRefreshIfStaleLoadsWhenNeverLoaded() async throws {
        StubURLProtocol.handler = { request in
            let path = request.url?.path ?? ""
            if path == "/api/v1/channels" || path == "/api/v1/guide" {
                return (200, "[]".data(using: .utf8)!, [:])
            }
            return (500, Data(), [:])
        }

        let client = makeClient()
        await seedAccess(client)
        let model = ChannelListModel(client: client, now: { [weak self] in self?.clock ?? Date() })
        XCTAssertEqual(model.state, .loading)

        await model.refreshIfStale()
        XCTAssertEqual(model.state, .empty)
    }
}
