import Foundation
import Security

// MARK: - Protocol

public protocol SessionStore: Sendable {
    func loadServer() -> URL?
    func loadRefreshToken() -> String?
    /// Pass `nil` for either value to clear that field.
    func save(server: URL?, refreshToken: String?)
}

// MARK: - In-memory (tests)

public final class InMemorySessionStore: SessionStore, @unchecked Sendable {
    private let lock = NSLock()
    private var server: URL?
    private var refreshToken: String?

    public init() {}

    public func loadServer() -> URL? {
        lock.lock()
        defer { lock.unlock() }
        return server
    }

    public func loadRefreshToken() -> String? {
        lock.lock()
        defer { lock.unlock() }
        return refreshToken
    }

    public func save(server: URL?, refreshToken: String?) {
        lock.lock()
        defer { lock.unlock() }
        self.server = server
        self.refreshToken = refreshToken
    }
}

// MARK: - Keychain

/// Thin Keychain-backed store. Not unit-tested against the real keychain in
/// `swift test` (macOS test host may prompt); contract tests use `InMemorySessionStore`.
public final class KeychainSessionStore: SessionStore, @unchecked Sendable {
    private let service: String
    private let serverAccount = "server"
    private let refreshAccount = "refreshToken"

    public init(service: String = "app.bowtie") {
        self.service = service
    }

    public func loadServer() -> URL? {
        guard let raw = read(account: serverAccount), let url = URL(string: raw) else {
            return nil
        }
        return url
    }

    public func loadRefreshToken() -> String? {
        read(account: refreshAccount)
    }

    public func save(server: URL?, refreshToken: String?) {
        if let server {
            write(account: serverAccount, value: server.absoluteString)
        } else {
            delete(account: serverAccount)
        }
        if let refreshToken {
            write(account: refreshAccount, value: refreshToken)
        } else {
            delete(account: refreshAccount)
        }
    }

    // MARK: SecItem helpers

    private func read(account: String) -> String? {
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

    private func write(account: String, value: String) {
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

    private func delete(account: String) {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
        ]
        SecItemDelete(query as CFDictionary)
    }
}
