import Foundation

/// Decides when a live HLS player is stalled, from AVPlayer's time-control
/// transitions. Pure (times are passed in) so both player bridges share it and
/// it is unit-testable.
///
/// AVPlayer begins every load in "waiting to minimize stalls" while it buffers;
/// that is startup, not a stall. A wait only counts once the current item has
/// played, and only if it lasts `grace`. An item that never starts playing is
/// stalled after `startupTimeout`.
public struct StallGate: Sendable {
    public enum Signal: Equatable, Sendable {
        case stalled
        case recovered
    }

    private enum Phase {
        case idle
        case starting(since: TimeInterval)
        case playing
        case waiting(since: TimeInterval)
        case paused
        case stalled
    }

    public let grace: TimeInterval
    public let startupTimeout: TimeInterval
    private var phase: Phase = .idle

    public init(grace: TimeInterval = 4, startupTimeout: TimeInterval = 20) {
        self.grace = grace
        self.startupTimeout = startupTimeout
    }

    /// A new item was loaded (initial load or a retry).
    public mutating func loaded(at time: TimeInterval) {
        phase = .starting(since: time)
    }

    /// AVPlayer is playing. Returns `.recovered` when leaving startup, a wait,
    /// or a stall.
    public mutating func playing(at time: TimeInterval) -> Signal? {
        if case .playing = phase {
            return nil
        }
        phase = .playing
        return .recovered
    }

    /// AVPlayer is waiting for data (to minimize stalls / buffer empty).
    public mutating func waiting(at time: TimeInterval) {
        if case .playing = phase {
            phase = .waiting(since: time)
        } else if case .paused = phase {
            phase = .waiting(since: time)
        }
        // While starting, waiting is expected; while waiting/stalled, keep the
        // original timestamp.
    }

    /// The player is paused; a paused player is never stalled. A pause before
    /// the item first plays (AVPlayer reports one around replaceCurrentItem)
    /// keeps the startup window running.
    public mutating func paused(at time: TimeInterval) {
        if case .starting = phase {
            return
        }
        phase = .paused
    }

    /// Periodic check (about once a second). Returns `.stalled` once per stall.
    public mutating func check(at time: TimeInterval) -> Signal? {
        switch phase {
        case .starting(let since) where time - since >= startupTimeout,
             .waiting(let since) where time - since >= grace:
            phase = .stalled
            return .stalled
        default:
            return nil
        }
    }
}
