import Foundation
import Observation

/// "Continue watching": recordings the caller started and hasn't finished.
/// Pure rules shared by every Apple app (Android `:core` mirrors them).
public enum ContinueWatching {
    /// At least a minute watched…
    public static let minPositionSec = 60
    /// …and more than two minutes still to go.
    public static let endMarginSec = 120
    public static let maxItems = 10

    /// Ready, not parental-locked, and part-watched (known length).
    public static func isEligible(_ recording: Recording) -> Bool {
        guard recording.isPlayable, recording.durationSec > 0 else { return false }
        return recording.positionSec >= minPositionSec
            && recording.positionSec < recording.durationSec - endMarginSec
    }

    /// Most recently watched first (when the position was last saved);
    /// recordings without a save time go last, newest recording first.
    public static func items(from recordings: [Recording]) -> [Recording] {
        let sorted = recordings.filter(isEligible).sorted { a, b in
            switch (a.positionUpdatedAt, b.positionUpdatedAt) {
            case let (x?, y?) where x != y:
                return x > y
            case (.some, nil):
                return true
            case (nil, .some):
                return false
            default:
                if a.start != b.start { return a.start > b.start }
                return a.id > b.id
            }
        }
        return Array(sorted.prefix(maxItems))
    }

    /// "less than a minute left", "23 min left", "1 hr left", "1 hr 5 min left".
    public static func remainingText(_ recording: Recording) -> String {
        let remaining = recording.durationSec - recording.positionSec
        guard remaining >= 60 else { return "less than a minute left" }
        let hours = remaining / 3600
        let minutes = (remaining % 3600) / 60
        if hours == 0 { return "\(minutes) min left" }
        if minutes == 0 { return "\(hours) hr left" }
        return "\(hours) hr \(minutes) min left"
    }

    /// 0…1 watched, for the card's progress bar.
    public static func progress(_ recording: Recording) -> Double {
        RecordingLogic.watchedFraction(recording)
    }
}

/// The "Continue watching" shelf: loads the caller's recorded list and keeps
/// the part-watched ones. Playback goes through `RecordingsModel.play`.
@Observable
@MainActor
public final class ContinueWatchingModel {
    /// Empty hides the shelf.
    public private(set) var items: [Recording] = []
    /// Last failed remove, for an alert or toast.
    public var actionError: String?

    private let client: BowtieClient
    private var generation: UInt64 = 0

    public init(client: BowtieClient) {
        self.client = client
    }

    /// Refreshes the shelf. A failure keeps what's shown (it's a convenience row).
    public func load() async {
        generation &+= 1
        let gen = generation
        guard let rows = try? await client.recordings(filter: .recorded) else { return }
        guard gen == generation else { return }
        items = ContinueWatching.items(from: rows)
    }

    /// "Remove from Continue watching": resets the saved position to 0.
    public func remove(_ recording: Recording) async {
        do {
            try await client.saveRecordingPosition(id: recording.id, positionSec: 0)
            items.removeAll { $0.id == recording.id }
        } catch {
            actionError = RecordingErrorCopy.message(for: error)
        }
    }
}
