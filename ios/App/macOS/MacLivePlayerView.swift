import SwiftUI
#if SHAREPLAY
import GroupActivities
#endif
import AppKit
import AVKit
import BowtieKit

/// Live TV in the detail pane. Same session machine as iOS (`PlayerModel`:
/// session replace, heartbeats, 422 / 404 / tuners-busy handling) and the same
/// `PlayerBridge` (stall recovery, live edge, out-of-window clamp).
///
/// One instance stays on screen across channel changes: picking a channel
/// calls `PlayerModel.play`, which replaces the session underneath it.
struct MacLivePlayerView: View {
    let serverURL: URL
    let maxQuality: String
    let nowTitle: String?
    /// When the program now on the channel ends (guide), for End of this program.
    let programEnd: Date?
    @Bindable var playerModel: PlayerModel
    let bridge: PlayerBridge
    /// "Back" on an error panel: stop and clear the selection.
    let onLeave: () -> Void

    @State private var showChrome = true
    @State private var showStats = false
    @State private var hideChromeTask: Task<Void, Never>?
    @State private var stallRetryTask: Task<Void, Never>?
    @State private var stallAttempt = 0
    @State private var indicatedBitrate: Double?
    @State private var droppedFrames: Int?
    @State private var statsPollTask: Task<Void, Never>?
    @State private var outOfWindowNotice: String?
    @State private var noticeHideTask: Task<Void, Never>?
    /// Survives channel changes (this view stays up); gone when Live TV is left.
    @State private var sleepTimer = SleepTimer()
    #if SHAREPLAY
    @StateObject private var groupState = GroupStateObserver()
    @State private var sharingActivity: SharingActivity?
    @State private var isStartingSharePlay = false
    #endif

    /// Stall retry backoff: 1s, 2s, 4s (3 attempts).
    private static let stallBackoffs: [Duration] = [
        .seconds(1), .seconds(2), .seconds(4),
    ]

    private static let chromeHideDelay: Duration = .seconds(3)
    private static let noticeHideDelay: Duration = .seconds(4)

    var body: some View {
        ZStack {
            Color.black

            MacPlayerSurface(player: bridge.player, allowsPictureInPicture: true, bridge: bridge)

            if showChrome || isBlockingError {
                chromeLayer
                    .transition(.opacity)
            }

            if case .stalled = playerModel.state {
                spinner
            } else if case .starting = playerModel.state {
                spinner
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
                        .allowsHitTesting(false)
                        .transition(.opacity)
                        .accessibilityLabel(notice)
                }
            }
            .padding(.bottom, 96)
            .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .bottom)
        }
        .onContinuousHover { phase in
            if case .active = phase {
                bumpChrome()
            }
        }
        .onAppear {
            bumpChrome()
            startStatsPolling()
        }
        .onDisappear {
            hideChromeTask?.cancel()
            statsPollTask?.cancel()
            stallRetryTask?.cancel()
            noticeHideTask?.cancel()
            // PiP-safe: keep the session while picture in picture is up; the
            // main view stops it when PiP ends.
            if bridge.isPictureInPictureActive {
                bridge.shouldStopWhenPiPEnds = true
            } else {
                bridge.replacePlayer(nil)
                Task { await playerModel.stop() }
            }
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
        .task(id: sessionIdentity) {
            loadPlayerIfNeeded()
        }
        #if SHAREPLAY
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
        #endif
        .drivesSleepTimer(sleepTimer) {
            // Stop here, not in onDisappear: that keeps the session for PiP.
            stallRetryTask?.cancel()
            bridge.replacePlayer(nil)
            Task {
                await playerModel.stop()
                onLeave()
            }
        }
    }

    // MARK: - Chrome

    private var chromeLayer: some View {
        VStack(spacing: 0) {
            topBar
            Spacer()
            if showStats, let meta = sessionMeta {
                HStack {
                    StatsOverlay(
                        meta: meta,
                        indicatedBitrate: indicatedBitrate,
                        droppedFrames: droppedFrames
                    )
                    Spacer(minLength: 0)
                }
                .padding(.horizontal, 16)
                // Clear of AVKit's floating transport bar.
                .padding(.bottom, 90)
            }
            if isBlockingError {
                errorPanel
                    .frame(maxWidth: 460)
                    .padding(.horizontal, 20)
                    .padding(.bottom, 32)
                Spacer()
            }
        }
        .animation(.easeInOut(duration: 0.2), value: showChrome)
    }

    private var topBar: some View {
        HStack(alignment: .center, spacing: 12) {
            if let channel = playerModel.currentChannel {
                Text(channel.guideNumber)
                    .font(Theme.channelNumber(34))
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
                            .font(Theme.body(13))
                            .foregroundStyle(Theme.dim)
                            .lineLimit(1)
                    }
                }
            }

            Spacer(minLength: 0)

            if !isBlockingError {
                livePill
                qualityMenu
                SleepTimerMenu(timer: sleepTimer, programEnd: programEnd)
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
                        .font(.system(size: 18, weight: .medium))
                        .foregroundStyle(Theme.amber)
                        .frame(width: 32, height: 32)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help(showStats ? "Hide stats" : "Show stats")
                .accessibilityLabel(showStats ? "Hide stats" : "Show stats")
            }
        }
        .padding(.horizontal, 16)
        .padding(.top, 12)
        .padding(.bottom, 24)
        .background(
            LinearGradient(
                colors: [Color.black.opacity(0.75), Color.black.opacity(0)],
                startPoint: .top,
                endPoint: .bottom
            )
            .allowsHitTesting(false)
        )
    }

    /// "Live": red dot at the live point; when behind, shows how far and
    /// jumps back on click (also L).
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
                        .font(Theme.label(13))
                        .foregroundStyle(live ? Theme.text : Theme.amber)
                        .lineLimit(1)
                        .monospacedDigit()
                }
                .padding(.horizontal, 10)
                .padding(.vertical, 6)
                .background(Theme.raised.opacity(0.9))
                .clipShape(Capsule())
                .fixedSize()
            }
            .buttonStyle(.plain)
            .disabled(live)
            .help(live ? "Watching live" : "Jump to live (L)")
            .accessibilityLabel("Live")
            .accessibilityValue(live ? "Watching live" : "\(Int(behind)) seconds behind")
            .accessibilityHint(live ? "" : "Jump to live")
        }
    }

    private var qualityMenu: some View {
        Menu {
            // Choices are a ceiling: playback adapts below it.
            Section("The most this player will use") {
                Toggle("Auto", isOn: profileBinding(""))
                ForEach(GuideLogic.allowedProfiles(maxQuality: maxQuality), id: \.self) { profile in
                    Toggle(profile.capitalized, isOn: profileBinding(profile))
                }
            }
        } label: {
            Label(qualityLabel, systemImage: "slider.horizontal.3")
                .font(Theme.label(13))
        }
        .menuStyle(.borderlessButton)
        .fixedSize()
        .foregroundStyle(Theme.amber)
        .padding(.horizontal, 10)
        .padding(.vertical, 6)
        .background(Theme.raised.opacity(0.9))
        .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
        .help("Quality")
        .accessibilityLabel("Quality")
        .accessibilityValue(qualityLabel)
    }

    private func profileBinding(_ profile: String) -> Binding<Bool> {
        Binding(
            get: { playerModel.selectedProfile == profile },
            set: { on in
                if on {
                    Task { await playerModel.setProfile(profile) }
                }
            }
        )
    }

    private var qualityLabel: String {
        playerModel.selectedProfile.isEmpty ? "Auto" : playerModel.selectedProfile.capitalized
    }

    private var spinner: some View {
        VStack(spacing: 12) {
            ProgressView()
                .tint(Theme.amber)
            Text(playerModel.state == .stalled ? "Reconnecting…" : "Starting…")
                .font(Theme.body(14))
                .foregroundStyle(Theme.dim)
        }
        .padding(20)
        .background(Theme.bg.opacity(0.7))
        .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
        .allowsHitTesting(false)
        .accessibilityLabel(playerModel.state == .stalled ? "Reconnecting" : "Starting")
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
            withAnimation(.easeOut(duration: 0.25)) {
                outOfWindowNotice = nil
            }
        }
    }

    // MARK: - SharePlay

    #if SHAREPLAY
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
                .font(.system(size: 16, weight: .medium))
                .foregroundStyle(inGroup ? Theme.bg : Theme.amber)
                .frame(width: 32, height: 32)
                .background(inGroup ? Theme.amber : Theme.raised.opacity(0.9))
                .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(inGroup || isStartingSharePlay)
        .help(inGroup ? "Watching together" : "Watch Together (SharePlay)")
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
    #endif

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
            panel {
                Text("All tuners are in use")
                    .font(Theme.title(18))
                    .foregroundStyle(Theme.alert)
                    .multilineTextAlignment(.center)

                if let otherApps = TunersBusyCopy.otherAppsLine(otherInUse: otherInUse) {
                    Text(otherApps)
                        .font(Theme.body(14))
                        .foregroundStyle(Theme.text)
                        .multilineTextAlignment(.center)
                }

                if !sessions.isEmpty {
                    VStack(alignment: .leading, spacing: 8) {
                        Text("Who's watching")
                            .font(Theme.label(12))
                            .foregroundStyle(Theme.dim)
                        ForEach(Array(sessions.enumerated()), id: \.offset) { _, session in
                            let names = session.viewers.map(\.username).joined(separator: ", ")
                            Text("\(session.channelName)\(names.isEmpty ? "" : " — \(names)")")
                                .font(Theme.body(13))
                                .foregroundStyle(Theme.text)
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(12)
                    .background(Theme.surface)
                    .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
                }
            }

        case .failed(let message):
            panel {
                Text(message)
                    .font(Theme.body(15))
                    .foregroundStyle(Theme.alert)
                    .multilineTextAlignment(.center)
            }

        default:
            EmptyView()
        }
    }

    private func panel<Content: View>(@ViewBuilder content: () -> Content) -> some View {
        VStack(spacing: 14) {
            content()
            HStack(spacing: 12) {
                Button("Try Again") {
                    Task { await playerModel.retry() }
                }
                .keyboardShortcut(.defaultAction)
                Button("Back") {
                    onLeave()
                }
            }
            .controlSize(.large)
        }
        .padding(20)
        .background(Theme.bg.opacity(0.92))
        .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous)
                .stroke(Theme.line, lineWidth: 1)
        )
    }

    // MARK: - Playback wiring (as iOS PlayerView)

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

    private var sessionMeta: SessionInfoMeta? {
        if case .playing(let s) = playerModel.state {
            return s.session
        }
        return playerModel.lastSession?.session
    }

    private func loadPlayerIfNeeded() {
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
            switch playerModel.state {
            case .idle, .tunersBusy:
                bridge.replacePlayer(nil)
            default:
                // Starting: wait for create. Failed: keep the last frame.
                break
            }
            return
        }
        bridge.load(url: ServerURL.resolve(path: session.playlistUrl, against: serverURL))
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
        let ownsSession: Bool = {
            if case .playing = playerModel.state { return true }
            return playerModel.lastSession != nil
        }()
        guard ownsSession else { return }

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
                // Re-load the same playlist URL.
                if let session = playerModel.lastSession {
                    bridge.load(url: ServerURL.resolve(path: session.playlistUrl, against: serverURL))
                }
            }
        }
    }

    /// Show the chrome and restart its hide timer (mouse movement bumps it).
    private func bumpChrome() {
        if !showChrome {
            showChrome = true
        }
        hideChromeTask?.cancel()
        guard !isBlockingError else { return }
        hideChromeTask = Task { @MainActor in
            do {
                try await Task.sleep(for: Self.chromeHideDelay)
            } catch {
                return
            }
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
}

#if SHAREPLAY
// MARK: - SharePlay sheet

private struct SharingActivity: Identifiable {
    let id = UUID()
    let activity: WatchChannelActivity
}

/// The system sheet that starts a FaceTime call (or Messages) with the
/// activity, for sharing when not already on a call.
private struct GroupActivitySharingView: NSViewControllerRepresentable {
    let activity: WatchChannelActivity

    func makeNSViewController(context: Context) -> NSViewController {
        (try? GroupActivitySharingController(activity)) ?? NSViewController()
    }

    func updateNSViewController(_ nsViewController: NSViewController, context: Context) {}
}
#endif
