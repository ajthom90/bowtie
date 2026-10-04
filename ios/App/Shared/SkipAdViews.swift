import SwiftUI
import BowtieKit

// Skip ad pieces for the recording players. The break logic lives in
// BowtieKit's `CommercialSkipper`, driven by `RecordingPlayerController`.

#if !os(tvOS)
/// "Skip ad" over the recording player (bottom-right, above the system
/// transport). tvOS uses the player's contextual action instead.
struct SkipAdButton: View {
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Label("Skip ad", systemImage: "forward.end.fill")
                .font(Theme.label(15))
                .foregroundStyle(Theme.bg)
                .padding(.horizontal, 16)
                .padding(.vertical, 10)
                .background(Theme.amber, in: RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
        }
        .buttonStyle(BowtiePlainButtonStyle())
        #if os(macOS)
        .help("Skip ad (S)")
        #endif
        .accessibilityLabel("Skip ad")
        .accessibilityHint("Jump to the end of this commercial break")
        .accessibilityIdentifier("bowtie.skipAd")
        .transition(.opacity)
    }
}
#endif

/// "Skipped ad", shown briefly after an automatic skip.
struct SkippedAdToast: View {
    #if os(tvOS)
    private let textSize: CGFloat = 24
    #else
    private let textSize: CGFloat = 15
    #endif

    var body: some View {
        Label("Skipped ad", systemImage: "forward.end.fill")
            .font(Theme.label(textSize))
            .foregroundStyle(Theme.text)
            .padding(.horizontal, 16)
            .padding(.vertical, 10)
            .background(Theme.bg.opacity(0.88), in: RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
            .transition(.opacity)
            .accessibilityIdentifier("bowtie.skippedAd")
            .onAppear {
                AccessibilityNotification.Announcement("Skipped ad").post()
            }
    }
}

extension View {
    /// Skip ad / Skipped ad for a recording player, bottom-right. `bottomInset`
    /// keeps the button clear of the system transport controls.
    func skipAdOverlay(_ controller: RecordingPlayerController, bottomInset: CGFloat) -> some View {
        overlay(alignment: .bottomTrailing) {
            Group {
                if controller.showsSkippedToast {
                    SkippedAdToast()
                } else if controller.activeCommercial != nil {
                    #if os(tvOS)
                    EmptyView()
                    #else
                    SkipAdButton { controller.skipCommercial() }
                    #endif
                }
            }
            .padding(.trailing, 24)
            .padding(.bottom, bottomInset)
            .animation(.easeInOut(duration: 0.2), value: controller.activeCommercial)
            .animation(.easeInOut(duration: 0.2), value: controller.showsSkippedToast)
        }
    }
}

/// "Skip ads automatically" for Settings (per device).
struct AutoSkipAdsToggle: View {
    @AppStorage(AutoSkipAds.defaultsKey) private var isOn = false

    var body: some View {
        Toggle(isOn: $isOn) {
            VStack(alignment: .leading, spacing: 4) {
                Text("Skip ads automatically")
                    .font(Theme.label())
                    .foregroundStyle(Theme.text)
                Text("In recordings, skip each detected commercial break once as it starts.")
                    #if os(tvOS)
                    .font(Theme.body(22))
                    #else
                    .font(Theme.body(13))
                    #endif
                    .foregroundStyle(Theme.dim)
            }
        }
        .tint(Theme.amber)
        .accessibilityIdentifier("bowtie.settings.autoSkipAds")
    }
}
