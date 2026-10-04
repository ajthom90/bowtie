import SwiftUI
import AVKit
import BowtieKit

/// Sleep timer tab in the player's info panel (swipe down), beside Quality
/// and Stats. Reads the timer directly, so the remaining time stays current.
struct TVSleepPanel: View {
    let timer: SleepTimer
    /// When the program now on the channel ends (nil: recordings, or unknown).
    let programEnd: Date?

    var body: some View {
        List {
            Text(timer.remaining.map { "Sleeping in \(SleepTimer.format($0))" } ?? "Stop playing after a while")
                .font(.caption)
                .foregroundStyle(.secondary)
                .monospacedDigit()
            ForEach(SleepTimer.options(programEnd: programEnd, now: Date()), id: \.self) { option in
                Button {
                    timer.start(option, programEnd: programEnd)
                } label: {
                    HStack {
                        Text(option.label)
                            .font(Theme.label(28))
                            .foregroundStyle(Theme.text)
                        Spacer()
                        if timer.option == option {
                            Image(systemName: "checkmark")
                                .foregroundStyle(Theme.amber)
                        }
                    }
                }
            }
        }
        .listStyle(.grouped)
        .focusSection()
    }
}

/// Installs the Sleep Timer panel and the player's remote-focusable
/// contextual action (an `AVPlayerViewController` contextual action, like
/// Skip Intro): Keep watching while the sleep prompt is up, otherwise Skip ad
/// inside a commercial break (recordings).
@MainActor
final class TVSleepTimerSupport {
    private enum ContextualAction: Equatable {
        case none
        case keepWatching
        case skipAd
    }

    private var host: UIHostingController<TVSleepPanel>?
    private var shownAction: ContextualAction = .none

    nonisolated init() {}

    /// The info-panel tab; add it to `customInfoViewControllers`.
    func makePanel(timer: SleepTimer, programEnd: Date?) -> UIViewController {
        let panel = UIHostingController(rootView: TVSleepPanel(timer: timer, programEnd: programEnd))
        panel.title = "Sleep Timer"
        host = panel
        return panel
    }

    /// `skipAd` is non-nil while a recording is inside a commercial break.
    /// The action list is replaced only when it changes, so the pill isn't
    /// re-presented on every view update.
    func update(
        _ vc: AVPlayerViewController,
        timer: SleepTimer,
        programEnd: Date?,
        warning: Bool,
        skipAd: (() -> Void)? = nil
    ) {
        host?.rootView = TVSleepPanel(timer: timer, programEnd: programEnd)
        let wanted: ContextualAction = warning ? .keepWatching : (skipAd != nil ? .skipAd : .none)
        guard wanted != shownAction else { return }
        shownAction = wanted
        switch wanted {
        case .none:
            vc.contextualActions = []
        case .keepWatching:
            vc.contextualActions = [UIAction(title: "Keep watching") { [weak timer] _ in timer?.extend() }]
        case .skipAd:
            vc.contextualActions = [
                UIAction(title: "Skip ad", image: UIImage(systemName: "forward.end.fill")) { _ in skipAd?() },
            ]
        }
    }
}
