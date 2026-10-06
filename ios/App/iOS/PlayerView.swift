import SwiftUI
import AVKit
import AVFoundation
import GroupActivities
import UIKit // with GroupActivities: GroupActivitySharingController
import BowtieKit

/// Full-screen HLS player: AVPlayerViewController wrapper with auto-hiding chrome,
/// quality menu, stats overlay, AirPlay route picker, and PiP-safe teardown.
struct PlayerView: View {
    let channel: Channel
    let serverURL: URL
    let maxQuality: String
    var nowTitle: String?
    /// When the program now on a channel ends (guide), for End of this program.
    var programEnd: (Channel) -> Date? = { _ in nil }
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
    /// "On a FaceTime call" for SharePlay purposes.
    @StateObject private var groupState = GroupStateObserver()
    @State private var sharingActivity: SharingActivity?
    @State private var isStartingSharePlay = false
    /// Survives channel changes here; gone when the player is.
    @State private var sleepTimer = SleepTimer()

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
            } else if let note = playerModel.weakSignalNote {
                // Stays on screen while the signal is weak; with the chrome up
                // it sits under the Live pill instead.
                WeakSignalNote(text: note)
                    .padding(.horizontal, 16)
                    .padding(.top, 12)
                    .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
                    .allowsHitTesting(false)
                    .transition(.opacity)
            }

            if case .stalled = playerModel.state {
                stalledSpinner
            } else if case .starting = playerModel.state {
                stalledSpinner
            }

            VStack(spacing: 8) {
                if sleepTimer.isWarning, let remaining = sleepTimer.remaining {
                    SleepWarningBanner(remaining: remaining) {
                        sleepTimer.extend()
                    }
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
                        .transition(.opacity)
                        .accessibilityLabel(notice)
                }
            }
            .padding(.bottom, 96)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottom)
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
        .announcesWeakSignal(playerModel)
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
        .drivesSleepTimer(sleepTimer) {
            Task { await leave() }
        }
        .task(id: coordinationKey) {
            coordinatePlayback()
        }
        .sheet(item: $sharingActivity) { item in
            GroupActivitySharingView(activity: item.activity)
        }
        .alert(
            "Leave Watch Together?",
            isPresented: groupZapBinding,
            presenting: playerModel.pendingGroupZap
        ) { zap in
            Button("Watch \(zap.name)", role: .destructive) {
                Task { await playerModel.confirmGroupZap(zap) }
            }
            Button("Keep Watching Together", role: .cancel) {
                playerModel.cancelGroupZap()
            }
        } message: { zap in
            Text("You're watching \(playerModel.currentChannel?.name ?? "this channel") with your group. Watching \(zap.name) leaves the group.")
        }
    }

    private func showOutOfWindowNotice() {
        showNotice(PlayerModel.outOfWindowNotice)
    }

    private func showNotice(_ text: String) {
        outOfWindowNotice = text
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
                        droppedFrames: droppedFrames,
                        signalLine: playerModel.signalStatsLine
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
                // Scrolls sideways only when it doesn't fit (narrow iPhones);
                // otherwise taps beside the buttons still reach AVKit.
                ViewThatFits(in: .horizontal) {
                    controlsRow
                    ScrollView(.horizontal, showsIndicators: false) {
                        controlsRow
                    }
                }
                if let note = playerModel.weakSignalNote {
                    WeakSignalNote(text: note)
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

    private var controlsRow: some View {
        HStack(spacing: 10) {
            livePill
            playerButtons
        }
    }

    private var identityRow: some View {
        HStack(alignment: .center, spacing: 12) {
            Text(shownChannel.guideNumber)
                .font(Theme.channelNumber(36))
                .foregroundStyle(Theme.amber)
                .fixedSize()
                .accessibilityLabel("Channel \(shownChannel.guideNumber)")

            VStack(alignment: .leading, spacing: 2) {
                Text(shownChannel.name)
                    .font(Theme.label(16))
                    .foregroundStyle(Theme.text)
                    .lineLimit(1)
                if shownChannel.id == channel.id, let title = nowTitle, !title.isEmpty {
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
            SleepTimerButton(
                timer: sleepTimer,
                programEnd: { programEnd(shownChannel) },
                onOpen: { bumpChrome(for: Self.menuOpenDelay) }
            )
            #if SHAREPLAY
            if canShare || playerModel.groupRole != nil {
                sharePlayButton
            }
            #endif

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

    // MARK: - SharePlay

    /// The channel actually playing: a SharePlay group can change it under
    /// this view (and a participant may decline a channel they picked).
    private var shownChannel: Channel {
        playerModel.currentChannel ?? channel
    }

    /// The server gave this stream a session ID, so others can join it.
    private var canShare: Bool {
        guard case .playing(let session) = playerModel.state else { return false }
        return !(session.session?.id ?? "").isEmpty
    }

    /// "Watch Together": on a FaceTime call, start SharePlay; otherwise offer
    /// the system sheet that starts a call with the activity.
    private var sharePlayButton: some View {
        let inGroup = playerModel.groupRole != nil
        return Button {
            bumpChrome()
            Task { await startSharePlay() }
        } label: {
            Image(systemName: "shareplay")
                .font(.system(size: 18, weight: .medium))
                .foregroundStyle(inGroup ? Theme.bg : Theme.amber)
                .frame(width: 44, height: 44)
                .background(inGroup ? Theme.amber : Theme.raised.opacity(0.9))
                .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
        }
        .buttonStyle(.plain)
        .disabled(inGroup || isStartingSharePlay)
        .accessibilityLabel(inGroup ? "Watching together" : "Watch Together")
        .accessibilityHint(inGroup ? "" : "Watch this channel with people on a FaceTime call")
        .accessibilityIdentifier("bowtie.shareplay")
    }

    private func startSharePlay() async {
        guard !isStartingSharePlay else { return }
        isStartingSharePlay = true
        defer { isStartingSharePlay = false }
        guard let activity = await playerModel.makeWatchActivity() else {
            showNotice("SharePlay needs a newer Bowtie server")
            return
        }
        if groupState.isEligibleForGroupSession {
            do {
                _ = try await activity.activate()
            } catch {
                showNotice("Couldn't start SharePlay")
            }
        } else {
            sharingActivity = SharingActivity(activity: activity)
        }
    }

    /// Bind the group (if any) to the current AVPlayer, whichever came first.
    private var coordinationKey: String {
        let player = bridge.player.map { "\(ObjectIdentifier($0).hashValue)" } ?? "-"
        let group = playerModel.group.map { "\(ObjectIdentifier($0).hashValue)" } ?? "-"
        return "\(player)|\(group)"
    }

    private func coordinatePlayback() {
        guard let group = playerModel.group, let player = bridge.player else { return }
        group.coordinate(player)
    }

    private var groupZapBinding: Binding<Bool> {
        Binding(
            get: { playerModel.pendingGroupZap != nil },
            set: { if !$0 { playerModel.cancelGroupZap() } }
        )
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
        SharedItemIdentity.register(playlistURL: url, sessionId: session.session?.id)
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
                    SharedItemIdentity.register(playlistURL: url, sessionId: session.session?.id)
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

// MARK: - SharePlay sheet

private struct SharingActivity: Identifiable {
    let id = UUID()
    let activity: WatchChannelActivity
}

/// The system sheet that starts a FaceTime call (or Messages) with the
/// activity, for sharing when not already on a call.
private struct GroupActivitySharingView: UIViewControllerRepresentable {
    let activity: WatchChannelActivity

    func makeUIViewController(context: Context) -> UIViewController {
        (try? GroupActivitySharingController(activity)) ?? UIViewController()
    }

    func updateUIViewController(_ uiViewController: UIViewController, context: Context) {}
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
