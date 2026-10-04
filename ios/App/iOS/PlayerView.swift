import SwiftUI
import AVKit
import AVFoundation
import BowtieKit

/// Full-screen HLS player: AVPlayerViewController wrapper with auto-hiding chrome,
/// quality menu, stats overlay, AirPlay route picker, and PiP-safe teardown.
struct PlayerView: View {
    let channel: Channel
    let serverURL: URL
    let maxQuality: String
    var nowTitle: String?
    @Bindable var playerModel: PlayerModel

    @Environment(\.dismiss) private var dismiss

    @State private var bridge = PlayerBridge()
    @State private var showChrome = true
    @State private var showStats = false
    @State private var showAudioChoices = false
    @State private var isStopping = false
    @State private var hideChromeTask: Task<Void, Never>?
    @State private var stallRetryTask: Task<Void, Never>?
    @State private var stallAttempt = 0
    @State private var indicatedBitrate: Double?
    @State private var droppedFrames: Int?
    @State private var statsPollTask: Task<Void, Never>?
    @State private var outOfWindowNotice: String?
    @State private var noticeHideTask: Task<Void, Never>?

    /// Stall retry backoff: 1s, 2s, 4s (3 attempts).
    private static let stallBackoffs: [Duration] = [
        .seconds(1), .seconds(2), .seconds(4),
    ]

    private static let chromeHideDelay: Duration = .seconds(3)
    private static let menuOpenDelay: Duration = .seconds(20)
    private static let noticeHideDelay: Duration = .seconds(4)

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()

            // Taps go to AVPlayerViewController so its transport (scrubber,
            // skip, play/pause) appears; the container reports them so the
            // Bowtie chrome shows at the same time.
            PlayerContainer(
                bridge: bridge,
                onPictureInPictureActiveChange: { active in
                    bridge.isPictureInPictureActive = active
                },
                onTap: { bumpChrome() }
            )
            .ignoresSafeArea()

            if showChrome || isBlockingError {
                chromeLayer
                    .transition(.opacity)
            }

            if case .stalled = playerModel.state {
                stalledSpinner
            } else if case .starting = playerModel.state {
                stalledSpinner
            }

            if let notice = outOfWindowNotice {
                Text(notice)
                    .font(Theme.body(14))
                    .foregroundStyle(Theme.text)
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, 16)
                    .padding(.vertical, 12)
                    .background(Theme.bg.opacity(0.88))
                    .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
                    .padding(.bottom, 96)
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottom)
                    .transition(.opacity)
                    .accessibilityLabel(notice)
            }
        }
        .navigationBarBackButtonHidden(true)
        .toolbar(.hidden, for: .navigationBar)
        .statusBarHidden(true)
        .onAppear {
            configureAudioSession()
            bumpChrome()
            startStatsPolling()
        }
        .onDisappear {
            hideChromeTask?.cancel()
            statsPollTask?.cancel()
            stallRetryTask?.cancel()
            // PiP-safe: keep session alive while picture-in-picture is active.
            if !bridge.isPictureInPictureActive {
                Task { await playerModel.stop() }
            } else {
                bridge.shouldStopWhenPiPEnds = true
            }
        }
        .onReceive(NotificationCenter.default.publisher(for: UIApplication.willTerminateNotification)) { _ in
            Task { await playerModel.stop() }
        }
        .onChange(of: playerModel.state) { _, newState in
            handleStateChange(newState)
        }
        .onChange(of: bridge.playerErrorIsForbidden) { _, isForbidden in
            if isForbidden {
                bridge.playerErrorIsForbidden = false
                Task { await playerModel.playbackAuthFailed() }
            }
        }
        .onChange(of: bridge.playerDidStall) { _, stalled in
            if stalled {
                bridge.playerDidStall = false
                beginStallRecovery()
            }
        }
        .onChange(of: bridge.playerDidRecover) { _, recovered in
            if recovered {
                bridge.playerDidRecover = false
                stallRetryTask?.cancel()
                stallAttempt = 0
                if case .stalled = playerModel.state {
                    playerModel.resumePlaying()
                }
            }
        }
        .onChange(of: bridge.playerDidJumpToLive) { _, jumped in
            if jumped {
                bridge.playerDidJumpToLive = false
                showOutOfWindowNotice()
            }
        }
        .onChange(of: bridge.pipDidEndAndShouldStop) { _, shouldStop in
            if shouldStop {
                bridge.pipDidEndAndShouldStop = false
                Task {
                    await playerModel.stop()
                    dismiss()
                }
            }
        }
        .task(id: sessionIdentity) {
            await loadPlayerIfNeeded()
        }
    }

    private func showOutOfWindowNotice() {
        outOfWindowNotice = PlayerModel.outOfWindowNotice
        noticeHideTask?.cancel()
        noticeHideTask = Task { @MainActor in
            do {
                try await Task.sleep(for: Self.noticeHideDelay)
            } catch {
                return
            }
            guard !Task.isCancelled else { return }
            withAnimation(.easeOut(duration: 0.25)) {
                outOfWindowNotice = nil
            }
        }
    }

    // MARK: - Chrome

    private var chromeLayer: some View {
        VStack(spacing: 0) {
            topBar
            Spacer()
            if showStats, let metaOrNil = sessionMetaOptional {
                HStack {
                    StatsOverlay(
                        meta: metaOrNil,
                        indicatedBitrate: indicatedBitrate,
                        droppedFrames: droppedFrames
                    )
                    Spacer(minLength: 0)
                }
                .padding(.horizontal, 16)
                .padding(.bottom, 8)
            }
            if isBlockingError {
                errorPanel
                    .padding(.horizontal, 20)
                    .padding(.bottom, 24)
            }
        }
        .animation(.easeInOut(duration: 0.2), value: showChrome)
    }

    private var topBar: some View {
        VStack(alignment: .leading, spacing: 10) {
            identityRow
            // The bottom edge belongs to the system transport (live scrubber),
            // so Bowtie's player controls get a second row up here.
            if !isBlockingError {
                HStack(spacing: 10) {
                    livePill
                    playerButtons
                }
            }
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
            // Decoration only: AVKit's top buttons sit under this gradient.
            .allowsHitTesting(false)
        )
    }

    private var identityRow: some View {
        HStack(alignment: .center, spacing: 12) {
            Text(channel.guideNumber)
                .font(Theme.channelNumber(36))
                .foregroundStyle(Theme.amber)
                .fixedSize()
                .accessibilityLabel("Channel \(channel.guideNumber)")

            VStack(alignment: .leading, spacing: 2) {
                Text(channel.name)
                    .font(Theme.label(16))
                    .foregroundStyle(Theme.text)
                    .lineLimit(1)
                if let title = nowTitle, !title.isEmpty {
                    Text(title)
                        .font(Theme.body(14))
                        .foregroundStyle(Theme.dim)
                        .lineLimit(1)
                }
            }

            Spacer(minLength: 0)

            Button {
                Task { await leave() }
            } label: {
                Text("Done")
                    .font(Theme.label(16))
                    .foregroundStyle(Theme.amber)
                    .padding(.horizontal, 12)
                    .padding(.vertical, 8)
            }
            .buttonStyle(.plain)
            .disabled(isStopping)
            .accessibilityLabel("Done")
            .accessibilityHint("Stop playback and return to the channel list")
        }
    }

    /// "Live": red dot when at the live point; when behind, shows how far and
    /// jumps back to live on tap.
    @ViewBuilder
    private var livePill: some View {
        if let behind = bridge.secondsBehindLive {
            let live = LiveEdge.isLive(secondsBehind: behind)
            Button {
                bridge.jumpToLive()
                bumpChrome()
            } label: {
                HStack(spacing: 6) {
                    Circle()
                        .fill(live ? Color.red : Theme.dim)
                        .frame(width: 8, height: 8)
                    Text(live ? "Live" : "Live \(LiveEdge.behindLabel(secondsBehind: behind))")
                        .font(Theme.label(14))
                        .foregroundStyle(live ? Theme.text : Theme.amber)
                        .lineLimit(1)
                        .monospacedDigit()
                }
                .padding(.horizontal, 10)
                .padding(.vertical, 8)
                .background(Theme.raised.opacity(0.9))
                .clipShape(Capsule())
                .fixedSize()
            }
            .buttonStyle(.plain)
            .disabled(live)
            .accessibilityLabel("Live")
            .accessibilityValue(live ? "Watching live" : "\(Int(behind)) seconds behind")
            .accessibilityHint(live ? "" : "Jump to live")
        }
    }

    private var playerButtons: some View {
        HStack(spacing: 10) {
            qualityMenu
            if bridge.audioOptionNames.count > 1 {
                audioMenu
            }

            Button {
                showStats.toggle()
                bumpChrome()
            } label: {
                Image(systemName: showStats ? "info.circle.fill" : "info.circle")
                    .font(.system(size: 22, weight: .medium))
                    .foregroundStyle(Theme.amber)
                    .frame(width: 44, height: 44)
            }
            .buttonStyle(.plain)
            .accessibilityLabel(showStats ? "Hide stats" : "Show stats")
        }
    }

    /// Broadcast audio language (e.g. Español). AVKit's inline controls on
    /// iPhone have no audio choice, so Bowtie offers one. A dialog (not a
    /// Menu) so the chrome's auto-hide can't close it mid-choice.
    private var audioMenu: some View {
        Button {
            showAudioChoices = true
            bumpChrome(for: Self.menuOpenDelay)
        } label: {
            Image(systemName: "waveform")
                .font(.system(size: 18, weight: .medium))
                .foregroundStyle(Theme.amber)
                .frame(width: 44, height: 44)
                .background(Theme.raised.opacity(0.9))
                .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Audio")
        .accessibilityIdentifier("bowtie.audio")
        .accessibilityValue(bridge.selectedAudioIndex.map { bridge.audioOptionNames[$0] } ?? "")
        .confirmationDialog("Audio", isPresented: $showAudioChoices, titleVisibility: .visible) {
            ForEach(Array(bridge.audioOptionNames.enumerated()), id: \.offset) { index, name in
                Button(bridge.selectedAudioIndex == index ? "\(name) ✓" : name) {
                    bridge.selectAudio(index)
                    bumpChrome()
                }
            }
        }
    }

    private var qualityMenu: some View {
        Menu {
            // Choices are a ceiling: playback adapts below it.
            Text("The most this player will use")
            Button {
                Task { await playerModel.setProfile("") }
            } label: {
                labelRow(title: "Auto", selected: playerModel.selectedProfile.isEmpty)
            }
            ForEach(GuideLogic.allowedProfiles(maxQuality: maxQuality), id: \.self) { profile in
                Button {
                    Task { await playerModel.setProfile(profile) }
                } label: {
                    labelRow(
                        title: profile.capitalized,
                        selected: playerModel.selectedProfile == profile
                    )
                }
            }
        } label: {
            HStack(spacing: 6) {
                Image(systemName: "slider.horizontal.3")
                Text(qualityLabel)
                    .font(Theme.label(14))
                    .lineLimit(1)
            }
            .fixedSize()
            .foregroundStyle(Theme.amber)
            .padding(.horizontal, 12)
            .padding(.vertical, 10)
            .background(Theme.raised.opacity(0.9))
            .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
        }
        .accessibilityLabel("Quality")
        .accessibilityValue(qualityLabel)
    }

    private func labelRow(title: String, selected: Bool) -> some View {
        HStack {
            Text(title)
            if selected {
                Image(systemName: "checkmark")
            }
        }
    }

    private var qualityLabel: String {
        playerModel.selectedProfile.isEmpty
            ? "Auto"
            : playerModel.selectedProfile.capitalized
    }

    private var stalledSpinner: some View {
        VStack(spacing: 12) {
            ProgressView()
                .tint(Theme.amber)
                .scaleEffect(1.2)
            Text(playerModel.state == .stalled ? "Reconnecting…" : "Starting…")
                .font(Theme.body(14))
                .foregroundStyle(Theme.dim)
        }
        .padding(20)
        .background(Theme.bg.opacity(0.7))
        .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
        .accessibilityLabel(playerModel.state == .stalled ? "Reconnecting" : "Starting")
    }

    // MARK: - Error panels

    private var isBlockingError: Bool {
        switch playerModel.state {
        case .failed, .tunersBusy:
            return true
        default:
            return false
        }
    }

    @ViewBuilder
    private var errorPanel: some View {
        switch playerModel.state {
        case .tunersBusy(let sessions, let otherInUse):
            VStack(spacing: 14) {
                Text("All tuners are in use")
                    .font(Theme.title(18))
                    .foregroundStyle(Theme.alert)
                    .multilineTextAlignment(.center)

                if let otherApps = TunersBusyCopy.otherAppsLine(otherInUse: otherInUse) {
                    Text(otherApps)
                        .font(Theme.body(15))
                        .foregroundStyle(Theme.text)
                        .multilineTextAlignment(.center)
                }

                if !sessions.isEmpty {
                    VStack(alignment: .leading, spacing: 8) {
                        Text("Who's watching")
                            .font(Theme.label(13))
                            .foregroundStyle(Theme.dim)
                        ForEach(Array(sessions.enumerated()), id: \.offset) { _, session in
                            let names = session.viewers.map(\.username).joined(separator: ", ")
                            Text("\(session.channelName)\(names.isEmpty ? "" : " — \(names)")")
                                .font(Theme.body(14))
                                .foregroundStyle(Theme.text)
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(12)
                    .background(Theme.surface)
                    .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
                }

                errorActions
            }
            .padding(20)
            .background(Theme.bg.opacity(0.92))
            .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous)
                    .stroke(Theme.line, lineWidth: 1)
            )

        case .failed(let message):
            VStack(spacing: 14) {
                Text(message)
                    .font(Theme.body(16))
                    .foregroundStyle(Theme.alert)
                    .multilineTextAlignment(.center)
                errorActions
            }
            .padding(20)
            .background(Theme.bg.opacity(0.92))
            .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous)
                    .stroke(Theme.line, lineWidth: 1)
            )

        default:
            EmptyView()
        }
    }

    private var errorActions: some View {
        HStack(spacing: 12) {
            Button {
                Task { await playerModel.retry() }
            } label: {
                Text("Try again")
                    .font(Theme.label(16))
                    .padding(.horizontal, 18)
                    .padding(.vertical, 10)
                    .background(Theme.raised)
                    .foregroundStyle(Theme.text)
                    .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Try again")

            Button {
                Task { await leave() }
            } label: {
                Text("Back")
                    .font(Theme.label(16))
                    .padding(.horizontal, 18)
                    .padding(.vertical, 10)
                    .foregroundStyle(Theme.amber)
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Back")
        }
    }

    // MARK: - Playback wiring

    private var sessionIdentity: String {
        switch playerModel.state {
        case .playing(let s):
            return "\(s.viewerId)|\(s.playlistUrl)"
        case .stalled:
            if let s = playerModel.lastSession {
                return "stall|\(s.viewerId)|\(s.playlistUrl)"
            }
            return "stalled"
        default:
            return "\(playerModel.state)"
        }
    }

    private var sessionMetaOptional: SessionInfoMeta? {
        if case .playing(let s) = playerModel.state {
            return s.session
        }
        return playerModel.lastSession?.session
    }

    private func loadPlayerIfNeeded() async {
        let session: CreatedSession?
        switch playerModel.state {
        case .playing(let s):
            session = s
        case .stalled:
            session = playerModel.lastSession
        default:
            session = nil
        }
        guard let session else {
            if case .idle = playerModel.state {
                bridge.replacePlayer(nil)
            } else if case .failed = playerModel.state {
                // Keep last frame if any; no new load.
            } else if case .tunersBusy = playerModel.state {
                bridge.replacePlayer(nil)
            } else if case .starting = playerModel.state {
                // Wait for create.
            }
            return
        }

        let url = ServerURL.resolve(path: session.playlistUrl, against: serverURL)
        bridge.load(url: url)
        stallAttempt = 0
    }

    private func handleStateChange(_ newState: PlayerModel.State) {
        switch newState {
        case .playing:
            stallAttempt = 0
            stallRetryTask?.cancel()
            bumpChrome()
        case .starting:
            stallRetryTask?.cancel()
            showChrome = true
        case .stalled:
            showChrome = true
        case .failed, .tunersBusy:
            stallRetryTask?.cancel()
            showChrome = true
            bridge.replacePlayer(nil)
        case .idle:
            stallRetryTask?.cancel()
            bridge.replacePlayer(nil)
        }
    }

    private func beginStallRecovery() {
        // Only recover while we still own a live session.
        guard playerModel.lastSession != nil || {
            if case .playing = playerModel.state { return true }
            return false
        }() else { return }

        if case .playing = playerModel.state {
            playerModel.markStalled()
        }
        guard case .stalled = playerModel.state else { return }

        stallRetryTask?.cancel()
        stallRetryTask = Task { @MainActor in
            while !Task.isCancelled {
                if stallAttempt >= Self.stallBackoffs.count {
                    playerModel.stallFailed()
                    return
                }
                let delay = Self.stallBackoffs[stallAttempt]
                stallAttempt += 1
                do {
                    try await Task.sleep(for: delay)
                } catch {
                    return
                }
                guard !Task.isCancelled else { return }
                guard case .stalled = playerModel.state else { return }
                // Re-seek / re-load the same playlist URL.
                if let session = playerModel.lastSession {
                    let url = ServerURL.resolve(path: session.playlistUrl, against: serverURL)
                    bridge.load(url: url)
                }
            }
        }
    }

    /// Show the chrome and restart its hide timer. Opening a menu passes a
    /// longer delay: hiding the chrome would close the menu mid-choice.
    private func bumpChrome(for delay: Duration = Self.chromeHideDelay) {
        showChrome = true
        hideChromeTask?.cancel()
        guard !isBlockingError else { return }
        hideChromeTask = Task { @MainActor in
            do {
                try await Task.sleep(for: delay)
            } catch {
                return
            }
            guard !Task.isCancelled else { return }
            guard !isBlockingError else { return }
            withAnimation(.easeOut(duration: 0.25)) {
                showChrome = false
            }
        }
    }

    private func startStatsPolling() {
        statsPollTask?.cancel()
        statsPollTask = Task { @MainActor in
            while !Task.isCancelled {
                let sample = bridge.accessLogSample()
                indicatedBitrate = sample.bitrate
                droppedFrames = sample.dropped
                do {
                    try await Task.sleep(for: .seconds(1))
                } catch {
                    return
                }
            }
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

    @MainActor
    private func leave() async {
        guard !isStopping else { return }
        isStopping = true
        stallRetryTask?.cancel()
        hideChromeTask?.cancel()
        await playerModel.stop()
        dismiss()
    }
}

// MARK: - Bridge (player ownership + PiP / error flags)

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
            p.usesExternalPlaybackWhileExternalScreenIsActive = true
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

// MARK: - AVPlayerViewController representable

private struct PlayerContainer: UIViewControllerRepresentable {
    var bridge: PlayerBridge
    var onPictureInPictureActiveChange: (Bool) -> Void
    var onTap: () -> Void

    func makeUIViewController(context: Context) -> AVPlayerViewController {
        let vc = AVPlayerViewController()
        vc.allowsPictureInPicturePlayback = true
        vc.canStartPictureInPictureAutomaticallyFromInline = true
        // Spec D: native live-DVR scrubber (do not force linear-only playback).
        vc.showsPlaybackControls = true
        vc.requiresLinearPlayback = false
        vc.delegate = context.coordinator
        vc.player = bridge.player
        // Observe taps without taking them from AVKit, which uses them to show
        // its transport controls.
        let tap = UITapGestureRecognizer(target: context.coordinator, action: #selector(Coordinator.handleTap))
        tap.cancelsTouchesInView = false
        tap.delegate = context.coordinator
        vc.view.addGestureRecognizer(tap)
        return vc
    }

    func updateUIViewController(_ vc: AVPlayerViewController, context: Context) {
        if vc.player !== bridge.player {
            vc.player = bridge.player
        }
        context.coordinator.onPictureInPictureActiveChange = onPictureInPictureActiveChange
        context.coordinator.onTap = onTap
        context.coordinator.bridge = bridge
    }

    func makeCoordinator() -> Coordinator {
        Coordinator(bridge: bridge, onPictureInPictureActiveChange: onPictureInPictureActiveChange, onTap: onTap)
    }

    final class Coordinator: NSObject, AVPlayerViewControllerDelegate, UIGestureRecognizerDelegate {
        var bridge: PlayerBridge
        var onPictureInPictureActiveChange: (Bool) -> Void
        var onTap: () -> Void

        init(
            bridge: PlayerBridge,
            onPictureInPictureActiveChange: @escaping (Bool) -> Void,
            onTap: @escaping () -> Void
        ) {
            self.bridge = bridge
            self.onPictureInPictureActiveChange = onPictureInPictureActiveChange
            self.onTap = onTap
        }

        @objc func handleTap() {
            onTap()
        }

        func gestureRecognizer(
            _ gestureRecognizer: UIGestureRecognizer,
            shouldRecognizeSimultaneouslyWith otherGestureRecognizer: UIGestureRecognizer
        ) -> Bool {
            true
        }

        func playerViewControllerWillStartPictureInPicture(_ playerViewController: AVPlayerViewController) {
            Task { @MainActor in
                self.bridge.isPictureInPictureActive = true
                self.onPictureInPictureActiveChange(true)
            }
        }

        func playerViewControllerDidStartPictureInPicture(_ playerViewController: AVPlayerViewController) {
            Task { @MainActor in
                self.bridge.isPictureInPictureActive = true
                self.onPictureInPictureActiveChange(true)
            }
        }

        func playerViewControllerDidStopPictureInPicture(_ playerViewController: AVPlayerViewController) {
            Task { @MainActor in
                self.bridge.isPictureInPictureActive = false
                self.onPictureInPictureActiveChange(false)
                if self.bridge.shouldStopWhenPiPEnds {
                    self.bridge.shouldStopWhenPiPEnds = false
                    self.bridge.pipDidEndAndShouldStop = true
                }
            }
        }

        func playerViewController(
            _ playerViewController: AVPlayerViewController,
            restoreUserInterfaceForPictureInPictureStopWithCompletionHandler completionHandler: @escaping (Bool) -> Void
        ) {
            // User returned from PiP into the app — keep the session; do not stop.
            Task { @MainActor in
                self.bridge.shouldStopWhenPiPEnds = false
                self.bridge.isPictureInPictureActive = false
                self.onPictureInPictureActiveChange(false)
                completionHandler(true)
            }
        }
    }
}

// MARK: - Preview

#Preview {
    NavigationStack {
        PlayerView(
            channel: Channel(id: 1, guideNumber: "7.1", name: "Local News", logoUrl: ""),
            serverURL: URL(string: "http://127.0.0.1:8400")!,
            maxQuality: "high",
            nowTitle: "Evening Report",
            playerModel: PlayerModel(
                client: BowtieClient(
                    server: URL(string: "http://127.0.0.1:8400")!,
                    store: InMemorySessionStore()
                ),
                caps: Caps.make(maxHeight: 1080)
            )
        )
    }
}
