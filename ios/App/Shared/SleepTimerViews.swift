import SwiftUI
import BowtieKit

// Sleep timer pieces shared by the live and recording players. The timer
// itself (and its copy) lives in BowtieKit's `SleepTimer`.

extension View {
    /// Drives `timer` while this player is on screen: ticks once a second and
    /// runs `onFire` (stop playback, leave the player) when it runs out.
    func drivesSleepTimer(_ timer: SleepTimer, onFire: @escaping @MainActor () -> Void) -> some View {
        task {
            timer.onFire = onFire
            while !Task.isCancelled {
                timer.tick()
                try? await Task.sleep(for: .seconds(1))
            }
        }
    }
}

/// "Still watching? Sleeping in 0:59" with Keep watching. On tvOS the
/// button is the player's contextual action (it takes remote focus), so the
/// banner there is text only.
struct SleepWarningBanner: View {
    let remaining: TimeInterval
    var onKeepWatching: (() -> Void)?

    #if os(tvOS)
    private let textSize: CGFloat = 24
    #else
    private let textSize: CGFloat = 14
    #endif

    var body: some View {
        HStack(spacing: 14) {
            Text(SleepTimer.promptText(remaining: remaining))
                .font(Theme.body(textSize))
                .foregroundStyle(Theme.text)
                .monospacedDigit()
            if let onKeepWatching {
                Button("Keep watching", action: onKeepWatching)
                    #if os(macOS)
                    .keyboardShortcut(.defaultAction)
                    #else
                    .font(Theme.label(textSize))
                    .foregroundStyle(Theme.amber)
                    .buttonStyle(.plain)
                    #endif
                    .accessibilityIdentifier("bowtie.sleep.keepWatching")
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
        .background(Theme.bg.opacity(0.88))
        .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
        .transition(.opacity)
        .accessibilityElement(children: .contain)
    }
}

#if os(macOS)
/// Sleep timer menu for the Mac player chrome (beside Quality).
struct SleepTimerMenu: View {
    let timer: SleepTimer
    let programEnd: Date?

    var body: some View {
        Menu {
            Section(timer.remaining.map { "Sleeping in \(SleepTimer.format($0))" } ?? "Sleep timer") {
                ForEach(SleepTimer.options(programEnd: programEnd, now: Date()), id: \.self) { option in
                    Toggle(option.label, isOn: Binding(
                        get: { timer.option == option },
                        set: { _ in timer.start(option, programEnd: programEnd) }
                    ))
                }
            }
        } label: {
            Label(sleepLabel, systemImage: timer.isActive ? "moon.zzz.fill" : "moon.zzz")
                .font(Theme.label(13))
                .monospacedDigit()
        }
        .menuStyle(.borderlessButton)
        .fixedSize()
        .foregroundStyle(Theme.amber)
        .padding(.horizontal, 10)
        .padding(.vertical, 6)
        .background(Theme.raised.opacity(0.9))
        .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
        .help("Sleep timer")
        .accessibilityLabel("Sleep timer")
        .accessibilityValue(sleepLabel)
    }

    private var sleepLabel: String {
        timer.remaining.map { SleepTimer.format($0) } ?? "Sleep"
    }
}
#endif

#if os(iOS)
/// Sleep timer button for the iOS player chrome. A dialog (not a Menu) so the
/// chrome's auto-hide can't close it mid-choice.
struct SleepTimerButton: View {
    let timer: SleepTimer
    let programEnd: () -> Date?
    /// The player keeps its chrome up while the dialog is open.
    var onOpen: () -> Void = {}

    @State private var showChoices = false
    @State private var choiceEnd: Date?

    var body: some View {
        Button {
            choiceEnd = programEnd()
            showChoices = true
            onOpen()
        } label: {
            HStack(spacing: 6) {
                Image(systemName: timer.isActive ? "moon.zzz.fill" : "moon.zzz")
                if let remaining = timer.remaining {
                    Text(SleepTimer.format(remaining))
                        .font(Theme.label(14))
                        .monospacedDigit()
                }
            }
            .font(.system(size: 18, weight: .medium))
            .foregroundStyle(Theme.amber)
            .frame(minWidth: 44, minHeight: 44)
            .padding(.horizontal, timer.isActive ? 10 : 0)
            .background(Theme.raised.opacity(0.9))
            .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
            .fixedSize()
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Sleep timer")
        .accessibilityValue(timer.remaining.map { "Sleeping in \(SleepTimer.format($0))" } ?? "Off")
        .accessibilityIdentifier("bowtie.sleep")
        .confirmationDialog("Sleep timer", isPresented: $showChoices, titleVisibility: .visible) {
            ForEach(SleepTimer.options(programEnd: choiceEnd, now: Date()), id: \.self) { option in
                Button(timer.option == option ? "\(option.label) ✓" : option.label) {
                    timer.start(option, programEnd: choiceEnd)
                }
            }
        } message: {
            if let remaining = timer.remaining {
                Text("Sleeping in \(SleepTimer.format(remaining))")
            }
        }
    }
}
#endif
