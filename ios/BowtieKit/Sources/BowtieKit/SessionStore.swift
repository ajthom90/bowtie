import Foundation
import Security

// MARK: - Protocol

/// Saved servers plus the session (refresh token) for each. `loadServer`,
/// `loadRefreshToken` and `save` act on the active server, so `BowtieClient`
/// needs no knowledge of the others.
public protocol SessionStore: Sendable {
    func loadServer() -> URL?
    func loadRefreshToken() -> String?
    /// Makes `server` active (adding it to the saved list) and stores its
    /// refresh token; a nil token signs that server out. A nil `server` only
    /// deactivates: saved servers and their logins are kept.
    func save(server: URL?, refreshToken: String?)

    /// Saved servers, oldest first.
    func savedServers() -> [SavedServer]
    /// Makes a saved server active.
    func selectServer(_ url: URL)
    /// Forgets a server and its login; if it was active, none is.
    func removeServer(_ url: URL)
    /// Whether a login (refresh token) is stored for `url`, active or not.
    func hasLogin(for url: URL) -> Bool
}

public struct SavedServer: Codable, Equatable, Identifiable, Sendable {
    public let url: URL
    public var id: String { url.absoluteString }

    /// "host:port" (port omitted when it is the scheme default).
    public var label: String {
        let host = url.host ?? url.absoluteString
        if let port = url.port {
            return "\(host):\(port)"
        }
        return host
    }

    public init(url: URL) {
        self.url = url
    }
}

// MARK: - Store

/// String key/value persistence behind `MultiServerStore`.
public protocol KeyValueBacking: Sendable {
    func read(account: String) -> String?
    func write(account: String, value: String)
    func delete(account: String)
}

public class MultiServerStore: SessionStore, @unchecked Sendable {
    private let backing: KeyValueBacking
    private let lock = NSLock()
    private let serversAccount = "servers"
    private let activeAccount = "activeServer"

    public init(backing: KeyValueBacking) {
        self.backing = backing
        migrateLegacy()
    }

    public func loadServer() -> URL? {
        lock.lock()
        defer { lock.unlock() }
        return active()
    }

    public func loadRefreshToken() -> String? {
        lock.lock()
        defer { lock.unlock() }
        guard let url = active() else { return nil }
        return backing.read(account: tokenAccount(url))
    }

    public func save(server: URL?, refreshToken: String?) {
        lock.lock()
        defer { lock.unlock() }
        guard let server else {
            backing.delete(account: activeAccount)
            return
        }
        add(server)
        backing.write(account: activeAccount, value: server.absoluteString)
        if let refreshToken {
            backing.write(account: tokenAccount(server), value: refreshToken)
        } else {
            backing.delete(account: tokenAccount(server))
        }
    }

    public func savedServers() -> [SavedServer] {
        lock.lock()
        defer { lock.unlock() }
        return servers()
    }

    public func selectServer(_ url: URL) {
        lock.lock()
        defer { lock.unlock() }
        add(url)
        backing.write(account: activeAccount, value: url.absoluteString)
    }

    public func removeServer(_ url: URL) {
        lock.lock()
        defer { lock.unlock() }
        writeServers(servers().filter { $0.url != url })
        backing.delete(account: tokenAccount(url))
        if active() == url {
            backing.delete(account: activeAccount)
        }
    }

    public func hasLogin(for url: URL) -> Bool {
        lock.lock()
        defer { lock.unlock() }
        return backing.read(account: tokenAccount(url)) != nil
    }

    // MARK: Private (call with lock held)

    private func active() -> URL? {
        backing.read(account: activeAccount).flatMap(URL.init(string:))
    }

    private func servers() -> [SavedServer] {
        guard let raw = backing.read(account: serversAccount),
              let list = try? JSONDecoder().decode([SavedServer].self, from: Data(raw.utf8))
        else {
            return []
        }
        return list
    }

    private func writeServers(_ list: [SavedServer]) {
        guard let data = try? JSONEncoder().encode(list) else { return }
        backing.write(account: serversAccount, value: String(decoding: data, as: UTF8.self))
    }

    private func add(_ url: URL) {
        var list = servers()
        if !list.contains(where: { $0.url == url }) {
            list.append(SavedServer(url: url))
            writeServers(list)
        }
    }

    private func tokenAccount(_ url: URL) -> String {
        "refreshToken:" + url.absoluteString
    }

    /// Single-server builds stored "server" + "refreshToken".
    private func migrateLegacy() {
        lock.lock()
        defer { lock.unlock() }
        guard let raw = backing.read(account: "server"), let url = URL(string: raw) else { return }
        add(url)
        if active() == nil {
            backing.write(account: activeAccount, value: url.absoluteString)
        }
        if let token = backing.read(account: "refreshToken") {
            backing.write(account: tokenAccount(url), value: token)
        }
        backing.delete(account: "server")
        backing.delete(account: "refreshToken")
    }
}

/// In-memory store for tests and previews.
public final class InMemorySessionStore: MultiServerStore, @unchecked Sendable {
    public init() {
        super.init(backing: MemoryBacking())
    }
}

/// Keychain-backed store. Not unit-tested against the real keychain in
/// `swift test` (macOS test host may prompt); contract tests use the memory backing.
public final class KeychainSessionStore: MultiServerStore, @unchecked Sendable {
    public init(service: String = "app.bowtie") {
        super.init(backing: KeychainBacking(service: service))
    }
}

// MARK: - Backings

public final class MemoryBacking: KeyValueBacking, @unchecked Sendable {
    private let lock = NSLock()
    private var values: [String: String] = [:]

    public init() {}

    public func read(account: String) -> String? {
        lock.lock()
        defer { lock.unlock() }
        return values[account]
    }

    public func write(account: String, value: String) {
        lock.lock()
        defer { lock.unlock() }
        values[account] = value
    }

    public func delete(account: String) {
        lock.lock()
        defer { lock.unlock() }
        values[account] = nil
    }
}

public final class KeychainBacking: KeyValueBacking, @unchecked Sendable {
    private let service: String

    public init(service: String) {
        self.service = service
    }

    public func read(account: String) -> String? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        guard status == errSecSuccess, let data = item as? Data else {
            return nil
        }
        return String(data: data, encoding: .utf8)
    }

    public func write(account: String, value: String) {
        delete(account: account)
        let data = Data(value.utf8)
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecValueData as String: data,
            kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
        ]
        SecItemAdd(query as CFDictionary, nil)
    }

    public func delete(account: String) {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
        SecItemDelete(query as CFDictionary)
    }
}
