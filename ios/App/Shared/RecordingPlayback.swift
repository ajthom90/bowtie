import SwiftUI
import AVKit
import AVFoundation
import Combine
import BowtieKit

/// Owns the AVPlayer for one recording: starts at `playback.startSec`, saves
/// the position every 15 s and once more when playback ends (dismiss/background).
/// Also drives Skip ad: `activeCommercial` while inside a detected break, and
/// auto-skip (once per break) when "Skip ads automatically" is on.
@Observable
@MainActor
final class RecordingPlayerController {
    let playback: RecordingsModel.Playback
    private(set) var player: AVPlayer?
    private(set) var errorMessage: String?
    /// The commercial break playing now: show Skip ad.
    private(set) var activeCommercial: Commercial?
    /// Briefly true after an automatic skip: show "Skipped ad".
    private(set) var showsSkippedToast = false

    private let model: RecordingsModel
    @ObservationIgnored private let defaults: UserDefaults
    @ObservationIgnored private var skipper: CommercialSkipper
    @ObservationIgnored private var timeObserver: Any?
    @ObservationIgnored private var isSkipping = false
    private var startTask: Task<Void, Never>?
    private var saveTask: Task<Void, Never>?
    private var toastTask: Task<Void, Never>?
    /// False until the start seek lands, so a quick exit can't overwrite the
    /// saved resume point with 0.
    private var hasStarted = false

    static let saveInterval: Duration = .seconds(15)
    static let commercialCheckInterval = CMTime(value: 1, timescale: 2)
    static let skippedToastDuration: Duration = .milliseconds(1500)

    init(playback: RecordingsModel.Playback, model: RecordingsModel, defaults: UserDefaults = .standard) {
        self.playback = playback
        self.model = model
        self.defaults = defaults
        skipper = CommercialSkipper(playback.recording.commercials)
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
        if !skipper.segments.isEmpty {
            timeObserver = player.addPeriodicTimeObserver(
                forInterval: Self.commercialCheckInterval,
                queue: .main
            ) { [weak self] time in
                MainActor.assumeIsolated {
                    self?.checkCommercials(at: time.seconds)
                }
            }
        }

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
        toastTask?.cancel()
        guard let player else { return }
        if let timeObserver {
            player.removeTimeObserver(timeObserver)
            self.timeObserver = nil
        }
        player.pause()
        saveNow()
        self.player = nil
        activeCommercial = nil
        showsSkippedToast = false
    }

    /// Skip ad: jump to the end of the break playing now.
    func skipCommercial() {
        guard let player, let target = skipper.skip(at: player.currentTime().seconds) else { return }
        seekPastCommercial(to: target)
    }

    private func checkCommercials(at seconds: Double) {
        guard hasStarted, !isSkipping, let player else { return }
        // Only while actually playing: scrubbing while paused never jumps.
        if AutoSkipAds.isOn(in: defaults),
           player.timeControlStatus == .playing,
           let target = skipper.autoSkipTarget(at: seconds) {
            seekPastCommercial(to: target)
            flashSkippedToast()
            return
        }
        let active = skipper.active(at: seconds)
        if active != activeCommercial {
            activeCommercial = active
        }
    }

    private func seekPastCommercial(to seconds: Double) {
        guard let player else { return }
        activeCommercial = nil
        isSkipping = true
        Task { [weak self] in
            // Exact, so a keyframe seek can't land just inside the break.
            _ = await player.seek(
                to: CMTime(seconds: seconds, preferredTimescale: 600),
                toleranceBefore: .zero,
                toleranceAfter: .zero
            )
            self?.isSkipping = false
        }
    }

    private func flashSkippedToast() {
        showsSkippedToast = true
        toastTask?.cancel()
        toastTask = Task { [weak self] in
            try? await Task.sleep(for: Self.skippedToastDuration)
            guard !Task.isCancelled else { return }
            self?.showsSkippedToast = false
        }
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
                guard let self else { return }
                self.saveNow()
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

#if canImport(UIKit)
/// Native AVKit transport (full VOD scrubbing). `onTap` observes taps without
/// taking them from AVKit (iOS chrome); omit it on tvOS.
struct RecordingVideoContainer: UIViewControllerRepresentable {
    var player: AVPlayer?
    var onTap: (() -> Void)?
    #if os(tvOS)
    /// Sleep Timer info panel + Keep watching contextual action.
    var sleepTimer: SleepTimer?
    var sleepWarning = false
    /// Non-nil inside a commercial break: offered as the Skip ad contextual
    /// action (it takes remote focus, so one click skips).
    var skipAd: (() -> Void)?
    #endif

    func makeUIViewController(context: Context) -> AVPlayerViewController {
        let vc = AVPlayerViewController()
        vc.showsPlaybackControls = true
        vc.requiresLinearPlayback = false
        #if os(iOS)
        vc.allowsPictureInPicturePlayback = false
        #endif
        vc.player = player
        #if os(tvOS)
        if let sleepTimer {
            vc.customInfoViewControllers = [
                context.coordinator.sleep.makePanel(timer: sleepTimer, programEnd: nil),
            ]
        }
        #endif
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
        #if os(tvOS)
        if let sleepTimer {
            context.coordinator.sleep.update(
                vc,
                timer: sleepTimer,
                programEnd: nil,
                warning: sleepWarning,
                skipAd: skipAd
            )
        }
        #endif
    }

    func makeCoordinator() -> Coordinator {
        Coordinator(onTap: onTap)
    }

    final class Coordinator: NSObject, UIGestureRecognizerDelegate {
        var onTap: (() -> Void)?
        #if os(tvOS)
        let sleep = TVSleepTimerSupport()
        #endif

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
#endif
