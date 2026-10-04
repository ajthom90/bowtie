import XCTest
import BowtieKit
@testable import Bowtie

@MainActor
final class AppModelTests: XCTestCase {
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

    private func makeSession() -> URLSession {
        SharedFixtures.makeStubSession()
    }

    // MARK: - Phase transitions

    func testFreshStoreStartsAtConnect() {
        let model = AppModel(store: store, urlSession: makeSession())
        XCTAssertEqual(model.phase, .connect)
        XCTAssertNil(model.client)
        XCTAssertNil(model.user)
    }

    func testStoredServerWithoutTokenStartsAtLogin() {
        store.save(server: SharedFixtures.baseURL, refreshToken: nil)
        let model = AppModel(store: store, urlSession: makeSession())
        XCTAssertEqual(model.phase, .login)
        XCTAssertNotNil(model.client)
    }

    func testStoredServerAndTokenStartsAtCheckingThenReady() async {
        store.save(server: SharedFixtures.baseURL, refreshToken: "stored-refresh")

        StubURLProtocol.handler = { request in
            XCTAssertEqual(request.url?.path, "/api/v1/auth/refresh")
            return (200, SharedFixtures.tokenPairJSON(access: "a2", refresh: "r2"), [:])
        }

        let model = AppModel(store: store, urlSession: makeSession())
        XCTAssertEqual(model.phase, .checking)

        await model.start()

        XCTAssertEqual(model.phase, .ready)
        XCTAssertEqual(model.user?.username, "alice")
        XCTAssertEqual(store.loadRefreshToken(), "r2")
    }

    func testDeviceSignInCompletesLogin() {
        store.save(server: SharedFixtures.baseURL, refreshToken: nil)
        let model = AppModel(store: store, urlSession: makeSession())
        let user = User(id: 1, username: "alice", role: "viewer", maxQuality: "high")

        model.completeDeviceSignIn(user: user)

        XCTAssertEqual(model.phase, .ready)
        XCTAssertEqual(model.user, user)
    }

    func testDeviceSignInIgnoredOutsideLogin() {
        let model = AppModel(store: store, urlSession: makeSession())

        model.completeDeviceSignIn(user: User(id: 1, username: "alice", role: "viewer", maxQuality: "high"))

        XCTAssertEqual(model.phase, .connect)
        XCTAssertNil(model.user)
    }

    func testBootstrapFailureFallsBackToLogin() async {
        store.save(server: SharedFixtures.baseURL, refreshToken: "dead-token")

        StubURLProtocol.handler = { request in
            XCTAssertEqual(request.url?.path, "/api/v1/auth/refresh")
            return (
                401,
                #"{"error":"invalid refresh token"}"#.data(using: .utf8)!,
                [:]
            )
        }

        let model = AppModel(store: store, urlSession: makeSession())
        XCTAssertEqual(model.phase, .checking)

        await model.start()

        XCTAssertEqual(model.phase, .login)
        XCTAssertNil(model.user)
        // Server kept for reconnect.
        XCTAssertEqual(store.loadServer(), SharedFixtures.baseURL)
        XCTAssertNil(store.loadRefreshToken())
    }

    // MARK: - Connect / sign-in / sign-out / change-server

    func testConnectValidatesHealthzAndAdvancesToLogin() async {
        StubURLProtocol.handler = { request in
            XCTAssertEqual(request.url?.path, "/healthz")
            return (200, Data(), [:])
        }

        let model = AppModel(store: store, urlSession: makeSession())
        let ok = await model.connect(rawURL: "test.bowtie.local:8400")
        XCTAssertTrue(ok)
        XCTAssertEqual(model.phase, .login)
        XCTAssertEqual(store.loadServer(), SharedFixtures.baseURL)
        XCTAssertNotNil(model.client)
    }

    func testConnectRejectsUnreachableServer() async {
        StubURLProtocol.handler = { _ in
            (503, Data(), [:])
        }

        let model = AppModel(store: store, urlSession: makeSession())
        let ok = await model.connect(rawURL: "test.bowtie.local:8400")
        XCTAssertFalse(ok)
        XCTAssertEqual(model.phase, .connect)
        XCTAssertNil(store.loadServer())
    }

    func testSignInAdvancesToReady() async throws {
        store.save(server: SharedFixtures.baseURL, refreshToken: nil)

        StubURLProtocol.handler = { request in
            XCTAssertEqual(request.url?.path, "/api/v1/auth/login")
            return (200, SharedFixtures.tokenPairJSON(), [:])
        }

        let model = AppModel(store: store, urlSession: makeSession())
        XCTAssertEqual(model.phase, .login)

        try await model.signIn(username: "alice", password: "s3cret")

        XCTAssertEqual(model.phase, .ready)
        XCTAssertEqual(model.user?.username, "alice")
    }

    func testSignOutReturnsToLoginKeepingServer() async {
        store.save(server: SharedFixtures.baseURL, refreshToken: "r1")

        StubURLProtocol.handler = { request in
            if request.url?.path == "/api/v1/auth/refresh" {
                return (200, SharedFixtures.tokenPairJSON(), [:])
            }
            if request.url?.path == "/api/v1/auth/logout" {
                return (204, Data(), [:])
            }
            return (500, Data(), [:])
        }

        let model = AppModel(store: store, urlSession: makeSession())
        await model.start()
        XCTAssertEqual(model.phase, .ready)

        await model.signOut()

        XCTAssertEqual(model.phase, .login)
        XCTAssertNil(model.user)
        XCTAssertEqual(store.loadServer(), SharedFixtures.baseURL)
        XCTAssertNil(store.loadRefreshToken())
    }

    func testChangeServerKeepsSavedServers() async {
        store.save(server: SharedFixtures.baseURL, refreshToken: "r1")

        StubURLProtocol.handler = { request in
            if request.url?.path == "/api/v1/auth/refresh" {
                return (200, SharedFixtures.tokenPairJSON(), [:])
            }
            return (500, Data(), [:])
        }

        let model = AppModel(store: store, urlSession: makeSession())
        await model.start()
        XCTAssertEqual(model.phase, .ready)

        model.changeServer()

        XCTAssertEqual(model.phase, .connect)
        XCTAssertNil(model.client)
        XCTAssertNil(model.user)
        XCTAssertNil(store.loadServer())
        XCTAssertNil(store.loadRefreshToken())
        XCTAssertEqual(model.savedServers.map(\.url), [SharedFixtures.baseURL], "the server stays saved")

        // Picking it again reuses its saved login.
        model.selectServer(SharedFixtures.baseURL)
        XCTAssertEqual(model.phase, .checking)
        await model.start()
        XCTAssertEqual(model.phase, .ready)
    }

    func testSelectServerWithoutLoginGoesToLogin() {
        let other = URL(string: "http://192.168.0.25:8400")!
        store.save(server: other, refreshToken: nil)
        store.save(server: SharedFixtures.baseURL, refreshToken: "r1")
        let model = AppModel(store: store, urlSession: makeSession())

        model.selectServer(other)

        XCTAssertEqual(model.phase, .login)
        XCTAssertEqual(model.serverURL, other)
        XCTAssertNotNil(model.client)
    }

    func testRemoveServerForgetsIt() {
        store.save(server: SharedFixtures.baseURL, refreshToken: "r1")
        store.save(server: nil, refreshToken: nil)
        let model = AppModel(store: store, urlSession: makeSession())

        model.removeServer(SharedFixtures.baseURL)

        XCTAssertTrue(model.savedServers.isEmpty)
        XCTAssertEqual(model.phase, .connect)
    }
}
