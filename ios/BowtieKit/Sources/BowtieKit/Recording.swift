import Foundation

// MARK: - Recording (openapi `Recording`)

/// A DVR recording: upcoming, in progress, recorded or missed.
public struct Recording: Codable, Equatable, Hashable, Identifiable, Sendable {
    public let id: Int64
    public let title: String
    public let subtitle: String
    public let description: String
    public let category: String
    public let channelId: Int64
    public let channelName: String
    public let start: Date
    public let stop: Date
    /// scheduled | waiting | recording | converting | ready | failed. See `status`.
    public let state: String
    /// More than a minute is missing (late start, dropped stream, or a restart).
    public let partial: Bool
    /// "" | noTuner | noSignal | diskFull | error.
    public let failure: String
    /// Server-side error text; technical, not shown as the main reason.
    public let failureDetail: String
    public let durationSec: Int
    public let sizeBytes: Int64
    /// Never deleted automatically when space runs low.
    public let protected: Bool
    /// The caller's resume position.
    public let positionSec: Int
    public let scheduledBy: String
    /// The caller may stop, delete or protect it (scheduler or admin).
    public let canManage: Bool
    /// The program's rating when scheduled ("" = not rated).
    public let rating: String
    /// Series rule that scheduled it (0 = a one-off recording).
    public let ruleId: Int64
    /// Parental controls block it for the caller: no description, and play is refused.
    public let locked: Bool
    /// Detected commercial breaks on the playback timeline (seconds), sorted
    /// and non-overlapping; empty when none were found or the server is older.
    public let commercials: [Commercial]

    public init(
        id: Int64,
        title: String,
        subtitle: String = "",
        description: String = "",
        category: String = "",
        channelId: Int64,
        channelName: String,
        start: Date,
        stop: Date,
        state: String,
        partial: Bool = false,
        failure: String = "",
        failureDetail: String = "",
        durationSec: Int = 0,
        sizeBytes: Int64 = 0,
        protected: Bool = false,
        positionSec: Int = 0,
        scheduledBy: String = "",
        canManage: Bool = false,
        rating: String = "",
        ruleId: Int64 = 0,
        locked: Bool = false,
        commercials: [Commercial] = []
    ) {
        self.id = id
        self.title = title
        self.subtitle = subtitle
        self.description = description
        self.category = category
        self.channelId = channelId
        self.channelName = channelName
        self.start = start
        self.stop = stop
        self.state = state
        self.partial = partial
        self.failure = failure
        self.failureDetail = failureDetail
        self.durationSec = durationSec
        self.sizeBytes = sizeBytes
        self.protected = protected
        self.positionSec = positionSec
        self.scheduledBy = scheduledBy
        self.canManage = canManage
        self.rating = rating
        self.ruleId = ruleId
        self.locked = locked
        self.commercials = commercials
    }

    private enum CodingKeys: String, CodingKey {
        case id, title, subtitle, description, category, channelId, channelName, start, stop
        case state, partial, failure, failureDetail, durationSec, sizeBytes, protected
        case positionSec, scheduledBy, canManage, rating, ruleId, locked, commercials
    }

    /// `rating`, `ruleId`, `locked` and `commercials` are absent from older servers.
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(Int64.self, forKey: .id)
        title = try c.decode(String.self, forKey: .title)
        subtitle = try c.decode(String.self, forKey: .subtitle)
        description = try c.decode(String.self, forKey: .description)
        category = try c.decode(String.self, forKey: .category)
        channelId = try c.decode(Int64.self, forKey: .channelId)
        channelName = try c.decode(String.self, forKey: .channelName)
        start = try c.decode(Date.self, forKey: .start)
        stop = try c.decode(Date.self, forKey: .stop)
        state = try c.decode(String.self, forKey: .state)
        partial = try c.decode(Bool.self, forKey: .partial)
        failure = try c.decode(String.self, forKey: .failure)
        failureDetail = try c.decode(String.self, forKey: .failureDetail)
        durationSec = try c.decode(Int.self, forKey: .durationSec)
        sizeBytes = try c.decode(Int64.self, forKey: .sizeBytes)
        protected = try c.decode(Bool.self, forKey: .protected)
        positionSec = try c.decode(Int.self, forKey: .positionSec)
        scheduledBy = try c.decode(String.self, forKey: .scheduledBy)
        canManage = try c.decode(Bool.self, forKey: .canManage)
        rating = try c.decodeIfPresent(String.self, forKey: .rating) ?? ""
        ruleId = try c.decodeIfPresent(Int64.self, forKey: .ruleId) ?? 0
        locked = try c.decodeIfPresent(Bool.self, forKey: .locked) ?? false
        commercials = try c.decodeIfPresent([Commercial].self, forKey: .commercials) ?? []
    }
}

public enum RecordingStatus: String, Sendable {
    case scheduled
    /// Inside its window, retrying for a free tuner.
    case waiting
    case recording
    /// Recorded; the server is packaging it for playback.
    case converting
    case ready
    case failed
    /// A state this app doesn't know yet.
    case unknown
}

extension Recording {
    public var status: RecordingStatus { RecordingStatus(rawValue: state) ?? .unknown }

    /// Only a finished, packaged recording the caller isn't locked out of can be played.
    public var isPlayable: Bool { status == .ready && !locked }

    /// Scheduled by a series rule ("Record Series").
    public var isSeries: Bool { ruleId > 0 }

    /// An upcoming episode someone removed; its series rule won't schedule it again.
    public var isSkipped: Bool { status == .failed && failure == "skipped" }

    /// Cancel (upcoming) or delete (anything else).
    public var canDelete: Bool { canManage }

    /// Stop now and keep what was recorded.
    public var canStop: Bool { canManage && status == .recording }

    /// Keep from automatic deletion; only meaningful once recorded.
    public var canProtect: Bool { canManage && (status == .ready || status == .converting) }

    /// Upcoming recordings are cancelled; everything else is deleted.
    public var deleteActionTitle: String {
        switch status {
        case .scheduled, .waiting: return "Cancel Recording"
        default: return "Delete"
        }
    }
}

/// `GuideProgram.recording`: present when the program is scheduled or recorded.
public struct RecordingMark: Codable, Equatable, Hashable, Sendable {
    public let id: Int64
    public let state: String

    public init(id: Int64, state: String) {
        self.id = id
        self.state = state
    }
}

/// `GET /recordings?state=` values.
public enum RecordingsFilter: String, Sendable {
    case upcoming
    case recorded
    case failed
}

/// 201 warning, e.g. `usesAllTuners`.
public struct RecordingWarning: Codable, Equatable, Sendable {
    public let code: String
    public let message: String

    public init(code: String, message: String) {
        self.code = code
        self.message = message
    }
}

/// `POST /recordings` 201 body.
public struct ScheduledRecording: Codable, Equatable, Sendable {
    public let recording: Recording
    public let warnings: [RecordingWarning]

    public init(recording: Recording, warnings: [RecordingWarning]) {
        self.recording = recording
        self.warnings = warnings
    }
}

/// `POST /recordings/{id}/play` 200 body. `playlistUrl` is server-relative and token-signed.
public struct RecordingPlayback: Codable, Equatable, Sendable {
    public let playlistUrl: String
    public let positionSec: Int
    public let durationSec: Int

    public init(playlistUrl: String, positionSec: Int, durationSec: Int) {
        self.playlistUrl = playlistUrl
        self.positionSec = positionSec
        self.durationSec = durationSec
    }
}
