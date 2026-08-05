import Foundation

/// Controllable `Clock` for debounce tests — never wall-clock sleep.
///
/// `sleep(until:)` parks on a continuation until `advance(by:)` moves
/// `now` past the deadline (or the task is cancelled).
final class ManualClock: Clock, @unchecked Sendable {
    typealias Duration = Swift.Duration

    struct Instant: InstantProtocol {
        fileprivate let offset: Duration

        static var zero: Instant { Instant(offset: .zero) }

        static func < (lhs: Instant, rhs: Instant) -> Bool {
            lhs.offset < rhs.offset
        }

        func advanced(by duration: Duration) -> Instant {
            Instant(offset: offset + duration)
        }

        func duration(to other: Instant) -> Duration {
            other.offset - offset
        }
    }

    private struct Waiter {
        let deadline: Instant
        let continuation: CheckedContinuation<Void, Error>
    }

    private let lock = NSLock()
    private var _now = Instant.zero
    private var waiters: [UUID: Waiter] = [:]

    var now: Instant {
        lock.lock()
        defer { lock.unlock() }
        return _now
    }

    var minimumResolution: Duration { .zero }

    /// Number of tasks currently suspended in `sleep`. Tests poll this so
    /// `advance` never races ahead of waiter registration.
    var pendingWaiterCount: Int {
        lock.lock()
        defer { lock.unlock() }
        return waiters.count
    }

    func sleep(until deadline: Instant, tolerance: Instant.Duration?) async throws {
        try Task.checkCancellation()

        let id = UUID()
        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
                lock.lock()
                if Task.isCancelled {
                    lock.unlock()
                    continuation.resume(throwing: CancellationError())
                    return
                }
                if deadline <= _now {
                    lock.unlock()
                    continuation.resume()
                    return
                }
                waiters[id] = Waiter(deadline: deadline, continuation: continuation)
                lock.unlock()
            }
        } onCancel: { [weak self] in
            guard let self else { return }
            self.lock.lock()
            let waiter = self.waiters.removeValue(forKey: id)
            self.lock.unlock()
            waiter?.continuation.resume(throwing: CancellationError())
        }
    }

    /// Advance virtual time and resume any sleepers whose deadline has passed.
    func advance(by duration: Duration) {
        lock.lock()
        _now = _now.advanced(by: duration)
        let now = _now
        let due = waiters.filter { $0.value.deadline <= now }
        for key in due.keys {
            waiters.removeValue(forKey: key)
        }
        lock.unlock()
        for (_, waiter) in due {
            waiter.continuation.resume()
        }
    }
}
