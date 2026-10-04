import Foundation

/// A coarse program category a guide filter chip selects.
public enum GuideBucket: String, CaseIterable, Sendable {
    case sports, movies, news, kids, new
}

/// Guide category chips: All · Sports · Movies · News · Kids · New.
///
/// The guide carries one raw category string per program (XMLTV `<category>`,
/// Schedules Direct genre, SiliconDust free guide). These rules map it to
/// buckets; the same rules and test vectors live in the web
/// (`guideFilterModel.ts`) and Android `:core` (`GuideFilter.kt`).
public enum GuideFilter: String, CaseIterable, Identifiable, Sendable {
    case all, sports, movies, news, kids, new

    public var id: String { rawValue }

    public var bucket: GuideBucket? { GuideBucket(rawValue: rawValue) }

    public var label: String {
        switch self {
        case .all: "All"
        case .sports: "Sports"
        case .movies: "Movies"
        case .news: "News"
        case .kids: "Kids"
        case .new: "New"
        }
    }

    /// "No sports on in this time window" (empty for `.all`).
    public var emptyCopy: String {
        let noun: String
        switch self {
        case .all: return ""
        case .sports: noun = "sports"
        case .movies: noun = "movies"
        case .news: noun = "news"
        case .kids: noun = "kids' shows"
        case .new: noun = "new episodes"
        }
        return "No \(noun) on in this time window"
    }

    // MARK: - Mapping

    /// Sport words / phrases matched as whole words ("Sports talk", "College football").
    private static let sportsPhrases = [
        "sport", "sports", "motorsport", "motorsports", "esports",
        "football", "basketball", "baseball", "soccer", "hockey", "golf", "tennis",
        "boxing", "wrestling", "racing", "volleyball", "softball", "lacrosse", "rugby",
        "cricket", "bowling", "skiing", "snowboarding", "skating", "gymnastics",
        "swimming", "cycling", "track field", "athletics", "olympics",
        "mixed martial arts", "mma", "rodeo", "curling", "billiards", "darts",
        "surfing", "triathlon", "polo", "handball", "badminton", "equestrian",
    ]

    /// The whole piece must be one of these ("Movie review" is not a movie).
    private static let moviePieces: Set<String> = [
        "movie", "movies", "film", "films", "feature film", "tv movie", "made for tv movie",
        "motion picture",
    ]

    private static let newsPhrases = ["news", "newsmagazine", "newscast", "weather"]

    /// "Family" is deliberately absent: family sitcoms and dramas are
    /// general-audience prime time, not children's programming.
    private static let kidsPhrases = [
        "children", "childrens", "kids", "animated", "animation", "cartoon", "cartoons",
        "educational", "preschool",
    ]

    /// Ratings that keep a program out of Kids (adult animation); letters/digits only.
    private static let matureRatings: Set<String> = ["tv14", "tvma", "r", "nc17", "x"]

    /// " word word " (lowercased ASCII letters/digits) for whole-word checks.
    private static func words(_ piece: String) -> String {
        let ws = piece.lowercased()
            .split(whereSeparator: { !(("a"..."z").contains($0) || ("0"..."9").contains($0)) })
        return ws.isEmpty ? "" : " " + ws.joined(separator: " ") + " "
    }

    private static func hasAny(_ padded: String, _ phrases: [String]) -> Bool {
        phrases.contains { padded.contains(" \($0) ") }
    }

    /// Schedules Direct program IDs: `MV` + digits movies, `SP` + digits sports events.
    private static func sdPrefix(_ programId: String, _ prefix: String) -> Bool {
        guard programId.hasPrefix(prefix) else { return false }
        let digits = programId.dropFirst(prefix.count).prefix(8)
        return digits.count == 8 && digits.allSatisfy { ("0"..."9").contains($0) }
    }

    /// Every bucket `program` belongs to (may be several, or none).
    public static func buckets(for program: GuideProgram) -> Set<GuideBucket> {
        var out = Set<GuideBucket>()
        for piece in program.category.split(whereSeparator: { ",;|".contains($0) }) {
            let w = words(String(piece))
            if w.isEmpty { continue }
            if hasAny(w, sportsPhrases) { out.insert(.sports) }
            if moviePieces.contains(w.trimmingCharacters(in: .whitespaces)) { out.insert(.movies) }
            if hasAny(w, newsPhrases) { out.insert(.news) }
            if hasAny(w, kidsPhrases) { out.insert(.kids) }
        }
        let pid = program.programId ?? ""
        if sdPrefix(pid, "MV") { out.insert(.movies) }
        if sdPrefix(pid, "SP") { out.insert(.sports) }
        let rating = (program.rating ?? "").lowercased()
            .filter { ("a"..."z").contains($0) || ("0"..."9").contains($0) }
        if matureRatings.contains(rating) { out.remove(.kids) }
        if program.isNew == true { out.insert(.new) }
        return out
    }

    // MARK: - Filtering

    /// `.all` matches everything; otherwise the program is in this bucket.
    /// Classifies `program`; rows carry their programs' buckets already.
    public func matches(_ program: GuideProgram) -> Bool {
        matches(Self.buckets(for: program))
    }

    /// `.all` matches everything; otherwise `buckets` holds this one.
    public func matches(_ buckets: Set<GuideBucket>) -> Bool {
        guard let bucket else { return true }
        return buckets.contains(bucket)
    }

    /// Some program of `row` overlapping `[from, to)` matches. `.all` keeps
    /// every channel, even one without guide data.
    public func matches(row: ChannelListModel.Row, from: Date, to: Date) -> Bool {
        guard self != .all else { return true }
        return row.programs.indices.contains { i in
            let p = row.programs[i]
            return p.start < to && p.stop > from && matches(row.programBuckets[i])
        }
    }

    /// The earliest matching program of `row` overlapping `[from, to)`.
    public func firstMatch(in row: ChannelListModel.Row, from: Date, to: Date) -> GuideProgram? {
        row.programs.indices
            .filter { i in
                let p = row.programs[i]
                return p.start < to && p.stop > from && matches(row.programBuckets[i])
            }
            .map { row.programs[$0] }
            .min { $0.start < $1.start }
    }

    /// Rows with something matching in `[from, to)`, order kept.
    public func visibleRows(_ rows: [ChannelListModel.Row], from: Date, to: Date) -> [ChannelListModel.Row] {
        guard self != .all else { return rows }
        return rows.filter { matches(row: $0, from: from, to: to) }
    }

    /// Rows for a list with a selection (macOS sidebar): `visibleRows`, plus
    /// the row for `keeping` in its usual place even when it doesn't match,
    /// so the selection (and the channel playing from it) never drops out.
    public func visibleRows(
        _ rows: [ChannelListModel.Row],
        from: Date,
        to: Date,
        keeping channelId: Int64?
    ) -> KeptRows {
        guard self != .all else { return KeptRows(rows: rows, keptId: nil) }
        var keptId: Int64?
        let shown = rows.filter { row in
            if matches(row: row, from: from, to: to) { return true }
            guard row.id == channelId else { return false }
            keptId = row.id
            return true
        }
        return KeptRows(rows: shown, keptId: keptId)
    }

    /// `visibleRows(_:from:to:keeping:)`'s answer.
    public struct KeptRows: Equatable {
        /// Rows to show, order kept.
        public let rows: [ChannelListModel.Row]
        /// The row shown only because it is kept (draw it dimmed); nil when it matches.
        public let keptId: Int64?

        public init(rows: [ChannelListModel.Row], keptId: Int64?) {
            self.rows = rows
            self.keptId = keptId
        }

        /// Nothing matches the filter (a kept row aside).
        public var hasNoMatches: Bool { rows.allSatisfy { $0.id == keptId } }
    }

    /// How a now/next row reads under this filter: which lines stay bright,
    /// and a later match to show when neither now nor next is one.
    public struct RowHighlight: Equatable, Sendable {
        public let nowMatches: Bool
        public let nextMatches: Bool
        public let later: GuideProgram?

        public init(nowMatches: Bool, nextMatches: Bool, later: GuideProgram?) {
            self.nowMatches = nowMatches
            self.nextMatches = nextMatches
            self.later = later
        }
    }

    /// Uses the buckets `row` worked out when the guide loaded.
    public func highlight(for row: ChannelListModel.Row, from: Date, to: Date) -> RowHighlight {
        guard self != .all else {
            return RowHighlight(nowMatches: true, nextMatches: true, later: nil)
        }
        let nowMatches = row.nowNext.now.map { matches(row.buckets(of: $0)) } ?? false
        let nextMatches = row.nowNext.next.map { matches(row.buckets(of: $0)) } ?? false
        let later = nowMatches || nextMatches ? nil : firstMatch(in: row, from: from, to: to)
        return RowHighlight(nowMatches: nowMatches, nextMatches: nextMatches, later: later)
    }

    // MARK: - Per-device memory

    public static let defaultsKey = "bowtie.guideFilter"

    public static func load(from defaults: UserDefaults = .standard) -> GuideFilter {
        defaults.string(forKey: defaultsKey).flatMap(GuideFilter.init(rawValue:)) ?? .all
    }

    public func save(to defaults: UserDefaults = .standard) {
        defaults.set(rawValue, forKey: Self.defaultsKey)
    }
}
