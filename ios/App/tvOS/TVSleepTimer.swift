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

/// Installs the Sleep Timer panel and the remote-focusable Keep watching
/// button (an `AVPlayerViewController` contextual action, like Skip Intro).
@MainActor
final class TVSleepTimerSupport {
    private var host: UIHostingController<TVSleepPanel>?
    private var showingKeepWatching = false

    nonisolated init() {}

    /// The info-panel tab; add it to `customInfoViewControllers`.
    func makePanel(timer: SleepTimer, programEnd: Date?) -> UIViewController {
        let panel = UIHostingController(rootView: TVSleepPanel(timer: timer, programEnd: programEnd))
        panel.title = "Sleep Timer"
        host = panel
        return panel
    }

    func update(
        _ vc: AVPlayerViewController,
        timer: SleepTimer,
        programEnd: Date?,
        warning: Bool
    ) {
        host?.rootView = TVSleepPanel(timer: timer, programEnd: programEnd)
        guard warning != showingKeepWatching else { return }
        showingKeepWatching = warning
        vc.contextualActions = warning
            ? [UIAction(title: "Keep watching") { [weak timer] _ in timer?.extend() }]
            : []
    }
}
