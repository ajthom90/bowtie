import Foundation

/// The player's text while a channel starts. A start can take a while (the
/// server answers once the tuner is on and the first segment is written);
/// after `slowAfter` the spinner says why instead of only "Starting…".
public enum StartingCopy {
    public static let starting = "Starting…"
    public static let slowStart = "Still starting — the stream is buffering. This can take a little while."
    public static let slowAfter: Duration = .seconds(5)

    public static func text(slow: Bool) -> String {
        slow ? slowStart : starting
    }
}
