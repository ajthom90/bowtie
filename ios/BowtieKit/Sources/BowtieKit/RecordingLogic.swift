import Foundation

/// The three lists on the Recordings screen.
public enum RecordingsTab: String, CaseIterable, Identifiable, Sendable {
    case upcoming
    case recorded
    case missed
    /// Series rules (`/recording-rules`), not recordings.
    case shows

    public var id: String { rawValue }

    public var title: String {
        switch self {
        case .upcoming: return "Upcoming"
        case .recorded: return "Recorded"
        case .missed: return "Missed"
        case .shows: return "Shows"
        }
    }

    /// The `?state=` filter; nil for Shows, which lists rules.
    public var filter: RecordingsFilter? {
        switch self {
        case .upcoming: return .upcoming
        case .recorded: return .recorded
        case .missed: return .failed
        case .shows: return nil
        }
    }

    public var emptyMessage: String {
        switch self {
        case .upcoming: return "Nothing scheduled. Press and hold a channel to record what's on."
        case .recorded: return "No recordings yet."
        case .missed: return "No missed recordings."
        case .shows: return "No shows. Choose Record Series on a program to record every new episode."
        }
    }
}

/// Pure copy, resume and formatting rules for DVR recordings (shared by iOS and tvOS).
public enum RecordingLogic {
    /// Colour family for a state badge; the apps map it to theme colours.
    public enum BadgeTone: Equatable, Sendable {
        case neutral
        case live
        case good
        case warning
        case alert
    }

    // MARK: - Labels

    public static func stateLabel(_ recording: Recording) -> String {
        switch recording.status {
        case .scheduled: return "Scheduled"
        case .waiting:
            return recording.failure == "noSignal" ? "Waiting for a signal" : "Waiting for a tuner"
        case .recording: return "Recording"
        case .converting: return "Finishing up"
        case .ready: return recording.partial ? "Partly recorded" : "Recorded"
        case .failed: return recording.isSkipped ? "Skipped" : "Missed"
        case .unknown: return recording.state.capitalized
        }
    }

    /// Why a recording failed, in plain words; nil when it didn't.
    public static func failureLabel(_ failure: String) -> String? {
        switch failure {
        case "": return nil
        case "noTuner": return "No tuner was free"
        case "noSignal": return "The antenna got no signal"
        case "diskFull": return "The server ran out of space"
        case "skipped": return "Skipped"
        default: return "Something went wrong on the server"
        }
    }

    /// Secondary line for a row: why it was missed, or that part is missing.
    public static func detailLine(_ recording: Recording) -> String? {
        if recording.isSkipped {
            return "This episode was removed, so the series won't record it"
        }
        if recording.status == .failed {
            let reason = failureLabel(recording.failure) ?? "It didn't record"
            return "Missed: " + reason.prefix(1).lowercased() + reason.dropFirst()
        }
        if recording.partial, recording.status == .ready || recording.status == .converting {
            return "Part of this program is missing"
        }
        return nil
    }

    public static func badgeTone(_ recording: Recording) -> BadgeTone {
        switch recording.status {
        case .recording: return .live
        case .failed: return recording.isSkipped ? .neutral : .alert
        case .waiting: return .warning
        case .ready: return recording.partial ? .warning : .good
        case .scheduled, .converting, .unknown: return .neutral
        }
    }

    // MARK: - Resume

    /// Offer to resume only past the first 10 s and before the last 30 s.
    public static let resumeMinSec = 10
    public static let resumeTailSec = 30

    /// The position to resume from, or nil to start at the beginning.
    /// An unknown duration (0) only applies the 10 s floor.
    public static func resumePosition(positionSec: Int, durationSec: Int) -> Int? {
        guard positionSec > resumeMinSec else { return nil }
        if durationSec > 0, positionSec >= durationSec - resumeTailSec {
            return nil
        }
        return positionSec
    }

    /// Whole seconds to save for a player time; nil when the time isn't a number yet.
    public static func positionToSave(seconds: Double) -> Int? {
        guard seconds.isFinite else { return nil }
        return max(0, Int(seconds.rounded(.down)))
    }

    /// 0…1 of the recording the caller has watched.
    public static func watchedFraction(_ recording: Recording) -> Double {
        guard recording.durationSec > 0 else { return 0 }
        return min(1, max(0, Double(recording.positionSec) / Double(recording.durationSec)))
    }

    // MARK: - Formatting

    /// "45 sec", "33 min", "1 hr", "1 hr 5 min".
    public static func formatDuration(_ seconds: Int) -> String {
        let s = max(0, seconds)
        if s > 0 && s < 60 { return "\(s) sec" }
        let hours = s / 3600
        let minutes = (s % 3600) / 60
        if hours == 0 { return "\(minutes) min" }
        if minutes == 0 { return "\(hours) hr" }
        return "\(hours) hr \(minutes) min"
    }

    /// Decimal units: "3.8 GB", "450 MB", "3 KB".
    public static func formatSize(_ bytes: Int64) -> String {
        let b = Double(max(0, bytes))
        if b >= 1_000_000_000 { return String(format: "%.1f GB", b / 1_000_000_000) }
        if b >= 1_000_000 { return String(format: "%.0f MB", b / 1_000_000) }
        return String(format: "%.0f KB", b / 1_000)
    }

    /// Player-style timestamp: "6:52", "1:02:03".
    public static func formatTimestamp(_ seconds: Int) -> String {
        let s = max(0, seconds)
        let hours = s / 3600
        let minutes = (s % 3600) / 60
        let secs = s % 60
        if hours > 0 {
            return String(format: "%d:%02d:%02d", hours, minutes, secs)
        }
        return String(format: "%d:%02d", minutes, secs)
    }

    // MARK: - Series

    /// Alert title after "Record Series".
    public static func seriesScheduledTitle(count: Int) -> String {
        switch count {
        case ..<1: return "Series Recording Set"
        case 1: return "Scheduled 1 episode"
        default: return "Scheduled \(count) episodes"
        }
    }

    /// Alert body after "Record Series". An empty channel name means any channel.
    public static func seriesScheduledMessage(title: String, channelName: String) -> String {
        let show = title.isEmpty ? "This show" : "\u{201C}\(title)\u{201D}"
        let place = channelName.isEmpty ? "any channel" : channelName
        return "New episodes of \(show) on \(place) will record as they appear in the guide."
    }

    // MARK: - Conflict

    /// Body for the "not enough tuners" alert, naming what already holds them.
    public static func conflictMessage(tunerCount: Int, conflicts: [Recording]) -> String {
        let tuners = tunerCount == 1 ? "1 tuner" : "\(tunerCount) tuners"
        var lines = ["Your antenna has \(tuners), and these recordings already need them then:"]
        lines += conflicts.map { "• \($0.title) (\($0.channelName))" }
        lines.append("Record anyway and this one records only if a tuner frees up.")
        return lines.joined(separator: "\n")
    }
}
