import Foundation
import Observation
import BowtieKit

/// Session-replace playback state machine shared by iOS and tvOS players.
///
/// CONTRACT:
/// - Every `createSession` sends `effectiveCaps` = base `caps` with
///   `profile = selectedProfile` (`""` = Auto).
/// - On 422: reset `selectedProfile` to `""` and retry ONCE; a second 422
///   → `.failed` with the device-can't-play copy.
/// - On 404: bump `channelsStaleGeneration` so the channel list reloads.
/// - `stop` is for real leave only (dismissal, sign-out, change-server,
///   termination) — never background / PiP.
/// - Stall UX: player UI calls `markStalled` → spinner; retries AVPlayer with
///   backoff (1s, 2s, 4s ×3); `resumePlaying` on recovery or `stallFailed` after.
/// - Heartbeats (spec C / A6): 15s while session is open (playing OR stalled);
///   stream-token auth; stop only on real leave. Each beat brings the antenna
///   reading (`signal`) for the weak-signal note and the stats panel.
/// - Viewer-facing failure text is plain words (`ViewerErrorCopy`); the
///   technical cause goes to the log.
@Observable
@MainActor
public final class PlayerModel {
    public enum State: Equatable {
        case idle
        case starting
        case playing(CreatedSession)
        case stalled
        case failed(String)
        case tunersBusy([ActiveSessionSummary], otherInUse: Int)

        public static func == (lhs: State, rhs: State) -> Bool {
            switch (lhs, rhs) {
            case (.idle, .idle), (.starting, .starting), (.stalled, .stalled):
                return true
            case let (.playing(a), .playing(b)):
                return a.viewerId == b.viewerId
                    && a.playlistUrl == b.playlistUrl
                    && a.session == b.session
            case let (.failed(a), .failed(b)):
                return a == b
            case let (.tunersBusy(a, ao), .tunersBusy(b, bo)):
                return a == b && ao == bo
            default:
                return false
            }
        }
    }

    /// Double-422 negotiation failure (Auto was tried too).
    public static let deviceCantPlayMessage =
        "This channel can't play on this device."

    /// Surface after a second mid-play 403 without a successful silent replace.
    public static let playbackAuthFailedMessage = ViewerErrorCopy.streamStopped

    /// Surface after stall retries (1s, 2s, 4s) are exhausted.
    public static let stallFailedMessage = ViewerErrorCopy.streamStopped

    /// Create-session 404: the channel was disabled or removed.
    public static let channelGoneMessage =
        "This channel isn't available anymore."

    /// Out-of-window clamp notice (spec B) — exact copy.
    public static let outOfWindowNotice =
        "Jumped to live — paused longer than the buffer"

    /// Client heartbeat interval (spec C).
    nonisolated public static let heartbeatInterval: Duration = .seconds(15)

    public private(set) var state: State = .idle
    public private(set) var currentChannel: Channel?
    /// `""` = Auto.
    public var selectedProfile: String = ""

    /// Last successfully created session. Kept through `.stalled` for retry/stats.
    public private(set) var lastSession: CreatedSession?

    /// Monotonic token bumped on create-session 404 so ChannelList reloads.
    public private(set) var channelsStaleGeneration: UInt64 = 0

    /// Latest antenna reading from the heartbeat; nil while unknown. A beat
    /// that gets no answer keeps the last reading, so the note doesn't flash.
    public private(set) var signal: SessionSignal?

    /// The live session is open (playing, or reconnecting after a stall).
    private var isSessionOpen: Bool {
        switch state {
        case .playing, .stalled:
            return true
        default:
            return false
        }
    }

    /// Show "Weak signal (46%) — …" over live TV. The server already needs two
    /// bad readings in a row before it says weak.
    public var showsWeakSignalNote: Bool { weakSignalNote != nil }

    /// The weak-signal note's text, or nil when it should be hidden.
    public var weakSignalNote: String? {
        guard isSessionOpen, let signal, signal.weak else { return nil }
        return signal.weakNote
    }

    /// "Signal quality 46% · strength 96% · error-free 0%" for the stats
    /// panel; nil while the reading is unknown.
    public var signalStatsLine: String? {
        guard isSessionOpen else { return nil }
        return signal?.statsLine
    }

    private let client: BowtieClient
    private let caps: ClientCaps
    private let debounce: Duration
    private let clock: any Clock<Duration>
    private let heartbeatInterval: Duration

    private var replaceTask: Task<Void, Never>?
    private var heartbeatTask: Task<Void, Never>?
    private var activeViewerId: String?
    /// Generation token so cancelled replace tasks never clobber newer state.
    private var generation: UInt64 = 0
    /// Mid-play 403: one silent replace, then fail.
    private var authFailureRetried = false

    public init(
        client: BowtieClient,
        caps: ClientCaps,
        debounce: Duration = .milliseconds(400),
        clock: any Clock<Duration> = ContinuousClock(),
        heartbeatInterval: Duration = PlayerModel.heartbeatInterval
    ) {
        self.client = client
        self.caps = caps
        self.debounce = debounce
        self.clock = clock
        self.heartbeatInterval = heartbeatInterval
    }

    /// Caps sent on every create: base device caps + current profile selection.
    public var effectiveCaps: ClientCaps {
        var copy = caps
        copy.profile = selectedProfile
        return copy
    }

    // MARK: - Public API

    /// Session-replace play: cancel in-flight create, DELETE old, debounce, POST new.
    ///
    /// SharePlay: a participant picking another channel doesn't zap — it
    /// parks the channel in `pendingGroupZap` for the player to confirm
    /// leaving the group. A sharer's zap moves the group along.
    public func play(channel: Channel) async {
        if groupRole == .participant {
            guard channel.id == currentChannel?.id else {
                pendingGroupZap = channel
                return
            }
        } else {
            joinSessionId = nil
        }
        currentChannel = channel
        authFailureRetried = false
        await scheduleReplace()
    }

    /// Quality change: same replace machine, keeps the current channel.
    public func setProfile(_ p: String) async {
        selectedProfile = p
        guard currentChannel != nil else { return }
        authFailureRetried = false
        await scheduleReplace()
    }

    /// Real leave only: DELETE active session and return to idle.
    public func stop() async {
        leaveGroup()
        groupPresentation = nil
        sharedSessionId = nil
        replaceTask?.cancel()
        replaceTask = nil
        stopHeartbeat()
        generation &+= 1

        let viewerId = activeViewerId
        activeViewerId = nil
        currentChannel = nil
        lastSession = nil
        authFailureRetried = false

        if let viewerId {
            await client.deleteSession(viewerId: viewerId)
        }
        state = .idle
    }

    /// Mid-play playlist/segment 403: one silent session replace, then `.failed`.
    public func playbackAuthFailed() async {
        guard case .playing = state else { return }
        if authFailureRetried {
            state = .failed(Self.playbackAuthFailedMessage)
            return
        }
        authFailureRetried = true
        await scheduleReplace()
    }

    /// Player detected underrun / network stall while a session is live.
    public func markStalled() {
        switch state {
        case .playing(let session):
            lastSession = session
            state = .stalled
        case .stalled:
            break
        default:
            break
        }
    }

    /// AVPlayer recovered after a stall (or a successful bounded retry).
    public func resumePlaying() {
        guard case .stalled = state, let session = lastSession else { return }
        state = .playing(session)
    }

    /// Stall retries exhausted → failed with retryable copy.
    public func stallFailed(_ message: String? = nil) {
        state = .failed(message ?? Self.stallFailedMessage)
    }

    /// Retry after tuners-busy or other recoverable failure (same channel).
    public func retry() async {
        guard currentChannel != nil else { return }
        authFailureRetried = false
        await scheduleReplace()
    }

    // MARK: - SharePlay

    public enum GroupRole: Equatable {
        /// This device shared the channel; its zaps move the group.
        case sharer
        /// Joined someone else's share; follows the sharer.
        case participant
    }

    public private(set) var groupRole: GroupRole?
    /// A participant chose another channel; the player asks before leaving the group.
    public private(set) var pendingGroupZap: Channel?
    /// The group picked a channel to watch: the channel list opens the player.
    private(set) var groupPresentation: GroupPresentation?
    private(set) var group: (any WatchGroup)?
    /// Sent as `joinSessionId` on every create while following a group.
    private var joinSessionId: String?
    /// Server session this device offered to share (marks its own group as ours).
    private var sharedSessionId: String?

    /// The activity for sharing what's playing, or nil when the server can't
    /// do SharePlay (no `session.id` or `serverId` — an older server).
    func makeWatchActivity() async -> WatchChannelActivity? {
        guard let channel = currentChannel,
              let sessionId = lastSession?.session?.id,
              !sessionId.isEmpty,
              let version = try? await client.version(),
              let serverId = version.serverId,
              !serverId.isEmpty,
              currentChannel?.id == channel.id
        else {
            return nil
        }
        sharedSessionId = sessionId
        return WatchChannelActivity(
            serverId: serverId,
            serverName: version.serverName ?? "",
            channelId: channel.id,
            channelName: channel.name,
            sessionId: sessionId
        )
    }

    /// A SharePlay session the app decided to join. Our own share: join and
    /// keep playing. Someone else's: join and play their session.
    func receiveGroup(_ newGroup: any WatchGroup) async {
        if let group, group !== newGroup {
            group.leave()
        }
        group = newGroup
        pendingGroupZap = nil
        newGroup.onActivityChange = { [weak self, weak newGroup] activity in
            guard let self, let newGroup, self.group === newGroup, self.groupRole == .participant else {
                return
            }
            Task { await self.follow(activity) }
        }
        newGroup.onEnd = { [weak self, weak newGroup] in
            guard let self, let newGroup, self.group === newGroup else { return }
            self.dropGroup()
        }

        let activity = newGroup.activity
        if let sharedSessionId, activity.sessionId == sharedSessionId {
            groupRole = .sharer
            newGroup.join()
            return
        }
        groupRole = .participant
        newGroup.join()
        await follow(activity)
    }

    /// Leave the group and play the channel the participant picked.
    /// Pass the channel when the confirmation UI may already have cleared it.
    func confirmGroupZap(_ channel: Channel? = nil) async {
        guard let channel = channel ?? pendingGroupZap else { return }
        leaveGroup()
        await play(channel: channel)
    }

    func cancelGroupZap() {
        pendingGroupZap = nil
    }

    func groupPresentationShown() {
        groupPresentation = nil
    }

    /// Leave the SharePlay group (if any); playback continues.
    func leaveGroup() {
        group?.leave()
        dropGroup()
    }

    private func dropGroup() {
        group = nil
        groupRole = nil
        joinSessionId = nil
        pendingGroupZap = nil
    }

    /// Participant: play the group's channel on the sharer's server session.
    private func follow(_ activity: WatchChannelActivity) async {
        if currentChannel?.id == activity.channelId,
           joinSessionId == activity.sessionId,
           lastSession?.session?.id == activity.sessionId {
            return
        }
        let channel = currentChannel?.id == activity.channelId
            ? currentChannel!
            : Channel(id: activity.channelId, guideNumber: "", name: activity.channelName, logoUrl: "")
        joinSessionId = activity.sessionId
        pendingGroupZap = nil
        currentChannel = channel
        groupPresentation = GroupPresentation(channel: channel)
        authFailureRetried = false
        await scheduleReplace()

        // The activity carries only id and name; fill in number and logo.
        if channel.guideNumber.isEmpty,
           let full = try? await client.channels().first(where: { $0.id == activity.channelId }),
           currentChannel?.id == full.id {
            currentChannel = full
        }
    }

    /// Sharer: a new session (zap or quality change) moves the group to it.
    private func moveGroupIfSharing(channel: Channel, session: CreatedSession) {
        guard groupRole == .sharer, let group, let sessionId = session.session?.id, !sessionId.isEmpty else {
            return
        }
        let activity = group.activity
        guard activity.sessionId != sessionId || activity.channelId != channel.id else { return }
        sharedSessionId = sessionId
        group.update(activity.following(channelId: channel.id, channelName: channel.name, sessionId: sessionId))
    }

    // MARK: - Replace machine

    private func scheduleReplace() async {
        replaceTask?.cancel()
        generation &+= 1
        let gen = generation
        let task = Task { @MainActor in
            await self.performReplace(generation: gen)
        }
        replaceTask = task
        await task.value
    }

    private func performReplace(generation gen: UInt64) async {
        guard let channel = currentChannel else { return }

        let oldViewerId = activeViewerId
        activeViewerId = nil
        stopHeartbeat()
        state = .starting

        if let oldViewerId {
            await client.deleteSession(viewerId: oldViewerId)
        }

        guard isCurrent(gen) else { return }

        do {
            try await clock.sleep(for: debounce)
        } catch {
            // Cancelled during debounce — a newer replace owns the machine.
            return
        }

        guard isCurrent(gen) else { return }

        await createSession(channel: channel, generation: gen, isRetry: false)
    }

    private func createSession(channel: Channel, generation gen: UInt64, isRetry: Bool) async {
        do {
            let session = try await client.createSession(
                channelId: channel.id,
                caps: effectiveCaps,
                joinSessionId: joinSessionId
            )
            guard isCurrent(gen) else {
                // Orphaned success — tear down so we don't leak a tuner.
                await client.deleteSession(viewerId: session.viewerId)
                return
            }
            activeViewerId = session.viewerId
            lastSession = session
            state = .playing(session)
            moveGroupIfSharing(channel: channel, session: session)
            startHeartbeat(viewerId: session.viewerId, playlistUrl: session.playlistUrl)
        } catch let error as BowtieError {
            guard isCurrent(gen) else { return }
            await handleCreateError(
                error,
                channel: channel,
                generation: gen,
                isRetry: isRetry
            )
        } catch {
            guard isCurrent(gen) else { return }
            state = .failed(ViewerErrorCopy.message(for: error))
        }
    }

    // MARK: - Heartbeats (A6: session-open, continues through stalled)

    private func startHeartbeat(viewerId: String, playlistUrl: String) {
        stopHeartbeat()
        // Interval of zero disables beats (test harness default) so ManualClock
        // debounce waiters are not confused with heartbeat sleeps.
        guard heartbeatInterval > .zero else { return }
        guard let token = Self.streamToken(from: playlistUrl) else { return }
        let interval = heartbeatInterval
        heartbeatTask = Task { @MainActor in
            while !Task.isCancelled {
                do {
                    try await self.clock.sleep(for: interval)
                } catch {
                    return
                }
                guard !Task.isCancelled else { return }
                // A6: keyed on session open — continue while this viewer is still active
                // (playing or stalled). Stop only when replaced or stop() clears it.
                guard self.activeViewerId == viewerId else { return }
                let reply = await self.client.heartbeat(viewerId: viewerId, token: token)
                guard self.activeViewerId == viewerId, !Task.isCancelled else { return }
                switch reply {
                case .success(let signal):
                    self.signal = signal
                case .failure(.parental(let message)):
                    self.parentalStop(viewerId: viewerId, message: message)
                    return
                case .failure:
                    // No new reading: keep the last one.
                    break
                }
            }
        }
    }

    /// The server stopped this viewer for parental controls (e.g. the next
    /// program is rated above the limit): end playback and say why.
    private func parentalStop(viewerId: String, message: String) {
        guard activeViewerId == viewerId else { return }
        activeViewerId = nil
        heartbeatTask = nil
        signal = nil
        state = .failed(message)
    }

    /// Also forgets the reading: it belonged to the session that's ending.
    private func stopHeartbeat() {
        heartbeatTask?.cancel()
        heartbeatTask = nil
        signal = nil
    }

    /// Extract `token` query from a playlist path/URL (relative or absolute).
    public static func streamToken(from playlistUrl: String) -> String? {
        if let items = URLComponents(string: playlistUrl)?.queryItems {
            return items.first(where: { $0.name == "token" })?.value
        }
        // Relative paths without a scheme: prefix a dummy base.
        if let items = URLComponents(string: "http://d.invalid\(playlistUrl.hasPrefix("/") ? "" : "/")\(playlistUrl)")?.queryItems {
            return items.first(where: { $0.name == "token" })?.value
        }
        return nil
    }

    private func handleCreateError(
        _ error: BowtieError,
        channel: Channel,
        generation gen: UInt64,
        isRetry: Bool
    ) async {
        switch error {
        case .negotiationFailed:
            // 422: force Auto and retry once; second 422 → device-can't-play.
            selectedProfile = ""
            if !isRetry {
                await createSession(channel: channel, generation: gen, isRetry: true)
            } else {
                state = .failed(Self.deviceCantPlayMessage)
            }

        case .tunersBusy(let sessions, let otherInUse):
            state = .tunersBusy(sessions, otherInUse: otherInUse)

        case .notFound:
            // 404: channel unknown/disabled — signal list reload.
            channelsStaleGeneration &+= 1
            state = .failed(Self.channelGoneMessage)

        case .unauthorized, .parental, .server, .recordingConflict, .network, .badResponse, .invalidServerURL:
            // Server messages (e.g. parental: what's blocked) pass through;
            // transport and decoding detail never reaches the viewer.
            state = .failed(ViewerErrorCopy.message(for: error))
        }
    }

    private func isCurrent(_ gen: UInt64) -> Bool {
        !Task.isCancelled && gen == generation
    }
}
