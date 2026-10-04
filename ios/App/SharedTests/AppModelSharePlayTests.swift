import XCTest
import BowtieKit
@testable import Bowtie

@MainActor
final class AppModelSharePlayTests: XCTestCase {
    private var store: InMemorySessionStore!
    private let home = SharedFixtures.baseURL
    private let tailnet = URL(string: "http://bowtie.tailnet.ts.net:8400")!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
        store = InMemorySessionStore()
    }

    override func tearDown() {
        StubURLProtocol.reset()
        super.tearDown()
    }

    /// `/version` answers per host; refresh always succeeds.
    private func stubServers(_ ids: [String: String]) {
        StubURLProtocol.handler = { request in
            switch request.url?.path {
            case "/api/v1/version":
                let id = ids[request.url?.host ?? ""] ?? "unknown"
                return (200, #"{"version":"0.7.0","serverId":"\#(id)","serverName":"Den"}"#.data(using: .utf8)!, [:])
            case "/api/v1/auth/refresh":
                return (200, SharedFixtures.tokenPairJSON(), [:])
            default:
                return (500, Data(), [:])
            }
        }
    }

    private func group(serverId: String = "srv-1") -> FakeWatchGroup {
        FakeWatchGroup(WatchChannelActivity(
            serverId: serverId,
            serverName: "Living Room",
            channelId: 10,
            channelName: "WABC",
            sessionId: "sess-1"
        ))
    }

    func testJoinsOnCurrentServer() async {
        store.save(server: home, refreshToken: "r1")
        stubServers([home.host!: "srv-1"])
        let model = AppModel(store: store, urlSession: SharedFixtures.makeStubSession())

        // Launched by SharePlay: still checking the stored login.
        await model.receiveGroup(group())

        XCTAssertEqual(model.phase, .ready)
        XCTAssertNotNil(model.takeGroupJoin())
        XCTAssertNil(model.takeGroupJoin(), "handed over once")
        XCTAssertNil(model.watchTogetherMessage)
    }

    func testSwitchesToSavedServerWithLogin() async {
        store.save(server: tailnet, refreshToken: "r-tail")
        store.save(server: home, refreshToken: "r-home")
        stubServers([home.host!: "srv-other", tailnet.host!: "srv-1"])
        let model = AppModel(store: store, urlSession: SharedFixtures.makeStubSession())
        await model.start()

        await model.receiveGroup(group())

        XCTAssertEqual(model.serverURL, tailnet)
        XCTAssertEqual(model.phase, .ready)
        XCTAssertNotNil(model.takeGroupJoin())
    }

    func testAsksToSignInWhenNoLogin() async {
        store.save(server: tailnet, refreshToken: nil)
        store.save(server: home, refreshToken: "r-home")
        stubServers([home.host!: "srv-other", tailnet.host!: "srv-1"])
        let model = AppModel(store: store, urlSession: SharedFixtures.makeStubSession())
        await model.start()

        let g = group()
        await model.receiveGroup(g)

        XCTAssertEqual(model.watchTogetherMessage, "Sign in to Living Room to watch together")
        XCTAssertNil(model.takeGroupJoin())
        XCTAssertEqual(g.joinCount, 0)
        XCTAssertEqual(model.serverURL, home, "stays on the current server")
    }
}
