import SwiftUI
import BowtieKit

/// Full-screen tvOS VOD player for a recording: native AVPlayerViewController
/// transport (scrubbing, title from item metadata). Menu goes back; swipe
/// down for the Sleep Timer.
struct TVRecordingPlayerView: View {
    @State private var controller: RecordingPlayerController

    @State private var sleepTimer = SleepTimer()

    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.dismiss) private var dismiss

    init(playback: RecordingsModel.Playback, model: RecordingsModel) {
        _controller = State(initialValue: RecordingPlayerController(playback: playback, model: model))
    }

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()

            RecordingVideoContainer(
                player: controller.player,
                sleepTimer: sleepTimer,
                sleepWarning: sleepTimer.isWarning
            )
            .ignoresSafeArea()

            if sleepTimer.isWarning, let remaining = sleepTimer.remaining {
                // Keep watching is the player's contextual action (remote focus).
                SleepWarningBanner(remaining: remaining)
                    .padding(.top, 60)
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
            }

            if let error = controller.errorMessage {
                Text(error)
                    .font(Theme.body(24))
                    .foregroundStyle(Theme.alert)
                    .padding(32)
                    .background(Theme.bg.opacity(0.9))
                    .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
            }
        }
        .toolbar(.hidden, for: .navigationBar)
        .onAppear {
            controller.start()
        }
        .onDisappear {
            controller.finish()
        }
        .drivesSleepTimer(sleepTimer) {
            // Same as Menu: stop, save the position, close the player.
            controller.finish()
            dismiss()
        }
        .onChange(of: scenePhase) { _, phase in
            if phase != .active {
                controller.saveNow()
            }
        }
    }
}
