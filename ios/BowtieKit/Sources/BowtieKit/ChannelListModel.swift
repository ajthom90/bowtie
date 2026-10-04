import Foundation
import Observation

/// Loads the channel list and joins each row with guide now/next for a 4-hour window.
/// Starred channels sort first; the user's recently watched channels load alongside.
@Observable
@MainActor
public final class ChannelListModel {
    public struct Row: Equatable, Identifiable {
        public let channel: Channel
        public let nowNext: GuideLogic.NowNext
        /// The channel's programs in the loaded guide window (category filters).
        public let programs: [GuideProgram]

        public var id: Int64 { channel.id }

        public init(channel: Channel, nowNext: GuideLogic.NowNext, programs: [GuideProgram] = []) {
            self.channel = channel
            self.nowNext = nowNext
            self.programs = programs
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
    /// Each channel's position in the server's list, so an unstarred channel
    /// returns to its original slot.
    private var serverOrder: [Int64: Int] = [:]

    /// Guide request window length: now … now+4h (matches design default).
    private let guideWindow: TimeInterval = 4 * 60 * 60

    public init(
        client: BowtieClient,
        now: @escaping () -> Date = Date.init,
        defaults: UserDefaults = .standard
    ) {
        self.client = client
        self.now = now
        self.defaults = defaults
        self.filter = GuideFilter.load(from: defaults)
    }

    // MARK: - Category filter

    private let defaults: UserDefaults

    /// The guide category chip; remembered on this device.
    public var filter: GuideFilter {
        didSet { filter.save(to: defaults) }
    }

    /// End of the loaded guide window (start is "now").
    public private(set) var windowEnd: Date?

    /// Loaded rows with something matching `filter` between `date` and the
    /// end of the loaded window. `.all` returns every row.
    public func filteredRows(at date: Date) -> [Row] {
        filter.visibleRows(rows, from: date, to: windowEnd(from: date))
    }

    /// How `row` reads under `filter` at `date` (dimmed lines, a later match).
    public func highlight(for row: Row, at date: Date) -> GuideFilter.RowHighlight {
        filter.highlight(nowNext: row.nowNext, programs: row.programs, from: date, to: windowEnd(from: date))
    }

    private func windowEnd(from date: Date) -> Date {
        windowEnd ?? date.addingTimeInterval(guideWindow)
    }

    /// Fetches channels + guide(now..now+4h) and joins via `GuideLogic.nowNext`.
    /// A reload keeps the current rows on screen until the new ones arrive.
    public func load() async {
        if case .loaded = state {} else {
            state = .loading
        }
        let at = now()
        let stop = at.addingTimeInterval(guideWindow)

        do {
            async let channelsTask = client.channels()
            async let guideTask = client.guide(start: at, stop: stop)
            async let recentsTask = Self.fetchRecents(client: client)
            let channels = try await channelsTask
            let guide = try await guideTask
            apply(await recentsTask)

            if channels.isEmpty {
                state = .empty
                lastLoadedAt = at
                return
            }

            serverOrder = Dictionary(
                channels.enumerated().map { ($0.element.id, $0.offset) },
                uniquingKeysWith: { first, _ in first }
            )
            let byId = Dictionary(guide.map { ($0.channelId, $0) }, uniquingKeysWith: { first, _ in first })
            let rows: [Row] = channels.map { channel in
                let programs = byId[channel.id]?.programs ?? []
                return Row(
                    channel: channel,
                    nowNext: GuideLogic.nowNext(programs: programs, at: at),
                    programs: programs
                )
            }
            windowEnd = stop
            state = .loaded(sorted(rows))
            lastLoadedAt = at
        } catch {
            state = .failed(Self.message(for: error))
        }
    }

    // MARK: - Favorites

    /// Loaded rows (favorites first), or empty while loading / failed / empty.
    public var rows: [Row] {
        if case .loaded(let rows) = state { return rows }
        return []
    }

    /// The server reports `favorite` on channels. Older servers omit it, so the
    /// views hide stars and the Recent row.
    public var supportsFavorites: Bool {
        rows.contains { $0.channel.favorite != nil }
    }

    /// Starred channels in guide-number order.
    public var favoriteRows: [Row] { rows.filter { $0.channel.isFavorite } }

    /// Everything else, in server order.
    public var otherRows: [Row] { rows.filter { !$0.channel.isFavorite } }

    /// Last favorite / recents action that failed, for a toast. Cleared by
    /// `dismissActionError()`.
    public private(set) var actionError: String?

    public func dismissActionError() {
        actionError = nil
    }

    /// Stars or unstars a channel. Optimistic: the list re-sorts at once and
    /// reverts if the server refuses.
    public func toggleFavorite(channelId: Int64) async {
        guard supportsFavorites,
              let channel = rows.first(where: { $0.channel.id == channelId })?.channel
        else {
            return
        }
        let previous = channel.favorite
        let on = !channel.isFavorite
        setLocalFavorite(channelId: channelId, to: on)

        do {
            try await client.setFavorite(channelId: channelId, on: on)
        } catch {
            // Only undo our own flip; a later toggle may have changed it since.
            if rows.first(where: { $0.channel.id == channelId })?.channel.favorite == on {
                setLocalFavorite(channelId: channelId, to: previous)
            }
            actionError = on
                ? "Couldn't add \(channel.name) to Favorites"
                : "Couldn't remove \(channel.name) from Favorites"
        }
    }

    /// Numeric guide-number order: "9.1" < "9.2" < "9.10" < "11.1".
    public static func guideNumberPrecedes(_ lhs: String, _ rhs: String) -> Bool {
        lhs.compare(rhs, options: [.numeric]) == .orderedAscending
    }

    private func setLocalFavorite(channelId: Int64, to favorite: Bool?) {
        let updated = rows.map { row -> Row in
            guard row.channel.id == channelId else { return row }
            var channel = row.channel
            channel.favorite = favorite
            return Row(channel: channel, nowNext: row.nowNext, programs: row.programs)
        }
        state = .loaded(sorted(updated))
    }

    /// Favorites in guide-number order, then the rest in server order.
    private func sorted(_ rows: [Row]) -> [Row] {
        let position = { (row: Row) in self.serverOrder[row.channel.id] ?? Int.max }
        let favorites = rows.filter { $0.channel.isFavorite }.sorted { a, b in
            if Self.guideNumberPrecedes(a.channel.guideNumber, b.channel.guideNumber) { return true }
            if Self.guideNumberPrecedes(b.channel.guideNumber, a.channel.guideNumber) { return false }
            return position(a) < position(b)
        }
        let others = rows.filter { !$0.channel.isFavorite }.sorted { position($0) < position($1) }
        return favorites + others
    }

    // MARK: - Recents

    /// How many recent channels the Recent row shows.
    public static let recentsLimit = 8

    /// Recently watched channels, newest first, as last fetched.
    public private(set) var recents: [RecentChannel] = []

    /// False once the server answered 404 for recents (older server).
    public private(set) var recentsSupported = true

    /// Recents as playable channels, resolved through the loaded rows when
    /// present (so they carry `favorite` / `reception`). Empty on older servers.
    public var recentChannels: [Channel] {
        guard supportsFavorites, recentsSupported else { return [] }
        let byId = Dictionary(rows.map { ($0.channel.id, $0.channel) }, uniquingKeysWith: { first, _ in first })
        return recents.map { byId[$0.channelId] ?? $0.channel }
    }

    /// Show the Recent row: supported and non-empty.
    public var showsRecents: Bool { !recentChannels.isEmpty }

    /// Re-fetches only the recents (e.g. after leaving the player).
    public func refreshRecents() async {
        guard recentsSupported else { return }
        apply(await Self.fetchRecents(client: client))
    }

    /// Clears the watch history. Optimistic; restores the row on failure.
    public func clearRecents() async {
        let previous = recents
        recents = []
        do {
            try await client.clearRecents()
        } catch {
            recents = previous
            actionError = "Couldn't clear Recent channels"
        }
    }

    private enum RecentsResult: Sendable {
        case loaded([RecentChannel])
        case unsupported
        case failed
    }

    private nonisolated static func fetchRecents(client: BowtieClient) async -> RecentsResult {
        do {
            return .loaded(try await client.recents(limit: recentsLimit))
        } catch BowtieError.notFound {
            return .unsupported
        } catch {
            return .failed
        }
    }

    private func apply(_ result: RecentsResult) {
        switch result {
        case .loaded(let items):
            recents = items
            recentsSupported = true
        case .unsupported:
            recents = []
            recentsSupported = false
        case .failed:
            // A transient error must not cost the channel list; keep what we had.
            break
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
        case .recordingConflict(_, _, let message):
            return message
        case .notFound:
            return "Not found"
        case .parental(let message):
            return message
        case .server(_, let message):
            return message
        case .network(let message):
            return message
        case .invalidServerURL:
            return "Invalid server URL"
        }
    }
}
