import Foundation

// MARK: - Auth / user

public struct User: Codable, Equatable, Sendable {
    public let id: Int64
    public let username: String
    public let role: String
    public let maxQuality: String

    public init(id: Int64, username: String, role: String, maxQuality: String) {
        self.id = id
        self.username = username
        self.role = role
        self.maxQuality = maxQuality
    }
}

/// Login / refresh response (`TokenResponse` in OpenAPI).
public struct TokenPair: Codable, Sendable {
    public let accessToken: String
    public let refreshToken: String
    public let user: User

    public init(accessToken: String, refreshToken: String, user: User) {
        self.accessToken = accessToken
        self.refreshToken = refreshToken
        self.user = user
    }
}

// MARK: - Channels / guide

public struct Channel: Codable, Equatable, Hashable, Identifiable, Sendable {
    public let id: Int64
    public let guideNumber: String
    public let name: String
    public let logoUrl: String
    /// Last tune outcome learned by the server: "ok", "noSignal" or "unknown".
    /// Absent from servers older than 0.6.3.
    public let reception: String?

    public init(id: Int64, guideNumber: String, name: String, logoUrl: String, reception: String? = nil) {
        self.id = id
        self.guideNumber = guideNumber
        self.name = name
        self.logoUrl = logoUrl
        self.reception = reception
    }

    /// The antenna got no signal the last time this channel was tuned.
    public var hasNoSignal: Bool { reception == "noSignal" }
}

public struct GuideProgram: Codable, Equatable, Sendable {
    public let start: Date
    public let stop: Date
    public let title: String
    public let subtitle: String
    public let description: String
    public let category: String

    public init(
        start: Date,
        stop: Date,
        title: String,
        subtitle: String,
        description: String,
        category: String
    ) {
        self.start = start
        self.stop = stop
        self.title = title
        self.subtitle = subtitle
        self.description = description
        self.category = category
    }
}

public struct GuideChannel: Codable, Equatable, Sendable {
    public let channelId: Int64
    public let guideNumber: String
    public let name: String
    public let logoUrl: String
    public let programs: [GuideProgram]

    public init(
        channelId: Int64,
        guideNumber: String,
        name: String,
        logoUrl: String,
        programs: [GuideProgram]
    ) {
        self.channelId = channelId
        self.guideNumber = guideNumber
        self.name = name
        self.logoUrl = logoUrl
        self.programs = programs
    }
}

// MARK: - Server

/// `GET /api/v1/version`.
public struct ServerVersion: Codable, Equatable, Sendable {
    public let version: String
    /// Stable random ID of the server, whatever URL reaches it. Absent from older servers.
    public let serverId: String?
    /// Display name (BOWTIE_SERVER_NAME or the host name). Absent from older servers.
    public let serverName: String?

    public init(version: String, serverId: String?, serverName: String?) {
        self.version = version
        self.serverId = serverId
        self.serverName = serverName
    }
}

// MARK: - Sessions

public struct ClientCaps: Codable, Sendable {
    public var videoCodecs: [String]
    public var audioCodecs: [String]
    public var maxHeight: Int
    public var profile: String

    public init(
        videoCodecs: [String],
        audioCodecs: [String],
        maxHeight: Int,
        profile: String
    ) {
        self.videoCodecs = videoCodecs
        self.audioCodecs = audioCodecs
        self.maxHeight = maxHeight
        self.profile = profile
    }
}

public struct SessionInfoMeta: Codable, Equatable, Sendable {
    /// Server session ID, shared by everyone on the same stream (SharePlay
    /// joiners send it as `joinSessionId`). Absent from older servers.
    public let id: String?
    public let videoCodec: String
    public let profile: String
    public let backend: String
    public let channelName: String

    public init(
        id: String? = nil,
        videoCodec: String,
        profile: String,
        backend: String,
        channelName: String
    ) {
        self.id = id
        self.videoCodec = videoCodec
        self.profile = profile
        self.backend = backend
        self.channelName = channelName
    }
}

public struct CreatedSession: Codable, Sendable {
    public let viewerId: String
    public let playlistUrl: String
    /// Negotiated session fields for the stats overlay. Optional so clients
    /// tolerate responses that omit it (OpenAPI marks it required; wire may not).
    public let session: SessionInfoMeta?

    public init(viewerId: String, playlistUrl: String, session: SessionInfoMeta?) {
        self.viewerId = viewerId
        self.playlistUrl = playlistUrl
        self.session = session
    }
}

/// Slim view of a live session used by the 503 tuners-busy payload.
public struct ActiveSessionSummary: Codable, Sendable, Equatable {
    public let channelName: String
    public let viewers: [ViewerSummary]

    public struct ViewerSummary: Codable, Sendable, Equatable {
        public let username: String

        public init(username: String) {
            self.username = username
        }
    }

    public init(channelName: String, viewers: [ViewerSummary]) {
        self.channelName = channelName
        self.viewers = viewers
    }
}
