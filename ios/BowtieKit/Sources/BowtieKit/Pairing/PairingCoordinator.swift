import Foundation
import Observation

/// Owns the TV's pairing session and listener, and turns a phone's submitted address
/// into a connect attempt and, later, a sign-in link for the phone.
@MainActor @Observable
public final class PairingCoordinator {
    /// `http://ip:port/p?t=token`; nil until listening and whenever pairing is unavailable.
    public private(set) var pairURL: URL?

    @ObservationIgnored private let connect: @MainActor (String) async -> Bool
    @ObservationIgnored private let host: () -> String?
    @ObservationIgnored private let lifetime: Duration
    @ObservationIgnored private var session: PairingSession?
    @ObservationIgnored private(set) var listener: PairingListener?
    @ObservationIgnored private var lastServer: URL?
    @ObservationIgnored private var expiry: Task<Void, Never>?
    @ObservationIgnored private var generation = 0

    public init(connect: @escaping @MainActor (String) async -> Bool,
                host: @escaping () -> String? = LocalAddress.lanIPv4,
                lifetime: Duration = .seconds(900)) {
        self.connect = connect
        self.host = host
        self.lifetime = lifetime
    }

    /// No-op while a live session is listening; a locked-out or stopped session starts over with a new token.
    public func start() {
        if listener != nil, let session, session.state != .closed { return }
        stop()
        guard let ip = host() else { return }

        generation += 1
        let mine = generation
        let session = PairingSession()
        let listener = PairingListener(session: session, host: ip, queue: .main) { [weak self] address in
            MainActor.assumeIsolated { self?.submitted(address, from: session) }
        }
        self.session = session
        self.listener = listener
        listener.onFailure = { [weak self] in
            MainActor.assumeIsolated {
                guard let self, self.generation == mine else { return }
                self.stop()
            }
        }
        listener.onClosed = { [weak self] in
            MainActor.assumeIsolated {
                guard let self, self.generation == mine else { return }
                self.pairURL = nil   // locked out: the QR is dead; start() will issue a new one
            }
        }
        listener.start { [weak self] port in
            MainActor.assumeIsolated {
                guard let self, self.generation == mine else { return }
                guard let port else {
                    self.stop()
                    return
                }
                self.pairURL = URL(string: "http://\(ip):\(port)/p?t=\(session.token)")
            }
        }
        expiry = Task { [weak self, lifetime] in
            try? await Task.sleep(for: lifetime)
            guard !Task.isCancelled else { return }
            self?.stop()
        }
    }

    /// The TV got a sign-in code for `server`. Only the address the phone submitted gets the link;
    /// a TV that was switched to another server by hand must not send the phone elsewhere.
    public func codeIssued(server: URL, userCode: String) {
        guard let session, let lastServer, ServerURL.normalize(server.absoluteString) == lastServer,
              let link = PairingSession.linkURL(server: lastServer, userCode: userCode) else { return }
        session.codeIssued(link)
    }

    public func stop() {
        generation += 1
        expiry?.cancel()
        expiry = nil
        listener?.stop()
        listener = nil
        session?.close()
        session = nil
        lastServer = nil
        pairURL = nil
    }

    private func submitted(_ address: URL, from session: PairingSession) {
        guard self.session === session else { return }
        lastServer = address
        Task { [connect] in
            if await connect(address.absoluteString) == false { session.connectFailed(address) }
        }
    }
}
