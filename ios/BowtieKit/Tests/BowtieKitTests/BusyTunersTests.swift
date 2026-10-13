import XCTest
@testable import BowtieKit

/// All tuners busy: `watchable` on channels and guide rows, the list showing
/// only channels that can start, the note above it, and the 30 s re-check.
@MainActor
final class BusyTunersTests: XCTestCase {
    private var store: InMemorySessionStore!
    private let clock = TestFixtures.iso("2026-10-06T20:30:00Z")

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
        store = InMemorySessionStore()
    }

    override func tearDown() {
        StubURLProtocol.reset()
        super.tearDown()
    }

    // MARK: - Decoding

    func testChannelWithoutWatchableIsWatchable() throws {
        let json = #"{"id":1,"guideNumber":"4.1","name":"WABC","logoUrl":""}"#
        let channel = try JSONDecoder().decode(Channel.self, from: Data(json.utf8))
        XCTAssertNil(channel.watchable)
        XCTAssertTrue(channel.isWatchable, "older servers omit it: treat as watchable")
    }

    func testChannelWatchableFalseDecodes() throws {
        let json = #"{"id":1,"guideNumber":"4.1","name":"WABC","logoUrl":"","watchable":false}"#
        let channel = try JSONDecoder().decode(Channel.self, from: Data(json.utf8))
        XCTAssertEqual(channel.watchable, false)
        XCTAssertFalse(channel.isWatchable)
    }

    func testGuideChannelWatchableDecodes() throws {
        let busy = #"{"channelId":1,"guideNumber":"4.1","name":"WABC","logoUrl":"","programs":[],"watchable":false}"#
        let old = #"{"channelId":1,"guideNumber":"4.1","name":"WABC","logoUrl":"","programs":[]}"#
        XCTAssertFalse(try JSONDecoder().decode(GuideChannel.self, from: Data(busy.utf8)).isWatchable)
        XCTAssertTrue(try JSONDecoder().decode(GuideChannel.self, from: Data(old.utf8)).isWatchable)
    }

    // MARK: - Fixtures

    /// Channels with an explicit `watchable` (nil = field omitted) and favorite flag.
    private func channelsJSON(_ channels: [(id: Int64, number: String, watchable: Bool?, favorite: Bool)]) -> Data {
        let items = channels.map { c in
            let watchable = c.watchable.map { #","watchable":\#($0)"# } ?? ""
            return #"{"id":\#(c.id),"guideNumber":"\#(c.number)","name":"CH\#(c.id)","logoUrl":"","favorite":\#(c.favorite)\#(watchable)}"#
        }.joined(separator: ",")
        return Data("[\(items)]".utf8)
    }

    /// One program per channel, now; channel 1 and 3 show news.
    private func guideJSON(_ ids: [Int64]) -> Data {
        let items = ids.map { id in
            let category = id == 1 || id == 3 ? "News" : "Drama"
            return """
            {"channelId":\(id),"guideNumber":"\(id).1","name":"CH\(id)","logoUrl":"","programs":[
              {"start":"2026-10-06T20:00:00Z","stop":"2026-10-06T21:00:00Z","title":"Show \(id)","subtitle":"","description":"","category":"\(category)"}
            ]}
            """
        }.joined(separator: ",")
        return Data("[\(items)]".utf8)
    }

    /// A model whose `/channels` answer can change between calls.
    private final class Lineup: @unchecked Sendable {
        var channels: Data
        var channelsStatus = 200
        var channelsCalls = 0
        var guideCalls = 0
        var recents = Data("[]".utf8)
        init(_ channels: Data) { self.channels = channels }
    }

    private func makeModel(_ lineup: Lineup, guideIds: [Int64] = [1, 2, 3, 4]) async -> ChannelListModel {
        let guide = guideJSON(guideIds)
        StubURLProtocol.handler = { request in
            switch request.url?.path ?? "" {
            case "/api/v1/channels":
                lineup.channelsCalls += 1
                return (lineup.channelsStatus, lineup.channels, [:])
            case "/api/v1/guide":
                lineup.guideCalls += 1
                return (200, guide, [:])
            case "/api/v1/me/recents":
                return (200, lineup.recents, [:])
            default:
                return (204, Data(), [:])
            }
        }
        let client = BowtieClient(server: TestFixtures.baseURL, store: store, urlSession: TestFixtures.makeStubSession())
        await client.setAccessTokenForTesting("access-1")
        let at = clock
        let defaults = UserDefaults(suiteName: "BusyTunersTests-\(UUID().uuidString)")!
        return ChannelListModel(client: client, now: { at }, defaults: defaults)
    }

    // MARK: - Filtering and the note

    func testEverythingWatchableShowsAllAndNoNote() async {
        let lineup = Lineup(channelsJSON([(1, "1.1", true, false), (2, "2.1", nil, false)]))
        let model = await makeModel(lineup, guideIds: [1, 2])
        await model.load()

        XCTAssertEqual(model.filteredRows(at: clock).map(\.id), [1, 2])
        XCTAssertNil(model.tunersBusyNote)
    }

    func testSomeBusyShowsOnlyWatchableWithNote() async {
        let lineup = Lineup(channelsJSON([
            (1, "1.1", true, false), (2, "2.1", false, false), (3, "3.1", true, false), (4, "4.1", false, true),
        ]))
        let model = await makeModel(lineup)
        await model.load()

        XCTAssertEqual(model.filteredRows(at: clock).map(\.id), [1, 3], "busy favorite 4 is hidden too")
        XCTAssertEqual(model.tunersBusyNote, "All tuners are in use — showing channels you can join.")
        XCTAssertEqual(model.rows.count, 4, "busy rows stay loaded (titles, record menus look them up)")
    }

    func testAllBusyStaysLoadedWithTryAgainNote() async {
        let lineup = Lineup(channelsJSON([(1, "1.1", false, false), (2, "2.1", false, true)]))
        let model = await makeModel(lineup, guideIds: [1, 2])
        await model.load()

        guard case .loaded = model.state else {
            return XCTFail("all busy is not an empty lineup: \(model.state)")
        }
        XCTAssertTrue(model.filteredRows(at: clock).isEmpty)
        XCTAssertEqual(model.tunersBusyNote, "All tuners are in use. Try again in a few minutes.")
        XCTAssertTrue(model.allTunersBusy)
    }

    func testBusyFilterLayersOnCategoryFilter() async {
        // News on 1 and 3; 3 is busy.
        let lineup = Lineup(channelsJSON([
            (1, "1.1", true, false), (2, "2.1", true, false), (3, "3.1", false, false), (4, "4.1", true, false),
        ]))
        let model = await makeModel(lineup)
        await model.load()
        model.filter = .news

        XCTAssertEqual(model.filteredRows(at: clock).map(\.id), [1])
        XCTAssertEqual(model.filteredRows(at: clock, keeping: nil).rows.map(\.id), [1])
    }

    func testKeepingStillKeepsTheSelectedChannelWhenBusy() async {
        let lineup = Lineup(channelsJSON([(1, "1.1", true, false), (2, "2.1", false, false)]))
        let model = await makeModel(lineup, guideIds: [1, 2])
        await model.load()

        let kept = model.filteredRows(at: clock, keeping: 2)
        XCTAssertEqual(kept.rows.map(\.id), [1, 2], "dropping the selection would stop playback")
        XCTAssertEqual(kept.keptId, 2, "drawn dimmed")
        XCTAssertEqual(model.filteredRows(at: clock, keeping: 9).rows.map(\.id), [1])
    }

    func testRecentsHideBusyChannels() async {
        let lineup = Lineup(channelsJSON([(1, "1.1", true, false), (2, "2.1", false, false)]))
        lineup.recents = TestFixtures.recentsJSON([
            (2, "2.1", "CH2", "2026-10-06T19:00:00Z"),
            (1, "1.1", "CH1", "2026-10-06T18:00:00Z"),
        ])
        let model = await makeModel(lineup, guideIds: [1, 2])
        await model.load()

        XCTAssertEqual(model.recentChannels.map(\.id), [1])
    }

    // MARK: - Re-check

    func testRecheckPicksUpFreedTunerWithoutReloadingTheGuide() async {
        let lineup = Lineup(channelsJSON([(1, "1.1", false, false), (2, "2.1", false, false)]))
        let model = await makeModel(lineup, guideIds: [1, 2])
        await model.load()
        XCTAssertEqual(model.tunersBusyNote, "All tuners are in use. Try again in a few minutes.")
        let guideCalls = lineup.guideCalls

        lineup.channels = channelsJSON([(1, "1.1", true, false), (2, "2.1", false, false)])
        await model.recheckTuners()

        XCTAssertEqual(model.filteredRows(at: clock).map(\.id), [1])
        XCTAssertEqual(model.tunersBusyNote, "All tuners are in use — showing channels you can join.")
        XCTAssertEqual(model.rows.first?.nowNext.now?.title, "Show 1", "guide rows kept")
        XCTAssertEqual(lineup.guideCalls, guideCalls, "channels only")
    }

    func testRecheckFailureKeepsTheList() async {
        let lineup = Lineup(channelsJSON([(1, "1.1", true, false), (2, "2.1", false, false)]))
        let model = await makeModel(lineup, guideIds: [1, 2])
        await model.load()

        lineup.channelsStatus = 500
        await model.recheckTuners()

        guard case .loaded = model.state else {
            return XCTFail("a failed re-check must not cost the list: \(model.state)")
        }
        XCTAssertEqual(model.filteredRows(at: clock).map(\.id), [1])
    }

    func testRecheckWithChangedLineupReloadsEverything() async {
        let lineup = Lineup(channelsJSON([(1, "1.1", true, false)]))
        let model = await makeModel(lineup, guideIds: [1, 2])
        await model.load()
        let guideCalls = lineup.guideCalls

        lineup.channels = channelsJSON([(1, "1.1", true, false), (2, "2.1", true, false)])
        await model.recheckTuners()

        XCTAssertEqual(model.rows.map(\.id), [1, 2])
        XCTAssertEqual(lineup.guideCalls, guideCalls + 1)
    }

    func testRecheckKeepsLocalFavorite() async {
        // The re-check takes `watchable` / `reception`; stars stay as the user left them.
        let lineup = Lineup(channelsJSON([(1, "1.1", true, true), (2, "2.1", true, false)]))
        let model = await makeModel(lineup, guideIds: [1, 2])
        await model.load()

        lineup.channels = channelsJSON([(1, "1.1", true, false), (2, "2.1", false, false)])
        await model.recheckTuners()

        XCTAssertEqual(model.rows.first(where: { $0.id == 1 })?.channel.isFavorite, true)
        XCTAssertEqual(model.rows.first(where: { $0.id == 2 })?.channel.isWatchable, false)
    }
}
