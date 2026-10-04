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
    /// Single-flight: the root view and a SharePlay join may both ask.
    public func start() async {
        if let startTask {
            await startTask.value
            return
        }
        guard phase == .checking, let client else { return }
        let task = Task { @MainActor in
            do {
                let user = try await client.bootstrapFromStoredToken()
                // A server switch mid-bootstrap owns the phase now.
                guard self.client === client else { return }
                self.user = user
                self.phase = .ready
            } catch {
                guard self.client === client else { return }
                self.user = nil
                self.phase = .login
            }
        }
        startTask = task
        await task.value
        if startTask == task {
            startTask = nil
        }
    }

    private var startTask: Task<Void, Never>?

    public func signIn(username: String, password: String) async throws {
        guard let client else {
            throw BowtieError.invalidServerURL
        }
        let user = try await client.login(username: username, password: password)
        self.user = user
        self.phase = .ready
    }

    /// Quick sign-in (TV): a phone approved the code and `client` already holds
    /// the tokens, exactly as after `signIn`.
    public func completeDeviceSignIn(user: User) {
        guard phase == .login, client != nil else { return }
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
        startTask = nil
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

    // MARK: - SharePlay

    /// A group to join, waiting for the player (the root view hands it over).
    private(set) var pendingGroupJoin: GroupJoinRequest?
    /// "Sign in to <server> to watch together", shown as an alert.
    public var watchTogetherMessage: String?

    /// Joins SharePlay sessions for the app's lifetime (started by the root view).
    func observeGroupSessions() async {
        for await session in WatchChannelActivity.sessions() {
            await receiveGroup(LiveWatchGroup(session: session))
        }
    }

    /// Decide whether this app can join `group`'s server, switching to a
    /// saved server if that's where the login is.
    func receiveGroup(_ group: any WatchGroup) async {
        if phase == .checking {
            await start()
        }
        let activity = group.activity
        switch await watchTogetherDecision(for: activity) {
        case .join:
            pendingGroupJoin = GroupJoinRequest(group: group)
        case .switchServer(let url):
            selectServer(url)
            await start()
            if phase == .ready {
                pendingGroupJoin = GroupJoinRequest(group: group)
            } else {
                watchTogetherMessage = WatchTogetherDecision.signInMessage(serverName: activity.serverName)
            }
        case .signIn(let message):
            watchTogetherMessage = message
        }
    }

    /// Hand the pending group to the player (once).
    func takeGroupJoin() -> GroupJoinRequest? {
        defer { pendingGroupJoin = nil }
        return pendingGroupJoin
    }

    private func watchTogetherDecision(for activity: WatchChannelActivity) async -> WatchTogetherDecision {
        let current: WatchTogetherServer?
        if let url = serverURL {
            current = WatchTogetherServer(
                url: url,
                serverId: await serverId(of: url),
                isSignedIn: phase == .ready
            )
        } else {
            current = nil
        }
        let quick = WatchTogetherDecision.decide(
            serverId: activity.serverId,
            serverName: activity.serverName,
            current: current,
            saved: []
        )
        if quick == .join {
            return quick
        }

        // Ask every other saved server who it is (same server, another URL counts).
        let others = savedServers.map(\.url).filter { $0 != current?.url }
        let ids = await withTaskGroup(of: (URL, String?).self) { tasks in
            for url in others {
                tasks.addTask { [store, urlSession] in
                    let id = try? await BowtieClient(server: url, store: store, urlSession: urlSession)
                        .version()
                        .serverId
                    return (url, id)
                }
            }
            var ids: [URL: String] = [:]
            for await (url, id) in tasks {
                ids[url] = id
            }
            return ids
        }
        return WatchTogetherDecision.decide(
            serverId: activity.serverId,
            serverName: activity.serverName,
            current: current,
            saved: others.map {
                WatchTogetherServer(url: $0, serverId: ids[$0], isSignedIn: store.hasLogin(for: $0))
            }
        )
    }

    private func serverId(of url: URL) async -> String? {
        try? await BowtieClient(server: url, store: store, urlSession: urlSession).version().serverId
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
