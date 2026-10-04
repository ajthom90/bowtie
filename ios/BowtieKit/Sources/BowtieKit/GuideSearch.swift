import Foundation
import Observation

/// One `GET /api/v1/guide/search` hit: a program that hasn't ended yet.
public struct GuideSearchResult: Codable, Equatable, Hashable, Identifiable, Sendable {
    public let channelId: Int64
    public let guideNumber: String
    public let channelName: String
    public let logoUrl: String
    public let start: Date
    public let stop: Date
    public let title: String
    public let subtitle: String
    /// Blank when the program is locked.
    public let description: String
    public let category: String
    /// e.g. "TV-14"; "" or nil = not rated.
    public let rating: String?
    /// Parental controls block it for the caller.
    public let locked: Bool?
    /// Present when it is scheduled or recorded.
    public let recording: RecordingMark?

    public init(
        channelId: Int64,
        guideNumber: String,
        channelName: String,
        logoUrl: String = "",
        start: Date,
        stop: Date,
        title: String,
        subtitle: String = "",
        description: String = "",
        category: String = "",
        rating: String? = nil,
        locked: Bool? = nil,
        recording: RecordingMark? = nil
    ) {
        self.channelId = channelId
        self.guideNumber = guideNumber
        self.channelName = channelName
        self.logoUrl = logoUrl
        self.start = start
        self.stop = stop
        self.title = title
        self.subtitle = subtitle
        self.description = description
        self.category = category
        self.rating = rating
        self.locked = locked
        self.recording = recording
    }

    /// A channel and its start time identify one airing.
    public var id: String { "\(channelId)@\(Int64(start.timeIntervalSince1970))" }

    public var isLocked: Bool { locked == true }

    /// On the air at `date` (start inclusive, stop exclusive).
    public func isOnNow(at date: Date) -> Bool {
        start <= date && date < stop
    }

    /// The channel to play for "Watch".
    public var channel: Channel {
        Channel(id: channelId, guideNumber: guideNumber, name: channelName, logoUrl: logoUrl)
    }

    /// The guide program, for "Record" / "Record Series" (looked up by its exact start).
    public var program: GuideProgram {
        GuideProgram(
            start: start,
            stop: stop,
            title: title,
            subtitle: subtitle,
            description: description,
            category: category,
            recording: recording,
            rating: rating,
            locked: locked
        )
    }
}

/// Search screen state: one query's results, newest query wins.
@Observable
@MainActor
public final class GuideSearchModel {
    public enum State: Equatable {
        /// Nothing typed yet.
        case idle
        case searching
        case results([GuideSearchResult])
        /// Nothing matched.
        case empty
        case failed(String)
    }

    public private(set) var state: State = .idle
    /// The last query searched (trimmed).
    public private(set) var query = ""

    /// Rows to show (empty unless `.results`).
    public var results: [GuideSearchResult] {
        if case .results(let rows) = state { return rows }
        return []
    }

    private let client: BowtieClient
    private let limit: Int
    /// Bumped per search so a slow answer to an old query can't land.
    private var generation: UInt64 = 0

    public init(client: BowtieClient, limit: Int = 50) {
        self.client = client
        self.limit = limit
    }

    /// Searches for `text` (trimmed). Blank text clears the results.
    public func search(_ text: String) async {
        generation &+= 1
        let gen = generation
        let q = text.trimmingCharacters(in: .whitespacesAndNewlines)
        query = q
        guard !q.isEmpty else {
            state = .idle
            return
        }
        // A refresh of the same query keeps its rows on screen.
        if case .results = state {} else {
            state = .searching
        }
        do {
            let hits = try await client.searchGuide(query: q, limit: limit)
            guard gen == generation else { return }
            state = hits.isEmpty ? .empty : .results(hits)
        } catch {
            guard gen == generation else { return }
            state = .failed(RecordingErrorCopy.message(for: error))
        }
    }

    /// Runs the last query again (e.g. after scheduling, to show its REC mark).
    public func refresh() async {
        await search(query)
    }
}
