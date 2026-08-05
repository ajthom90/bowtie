import Foundation

// MARK: - Request / error wire shapes

private struct LoginBody: Encodable {
    let username: String
    let password: String
}

private struct RefreshBody: Encodable {
    let refreshToken: String
}

private struct ChangePasswordBody: Encodable {
    let currentPassword: String
    let newPassword: String
}

private struct CreateSessionBody: Encodable {
    let channelId: Int64
    let caps: ClientCaps
}

private struct ErrorBody: Decodable {
    let error: String
}

private struct TunersBusyBody: Decodable {
    let error: String
    let sessions: [ActiveSessionSummary]
}

// MARK: - Client

/// Viewer-only HTTP client for the Bowtie API.
///
/// Auth: bearer access token in memory; refresh token in `SessionStore`.
/// Refresh is single-flight via in-actor `Task` coalescing; the new refresh
/// token is persisted before any 401-retry fires.
public actor BowtieClient {
    private let server: URL
    private let store: SessionStore
    private let urlSession: URLSession

    public private(set) var currentUser: User?
    private var accessToken: String?

    /// In-flight refresh shared by concurrent 401 waiters.
    private var refreshTask: Task<Void, Error>?

    private let decoder: JSONDecoder = {
        let d = JSONDecoder()
        d.dateDecodingStrategy = .iso8601
        return d
    }()

    private let encoder: JSONEncoder = {
        let e = JSONEncoder()
        // Default keys are camelCase — matches OpenAPI field names.
        return e
    }()

    private let isoFormatter: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime]
        return f
    }()

    public init(server: URL, store: SessionStore, urlSession: URLSession = .shared) {
        self.server = server
        self.store = store
        self.urlSession = urlSession
    }

    // MARK: - Viewer allowlist

    public func login(username: String, password: String) async throws -> User {
        let pair: TokenPair = try await send(
            path: "/api/v1/auth/login",
            method: "POST",
            body: LoginBody(username: username, password: password),
            authorize: false,
            retryOn401: false
        )
        applyTokens(pair)
        return pair.user
    }

    /// Rotate the stored refresh token into a new pair. Throws `.unauthorized` if absent/dead.
    public func bootstrapFromStoredToken() async throws -> User {
        guard store.loadRefreshToken() != nil else {
            throw BowtieError.unauthorized
        }
        try await performRefresh()
        guard let user = currentUser else {
            throw BowtieError.unauthorized
        }
        return user
    }

    /// Best-effort logout: always clears local session even if the network call fails.
    public func logout() async {
        let token = store.loadRefreshToken()
        if let token {
            _ = try? await sendRaw(
                path: "/api/v1/auth/logout",
                method: "POST",
                body: RefreshBody(refreshToken: token),
                authorize: false,
                retryOn401: false
            )
        }
        clearSession(keepServer: true)
    }

    public func changePassword(current: String, new: String) async throws {
        _ = try await sendRaw(
            path: "/api/v1/me/password",
            method: "POST",
            body: ChangePasswordBody(currentPassword: current, newPassword: new),
            authorize: true,
            retryOn401: true
        )
    }

    public func channels() async throws -> [Channel] {
        try await send(
            path: "/api/v1/channels",
            method: "GET",
            body: nil as EmptyBody?,
            authorize: true,
            retryOn401: true
        )
    }

    public func guide(start: Date, stop: Date) async throws -> [GuideChannel] {
        var components = URLComponents(
            url: ServerURL.resolve(path: "/api/v1/guide", against: server),
            resolvingAgainstBaseURL: false
        )!
        components.queryItems = [
            URLQueryItem(name: "start", value: isoFormatter.string(from: start)),
            URLQueryItem(name: "stop", value: isoFormatter.string(from: stop)),
        ]
        guard let url = components.url else {
            throw BowtieError.invalidServerURL
        }
        return try await sendURL(
            url: url,
            method: "GET",
            body: nil as EmptyBody?,
            authorize: true,
            retryOn401: true
        )
    }

    public func createSession(channelId: Int64, caps: ClientCaps) async throws -> CreatedSession {
        try await send(
            path: "/api/v1/sessions",
            method: "POST",
            body: CreateSessionBody(channelId: channelId, caps: caps),
            authorize: true,
            retryOn401: true
        )
    }

    /// Best-effort DELETE; errors are swallowed.
    public func deleteSession(viewerId: String) async {
        let encoded = viewerId.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? viewerId
        _ = try? await sendRaw(
            path: "/api/v1/sessions/\(encoded)",
            method: "DELETE",
            body: nil as EmptyBody?,
            authorize: true,
            retryOn401: true
        )
    }

    public func me() async throws -> User {
        let user: User = try await send(
            path: "/api/v1/me",
            method: "GET",
            body: nil as EmptyBody?,
            authorize: true,
            retryOn401: true
        )
        currentUser = user
        return user
    }

    // MARK: - Test hooks (@testable)

    /// Seeds the in-memory access token without a network round-trip.
    func setAccessTokenForTesting(_ token: String?) {
        accessToken = token
    }

    /// Builds a URLRequest applying the same Authorization policy as live calls.
    func makeURLRequestForTesting(
        path: String,
        method: String,
        authorize: Bool
    ) -> URLRequest {
        let url = ServerURL.resolve(path: path, against: server)
        return makeRequest(url: url, method: method, bodyData: nil, authorize: authorize)
    }

    // MARK: - Token / session state

    private func applyTokens(_ pair: TokenPair) {
        accessToken = pair.accessToken
        currentUser = pair.user
        // Persist new refresh BEFORE any 401-retry observes it.
        store.save(server: server, refreshToken: pair.refreshToken)
    }

    private func clearSession(keepServer: Bool) {
        accessToken = nil
        currentUser = nil
        if keepServer {
            store.save(server: store.loadServer() ?? server, refreshToken: nil)
        } else {
            store.save(server: nil, refreshToken: nil)
        }
    }

    // MARK: - Single-flight refresh

    private func refreshSingleFlight() async throws {
        if let refreshTask {
            try await refreshTask.value
            return
        }
        let task = Task {
            try await self.performRefresh()
        }
        refreshTask = task
        do {
            try await task.value
            refreshTask = nil
        } catch {
            refreshTask = nil
            throw error
        }
    }

    private func performRefresh() async throws {
        guard let refreshToken = store.loadRefreshToken() else {
            clearSession(keepServer: true)
            throw BowtieError.unauthorized
        }

        do {
            let pair: TokenPair = try await send(
                path: "/api/v1/auth/refresh",
                method: "POST",
                body: RefreshBody(refreshToken: refreshToken),
                authorize: false,
                retryOn401: false
            )
            applyTokens(pair)
        } catch let error as BowtieError {
            if case .unauthorized = error {
                clearSession(keepServer: true)
            }
            throw error
        }
    }

    // MARK: - HTTP

    private struct EmptyBody: Encodable {}

    private func send<B: Encodable, T: Decodable>(
        path: String,
        method: String,
        body: B?,
        authorize: Bool,
        retryOn401: Bool
    ) async throws -> T {
        let data = try await sendRaw(
            path: path,
            method: method,
            body: body,
            authorize: authorize,
            retryOn401: retryOn401
        )
        do {
            return try decoder.decode(T.self, from: data)
        } catch {
            throw BowtieError.network("decode failed: \(error.localizedDescription)")
        }
    }

    private func sendURL<B: Encodable, T: Decodable>(
        url: URL,
        method: String,
        body: B?,
        authorize: Bool,
        retryOn401: Bool
    ) async throws -> T {
        let data = try await sendRawURL(
            url: url,
            method: method,
            body: body,
            authorize: authorize,
            retryOn401: retryOn401
        )
        do {
            return try decoder.decode(T.self, from: data)
        } catch {
            throw BowtieError.network("decode failed: \(error.localizedDescription)")
        }
    }

    private func sendRaw<B: Encodable>(
        path: String,
        method: String,
        body: B?,
        authorize: Bool,
        retryOn401: Bool
    ) async throws -> Data {
        let url = ServerURL.resolve(path: path, against: server)
        return try await sendRawURL(
            url: url,
            method: method,
            body: body,
            authorize: authorize,
            retryOn401: retryOn401
        )
    }

    private func sendRawURL<B: Encodable>(
        url: URL,
        method: String,
        body: B?,
        authorize: Bool,
        retryOn401: Bool
    ) async throws -> Data {
        let bodyData: Data?
        if let body {
            bodyData = try encoder.encode(body)
        } else {
            bodyData = nil
        }

        let request = makeRequest(url: url, method: method, bodyData: bodyData, authorize: authorize)
        let (data, response) = try await perform(request)

        if let http = response as? HTTPURLResponse, http.statusCode == 401, retryOn401 {
            try await refreshSingleFlight()
            let retry = makeRequest(url: url, method: method, bodyData: bodyData, authorize: authorize)
            let (data2, response2) = try await perform(retry)
            return try mapSuccess(data: data2, response: response2)
        }

        return try mapSuccess(data: data, response: response)
    }

    private func makeRequest(
        url: URL,
        method: String,
        bodyData: Data?,
        authorize: Bool
    ) -> URLRequest {
        var request = URLRequest(url: url)
        request.httpMethod = method
        if let bodyData {
            request.httpBody = bodyData
            request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        }
        request.setValue("application/json", forHTTPHeaderField: "Accept")

        if authorize, let token = accessToken, shouldAttachBearer(to: url) {
            request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        return request
    }

    /// Never attach Authorization to media/stream URLs (HLS uses `?token=`).
    private func shouldAttachBearer(to url: URL) -> Bool {
        let path = url.path
        return !path.contains("/api/v1/stream/")
    }

    private func perform(_ request: URLRequest) async throws -> (Data, URLResponse) {
        do {
            return try await urlSession.data(for: request)
        } catch {
            throw BowtieError.network(error.localizedDescription)
        }
    }

    private func mapSuccess(data: Data, response: URLResponse) throws -> Data {
        guard let http = response as? HTTPURLResponse else {
            throw BowtieError.network("non-HTTP response")
        }
        let status = http.statusCode
        switch status {
        case 200...299:
            return data
        case 401:
            throw BowtieError.unauthorized
        case 404:
            throw BowtieError.notFound
        case 422:
            let message = (try? decoder.decode(ErrorBody.self, from: data))?.error ?? "negotiation failed"
            throw BowtieError.negotiationFailed(message)
        case 503:
            if let busy = try? decoder.decode(TunersBusyBody.self, from: data) {
                throw BowtieError.tunersBusy(busy.sessions)
            }
            let message = (try? decoder.decode(ErrorBody.self, from: data))?.error ?? "service unavailable"
            throw BowtieError.server(status: status, message: message)
        default:
            let message = (try? decoder.decode(ErrorBody.self, from: data))?.error
                ?? HTTPURLResponse.localizedString(forStatusCode: status)
            throw BowtieError.server(status: status, message: message)
        }
    }
}
