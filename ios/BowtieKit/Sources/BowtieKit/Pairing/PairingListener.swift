import Foundation
import Network

/// Serves the pairing page: one TCP connection at a time, each parsed by `HTTPRequest`
/// and answered by the `PairingSession`. Everything runs on `queue`.
public final class PairingListener {
    private static let readLimit = 8192
    private static let readTimeout: TimeInterval = 5

    private let session: PairingSession
    private let queue: DispatchQueue
    private let onSubmit: (URL) -> Void
    private let host: String?
    private var listener: NWListener?
    private var busy = false

    /// Called on `queue` if the listener dies after it was ready, for any reason other than `stop()`.
    public var onFailure: (() -> Void)?

    /// With a `host`, binds only that IPv4 address (not every interface); otherwise all of them.
    public init(session: PairingSession, host: String? = nil, queue: DispatchQueue = .main, onSubmit: @escaping (URL) -> Void) {
        self.session = session
        self.host = host
        self.queue = queue
        self.onSubmit = onSubmit
    }

    /// Calls `completion` once, on `queue`, with the bound port, or nil if it couldn't bind.
    public func start(completion: @escaping (UInt16?) -> Void) {
        guard listener == nil else { return }
        let parameters = NWParameters.tcp
        if let host, let address = IPv4Address(host) {
            parameters.requiredLocalEndpoint = .hostPort(host: .ipv4(address), port: .any)
        } else if host != nil {
            queue.async { completion(nil) }
            return
        }
        guard let listener = try? NWListener(using: parameters) else {
            queue.async { completion(nil) }
            return
        }
        self.listener = listener
        var reported = false
        listener.stateUpdateHandler = { [weak self] state in
            switch state {
            case .ready where !reported:
                reported = true
                completion(listener.port?.rawValue)
            case .failed, .cancelled:
                self?.listener = nil
                if !reported {
                    reported = true
                    completion(nil)
                } else {
                    self?.onFailure?()
                }
            default:
                break
            }
        }
        listener.newConnectionHandler = { [weak self] connection in
            self?.accept(connection)
        }
        listener.start(queue: queue)
    }

    public func stop() {
        listener?.stateUpdateHandler = nil
        listener?.cancel()
        listener = nil
    }

    private func accept(_ connection: NWConnection) {
        guard !busy else {
            connection.cancel()
            return
        }
        busy = true

        var finished = false
        let finish = { [weak self] in
            guard !finished else { return }
            finished = true
            connection.cancel()
            self?.busy = false
        }
        let timeout = DispatchWorkItem { finish() }
        queue.asyncAfter(deadline: .now() + Self.readTimeout, execute: timeout)

        connection.stateUpdateHandler = { state in
            switch state {
            case .failed, .cancelled: finish()
            default: break
            }
        }
        connection.start(queue: queue)
        read(connection, buffer: Data(), timeout: timeout, finish: finish)
    }

    private func read(_ connection: NWConnection, buffer: Data, timeout: DispatchWorkItem, finish: @escaping () -> Void) {
        connection.receive(minimumIncompleteLength: 1, maximumLength: Self.readLimit) { [weak self] data, _, isComplete, error in
            guard let self, !timeout.isCancelled else { return }
            var buffer = buffer
            if let data { buffer.append(data) }

            let parsed = HTTPRequest.parse(buffer, limit: Self.readLimit)
            if parsed == .incomplete {
                if error != nil || isComplete { finish() } else { self.read(connection, buffer: buffer, timeout: timeout, finish: finish) }
                return
            }

            timeout.cancel()
            let (response, submitted) = self.session.handle(parsed)
            if let submitted { self.onSubmit(submitted) }
            connection.send(content: response.serialized(), contentContext: .finalMessage, isComplete: true,
                            completion: .contentProcessed { _ in finish() })
        }
    }
}
