import Foundation
import Observation

/// Loads the channel list and joins each row with guide now/next for a 4-hour window.
@Observable
@MainActor
public final class ChannelListModel {
    public struct Row: Equatable, Identifiable {
        public let channel: Channel
        public let nowNext: GuideLogic.NowNext

        public var id: Int64 { channel.id }

        public init(channel: Channel, nowNext: GuideLogic.NowNext) {
            self.channel = channel
            self.nowNext = nowNext
        }
    }

    public enum LoadState: Equatable {
        case loading
        case loaded([Row])
        case failed(String)
        case empty
    }

    public private(set) var state: LoadState = .loading

    private let client: BowtieClient
    private let now: () -> Date
    private let staleInterval: TimeInterval = 5 * 60
    private var lastLoadedAt: Date?

    /// Guide request window length: now … now+4h (matches design default).
    private let guideWindow: TimeInterval = 4 * 60 * 60

    public init(client: BowtieClient, now: @escaping () -> Date = Date.init) {
        self.client = client
        self.now = now
    }

    /// Fetches channels + guide(now..now+4h) and joins via `GuideLogic.nowNext`.
    public func load() async {
        state = .loading
        let at = now()
        let stop = at.addingTimeInterval(guideWindow)

        do {
            async let channelsTask = client.channels()
            async let guideTask = client.guide(start: at, stop: stop)
            let channels = try await channelsTask
            let guide = try await guideTask

            if channels.isEmpty {
                state = .empty
                lastLoadedAt = at
                return
            }

            let byId = Dictionary(uniqueKeysWithValues: guide.map { ($0.channelId, $0) })
            let rows: [Row] = channels.map { channel in
                let programs = byId[channel.id]?.programs ?? []
                return Row(
                    channel: channel,
                    nowNext: GuideLogic.nowNext(programs: programs, at: at)
                )
            }
            state = .loaded(rows)
            lastLoadedAt = at
        } catch {
            state = .failed(Self.message(for: error))
        }
    }

    /// Reloads when never loaded, or when the last successful load is ≥ 5 minutes old.
    /// Called on foreground and by a 5-minute timer while the list is visible.
    public func refreshIfStale() async {
        guard let lastLoadedAt else {
            await load()
            return
        }
        if now().timeIntervalSince(lastLoadedAt) >= staleInterval {
            await load()
        }
    }

    private static func message(for error: Error) -> String {
        guard let error = error as? BowtieError else {
            return error.localizedDescription
        }
        switch error {
        case .unauthorized:
            return "Unauthorized"
        case .tunersBusy:
            return "All tuners are in use"
        case .negotiationFailed(let message):
            return message
        case .notFound:
            return "Not found"
        case .server(_, let message):
            return message
        case .network(let message):
            return message
        case .invalidServerURL:
            return "Invalid server URL"
        }
    }
}
