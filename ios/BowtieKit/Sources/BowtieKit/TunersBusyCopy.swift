import Foundation

/// Copy for the tuners-busy screen, shared by iOS and tvOS.
public enum TunersBusyCopy {
    /// Line naming tuners held by other apps (e.g. Plex); nil when none.
    public static func otherAppsLine(otherInUse: Int) -> String? {
        guard otherInUse > 0 else { return nil }
        let tuners = otherInUse == 1 ? "1 tuner is" : "\(otherInUse) tuners are"
        return "\(tuners) in use by another app (like Plex)."
    }
}
