import AVFoundation
import BowtieKit

/// Remembers the viewer's audio language and captions choice across sessions
/// (iOS and tvOS). The player's own initial selection is ignored until the
/// saved choice has been applied, so it can't overwrite it.
@MainActor
final class MediaSelectionMemory {
    private var observer: NSObjectProtocol?
    private var applied = false
    /// Called after the saved choice is applied and on every later change.
    var onSelectionChange: ((AVPlayerItem) -> Void)?
    private let defaults: UserDefaults

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
    }

    /// Start watching a new item's selection changes.
    func watch(_ item: AVPlayerItem) {
        stop()
        applied = false
        observer = NotificationCenter.default.addObserver(
            forName: AVPlayerItem.mediaSelectionDidChangeNotification, object: item, queue: .main
        ) { [weak self] _ in
            MainActor.assumeIsolated {
                self?.record(item)
                self?.onSelectionChange?(item)
            }
        }
    }

    /// Apply the saved choice once the item is ready to play.
    func itemReady(_ item: AVPlayerItem) {
        guard !applied else { return }
        Task { await apply(to: item) }
    }

    func stop() {
        if let observer {
            NotificationCenter.default.removeObserver(observer)
        }
        observer = nil
    }

    private func apply(to item: AVPlayerItem) async {
        let prefs = MediaPrefs.load(from: defaults)
        if let audible = try? await item.asset.loadMediaSelectionGroup(for: .audible),
           let i = MediaPrefs.pickIndex(
               languages: audible.options.map(\.extendedLanguageTag),
               preferred: prefs.audioLanguage
           ) {
            item.select(audible.options[i], in: audible)
        }
        if let on = prefs.captionsOn,
           let legible = try? await item.asset.loadMediaSelectionGroup(for: .legible) {
            let cc = legible.options.first { !$0.hasMediaCharacteristic(.containsOnlyForcedSubtitles) }
            item.select(on ? cc : nil, in: legible)
        }
        applied = true
        onSelectionChange?(item)
    }

    private func record(_ item: AVPlayerItem) {
        guard applied else { return }
        Task {
            var prefs = MediaPrefs.load(from: defaults)
            if let audible = try? await item.asset.loadMediaSelectionGroup(for: .audible),
               let option = item.currentMediaSelection.selectedMediaOption(in: audible) {
                prefs.audioLanguage = option.extendedLanguageTag
            }
            if let legible = try? await item.asset.loadMediaSelectionGroup(for: .legible) {
                prefs.captionsOn = item.currentMediaSelection.selectedMediaOption(in: legible) != nil
            }
            prefs.save(to: defaults)
        }
    }
}
