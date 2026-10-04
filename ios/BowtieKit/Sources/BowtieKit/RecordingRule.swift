import Foundation

/// A series recording rule (openapi `RecordingRule`): record every matching
/// episode of a show.
public struct RecordingRule: Codable, Equatable, Hashable, Identifiable, Sendable {
    public let id: Int64
    public let title: String
    public let seriesId: String
    /// 0 = any channel.
    public let channelId: Int64
    public let channelName: String
    /// New episodes only (first airings).
    public let newOnly: Bool
    /// Keep only this many recordings of the show (0 = all).
    public let keepLatest: Int
    public let scheduledBy: String
    /// The caller may stop it (who set it up, or an admin).
    public let canManage: Bool
    public let createdAt: Date

    public init(
        id: Int64,
        title: String,
        seriesId: String = "",
        channelId: Int64,
        channelName: String,
        newOnly: Bool = true,
        keepLatest: Int = 0,
        scheduledBy: String = "",
        canManage: Bool = false,
        createdAt: Date
    ) {
        self.id = id
        self.title = title
        self.seriesId = seriesId
        self.channelId = channelId
        self.channelName = channelName
        self.newOnly = newOnly
        self.keepLatest = keepLatest
        self.scheduledBy = scheduledBy
        self.canManage = canManage
        self.createdAt = createdAt
    }

    public var anyChannel: Bool { channelId == 0 }

    /// "5.1 KSTP", or "Any channel".
    public var channelLabel: String {
        anyChannel || channelName.isEmpty ? "Any channel" : channelName
    }

    /// "5.1 KSTP · New episodes · Keeps 5 · by andrew"
    public var detailLine: String {
        var parts = [channelLabel, newOnly ? "New episodes" : "All episodes"]
        if keepLatest > 0 {
            parts.append("Keeps \(keepLatest)")
        }
        if !canManage, !scheduledBy.isEmpty {
            parts.append("by \(scheduledBy)")
        }
        return parts.joined(separator: " · ")
    }
}

/// `POST /recording-rules` 201 body.
public struct CreatedRecordingRule: Codable, Equatable, Sendable {
    public let rule: RecordingRule
    /// Upcoming airings scheduled right away.
    public let scheduled: Int

    public init(rule: RecordingRule, scheduled: Int) {
        self.rule = rule
        self.scheduled = scheduled
    }
}
