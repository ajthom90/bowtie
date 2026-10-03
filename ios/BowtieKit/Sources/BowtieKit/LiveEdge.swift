import Foundation

/// Where live playback sits relative to the live edge (the iOS "Live" pill).
/// AVPlayer's live point is `liveOffset` (its recommended offset from live)
/// back from the end of the seekable range. A channel that has only just
/// started can leave playback well behind that point; that counts as behind.
public enum LiveEdge {
    /// Drift (seconds) still shown as live; more than this is "behind".
    public static let tolerance: TimeInterval = 10

    public static func secondsBehind(seekableEnd: Double, current: Double, liveOffset: Double) -> Double {
        max(0, liveTarget(seekableEnd: seekableEnd, liveOffset: liveOffset) - current)
    }

    /// Where "jump to live" seeks: the player's live point, never before 0.
    public static func liveTarget(seekableEnd: Double, liveOffset: Double) -> Double {
        max(0, seekableEnd - liveOffset)
    }

    public static func isLive(secondsBehind: Double) -> Bool {
        secondsBehind < tolerance
    }

    /// "−1:30" for 90 seconds behind live.
    public static func behindLabel(secondsBehind: Double) -> String {
        let total = Int(secondsBehind.rounded(.down))
        return String(format: "−%d:%02d", total / 60, total % 60)
    }
}
