import Foundation
import Observation

/// Player sleep timer: stops playback after a chosen time, or when the live
/// program ends. One per player screen — it survives channel changes there and
/// goes away when the player does. Never persisted.
///
/// Time comes from the injected `now`, and nothing happens on its own: the
/// player calls `tick()` (about once a second), which updates `remaining`,
/// raises `isWarning` in the last minute, and calls `onFire` once at the end.
@Observable
@MainActor
public final class SleepTimer {
    public enum Option: Hashable, Sendable {
        case off
        case minutes(Int)
        /// Live TV only, when the guide knows when the program ends.
        case endOfProgram

        public var label: String {
            switch self {
            case .off:
                return "Off"
            case .minutes(let minutes) where minutes >= 120 && minutes % 60 == 0:
                return "\(minutes / 60) hours"
            case .minutes(let minutes):
                return "\(minutes) minutes"
            case .endOfProgram:
                return "End of this program"
            }
        }
    }

    /// The "Still watching?" prompt shows for this long before the timer fires.
    public static let warningLead: TimeInterval = 60
    /// Keep watching after "End of this program" adds this much.
    public static let endOfProgramExtension: TimeInterval = 30 * 60
    public static let durations = [15, 30, 45, 60, 90, 120]

    /// Choices to offer: End of this program only when the program's end is
    /// known and still ahead.
    public static func options(programEnd: Date?, now: Date) -> [Option] {
        var options: [Option] = [.off] + durations.map { .minutes($0) }
        if let programEnd, programEnd > now {
            options.append(.endOfProgram)
        }
        return options
    }

    /// "1:00", "29:41", "1:05:00". Rounds up, so the last second reads 0:01.
    public static func format(_ seconds: TimeInterval) -> String {
        let total = max(0, Int(seconds.rounded(.up)))
        let hours = total / 3600
        let minutes = (total % 3600) / 60
        let secs = total % 60
        if hours > 0 {
            return String(format: "%d:%02d:%02d", hours, minutes, secs)
        }
        return String(format: "%d:%02d", minutes, secs)
    }

    public static func promptText(remaining: TimeInterval) -> String {
        "Still watching? Sleeping in \(format(remaining))"
    }

    public private(set) var option: Option = .off
    /// Time left, or nil when off.
    public private(set) var remaining: TimeInterval?
    /// Called once when the timer runs out. Stop playback and leave the player.
    @ObservationIgnored public var onFire: (@MainActor () -> Void)?

    @ObservationIgnored private let now: () -> Date
    @ObservationIgnored private var deadline: Date?

    public init(now: @escaping () -> Date = Date.init) {
        self.now = now
    }

    public var isActive: Bool { deadline != nil }

    /// In the last minute: show "Still watching?".
    public var isWarning: Bool {
        guard let remaining else { return false }
        return remaining <= Self.warningLead
    }

    /// Start (or restart) with `option`. `.off` cancels. Returns false — and
    /// leaves the timer off — for End of this program without a future end.
    @discardableResult
    public func start(_ option: Option, programEnd: Date? = nil) -> Bool {
        let current = now()
        switch option {
        case .off:
            cancel()
            return true
        case .minutes(let minutes):
            deadline = current + TimeInterval(minutes * 60)
        case .endOfProgram:
            guard let programEnd, programEnd > current else {
                cancel()
                return false
            }
            deadline = programEnd
        }
        self.option = option
        refresh(at: current)
        return true
    }

    /// Keep watching: push the end out by the chosen duration (30 minutes
    /// for End of this program).
    public func extend() {
        guard let deadline else { return }
        let extra: TimeInterval
        switch option {
        case .minutes(let minutes):
            extra = TimeInterval(minutes * 60)
        case .endOfProgram:
            extra = Self.endOfProgramExtension
        case .off:
            return
        }
        self.deadline = deadline + extra
        refresh(at: now())
    }

    public func cancel() {
        deadline = nil
        option = .off
        remaining = nil
    }

    /// Advance to the current time; fires (once) when the time is up.
    public func tick() {
        guard deadline != nil else { return }
        refresh(at: now())
    }

    private func refresh(at current: Date) {
        guard let deadline else { return }
        let left = deadline.timeIntervalSince(current)
        if left <= 0 {
            cancel()
            onFire?()
            return
        }
        remaining = left
    }
}
