import SwiftUI
import BowtieKit

/// Full-screen tvOS VOD player for a recording: native AVPlayerViewController
/// transport (scrubbing, title from item metadata). Menu goes back.
struct TVRecordingPlayerView: View {
    @State private var controller: RecordingPlayerController

    @Environment(\.scenePhase) private var scenePhase

    init(playback: RecordingsModel.Playback, model: RecordingsModel) {
        _controller = State(initialValue: RecordingPlayerController(playback: playback, model: model))
    }

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()

            RecordingVideoContainer(player: controller.player)
                .ignoresSafeArea()

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
        .onChange(of: scenePhase) { _, phase in
            if phase != .active {
                controller.saveNow()
            }
        }
    }
}
