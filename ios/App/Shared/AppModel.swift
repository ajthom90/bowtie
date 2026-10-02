import Foundation
import Observation
import BowtieKit

/// Auth / connection state machine and DI root for the viewer apps.
@Observable
@MainActor
public final class AppModel {
    public enum Phase: Equatable {
        case connect
        case login
        case ready
        case checking
    }

    public private(set) var phase: Phase
    public private(set) var user: User?
    public var client: BowtieClient?

    /// Currently configured server base URL (nil on Connect).
    public var serverURL: URL? { store.loadServer() }

    private let store: SessionStore
    private let urlSession: URLSession

    /// - Parameters:
    ///   - store: persisted server + refresh token.
    ///   - urlSession: injectable for tests (healthz + client traffic).
    public init(store: SessionStore, urlSession: URLSession = .shared) {
        self.store = store
        self.urlSession = urlSession

        if let server = store.loadServer() {
            self.client = BowtieClient(server: server, store: store, urlSession: urlSession)
            self.phase = store.loadRefreshToken() != nil ? .checking : .login
        } else {
            self.client = nil
            self.phase = .connect
        }
        self.savedServers = store.savedServers()
    }

    /// Normalize + healthz-validate `rawURL`, persist server, advance to `.login`.
    @discardableResult
    public func connect(rawURL: String) async -> Bool {
        guard let url = ServerURL.normalize(rawURL) else { return false }
        let ok = await validateServer(url)
        guard ok else { return false }

        store.save(server: url, refreshToken: nil)
        savedServers = store.savedServers()
        client = BowtieClient(server: url, store: store, urlSession: urlSession)
        user = nil
        phase = .login
        return true
    }

    /// Bootstrap from a stored refresh token while in `.checking`.
    /// Success → `.ready`; failure → `.login` (server kept).
    public func start() async {
        guard phase == .checking, let client else { return }
        do {
            let user = try await client.bootstrapFromStoredToken()
            self.user = user
            self.phase = .ready
        } catch {
            self.user = nil
            self.phase = .login
        }
    }

    public func signIn(username: String, password: String) async throws {
        guard let client else {
            throw BowtieError.invalidServerURL
        }
        let user = try await client.login(username: username, password: password)
        self.user = user
        self.phase = .ready
    }

    /// Sign out → `.login`, keeping the server URL for reconnect.
    public func signOut() async {
        await client?.logout()
        user = nil
        phase = .login
    }

    /// Returns to Connect. The current server stays saved, signed in, for
    /// switching back.
    public func changeServer() {
        store.save(server: nil, refreshToken: nil)
        client = nil
        user = nil
        phase = .connect
        savedServers = store.savedServers()
    }

    /// Servers the user has connected to, oldest first.
    public private(set) var savedServers: [SavedServer] = []

    /// Switch to a saved server: its saved login → `.checking` (call `start`),
    /// otherwise `.login`.
    public func selectServer(_ url: URL) {
        store.selectServer(url)
        client = BowtieClient(server: url, store: store, urlSession: urlSession)
        user = nil
        phase = store.loadRefreshToken() != nil ? .checking : .login
    }

    public func removeServer(_ url: URL) {
        store.removeServer(url)
        savedServers = store.savedServers()
        if store.loadServer() == nil, phase != .connect {
            client = nil
            user = nil
            phase = .connect
        }
    }

    // MARK: - Private

    /// `GET {url}/healthz` must be HTTP 200, using the injected session so tests can stub it.
    private func validateServer(_ url: URL) async -> Bool {
        let healthURL = url.appendingPathComponent("healthz")
        var request = URLRequest(url: healthURL)
        request.httpMethod = "GET"
        request.timeoutInterval = 2
        do {
            let (_, response) = try await urlSession.data(for: request)
            guard let http = response as? HTTPURLResponse else { return false }
            return http.statusCode == 200
        } catch {
            return false
        }
    }
}
