import Foundation
import GroupActivities

// MARK: - Activity

/// SharePlay activity: "watch this channel on this Bowtie server with me".
///
/// Everyone in the group joins the sharer's server session (`sessionId`) so
/// they all read one playlist and its program dates agree, which is what
/// `AVPlayerPlaybackCoordinator` needs to keep live playback in sync.
public struct WatchChannelActivity: GroupActivity, Codable, Equatable, Hashable, Sendable {
    /// Fixed so iOS and tvOS agree (and so `swift test`, which has no bundle
    /// ID, can't trip the synthesized default).
    public static let activityIdentifier = "app.bowtie.WatchChannelActivity"

    /// `GET /api/v1/version` `serverId` of the sharer's server.
    public var serverId: String
    /// Shown as "Sign in to <serverName> to watch together".
    public var serverName: String
    public var channelId: Int64
    public var channelName: String
    /// The sharer's server session (`session.id`); joiners send it as `joinSessionId`.
    public var sessionId: String

    public init(
        serverId: String,
        serverName: String,
        channelId: Int64,
        channelName: String,
        sessionId: String
    ) {
        self.serverId = serverId
        self.serverName = serverName
        self.channelId = channelId
        self.channelName = channelName
        self.sessionId = sessionId
    }

    public var metadata: GroupActivityMetadata {
        var metadata = GroupActivityMetadata()
        metadata.title = channelName
        metadata.type = .watchTogether
        #if !os(macOS)
        // Lets an Apple TV pick the activity up from the iPhone on the call.
        metadata.supportsContinuationOnTV = true
        #endif
        return metadata
    }

    /// The sharer changed channel: same server, new channel and session.
    public func following(channelId: Int64, channelName: String, sessionId: String) -> WatchChannelActivity {
        WatchChannelActivity(
            serverId: serverId,
            serverName: serverName,
            channelId: channelId,
            channelName: channelName,
            sessionId: sessionId
        )
    }
}

// MARK: - Can this device join?

/// A server this app knows, as far as joining a group is concerned.
public struct WatchTogetherServer: Equatable, Sendable {
    public let url: URL
    /// From `GET /api/v1/version`; nil when unreachable or too old to say.
    public let serverId: String?
    /// The app holds a login (refresh token) for this URL.
    public let isSignedIn: Bool

    public init(url: URL, serverId: String?, isSignedIn: Bool) {
        self.url = url
        self.serverId = serverId
        self.isSignedIn = isSignedIn
    }
}

/// What to do with a SharePlay session that arrived.
public enum WatchTogetherDecision: Equatable, Sendable {
    /// Signed in to the sharer's server already: join.
    case join
    /// A saved server is the sharer's (maybe under another URL) and has a
    /// login: switch to it, then join.
    case switchServer(URL)
    /// No login on the sharer's server: tell the user.
    case signIn(message: String)

    /// - Parameters:
    ///   - serverId / serverName: from the activity.
    ///   - current: the active server, if any (`isSignedIn` = the app is ready on it).
    ///   - saved: every saved server (may include the current one).
    public static func decide(
        serverId: String,
        serverName: String,
        current: WatchTogetherServer?,
        saved: [WatchTogetherServer]
    ) -> WatchTogetherDecision {
        func matches(_ server: WatchTogetherServer) -> Bool {
            !serverId.isEmpty && server.serverId == serverId
        }
        if let current, current.isSignedIn, matches(current) {
            return .join
        }
        if let other = saved.first(where: { $0.url != current?.url && $0.isSignedIn && matches($0) }) {
            return .switchServer(other.url)
        }
        return .signIn(message: signInMessage(serverName: serverName))
    }

    public static func signInMessage(serverName: String) -> String {
        let name = serverName.trimmingCharacters(in: .whitespacesAndNewlines)
        return "Sign in to \(name.isEmpty ? "the sharer's Bowtie server" : name) to watch together"
    }
}
