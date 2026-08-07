import XCTest
@testable import BowtieKit

// MARK: - Tests

final class BowtieClientTests: XCTestCase {
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
        TestFixtures.makeStubSession()
    }

    // MARK: Request body OpenAPI field names (behavior contract item 0)

    func testLoginRequestBodyFieldNames() async throws {
        StubURLProtocol.handler = { request in
            XCTAssertEqual(request.httpMethod, "POST")
            XCTAssertEqual(request.url?.path, "/api/v1/auth/login")
            XCTAssertNil(request.value(forHTTPHeaderField: "Authorization"))
            return (200, TestFixtures.tokenPairJSON(), [:])
        }

        let client = BowtieClient(server: TestFixtures.baseURL, store: store, urlSession: makeSession())
        let user = try await client.login(username: "alice", password: "s3cret")
        XCTAssertEqual(user.username, "alice")
        XCTAssertEqual(store.loadRefreshToken(), "refresh-1")
        XCTAssertEqual(store.loadServer(), TestFixtures.baseURL)

        let body = try jsonBody(of: StubURLProtocol.recorded[0])
        XCTAssertEqual(body["username"] as? String, "alice")
        XCTAssertEqual(body["password"] as? String, "s3cret")
        XCTAssertEqual(Set(body.keys), Set(["username", "password"]))
    }

    func testRefreshRequestBodyFieldNames() async throws {
        store.save(server: TestFixtures.baseURL, refreshToken: "old-refresh")

        StubURLProtocol.handler = { request in
            if request.url?.path == "/api/v1/auth/refresh" {
                return (200, TestFixtures.tokenPairJSON(access: "a2", refresh: "r2"), [:])
            }
            return (500, Data(), [:])
        }

        let client = BowtieClient(server: TestFixtures.baseURL, store: store, urlSession: makeSession())
        _ = try await client.bootstrapFromStoredToken()

        let refreshReqs = StubURLProtocol.recorded.filter { $0.url?.path == "/api/v1/auth/refresh" }
        XCTAssertEqual(refreshReqs.count, 1)
        let body = try jsonBody(of: refreshReqs[0])
        XCTAssertEqual(body["refreshToken"] as? String, "old-refresh")
        XCTAssertEqual(Set(body.keys), Set(["refreshToken"]))
        XCTAssertEqual(store.loadRefreshToken(), "r2")
    }

    func testLogoutRequestBodyFieldNames() async throws {
        store.save(server: TestFixtures.baseURL, refreshToken: "to-revoke")
        let client = BowtieClient(server: TestFixtures.baseURL, store: store, urlSession: makeSession())
        await client.setAccessTokenForTesting("access-x")

        StubURLProtocol.handler = { request in
            XCTAssertEqual(request.url?.path, "/api/v1/auth/logout")
            return (204, Data(), [:])
        }

        await client.logout()

        XCTAssertNil(store.loadRefreshToken())
        // Server URL kept for reconnect.
        XCTAssertEqual(store.loadServer(), TestFixtures.baseURL)

        let body = try jsonBody(of: StubURLProtocol.recorded[0])
        XCTAssertEqual(body["refreshToken"] as? String, "to-revoke")
        XCTAssertEqual(Set(body.keys), Set(["refreshToken"]))
    }

    func testChangePasswordRequestBodyFieldNames() async throws {
        let client = BowtieClient(server: TestFixtures.baseURL, store: store, urlSession: makeSession())
        await client.setAccessTokenForTesting("access-1")

        StubURLProtocol.handler = { request in
            XCTAssertEqual(request.url?.path, "/api/v1/me/password")
            XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer access-1")
            return (204, Data(), [:])
        }

        try await client.changePassword(current: "old-pw", new: "new-pw")

        let body = try jsonBody(of: StubURLProtocol.recorded[0])
        XCTAssertEqual(body["currentPassword"] as? String, "old-pw")
        XCTAssertEqual(body["newPassword"] as? String, "new-pw")
        XCTAssertEqual(Set(body.keys), Set(["currentPassword", "newPassword"]))
    }

    func testCreateSessionRequestBodyFieldNames() async throws {
        let client = BowtieClient(server: TestFixtures.baseURL, store: store, urlSession: makeSession())
        await client.setAccessTokenForTesting("access-1")

        let sessionJSON = """
        {
          "viewerId": "vid-9",
          "playlistUrl": "/api/v1/stream/vid-9/index.m3u8?token=t",
          "session": {
            "videoCodec": "h264",
            "profile": "high",
            "backend": "videotoolbox",
            "channelName": "WABC"
          }
        }
        """.data(using: .utf8)!

        StubURLProtocol.handler = { request in
            XCTAssertEqual(request.url?.path, "/api/v1/sessions")
            XCTAssertEqual(request.httpMethod, "POST")
            return (200, sessionJSON, [:])
        }

        let caps = ClientCaps(
            videoCodecs: ["h264", "hevc"],
            audioCodecs: ["aac", "ac3"],
            maxHeight: 1080,
            profile: "high"
        )
        let created = try await client.createSession(channelId: 42, caps: caps)
        XCTAssertEqual(created.viewerId, "vid-9")

        let body = try jsonBody(of: StubURLProtocol.recorded[0])
        XCTAssertEqual(body["channelId"] as? Int, 42)
        guard let capsObj = body["caps"] as? [String: Any] else {
            return XCTFail("missing caps object")
        }
        XCTAssertEqual(capsObj["videoCodecs"] as? [String], ["h264", "hevc"])
        XCTAssertEqual(capsObj["audioCodecs"] as? [String], ["aac", "ac3"])
        XCTAssertEqual(capsObj["maxHeight"] as? Int, 1080)
        XCTAssertEqual(capsObj["profile"] as? String, "high")
        XCTAssertEqual(Set(body.keys), Set(["channelId", "caps"]))
        XCTAssertEqual(
            Set(capsObj.keys),
            Set(["videoCodecs", "audioCodecs", "maxHeight", "profile"])
        )
    }

    // MARK: Bearer attachment

    func testBearerAttached() async throws {
        let client = BowtieClient(server: TestFixtures.baseURL, store: store, urlSession: makeSession())
        await client.setAccessTokenForTesting("tok-abc")

        StubURLProtocol.handler = { request in
            XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer tok-abc")
            return (200, "[]".data(using: .utf8)!, [:])
        }

        _ = try await client.channels()
        XCTAssertEqual(StubURLProtocol.recorded.count, 1)
        XCTAssertEqual(
            StubURLProtocol.recorded[0].value(forHTTPHeaderField: "Authorization"),
            "Bearer tok-abc"
        )
    }

    func testNoAuthorizationOnStreamPaths() async throws {
        let client = BowtieClient(server: TestFixtures.baseURL, store: store, urlSession: makeSession())
        await client.setAccessTokenForTesting("tok-abc")

        let streamPath = "/api/v1/stream/vid-1/index.m3u8?token=secret"
        let req = await client.makeURLRequestForTesting(
            path: streamPath,
            method: "GET",
            authorize: true
        )
        XCTAssertNil(req.value(forHTTPHeaderField: "Authorization"))
        XCTAssertTrue(req.url!.absoluteString.contains("token=secret"))

        let meReq = await client.makeURLRequestForTesting(
            path: "/api/v1/me",
            method: "GET",
            authorize: true
        )
        XCTAssertEqual(meReq.value(forHTTPHeaderField: "Authorization"), "Bearer tok-abc")
    }

    // MARK: Single-flight refresh

    func testSingleFlightRefresh() async throws {
        let sessionStore = store!
        sessionStore.save(server: TestFixtures.baseURL, refreshToken: "old-refresh")
        let client = BowtieClient(server: TestFixtures.baseURL, store: sessionStore, urlSession: makeSession())
        await client.setAccessTokenForTesting("old-access")

        let userData = TestFixtures.sampleUserJSON.data(using: .utf8)!
        // Box shared counters so the @Sendable handler can mutate safely under a lock.
        final class FlightState: @unchecked Sendable {
            let lock = NSLock()
            var refreshHits = 0
            var refreshCompleted = false
        }
        let state = FlightState()

        StubURLProtocol.handler = { request in
            let path = request.url?.path ?? ""
            if path == "/api/v1/me" {
                state.lock.lock()
                let refreshed = state.refreshCompleted
                state.lock.unlock()

                if !refreshed {
                    // Initial calls with stale access token.
                    XCTAssertEqual(
                        request.value(forHTTPHeaderField: "Authorization"),
                        "Bearer old-access"
                    )
                    return (401, #"{"error":"unauthorized"}"#.data(using: .utf8)!, [:])
                }
                // Retries must see new access token AND new refresh already in store.
                XCTAssertEqual(
                    request.value(forHTTPHeaderField: "Authorization"),
                    "Bearer new-access"
                )
                XCTAssertEqual(
                    sessionStore.loadRefreshToken(),
                    "new-refresh",
                    "new refresh token must be persisted before retries fire"
                )
                return (200, userData, [:])
            }
            if path == "/api/v1/auth/refresh" {
                state.lock.lock()
                state.refreshHits += 1
                let rh = state.refreshHits
                state.lock.unlock()
                XCTAssertEqual(rh, 1, "exactly one refresh POST expected")
                // Simulate network latency so concurrent waiters coalesce.
                Thread.sleep(forTimeInterval: 0.05)
                state.lock.lock()
                state.refreshCompleted = true
                state.lock.unlock()
                return (200, TestFixtures.tokenPairJSON(access: "new-access", refresh: "new-refresh"), [:])
            }
            return (500, Data(), [:])
        }

        async let u1 = client.me()
        async let u2 = client.me()
        async let u3 = client.me()
        let users = try await [u1, u2, u3]
        XCTAssertEqual(users.count, 3)
        XCTAssertTrue(users.allSatisfy { $0.username == "alice" })

        let refreshPosts = StubURLProtocol.recorded.filter {
            $0.url?.path == "/api/v1/auth/refresh" && $0.httpMethod == "POST"
        }
        XCTAssertEqual(refreshPosts.count, 1, "exactly one refresh POST for 3 concurrent 401s")
        XCTAssertEqual(store.loadRefreshToken(), "new-refresh")

        // 3 initial 401s + 3 retries = 6 /me, plus 1 refresh.
        let meCalls = StubURLProtocol.recorded.filter { $0.url?.path == "/api/v1/me" }
        XCTAssertEqual(meCalls.count, 6)
    }

    func testRefreshFailureSignsOut() async throws {
        store.save(server: TestFixtures.baseURL, refreshToken: "dead-refresh")
        let client = BowtieClient(server: TestFixtures.baseURL, store: store, urlSession: makeSession())
        await client.setAccessTokenForTesting("old-access")

        StubURLProtocol.handler = { request in
            let path = request.url?.path ?? ""
            if path == "/api/v1/me" {
                return (401, #"{"error":"unauthorized"}"#.data(using: .utf8)!, [:])
            }
            if path == "/api/v1/auth/refresh" {
                return (401, #"{"error":"invalid refresh"}"#.data(using: .utf8)!, [:])
            }
            return (500, Data(), [:])
        }

        do {
            _ = try await client.me()
            XCTFail("expected unauthorized")
        } catch let error as BowtieError {
            XCTAssertEqual(error, .unauthorized)
        }

        XCTAssertNil(store.loadRefreshToken(), "token cleared on refresh failure")
        XCTAssertEqual(store.loadServer(), TestFixtures.baseURL, "server kept on refresh failure")
        let user = await client.currentUser
        XCTAssertNil(user)
    }

    // MARK: Error mapping

    func testTunersBusyDecoded() async throws {
        let client = BowtieClient(server: TestFixtures.baseURL, store: store, urlSession: makeSession())
        await client.setAccessTokenForTesting("access-1")

        let body = """
        {
          "error": "all tuners in use",
          "sessions": [
            {
              "id": "s1",
              "channelId": 1,
              "channelName": "WABC",
              "key": "k",
              "videoCodec": "h264",
              "profile": "high",
              "backend": "vt",
              "viewers": [
                { "id": "v1", "username": "bob", "lastSeen": "2024-06-15T20:00:00Z" }
              ],
              "startedAt": "2024-06-15T19:00:00Z"
            }
          ]
        }
        """.data(using: .utf8)!

        StubURLProtocol.handler = { _ in (503, body, [:]) }

        let caps = ClientCaps(videoCodecs: ["h264"], audioCodecs: ["aac"], maxHeight: 720, profile: "")
        do {
            _ = try await client.createSession(channelId: 1, caps: caps)
            XCTFail("expected tunersBusy")
        } catch let error as BowtieError {
            guard case .tunersBusy(let sessions) = error else {
                return XCTFail("expected tunersBusy, got \(error)")
            }
            XCTAssertEqual(sessions.count, 1)
            XCTAssertEqual(sessions[0].channelName, "WABC")
            XCTAssertEqual(sessions[0].viewers.map(\.username), ["bob"])
        }
    }

    func testNegotiation422() async throws {
        let client = BowtieClient(server: TestFixtures.baseURL, store: store, urlSession: makeSession())
        await client.setAccessTokenForTesting("access-1")

        let body = #"{"error":"no compatible codec"}"#.data(using: .utf8)!
        StubURLProtocol.handler = { _ in (422, body, [:]) }

        let caps = ClientCaps(videoCodecs: ["av1"], audioCodecs: ["opus"], maxHeight: 0, profile: "")
        do {
            _ = try await client.createSession(channelId: 1, caps: caps)
            XCTFail("expected negotiationFailed")
        } catch let error as BowtieError {
            XCTAssertEqual(error, .negotiationFailed("no compatible codec"))
        }
    }

    func testNotFound404() async throws {
        let client = BowtieClient(server: TestFixtures.baseURL, store: store, urlSession: makeSession())
        await client.setAccessTokenForTesting("access-1")

        StubURLProtocol.handler = { _ in
            (404, #"{"error":"channel not found"}"#.data(using: .utf8)!, [:])
        }

        let caps = ClientCaps(videoCodecs: ["h264"], audioCodecs: ["aac"], maxHeight: 1080, profile: "")
        do {
            _ = try await client.createSession(channelId: 999, caps: caps)
            XCTFail("expected notFound")
        } catch let error as BowtieError {
            XCTAssertEqual(error, .notFound)
        }
    }

    // MARK: Heartbeat (stream-token auth, no Bearer)

    func testHeartbeatRequestShapeTokenQueryNoAuthorization() async throws {
        let client = BowtieClient(server: TestFixtures.baseURL, store: store, urlSession: makeSession())
        await client.setAccessTokenForTesting("access-should-not-appear")

        StubURLProtocol.handler = { request in
            XCTAssertEqual(request.httpMethod, "POST")
            XCTAssertEqual(request.url?.path, "/api/v1/sessions/viewer-abc/heartbeat")
            let items = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems ?? []
            XCTAssertEqual(items.first(where: { $0.name == "token" })?.value, "stream-tok-xyz")
            XCTAssertNil(
                request.value(forHTTPHeaderField: "Authorization"),
                "heartbeat must not send Bearer — stream token alone authorizes"
            )
            return (204, Data(), [:])
        }

        await client.heartbeat(viewerId: "viewer-abc", token: "stream-tok-xyz")
        XCTAssertEqual(StubURLProtocol.recorded.count, 1)
    }

    func testHeartbeatSwallowsErrors() async throws {
        let client = BowtieClient(server: TestFixtures.baseURL, store: store, urlSession: makeSession())
        await client.setAccessTokenForTesting("access-1")

        StubURLProtocol.handler = { _ in
            (500, #"{"error":"boom"}"#.data(using: .utf8)!, [:])
        }

        // Must not throw.
        await client.heartbeat(viewerId: "vid", token: "tok")
        XCTAssertEqual(StubURLProtocol.recorded.count, 1)
    }

    // MARK: Best-effort teardown

    func testDeleteSwallowsErrors() async throws {
        let client = BowtieClient(server: TestFixtures.baseURL, store: store, urlSession: makeSession())
        await client.setAccessTokenForTesting("access-1")

        StubURLProtocol.handler = { _ in
            (500, #"{"error":"boom"}"#.data(using: .utf8)!, [:])
        }

        // Must not throw.
        await client.deleteSession(viewerId: "vid-gone")
        XCTAssertEqual(StubURLProtocol.recorded.count, 1)
        XCTAssertEqual(StubURLProtocol.recorded[0].httpMethod, "DELETE")
        XCTAssertTrue(StubURLProtocol.recorded[0].url!.path.contains("/api/v1/sessions/vid-gone"))
    }

    func testLogoutSwallowsErrors() async throws {
        store.save(server: TestFixtures.baseURL, refreshToken: "r1")
        let client = BowtieClient(server: TestFixtures.baseURL, store: store, urlSession: makeSession())
        await client.setAccessTokenForTesting("access-1")

        StubURLProtocol.handler = { _ in
            throw URLError(.timedOut)
        }

        await client.logout()
        XCTAssertNil(store.loadRefreshToken())
        XCTAssertEqual(store.loadServer(), TestFixtures.baseURL)
        let user = await client.currentUser
        XCTAssertNil(user)
    }

    func testGuideQueryParams() async throws {
        let client = BowtieClient(server: TestFixtures.baseURL, store: store, urlSession: makeSession())
        await client.setAccessTokenForTesting("access-1")

        StubURLProtocol.handler = { request in
            XCTAssertEqual(request.url?.path, "/api/v1/guide")
            let items = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)?.queryItems ?? []
            let keys = Set(items.compactMap(\.name))
            XCTAssertTrue(keys.contains("start"))
            XCTAssertTrue(keys.contains("stop"))
            return (200, "[]".data(using: .utf8)!, [:])
        }

        var cal = Calendar(identifier: .gregorian)
        cal.timeZone = TimeZone(secondsFromGMT: 0)!
        let start = cal.date(from: DateComponents(year: 2024, month: 6, day: 15, hour: 20))!
        let stop = cal.date(from: DateComponents(year: 2024, month: 6, day: 15, hour: 22))!
        let guide = try await client.guide(start: start, stop: stop)
        XCTAssertTrue(guide.isEmpty)
    }

    func testBootstrapUnauthorizedWhenNoToken() async throws {
        let client = BowtieClient(server: TestFixtures.baseURL, store: store, urlSession: makeSession())
        do {
            _ = try await client.bootstrapFromStoredToken()
            XCTFail("expected unauthorized")
        } catch let error as BowtieError {
            XCTAssertEqual(error, .unauthorized)
        }
        XCTAssertTrue(StubURLProtocol.recorded.isEmpty)
    }
}
