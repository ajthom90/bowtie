import Foundation

/// One detected commercial break (`Recording.commercials`): seconds on the
/// recording's playback timeline, `start` inclusive, `end` exclusive.
public struct Commercial: Codable, Equatable, Hashable, Sendable {
    public let start: Double
    public let end: Double

    public init(start: Double, end: Double) {
        self.start = start
        self.end = end
    }
}

/// "Skip ad" for one playback session of a recording.
///
/// Holds the cleaned-up breaks and remembers which ones were already skipped,
/// so auto-skip happens at most once per break: seeking back into one that
/// was skipped plays it (the Skip ad button still offers to skip it).
/// Time comes from the player; nothing here runs on its own.
public struct CommercialSkipper: Equatable, Sendable {
    /// Sorted, non-overlapping, non-empty breaks.
    public let segments: [Commercial]
    private var handled: Set<Int> = []

    public init(_ commercials: [Commercial]) {
        segments = Self.normalize(commercials)
    }

    /// Drops empty, backwards and non-finite breaks, clamps a negative start
    /// to 0, sorts by start and merges breaks that overlap or touch.
    public static func normalize(_ commercials: [Commercial]) -> [Commercial] {
        let clean = commercials
            .filter { $0.start.isFinite && $0.end.isFinite }
            .map { Commercial(start: max(0, $0.start), end: $0.end) }
            .filter { $0.end > $0.start }
            .sorted { ($0.start, $0.end) < ($1.start, $1.end) }
        var merged: [Commercial] = []
        for segment in clean {
            if let last = merged.last, segment.start <= last.end {
                merged[merged.count - 1] = Commercial(start: last.start, end: max(last.end, segment.end))
            } else {
                merged.append(segment)
            }
        }
        return merged
    }

    /// The break playing at `time`, if any.
    public func active(at time: Double) -> Commercial? {
        index(at: time).map { segments[$0] }
    }

    /// Skip ad pressed: where to seek (the break's end). Also counts as
    /// skipped for auto-skip.
    public mutating func skip(at time: Double) -> Double? {
        guard let i = index(at: time) else { return nil }
        handled.insert(i)
        return segments[i].end
    }

    /// Auto-skip: where to seek the first time playback is inside a break;
    /// nil once that break was skipped (or outside any break).
    public mutating func autoSkipTarget(at time: Double) -> Double? {
        guard let i = index(at: time), !handled.contains(i) else { return nil }
        handled.insert(i)
        return segments[i].end
    }

    private func index(at time: Double) -> Int? {
        guard time.isFinite else { return nil }
        return segments.firstIndex { $0.start <= time && time < $0.end }
    }
}

/// Per-device "Skip ads automatically" (off unless turned on).
public enum AutoSkipAds {
    public static let defaultsKey = "bowtie.skipAdsAutomatically"

    public static func isOn(in defaults: UserDefaults = .standard) -> Bool {
        defaults.bool(forKey: defaultsKey)
    }
}
