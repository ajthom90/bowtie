import SwiftUI
import BowtieKit

/// "Weak signal (46%) — the picture may break up." Small and quiet beside the
/// live controls (`announcesWeakSignal` tells VoiceOver).
struct WeakSignalNote: View {
    let text: String

    var body: some View {
        HStack(spacing: 6) {
            Image(systemName: "antenna.radiowaves.left.and.right")
                .foregroundStyle(Theme.amber)
            Text(text)
                .foregroundStyle(Theme.text)
                .lineLimit(2)
                .fixedSize(horizontal: false, vertical: true)
        }
        #if os(tvOS)
        .font(Theme.label(22))
        .padding(.horizontal, 20)
        .padding(.vertical, 12)
        #else
        .font(Theme.label(13))
        .padding(.horizontal, 10)
        .padding(.vertical, 6)
        #endif
        .background(Theme.bg.opacity(0.8), in: Capsule())
        .overlay(Capsule().stroke(Theme.amber.opacity(0.5), lineWidth: 1))
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(text)
    }
}

extension View {
    /// Tells VoiceOver once when the weak-signal note turns on (not each
    /// time the player chrome shows it again).
    func announcesWeakSignal(_ model: PlayerModel) -> some View {
        onChange(of: model.showsWeakSignalNote) { _, shows in
            if shows, let note = model.weakSignalNote {
                AccessibilityNotification.Announcement(note).post()
            }
        }
    }
}

/// "All tuners are in use — …" above a channel list (`ChannelListModel.tunersBusyNote`).
struct TunersBusyNote: View {
    let text: String

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            Image(systemName: "antenna.radiowaves.left.and.right.slash")
                .foregroundStyle(Theme.amber)
            Text(text)
                .foregroundStyle(Theme.text)
                .fixedSize(horizontal: false, vertical: true)
        }
        #if os(tvOS)
        .font(Theme.body(24))
        #elseif os(macOS)
        .font(Theme.body(12))
        #else
        .font(Theme.body(15))
        #endif
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(text)
    }
}
