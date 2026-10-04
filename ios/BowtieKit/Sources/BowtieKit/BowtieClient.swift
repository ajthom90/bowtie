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

private struct ScheduleRecordingBody: Encodable {
    let channelId: Int64
    /// RFC 3339, whole seconds: the guide program's exact start.
    let programStart: String
    /// Omitted unless true.
    let force: Bool?
}

private struct ProtectRecordingBody: Encodable {
    let protected: Bool
}

private struct RecordingPositionBody: Encodable {
    let positionSec: Int
}

private struct RecordingConflictBody: Decodable {
    let error: String
    let tunerCount: Int
    let conflicts: [Recording]
}

private struct ErrorBody: Decodable {
    let error: String
}

private struct TunersBusyBody: Decodable {
    let error: String
    let sessions: [ActiveSessionSummary]
    let otherInUse: Int?
}

// MARK: - Client

/// Viewer-only HTTP client for the Bowtie API.
///
/// Auth: bearer access token in memory; refresh token in `SessionStore`.
/// Refresh is single-flight via in-actor `Task` coalescing; the new refresh
/// token is persisted before any 401-retry fires.
public actor BowtieClient {
    private let server: URL

    /// Server base URL; resolves server-relative URLs such as recording playlists.
    public nonisolated var serverURL: URL { server }
    private let store: SessionStore
    private let urlSession: URLSession

    public private(set) var currentUser: User?
    private var accessToken: String?

    /// In-flight refresh shared by concurrent 401 waiters.
    private var refreshTask: Task<Void, Error>?

    private let decoder: JSONDecoder = {
        let d = JSONDecoder()
        // RFC 3339, with or without fractional seconds (Go may emit either).
        d.dateDecodingStrategy = .custom { decoder in
            let container = try decoder.singleValueContainer()
            let string = try container.decode(String.self)
            if let date = BowtieClient.parseDate(string) {
                return date
            }
            throw DecodingError.dataCorruptedError(
                in: container,
                debugDescription: "Expected an RFC 3339 date, got \(string)"
            )
        }
        return d
    }()

    private static func parseDate(_ string: String) -> Date? {
        let plain = ISO8601DateFormatter()
        plain.formatOptions = [.withInternetDateTime]
        if let date = plain.date(from: string) {
            return date
        }
        let fractional = ISO8601DateFormatter()
        fractional.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return fractional.date(from: string)
    }

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

    /// Session liveness beat (spec C). Auth is the stream token query param only —
    /// never Bearer (avoids racing access-token refresh mid-session). Best-effort.
    public func heartbeat(viewerId: String, token: String) async {
        let encoded = viewerId.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? viewerId
        var components = URLComponents(
            url: ServerURL.resolve(path: "/api/v1/sessions/\(encoded)/heartbeat", against: server),
            resolvingAgainstBaseURL: false
        )!
        components.queryItems = [URLQueryItem(name: "token", value: token)]
        guard let url = components.url else { return }
        _ = try? await sendRawURL(
            url: url,
            method: "POST",
            body: nil as EmptyBody?,
            authorize: false,
            retryOn401: false
        )
    }

    // MARK: - Favorites / recents

    /// Stars (`PUT`) or unstars (`DELETE`) a channel for the signed-in user.
    /// Both are idempotent and answer 204. Throws `.notFound` for an unknown or
    /// disabled channel, or on a server without favorites.
    public func setFavorite(channelId: Int64, on: Bool) async throws {
        _ = try await sendRaw(
            path: "/api/v1/me/favorites/\(channelId)",
            method: on ? "PUT" : "DELETE",
            body: nil as EmptyBody?,
            authorize: true,
            retryOn401: true
        )
    }

    /// Recently watched channels, newest first. Throws `.notFound` on a server
    /// without recents.
    public func recents(limit: Int = 8) async throws -> [RecentChannel] {
        var components = URLComponents(
            url: ServerURL.resolve(path: "/api/v1/me/recents", against: server),
            resolvingAgainstBaseURL: false
        )!
        components.queryItems = [URLQueryItem(name: "limit", value: String(limit))]
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

    /// Clears the signed-in user's watch history (204).
    public func clearRecents() async throws {
        _ = try await sendRaw(
            path: "/api/v1/me/recents",
            method: "DELETE",
            body: nil as EmptyBody?,
            authorize: true,
            retryOn401: true
        )
    }

    // MARK: - DVR recordings

    /// `GET /recordings`, optionally filtered by `?state=`.
    public func recordings(filter: RecordingsFilter?) async throws -> [Recording] {
        var components = URLComponents(
            url: ServerURL.resolve(path: "/api/v1/recordings", against: server),
            resolvingAgainstBaseURL: false
        )!
        if let filter {
            components.queryItems = [URLQueryItem(name: "state", value: filter.rawValue)]
        }
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

    /// Schedules the guide program on `channelId` that starts exactly at `programStart`.
    /// Throws `.recordingConflict` on a tuner conflict unless `force`.
    public func scheduleRecording(
        channelId: Int64,
        programStart: Date,
        force: Bool = false
    ) async throws -> ScheduledRecording {
        try await send(
            path: "/api/v1/recordings",
            method: "POST",
            body: ScheduleRecordingBody(
                channelId: channelId,
                programStart: isoFormatter.string(from: programStart),
                force: force ? true : nil
            ),
            authorize: true,
            retryOn401: true
        )
    }

    /// Cancels a scheduled recording, or deletes a recording and its files.
    public func deleteRecording(id: Int64) async throws {
        _ = try await sendRaw(
            path: "/api/v1/recordings/\(id)",
            method: "DELETE",
            body: nil as EmptyBody?,
            authorize: true,
            retryOn401: true
        )
    }

    /// Stops a recording now and keeps what was recorded.
    public func stopRecording(id: Int64) async throws {
        _ = try await sendRaw(
            path: "/api/v1/recordings/\(id)/stop",
            method: "POST",
            body: nil as EmptyBody?,
            authorize: true,
            retryOn401: true
        )
    }

    /// Keeps (or stops keeping) a recording from automatic deletion.
    public func setRecordingProtected(id: Int64, protected: Bool) async throws -> Recording {
        try await send(
            path: "/api/v1/recordings/\(id)",
            method: "PATCH",
            body: ProtectRecordingBody(protected: protected),
            authorize: true,
            retryOn401: true
        )
    }

    /// Token-signed HLS VOD URL plus the caller's resume position.
    public func playRecording(id: Int64) async throws -> RecordingPlayback {
        try await send(
            path: "/api/v1/recordings/\(id)/play",
            method: "POST",
            body: nil as EmptyBody?,
            authorize: true,
            retryOn401: true
        )
    }

    public func saveRecordingPosition(id: Int64, positionSec: Int) async throws {
        _ = try await sendRaw(
            path: "/api/v1/recordings/\(id)/position",
            method: "PUT",
            body: RecordingPositionBody(positionSec: max(0, positionSec)),
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
        case 409:
            if let conflict = try? decoder.decode(RecordingConflictBody.self, from: data) {
                throw BowtieError.recordingConflict(
                    tunerCount: conflict.tunerCount,
                    conflicts: conflict.conflicts,
                    message: conflict.error
                )
            }
            let message = (try? decoder.decode(ErrorBody.self, from: data))?.error
                ?? HTTPURLResponse.localizedString(forStatusCode: status)
            throw BowtieError.server(status: status, message: message)
        case 422:
            let message = (try? decoder.decode(ErrorBody.self, from: data))?.error ?? "negotiation failed"
            throw BowtieError.negotiationFailed(message)
        case 503:
            if let busy = try? decoder.decode(TunersBusyBody.self, from: data) {
                throw BowtieError.tunersBusy(busy.sessions, otherInUse: busy.otherInUse ?? 0)
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
