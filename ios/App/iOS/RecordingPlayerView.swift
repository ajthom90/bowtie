import SwiftUI
import AVFoundation
import BowtieKit

/// Full-screen VOD player for a recording: AVKit transport with full scrubbing,
/// plus auto-hiding Bowtie chrome (title + Done).
struct RecordingPlayerView: View {
    let model: RecordingsModel

    @State private var controller: RecordingPlayerController
    @State private var showChrome = true
    @State private var hideChromeTask: Task<Void, Never>?
    @State private var sleepTimer = SleepTimer()

    @Environment(\.dismiss) private var dismiss
    @Environment(\.scenePhase) private var scenePhase

    private static let chromeHideDelay: Duration = .seconds(3)
    private static let menuOpenDelay: Duration = .seconds(20)

    init(playback: RecordingsModel.Playback, model: RecordingsModel) {
        self.model = model
        _controller = State(initialValue: RecordingPlayerController(playback: playback, model: model))
    }

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()

            RecordingVideoContainer(player: controller.player, onTap: { bumpChrome() })
                .ignoresSafeArea()

            if showChrome || controller.errorMessage != nil {
                chrome
                    .transition(.opacity)
            }

            if sleepTimer.isWarning, let remaining = sleepTimer.remaining {
                SleepWarningBanner(remaining: remaining) {
                    sleepTimer.extend()
                }
                .padding(.bottom, 96)
                .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottom)
            }
        }
        .statusBarHidden(true)
        .preferredColorScheme(.dark)
        .onAppear {
            configureAudioSession()
            controller.start()
            bumpChrome()
        }
        .onDisappear {
            hideChromeTask?.cancel()
            controller.finish()
        }
        .drivesSleepTimer(sleepTimer) {
            // Same as Done: stop, save the position, close the player.
            hideChromeTask?.cancel()
            controller.finish()
            dismiss()
        }
        .onChange(of: scenePhase) { _, phase in
            if phase != .active {
                controller.saveNow()
            }
        }
    }

    private var chrome: some View {
        VStack(spacing: 0) {
            HStack(alignment: .center, spacing: 12) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(controller.playback.recording.title)
                        .font(Theme.label(17))
                        .foregroundStyle(Theme.text)
                        .lineLimit(1)
                    Text(subtitle)
                        .font(Theme.body(14))
                        .foregroundStyle(Theme.dim)
                        .lineLimit(1)
                }
                Spacer(minLength: 0)
                SleepTimerButton(
                    timer: sleepTimer,
                    programEnd: { nil },
                    onOpen: { bumpChrome(for: Self.menuOpenDelay) }
                )
                Button {
                    dismiss()
                } label: {
                    Text("Done")
                        .font(Theme.label(16))
                        .foregroundStyle(Theme.amber)
                        .padding(.horizontal, 12)
                        .padding(.vertical, 8)
                }
                .accessibilityLabel("Done")
                .accessibilityHint("Close the player")
            }
            .padding(.horizontal, 16)
            // Below AVKit's own top row (full screen, AirPlay, mute).
            .padding(.top, 64)
            .padding(.bottom, 10)
            .background(
                LinearGradient(
                    colors: [Color.black.opacity(0.75), Color.black.opacity(0)],
                    startPoint: .top,
                    endPoint: .bottom
                )
                .allowsHitTesting(false)
            )

            Spacer()

            if let error = controller.errorMessage {
                Text(error)
                    .font(Theme.body())
                    .foregroundStyle(Theme.alert)
                    .padding(16)
                    .background(Theme.bg.opacity(0.9))
                    .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
                    .padding(.bottom, 40)
            }
        }
        .animation(.easeInOut(duration: 0.2), value: showChrome)
    }

    private var subtitle: String {
        let recording = controller.playback.recording
        return recording.subtitle.isEmpty ? recording.channelName : "\(recording.subtitle) · \(recording.channelName)"
    }

    private func bumpChrome(for delay: Duration = Self.chromeHideDelay) {
        showChrome = true
        hideChromeTask?.cancel()
        hideChromeTask = Task { @MainActor in
            do {
                try await Task.sleep(for: delay)
            } catch {
                return
            }
            showChrome = false
        }
    }

    private func configureAudioSession() {
        do {
            let session = AVAudioSession.sharedInstance()
            try session.setCategory(.playback, mode: .moviePlayback, options: [])
            try session.setActive(true)
        } catch {
            // Best-effort; playback may still work with the default session.
        }
    }
}
