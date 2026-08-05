import SwiftUI
import AVKit
import AVFoundation
import BowtieKit

/// Full-screen tvOS player: native `AVPlayerViewController` transport with a
/// quality + stats tab panel via `customInfoViewControllers`.
struct TVPlayerView: View {
    let channel: Channel
    let serverURL: URL
    let maxQuality: String
    var nowTitle: String?
    @Bindable var playerModel: PlayerModel

    @Environment(\.dismiss) private var dismiss

    @State private var bridge = TVPlayerBridge()
    @State private var isStopping = false
    @State private var stallRetryTask: Task<Void, Never>?
    @State private var stallAttempt = 0
    @State private var indicatedBitrate: Double?
    @State private var droppedFrames: Int?
    @State private var statsPollTask: Task<Void, Never>?

    private static let stallBackoffs: [Duration] = [
        .seconds(1), .seconds(2), .seconds(4),
    ]

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()

            TVPlayerContainer(
                bridge: bridge,
                maxQuality: maxQuality,
                selectedProfile: playerModel.selectedProfile,
                sessionMeta: sessionMetaOptional,
                indicatedBitrate: indicatedBitrate,
                droppedFrames: droppedFrames,
                onSelectProfile: { profile in
                    Task { await playerModel.setProfile(profile) }
                }
            )
            .ignoresSafeArea()

            if case .stalled = playerModel.state {
                stalledSpinner
            } else if case .starting = playerModel.state {
                stalledSpinner
            }

            if isBlockingError {
                errorPanel
                    .padding(60)
            }
        }
        .navigationBarBackButtonHidden(isStopping)
        .onAppear {
            startStatsPolling()
        }
        .onDisappear {
            statsPollTask?.cancel()
            stallRetryTask?.cancel()
            Task { await playerModel.stop() }
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
        .task(id: sessionIdentity) {
            await loadPlayerIfNeeded()
        }
    }

    // MARK: - Overlay chrome

    private var stalledSpinner: some View {
        VStack(spacing: 16) {
            ProgressView()
                .tint(Theme.amber)
                .scaleEffect(1.4)
            Text(playerModel.state == .stalled ? "Reconnecting…" : "Starting…")
                .font(Theme.body(22))
                .foregroundStyle(Theme.dim)
        }
        .padding(28)
        .background(Theme.bg.opacity(0.75))
        .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
        .accessibilityLabel(playerModel.state == .stalled ? "Reconnecting" : "Starting")
    }

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
        case .tunersBusy(let sessions):
            VStack(spacing: 20) {
                Text("All tuners are in use")
                    .font(Theme.title(28))
                    .foregroundStyle(Theme.alert)
                    .multilineTextAlignment(.center)

                if !sessions.isEmpty {
                    VStack(alignment: .leading, spacing: 10) {
                        Text("Who's watching")
                            .font(Theme.label(18))
                            .foregroundStyle(Theme.dim)
                        ForEach(Array(sessions.enumerated()), id: \.offset) { _, session in
                            let names = session.viewers.map(\.username).joined(separator: ", ")
                            Text("\(session.channelName)\(names.isEmpty ? "" : " — \(names)")")
                                .font(Theme.body(20))
                                .foregroundStyle(Theme.text)
                        }
                    }
                    .frame(maxWidth: 640, alignment: .leading)
                    .padding(16)
                    .background(Theme.surface)
                    .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
                }

                errorActions
            }
            .padding(32)
            .background(Theme.bg.opacity(0.94))
            .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous)
                    .stroke(Theme.line, lineWidth: 1)
            )
            .focusSection()

        case .failed(let message):
            VStack(spacing: 20) {
                Text(message)
                    .font(Theme.body(24))
                    .foregroundStyle(Theme.alert)
                    .multilineTextAlignment(.center)
                errorActions
            }
            .padding(32)
            .background(Theme.bg.opacity(0.94))
            .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous)
                    .stroke(Theme.line, lineWidth: 1)
            )
            .focusSection()

        default:
            EmptyView()
        }
    }

    private var errorActions: some View {
        HStack(spacing: 24) {
            Button {
                Task { await playerModel.retry() }
            } label: {
                Text("Try again")
                    .font(Theme.label(22))
            }
            .accessibilityLabel("Try again")

            Button {
                Task { await leave() }
            } label: {
                Text("Back")
                    .font(Theme.label(22))
            }
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
            } else if case .tunersBusy = playerModel.state {
                bridge.replacePlayer(nil)
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
        case .starting:
            stallRetryTask?.cancel()
        case .stalled:
            break
        case .failed, .tunersBusy:
            stallRetryTask?.cancel()
            bridge.replacePlayer(nil)
        case .idle:
            stallRetryTask?.cancel()
            bridge.replacePlayer(nil)
        }
    }

    private func beginStallRecovery() {
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
                if let session = playerModel.lastSession {
                    let url = ServerURL.resolve(path: session.playlistUrl, against: serverURL)
                    bridge.load(url: url)
                }
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

    @MainActor
    private func leave() async {
        guard !isStopping else { return }
        isStopping = true
        stallRetryTask?.cancel()
        await playerModel.stop()
        dismiss()
    }
}

// MARK: - Bridge (player ownership + error / stall flags)

@MainActor
@Observable
final class TVPlayerBridge {
    var player: AVPlayer?
    var playerErrorIsForbidden = false
    var playerDidStall = false
    var playerDidRecover = false

    private var itemStatusObs: NSKeyValueObservation?
    private var itemKeepUpObs: NSKeyValueObservation?
    private var itemEmptyObs: NSKeyValueObservation?
    private var timeControlObs: NSKeyValueObservation?
    private var failedObserver: NSObjectProtocol?

    func load(url: URL) {
        let item = AVPlayerItem(url: url)
        if let player {
            player.replaceCurrentItem(with: item)
        } else {
            let p = AVPlayer(playerItem: item)
            player = p
        }
        observe(item: item)
        player?.play()
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
        if let player {
            timeControlObs = player.observe(\.timeControlStatus, options: [.new]) { [weak self] player, _ in
                Task { @MainActor in
                    self?.handleTimeControl(player)
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

    private func handleItemStatus(_ item: AVPlayerItem) {
        switch item.status {
        case .failed:
            handleFailure(item.error)
        case .readyToPlay:
            playerDidRecover = true
        case .unknown:
            break
        @unknown default:
            break
        }
    }

    private func handleBufferState(_ item: AVPlayerItem) {
        if item.isPlaybackBufferEmpty && !item.isPlaybackLikelyToKeepUp {
            if player?.timeControlStatus == .waitingToPlayAtSpecifiedRate {
                playerDidStall = true
            }
        } else if item.isPlaybackLikelyToKeepUp {
            playerDidRecover = true
        }
    }

    private func handleTimeControl(_ player: AVPlayer) {
        if player.timeControlStatus == .waitingToPlayAtSpecifiedRate {
            if player.reasonForWaitingToPlay == .toMinimizeStalls {
                playerDidStall = true
            }
        } else if player.timeControlStatus == .playing {
            playerDidRecover = true
        }
    }

    private func handleFailure(_ error: Error?) {
        if Self.isForbidden(error) {
            playerErrorIsForbidden = true
        } else {
            playerDidStall = true
        }
    }

    static func isForbidden(_ error: Error?) -> Bool {
        var current: NSError? = error as NSError?
        while let err = current {
            if err.domain == NSURLErrorDomain && err.code == NSURLErrorUserAuthenticationRequired {
                return true
            }
            for key in ["HTTPStatusCode", "statusCode", "httpStatus"] {
                if let status = err.userInfo[key] as? Int, status == 403 {
                    return true
                }
                if let status = err.userInfo[key] as? NSNumber, status.intValue == 403 {
                    return true
                }
            }
            if err.localizedDescription.contains("403") {
                return true
            }
            current = err.userInfo[NSUnderlyingErrorKey] as? NSError
        }
        return false
    }

    private func tearDownObservers() {
        itemStatusObs?.invalidate()
        itemKeepUpObs?.invalidate()
        itemEmptyObs?.invalidate()
        timeControlObs?.invalidate()
        itemStatusObs = nil
        itemKeepUpObs = nil
        itemEmptyObs = nil
        timeControlObs = nil
        if let failedObserver {
            NotificationCenter.default.removeObserver(failedObserver)
            self.failedObserver = nil
        }
    }
}

// MARK: - AVPlayerViewController + custom info panels

private struct TVPlayerContainer: UIViewControllerRepresentable {
    var bridge: TVPlayerBridge
    var maxQuality: String
    var selectedProfile: String
    var sessionMeta: SessionInfoMeta?
    var indicatedBitrate: Double?
    var droppedFrames: Int?
    var onSelectProfile: (String) -> Void

    func makeUIViewController(context: Context) -> AVPlayerViewController {
        let vc = AVPlayerViewController()
        vc.showsPlaybackControls = true
        vc.player = bridge.player
        context.coordinator.installInfoPanels(on: vc)
        return vc
    }

    func updateUIViewController(_ vc: AVPlayerViewController, context: Context) {
        if vc.player !== bridge.player {
            vc.player = bridge.player
        }
        context.coordinator.maxQuality = maxQuality
        context.coordinator.selectedProfile = selectedProfile
        context.coordinator.onSelectProfile = onSelectProfile
        context.coordinator.refreshQualityPanel()
        context.coordinator.refreshStatsPanel(
            meta: sessionMeta,
            indicatedBitrate: indicatedBitrate,
            droppedFrames: droppedFrames
        )
    }

    func makeCoordinator() -> Coordinator {
        Coordinator(
            maxQuality: maxQuality,
            selectedProfile: selectedProfile,
            onSelectProfile: onSelectProfile
        )
    }

    @MainActor
    final class Coordinator {
        var maxQuality: String
        var selectedProfile: String
        var onSelectProfile: (String) -> Void

        private var qualityHost: UIHostingController<TVQualityPanel>?
        private var statsHost: UIHostingController<TVStatsPanel>?

        init(
            maxQuality: String,
            selectedProfile: String,
            onSelectProfile: @escaping (String) -> Void
        ) {
            self.maxQuality = maxQuality
            self.selectedProfile = selectedProfile
            self.onSelectProfile = onSelectProfile
        }

        func installInfoPanels(on vc: AVPlayerViewController) {
            let quality = UIHostingController(
                rootView: TVQualityPanel(
                    maxQuality: maxQuality,
                    selectedProfile: selectedProfile,
                    onSelect: { [weak self] profile in
                        self?.onSelectProfile(profile)
                    }
                )
            )
            quality.title = "Quality"

            let stats = UIHostingController(
                rootView: TVStatsPanel(
                    meta: nil,
                    indicatedBitrate: nil,
                    droppedFrames: nil
                )
            )
            stats.title = "Stats"

            qualityHost = quality
            statsHost = stats
            vc.customInfoViewControllers = [quality, stats]
        }

        func refreshQualityPanel() {
            qualityHost?.rootView = TVQualityPanel(
                maxQuality: maxQuality,
                selectedProfile: selectedProfile,
                onSelect: { [weak self] profile in
                    self?.onSelectProfile(profile)
                }
            )
        }

        func refreshStatsPanel(
            meta: SessionInfoMeta?,
            indicatedBitrate: Double?,
            droppedFrames: Int?
        ) {
            statsHost?.rootView = TVStatsPanel(
                meta: meta,
                indicatedBitrate: indicatedBitrate,
                droppedFrames: droppedFrames
            )
        }
    }
}

// MARK: - Info panel content

/// Quality picker hosted inside `AVPlayerViewController.customInfoViewControllers`.
private struct TVQualityPanel: View {
    let maxQuality: String
    let selectedProfile: String
    let onSelect: (String) -> Void

    var body: some View {
        List {
            Button {
                onSelect("")
            } label: {
                qualityRow(title: "Auto", selected: selectedProfile.isEmpty)
            }

            ForEach(GuideLogic.allowedProfiles(maxQuality: maxQuality), id: \.self) { profile in
                Button {
                    onSelect(profile)
                } label: {
                    qualityRow(
                        title: profile.capitalized,
                        selected: selectedProfile == profile
                    )
                }
            }
        }
        .listStyle(.grouped)
        .focusSection()
    }

    private func qualityRow(title: String, selected: Bool) -> some View {
        HStack {
            Text(title)
                .font(Theme.label(28))
                .foregroundStyle(Theme.text)
            Spacer()
            if selected {
                Image(systemName: "checkmark")
                    .foregroundStyle(Theme.amber)
            }
        }
    }
}

/// Stats-for-nerds hosted as an info tab (reuses `StatsOverlay` layout tokens).
private struct TVStatsPanel: View {
    let meta: SessionInfoMeta?
    let indicatedBitrate: Double?
    let droppedFrames: Int?

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            Text("Playback stats")
                .font(Theme.title(28))
                .foregroundStyle(Theme.text)

            StatsOverlay(
                meta: meta,
                indicatedBitrate: indicatedBitrate,
                droppedFrames: droppedFrames
            )
            .scaleEffect(1.35, anchor: .topLeading)

            Spacer(minLength: 0)
        }
        .padding(40)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(Theme.bg)
        .focusSection()
    }
}

#Preview {
    NavigationStack {
        TVPlayerView(
            channel: Channel(id: 1, guideNumber: "7.1", name: "Local News", logoUrl: ""),
            serverURL: URL(string: "http://127.0.0.1:8400")!,
            maxQuality: "high",
            nowTitle: "Evening Report",
            playerModel: PlayerModel(
                client: BowtieClient(
                    server: URL(string: "http://127.0.0.1:8400")!,
                    store: InMemorySessionStore()
                ),
                caps: Caps.make(maxHeight: 2160)
            )
        )
    }
}
