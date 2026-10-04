import SwiftUI
import AVKit
import AVFoundation
import Combine
import BowtieKit

/// Owns the AVPlayer for one recording: starts at `playback.startSec`, saves
/// the position every 15 s and once more when playback ends (dismiss/background).
@Observable
@MainActor
final class RecordingPlayerController {
    let playback: RecordingsModel.Playback
    private(set) var player: AVPlayer?
    private(set) var errorMessage: String?

    private let model: RecordingsModel
    private var startTask: Task<Void, Never>?
    private var saveTask: Task<Void, Never>?
    /// False until the start seek lands, so a quick exit can't overwrite the
    /// saved resume point with 0.
    private var hasStarted = false

    static let saveInterval: Duration = .seconds(15)

    init(playback: RecordingsModel.Playback, model: RecordingsModel) {
        self.playback = playback
        self.model = model
    }

    func start() {
        guard player == nil else { return }
        let item = AVPlayerItem(url: playback.url)
        #if os(tvOS)
        item.externalMetadata = Self.metadata(for: playback.recording)
        #endif
        let player = AVPlayer(playerItem: item)
        self.player = player
        let startSec = playback.startSec

        startTask = Task { [weak self] in
            for await status in item.publisher(for: \.status).values {
                guard let self, !Task.isCancelled else { return }
                switch status {
                case .readyToPlay:
                    if startSec > 0 {
                        let target = CMTime(seconds: Double(startSec), preferredTimescale: 600)
                        _ = await player.seek(to: target, toleranceBefore: .zero, toleranceAfter: .zero)
                    }
                    guard !Task.isCancelled else { return }
                    self.hasStarted = true
                    player.play()
                    self.startSaving()
                    return
                case .failed:
                    self.errorMessage = "This recording couldn't be played."
                    return
                default:
                    continue
                }
            }
        }
    }

    /// Saves the current position now (e.g. the app is going to the background).
    func saveNow() {
        guard hasStarted, let player else { return }
        let seconds = player.currentTime().seconds
        let id = playback.recording.id
        let model = model
        // Unstructured: must outlive the view that triggered it.
        Task { await model.savePosition(recordingId: id, seconds: seconds) }
    }

    /// Stops playback and saves the final position.
    func finish() {
        startTask?.cancel()
        saveTask?.cancel()
        guard let player else { return }
        player.pause()
        saveNow()
        self.player = nil
    }

    private func startSaving() {
        saveTask?.cancel()
        saveTask = Task { [weak self] in
            while !Task.isCancelled {
                do {
                    try await Task.sleep(for: Self.saveInterval)
                } catch {
                    return
                }
                self?.saveNow()
            }
        }
    }

    #if os(tvOS)
    private static func metadata(for recording: Recording) -> [AVMetadataItem] {
        func item(_ id: AVMetadataIdentifier, _ value: String) -> AVMetadataItem {
            let m = AVMutableMetadataItem()
            m.identifier = id
            m.value = value as NSString
            m.extendedLanguageTag = "und"
            return m
        }
        var items = [item(.commonIdentifierTitle, recording.title)]
        if !recording.subtitle.isEmpty {
            items.append(item(.iTunesMetadataTrackSubTitle, recording.subtitle))
        }
        if !recording.description.isEmpty {
            items.append(item(.commonIdentifierDescription, recording.description))
        }
        return items
    }
    #endif
}

/// Native AVKit transport (full VOD scrubbing). `onTap` observes taps without
/// taking them from AVKit (iOS chrome); omit it on tvOS.
struct RecordingVideoContainer: UIViewControllerRepresentable {
    var player: AVPlayer?
    var onTap: (() -> Void)?

    func makeUIViewController(context: Context) -> AVPlayerViewController {
        let vc = AVPlayerViewController()
        vc.showsPlaybackControls = true
        vc.requiresLinearPlayback = false
        #if os(iOS)
        vc.allowsPictureInPicturePlayback = false
        #endif
        vc.player = player
        if onTap != nil {
            let tap = UITapGestureRecognizer(target: context.coordinator, action: #selector(Coordinator.handleTap))
            tap.cancelsTouchesInView = false
            tap.delegate = context.coordinator
            vc.view.addGestureRecognizer(tap)
        }
        return vc
    }

    func updateUIViewController(_ vc: AVPlayerViewController, context: Context) {
        if vc.player !== player {
            vc.player = player
        }
        context.coordinator.onTap = onTap
    }

    func makeCoordinator() -> Coordinator {
        Coordinator(onTap: onTap)
    }

    final class Coordinator: NSObject, UIGestureRecognizerDelegate {
        var onTap: (() -> Void)?

        init(onTap: (() -> Void)?) {
            self.onTap = onTap
        }

        @objc func handleTap() {
            onTap?()
        }

        func gestureRecognizer(
            _ gestureRecognizer: UIGestureRecognizer,
            shouldRecognizeSimultaneouslyWith otherGestureRecognizer: UIGestureRecognizer
        ) -> Bool {
            true
        }
    }
}
