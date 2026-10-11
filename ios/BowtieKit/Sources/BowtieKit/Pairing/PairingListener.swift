import Foundation
import Network

/// Serves the pairing page: one TCP connection at a time, each parsed by `HTTPRequest`
/// and answered by the `PairingSession`. Everything runs on `queue`.
public final class PairingListener {
    private static let readLimit = 8192
    private static let readTimeout: TimeInterval = 5
    private static let maxOpen = 4

    private let session: PairingSession
    private let queue: DispatchQueue
    private let onSubmit: (URL) -> Void
    private let host: String?
    private var listener: NWListener?
    private var open: [Slot] = []

    /// Called on `queue` if the listener dies after it was ready, for any reason other than `stop()`.
    public var onFailure: (() -> Void)?
    /// Called on `queue` when a request locks the session out; the listener keeps answering 403 until stopped.
    public var onClosed: (() -> Void)?

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
                listener.stateUpdateHandler = nil
                listener.newConnectionHandler = nil
                listener.cancel()
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

    /// Test seam: cancels the underlying listener the way a network failure would, leaving handlers in place.
    func simulateFailure() {
        listener?.cancel()
    }

    public func stop() {
        listener?.stateUpdateHandler = nil
        listener?.cancel()
        listener = nil
    }

    private final class Slot {
        let connection: NWConnection
        var deadline: DispatchWorkItem?
        var finished = false
        init(_ connection: NWConnection) { self.connection = connection }
    }

    /// Connections are served independently (browsers open idle speculative sockets next to the real
    /// request), each on its own deadline from accept; at most `maxOpen` at once. All session access
    /// stays on `queue`, so state changes are serialized.
    private func accept(_ connection: NWConnection) {
        guard open.count < Self.maxOpen else {
            connection.cancel()
            return
        }
        let slot = Slot(connection)
        open.append(slot)
        let deadline = DispatchWorkItem { [weak self] in self?.finish(slot) }
        slot.deadline = deadline
        queue.asyncAfter(deadline: .now() + Self.readTimeout, execute: deadline)
        connection.stateUpdateHandler = { [weak self] state in
            switch state {
            case .failed, .cancelled: self?.finish(slot)
            default: break
            }
        }
        connection.start(queue: queue)
        read(slot, buffer: Data())
    }

    private func finish(_ slot: Slot) {
        guard !slot.finished else { return }
        slot.finished = true
        slot.deadline?.cancel()
        slot.connection.stateUpdateHandler = nil
        slot.connection.cancel()
        open.removeAll { $0 === slot }
    }

    private func read(_ slot: Slot, buffer: Data) {
        slot.connection.receive(minimumIncompleteLength: 1, maximumLength: Self.readLimit) { [weak self] data, _, isComplete, error in
            guard let self, !slot.finished else { return }
            var buffer = buffer
            if let data { buffer.append(data) }

            let parsed = HTTPRequest.parse(buffer, limit: Self.readLimit)
            if parsed == .incomplete {
                if error != nil || isComplete { self.finish(slot) } else { self.read(slot, buffer: buffer) }
                return
            }

            let wasClosed = self.session.state == .closed
            let (response, submitted) = self.session.handle(parsed)
            if let submitted { self.onSubmit(submitted) }
            if !wasClosed, self.session.state == .closed { self.onClosed?() }
            slot.connection.send(content: response.serialized(), contentContext: .finalMessage, isComplete: true,
                                 completion: .contentProcessed { [weak self] _ in self?.finish(slot) })
        }
    }
}
