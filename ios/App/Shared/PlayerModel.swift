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
///   stream-token auth; stop only on real leave.
@Observable
@MainActor
public final class PlayerModel {
    public enum State: Equatable {
        case idle
        case starting
        case playing(CreatedSession)
        case stalled
        case failed(String)
        case tunersBusy([ActiveSessionSummary])

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
            case let (.tunersBusy(a), .tunersBusy(b)):
                return a == b
            default:
                return false
            }
        }
    }

    /// Spec-mandated copy for double-422 negotiation failure.
    public static let deviceCantPlayMessage =
        "This device can't play this channel at that quality"

    /// Surface after a second mid-play 403 without a successful silent replace.
    public static let playbackAuthFailedMessage =
        "Playback authorization failed"

    /// Surface after stall retries (1s, 2s, 4s) are exhausted.
    public static let stallFailedMessage =
        "Playback stalled"

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
    public func play(channel: Channel) async {
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
                caps: effectiveCaps
            )
            guard isCurrent(gen) else {
                // Orphaned success — tear down so we don't leak a tuner.
                await client.deleteSession(viewerId: session.viewerId)
                return
            }
            activeViewerId = session.viewerId
            lastSession = session
            state = .playing(session)
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
            state = .failed(error.localizedDescription)
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
                await self.client.heartbeat(viewerId: viewerId, token: token)
            }
        }
    }

    private func stopHeartbeat() {
        heartbeatTask?.cancel()
        heartbeatTask = nil
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

        case .tunersBusy(let sessions):
            state = .tunersBusy(sessions)

        case .notFound:
            // 404: channel unknown/disabled — signal list reload.
            channelsStaleGeneration &+= 1
            state = .failed("Channel not found")

        case .unauthorized:
            state = .failed("Signed out")

        case .server(_, let message):
            state = .failed(message)

        case .network(let message):
            state = .failed(message)

        case .invalidServerURL:
            state = .failed("Invalid server")
        }
    }

    private func isCurrent(_ gen: UInt64) -> Bool {
        !Task.isCancelled && gen == generation
    }
}
