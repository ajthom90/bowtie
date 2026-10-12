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

    // MARK: - TV pairing

    private func makePairingModel() -> AppModel {
        AppModel(store: store, urlSession: makeSession(), pairingHost: { "127.0.0.1" })
    }

    private func startPairing(_ model: AppModel) async throws -> URL {
        model.pairing.start()
        for _ in 0..<100 where model.pairing.pairURL == nil { try await Task.sleep(nanoseconds: 20_000_000) }
        return try XCTUnwrap(model.pairing.pairURL)
    }

    /// POSTs `server` to the pairing page the way the phone's browser would.
    private func submitFromPhone(_ pairURL: URL, server: String) async throws {
        let token = URLComponents(url: pairURL, resolvingAgainstBaseURL: false)?
            .queryItems?.first { $0.name == "t" }?.value ?? ""
        var request = URLRequest(url: pairURL.deletingLastPathComponent().appendingPathComponent("p"))
        request.httpMethod = "POST"
        request.setValue("application/x-www-form-urlencoded", forHTTPHeaderField: "Content-Type")
        let form = "t=\(token)&server=" + (server.addingPercentEncoding(withAllowedCharacters: .alphanumerics) ?? "")
        request.httpBody = Data(form.utf8)
        _ = try await URLSession(configuration: .ephemeral).data(for: request)
    }

    func testTypedConnectFailureLeavesPairingRunningAndSuccessStopsIt() async throws {
        StubURLProtocol.handler = { request in
            (request.url?.host == "test.bowtie.local" ? 200 : 503, Data(), [:])
        }
        let model = makePairingModel()
        let pairURL = try await startPairing(model)

        let bad = await model.connectTyped(rawURL: "nowhere.bowtie.local:8400")
        XCTAssertFalse(bad)
        XCTAssertEqual(model.pairing.pairURL, pairURL)

        let good = await model.connectTyped(rawURL: "test.bowtie.local:8400")
        XCTAssertTrue(good)
        XCTAssertNil(model.pairing.pairURL)
    }

    func testSlowPhoneConnectLosesToTypedConnect() async throws {
        StubURLProtocol.handler = { request in
            if request.url?.host == "slow.bowtie.local" { Thread.sleep(forTimeInterval: 0.6) }
            return (200, Data(), [:])
        }
        let model = makePairingModel()
        let pairURL = try await startPairing(model)

        try await submitFromPhone(pairURL, server: "http://slow.bowtie.local:8400")
        for _ in 0..<100 where !StubURLProtocol.recorded.contains(where: { $0.url?.host == "slow.bowtie.local" }) {
            try await Task.sleep(nanoseconds: 20_000_000)
        }
        XCTAssertTrue(StubURLProtocol.recorded.contains { $0.url?.host == "slow.bowtie.local" })

        let ok = await model.connectTyped(rawURL: "test.bowtie.local:8400")
        XCTAssertTrue(ok)
        try await Task.sleep(nanoseconds: 1_000_000_000)

        XCTAssertEqual(model.serverURL, SharedFixtures.baseURL)
        XCTAssertEqual(model.client?.serverURL, SharedFixtures.baseURL)
        XCTAssertEqual(model.phase, .login)
    }

    func testPhoneConnectIgnoredOutsideConnectPhase() async throws {
        StubURLProtocol.handler = { _ in (200, Data(), [:]) }
        let model = makePairingModel()
        let pairURL = try await startPairing(model)
        store.save(server: SharedFixtures.baseURL, refreshToken: nil)
        model.selectServer(SharedFixtures.baseURL)   // phase leaves .connect

        try await submitFromPhone(pairURL, server: "http://other.bowtie.local:8400")
        try await Task.sleep(nanoseconds: 300_000_000)

        XCTAssertFalse(StubURLProtocol.recorded.contains { $0.url?.host == "other.bowtie.local" })
        XCTAssertEqual(model.serverURL, SharedFixtures.baseURL)
    }

    func testChangeServerStartsAFreshPairingSession() async throws {
        StubURLProtocol.handler = { _ in (200, Data(), [:]) }
        let model = makePairingModel()
        let first = try await startPairing(model)
        try await submitFromPhone(first, server: "http://test.bowtie.local:8400")
        for _ in 0..<100 where model.phase != .login { try await Task.sleep(nanoseconds: 20_000_000) }
        XCTAssertEqual(model.phase, .login)

        model.changeServer()
        XCTAssertNil(model.pairing.pairURL)
        let second = try await startPairing(model)
        XCTAssertNotEqual(first, second)
    }

    func testRemovingTheLastServerStartsAFreshPairingSession() async throws {
        StubURLProtocol.handler = { _ in (200, Data(), [:]) }
        let model = makePairingModel()
        let first = try await startPairing(model)
        try await submitFromPhone(first, server: "http://test.bowtie.local:8400")
        for _ in 0..<100 where model.phase != .login { try await Task.sleep(nanoseconds: 20_000_000) }
        let server = try XCTUnwrap(model.serverURL)

        model.removeServer(server)
        XCTAssertEqual(model.phase, .connect)
        XCTAssertNil(model.pairing.pairURL)
        let second = try await startPairing(model)
        XCTAssertNotEqual(first, second)
    }

    func testSignInStopsPairing() async throws {
        StubURLProtocol.handler = { _ in (200, SharedFixtures.tokenPairJSON(), [:]) }
        store.save(server: SharedFixtures.baseURL, refreshToken: nil)
        let model = makePairingModel()
        _ = try await startPairing(model)
        try await model.signIn(username: "alice", password: "pw")
        XCTAssertNil(model.pairing.pairURL)
    }
}
