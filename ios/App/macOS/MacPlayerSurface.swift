import SwiftUI
import AVKit

/// AppKit's `AVPlayerView` with floating controls: transport and scrubber
/// (live DVR window or full VOD), audio / captions menu, picture in picture,
/// and its own full-screen button.
///
/// With a `bridge` (live), PiP start / stop is reported to it so leaving the
/// live view during PiP keeps the session until PiP ends, as on iOS.
struct MacPlayerSurface: NSViewRepresentable {
    var player: AVPlayer?
    var allowsPictureInPicture = true
    var bridge: PlayerBridge?

    func makeNSView(context: Context) -> AVPlayerView {
        let view = AVPlayerView()
        view.controlsStyle = .floating
        view.showsFullScreenToggleButton = true
        view.allowsPictureInPicturePlayback = allowsPictureInPicture
        view.pictureInPictureDelegate = context.coordinator
        // Live Text / subject lifting on TV frames is just CPU.
        view.allowsVideoFrameAnalysis = false
        view.player = player
        return view
    }

    func updateNSView(_ view: AVPlayerView, context: Context) {
        if view.player !== player {
            view.player = player
        }
        view.allowsPictureInPicturePlayback = allowsPictureInPicture
        context.coordinator.bridge = bridge
    }

    func makeCoordinator() -> Coordinator {
        Coordinator(bridge: bridge)
    }

    final class Coordinator: NSObject, AVPlayerViewPictureInPictureDelegate {
        var bridge: PlayerBridge?

        init(bridge: PlayerBridge?) {
            self.bridge = bridge
        }

        func playerViewWillStartPicture(inPicture playerView: AVPlayerView) {
            MainActor.assumeIsolated {
                bridge?.isPictureInPictureActive = true
            }
        }

        func playerViewDidStopPicture(inPicture playerView: AVPlayerView) {
            MainActor.assumeIsolated {
                guard let bridge else { return }
                bridge.isPictureInPictureActive = false
                if bridge.shouldStopWhenPiPEnds {
                    bridge.shouldStopWhenPiPEnds = false
                    bridge.pipDidEndAndShouldStop = true
                }
            }
        }

        func playerView(
            _ playerView: AVPlayerView,
            restoreUserInterfaceForPictureInPictureStopWithCompletionHandler completionHandler: @escaping (Bool) -> Void
        ) {
            // Back into the window: the live view is still on screen, so keep
            // the session. (If it was left, `shouldStopWhenPiPEnds` stops it.)
            completionHandler(true)
        }
    }
}
