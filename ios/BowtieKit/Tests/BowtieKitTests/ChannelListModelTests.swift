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
