import AVFoundation
import Observation
import BowtieKit

#if os(iOS) || os(macOS)
// MARK: - Bridge (player ownership + PiP / error flags)

/// Live AVPlayer owner shared by the iOS and macOS players: stall detection
/// (StallGate), live-edge position, out-of-window clamp, audio choices and
/// the PiP-safe teardown flags. tvOS has its own `TVPlayerBridge`.
@MainActor
@Observable
final class PlayerBridge {
    var player: AVPlayer?
    var isPictureInPictureActive = false
    /// Set when the hosting view disappears into PiP; stop when PiP ends.
    var shouldStopWhenPiPEnds = false
    var pipDidEndAndShouldStop = false
    var playerErrorIsForbidden = false
    var playerDidStall = false
    var playerDidRecover = false
    /// Out-of-window clamp: position fell before seekable start → jumped to live edge.
    var playerDidJumpToLive = false
    /// Seconds behind the live point; nil until the seekable range is known.
    var secondsBehindLive: Double?
    /// Broadcast audio choices (AVKit's inline controls offer none on iPhone).
    var audioOptionNames: [String] = []
    var selectedAudioIndex: Int?
    @ObservationIgnored private var audibleGroup: AVMediaSelectionGroup?

    private var itemStatusObs: NSKeyValueObservation?
    private var itemKeepUpObs: NSKeyValueObservation?
    private var itemEmptyObs: NSKeyValueObservation?
    private var seekableObs: NSKeyValueObservation?
    private var timeControlObs: NSKeyValueObservation?
    private var endObserver: NSObjectProtocol?
    private var failedObserver: NSObjectProtocol?
    /// Saved audio language / captions choice, applied per item.
    @ObservationIgnored private lazy var mediaMemory: MediaSelectionMemory = {
        let memory = MediaSelectionMemory()
        memory.onSelectionChange = { [weak self] item in self?.refreshAudio(item) }
        return memory
    }()
    private var boundaryTimeObserver: Any?
    private var periodicTimeObserver: Any?
    /// Startup buffering is not a stall; StallGate decides (shared with tvOS).
    private var stallGate = StallGate()
    private var stallTicker: Task<Void, Never>?
    private var now: TimeInterval { ProcessInfo.processInfo.systemUptime }

    func load(url: URL) {
        let item = AVPlayerItem(url: url)
        if let player {
            player.replaceCurrentItem(with: item)
        } else {
            let p = AVPlayer(playerItem: item)
            p.allowsExternalPlayback = true
            #if os(iOS)
            p.usesExternalPlaybackWhileExternalScreenIsActive = true
            #endif
            player = p
        }
        observe(item: item)
        secondsBehindLive = nil
        stallGate.loaded(at: now)
        startStallTicker()
        player?.play()
    }

    private func startStallTicker() {
        stallTicker?.cancel()
        stallTicker = Task { @MainActor [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(1))
                guard let self, !Task.isCancelled else { return }
                if self.stallGate.check(at: self.now) == .stalled {
                    self.playerDidStall = true
                }
            }
        }
    }

    func replacePlayer(_ newPlayer: AVPlayer?) {
        tearDownObservers()
        player?.pause()
        player?.replaceCurrentItem(with: nil)
        player = newPlayer
    }

    func accessLogSample() -> (bitrate: Double?, dropped: Int?) {
        guard let event = player?.currentItem?.accessLog()?.events.last else {
            return (nil, nil)
        }
        let bitrate: Double? = event.indicatedBitrate > 0 ? event.indicatedBitrate : nil
        let dropped: Int? = event.numberOfDroppedVideoFrames >= 0
            ? event.numberOfDroppedVideoFrames
            : nil
        return (bitrate, dropped)
    }

    private func observe(item: AVPlayerItem) {
        tearDownObservers()
        mediaMemory.watch(item)

        itemStatusObs = item.observe(\.status, options: [.new]) { [weak self] item, _ in
            Task { @MainActor in
                self?.handleItemStatus(item)
            }
        }
        itemKeepUpObs = item.observe(\.isPlaybackLikelyToKeepUp, options: [.new]) { [weak self] item, _ in
            Task { @MainActor in
                self?.handleBufferState(item)
            }
        }
        itemEmptyObs = item.observe(\.isPlaybackBufferEmpty, options: [.new]) { [weak self] item, _ in
            Task { @MainActor in
                self?.handleBufferState(item)
            }
        }
        // Spec B / Task 7: when current position falls below seekable range start
        // (paused longer than the DVR buffer), clamp to live edge + notice.
        seekableObs = item.observe(\.seekableTimeRanges, options: [.new]) { [weak self] _, _ in
            Task { @MainActor in
                self?.clampIfBehindSeekableWindow()
            }
        }
        if let player {
            timeControlObs = player.observe(\.timeControlStatus, options: [.new]) { [weak self] player, _ in
                Task { @MainActor in
                    self?.handleTimeControl(player)
                }
            }
            // Periodic check so a paused head that slowly exits the window is caught.
            let interval = CMTime(seconds: 1, preferredTimescale: 600)
            periodicTimeObserver = player.addPeriodicTimeObserver(
                forInterval: interval,
                queue: .main
            ) { [weak self] _ in
                Task { @MainActor in
                    self?.clampIfBehindSeekableWindow()
                    self?.updateLivePosition()
                }
            }
        }

        failedObserver = NotificationCenter.default.addObserver(
            forName: .AVPlayerItemFailedToPlayToEndTime,
            object: item,
            queue: .main
        ) { [weak self] note in
            Task { @MainActor in
                let error = note.userInfo?[AVPlayerItemFailedToPlayToEndTimeErrorKey] as? Error
                self?.handleFailure(error)
            }
        }
    }

    /// When playback position is before the first seekable range start, seek to
    /// the live edge (range end) and flag the out-of-window notice.
    func updateLivePosition() {
        guard let player, let item = player.currentItem,
              let range = item.seekableTimeRanges.last?.timeRangeValue,
              range.duration.isNumeric, range.duration.seconds > 0 else {
            secondsBehindLive = nil
            return
        }
        let current = player.currentTime()
        guard current.isNumeric else { return }
        secondsBehindLive = LiveEdge.secondsBehind(
            seekableEnd: CMTimeRangeGetEnd(range).seconds,
            current: current.seconds,
            liveOffset: liveOffsetSeconds(item)
        )
    }

    private func liveOffsetSeconds(_ item: AVPlayerItem) -> Double {
        let offset = item.recommendedTimeOffsetFromLive
        return offset.isNumeric ? offset.seconds : 0
    }

    /// Refresh the audio choices and the selected one from the item.
    func refreshAudio(_ item: AVPlayerItem) {
        Task {
            guard let group = try? await item.asset.loadMediaSelectionGroup(for: .audible) else {
                audibleGroup = nil
                audioOptionNames = []
                selectedAudioIndex = nil
                return
            }
            audibleGroup = group
            audioOptionNames = group.options.map(\.displayName)
            let current = item.currentMediaSelection.selectedMediaOption(in: group)
            selectedAudioIndex = current.flatMap { group.options.firstIndex(of: $0) }
        }
    }

    /// Select broadcast audio track `index` (MediaSelectionMemory saves it).
    func selectAudio(_ index: Int) {
        guard let item = player?.currentItem, let group = audibleGroup,
              group.options.indices.contains(index) else { return }
        item.select(group.options[index], in: group)
    }

    /// Seek to where AVPlayer plays live (its recommended offset from the edge).
    func jumpToLive() {
        guard let player, let item = player.currentItem,
              let range = item.seekableTimeRanges.last?.timeRangeValue,
              range.duration.isNumeric else { return }
        // The player's live point, not the very end (seeking there stalls).
        let target = CMTime(
            seconds: LiveEdge.liveTarget(
                seekableEnd: CMTimeRangeGetEnd(range).seconds,
                liveOffset: liveOffsetSeconds(item)
            ),
            preferredTimescale: 600
        )
        player.seek(to: target) { [weak self] _ in
            Task { @MainActor in self?.updateLivePosition() }
        }
        if player.timeControlStatus == .paused {
            player.play()
        }
    }

    func clampIfBehindSeekableWindow() {
        guard let player, let item = player.currentItem else { return }
        let ranges = item.seekableTimeRanges
        guard let value = ranges.first else { return }
        let range = value.timeRangeValue
        guard range.duration.isNumeric, range.duration.seconds > 0 else { return }
        let current = player.currentTime()
        guard current.isNumeric else { return }
        let start = range.start
        if CMTimeCompare(current, start) < 0 {
            let liveEdge = CMTimeRangeGetEnd(range)
            player.seek(to: liveEdge, toleranceBefore: .zero, toleranceAfter: .zero)
            playerDidJumpToLive = true
        }
    }

    private func handleItemStatus(_ item: AVPlayerItem) {
        switch item.status {
        case .failed:
            handleFailure(item.error)
        case .readyToPlay:
            playerDidRecover = true
            mediaMemory.itemReady(item)
        case .unknown:
            break
        @unknown default:
            break
        }
    }

    private func handleBufferState(_ item: AVPlayerItem) {
        // Live HLS underrun: StallGate reports a stall only if it outlasts its grace.
        if item.isPlaybackBufferEmpty && !item.isPlaybackLikelyToKeepUp,
           player?.timeControlStatus == .waitingToPlayAtSpecifiedRate {
            stallGate.waiting(at: now)
        }
    }

    private func handleTimeControl(_ player: AVPlayer) {
        switch player.timeControlStatus {
        case .playing:
            if stallGate.playing(at: now) == .recovered {
                playerDidRecover = true
            }
        case .waitingToPlayAtSpecifiedRate:
            stallGate.waiting(at: now)
        case .paused:
            stallGate.paused(at: now)
        @unknown default:
            break
        }
    }

    private func handleFailure(_ error: Error?) {
        if Self.isForbidden(error) {
            playerErrorIsForbidden = true
        } else {
            // Network / other media errors → stall recovery path.
            playerDidStall = true
        }
    }

    /// Walk the NSError chain for HTTP 403 / unauthorized media responses.
    static func isForbidden(_ error: Error?) -> Bool {
        var current: NSError? = error as NSError?
        while let err = current {
            if err.domain == NSURLErrorDomain && err.code == NSURLErrorUserAuthenticationRequired {
                return true
            }
            // AVFoundation / URL loading often surface HTTP status in userInfo.
            for key in ["HTTPStatusCode", "statusCode", "httpStatus"] {
                if let status = err.userInfo[key] as? Int, status == 403 {
                    return true
                }
                if let status = err.userInfo[key] as? NSNumber, status.intValue == 403 {
                    return true
                }
            }
            // String-match as a last resort for wrapped "403" messages.
            if err.localizedDescription.contains("403") {
                return true
            }
            current = err.userInfo[NSUnderlyingErrorKey] as? NSError
        }
        return false
    }

    private func tearDownObservers() {
        mediaMemory.stop()
        stallTicker?.cancel()
        stallTicker = nil
        itemStatusObs?.invalidate()
        itemKeepUpObs?.invalidate()
        itemEmptyObs?.invalidate()
        seekableObs?.invalidate()
        timeControlObs?.invalidate()
        itemStatusObs = nil
        itemKeepUpObs = nil
        itemEmptyObs = nil
        seekableObs = nil
        timeControlObs = nil
        if let player, let periodicTimeObserver {
            player.removeTimeObserver(periodicTimeObserver)
        }
        periodicTimeObserver = nil
        boundaryTimeObserver = nil
        if let endObserver {
            NotificationCenter.default.removeObserver(endObserver)
            self.endObserver = nil
        }
        if let failedObserver {
            NotificationCenter.default.removeObserver(failedObserver)
            self.failedObserver = nil
        }
    }
}
#endif
