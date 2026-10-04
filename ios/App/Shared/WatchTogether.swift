import AVFoundation
import Combine
import Foundation
import GroupActivities
import SwiftUI
import BowtieKit

// MARK: - Group seam

/// A SharePlay group watching a channel. `PlayerModel` talks to this, not to
/// `GroupSession` (which tests can't create).
@MainActor
protocol WatchGroup: AnyObject {
    var activity: WatchChannelActivity { get }
    func join()
    func leave()
    /// Sharer changed channel: everyone follows.
    func update(_ activity: WatchChannelActivity)
    /// Sync pause / seek / rate of `player` with the group.
    func coordinate(_ player: AVPlayer)
    /// The activity changed (any member's update, including our own).
    var onActivityChange: (@MainActor (WatchChannelActivity) -> Void)? { get set }
    /// The session ended for this device.
    var onEnd: (@MainActor () -> Void)? { get set }
}

/// `GroupSession<WatchChannelActivity>` behind `WatchGroup`.
@MainActor
final class LiveWatchGroup: WatchGroup {
    let session: GroupSession<WatchChannelActivity>
    var onActivityChange: (@MainActor (WatchChannelActivity) -> Void)?
    var onEnd: (@MainActor () -> Void)?

    private var cancellables = Set<AnyCancellable>()
    /// Weak on the coordinator, so held here.
    private let itemIdentity = SharedItemIdentity()

    init(session: GroupSession<WatchChannelActivity>) {
        self.session = session
        session.$activity
            .dropFirst()
            .receive(on: DispatchQueue.main)
            .sink { [weak self] activity in
                self?.onActivityChange?(activity)
            }
            .store(in: &cancellables)
        session.$state
            .receive(on: DispatchQueue.main)
            .sink { [weak self] state in
                if case .invalidated = state {
                    self?.onEnd?()
                    self?.cancellables.removeAll()
                }
            }
            .store(in: &cancellables)
    }

    var activity: WatchChannelActivity { session.activity }

    func join() { session.join() }

    func leave() { session.leave() }

    func update(_ activity: WatchChannelActivity) {
        session.activity = activity
    }

    func coordinate(_ player: AVPlayer) {
        player.playbackCoordinator.delegate = itemIdentity
        player.playbackCoordinator.coordinateWithSession(session)
    }
}

/// Each viewer's playlist URL is their own (`/stream/<viewerId>/…?token=`),
/// so the coordinator's default item identity (the URL) would never match
/// across devices. Everyone in a group plays the same server session, so that
/// ID is the item identity; players register each playlist they load.
final class SharedItemIdentity: NSObject, AVPlayerPlaybackCoordinatorDelegate {
    private static let lock = NSLock()
    nonisolated(unsafe) private static var sessionByPlaylist: [String: String] = [:]

    /// Record which server session a playlist URL plays.
    static func register(playlistURL: URL, sessionId: String?) {
        guard let sessionId, !sessionId.isEmpty else { return }
        lock.lock()
        defer { lock.unlock() }
        sessionByPlaylist[key(playlistURL)] = sessionId
    }

    static func identifier(for url: URL?) -> String {
        guard let url else { return UUID().uuidString }
        lock.lock()
        defer { lock.unlock() }
        if let sessionId = sessionByPlaylist[key(url)] {
            return "bowtie-session:\(sessionId)"
        }
        return url.absoluteString
    }

    private static func key(_ url: URL) -> String {
        // The token query rotates on a silent replace; the path names the viewer.
        url.path
    }

    func playbackCoordinator(
        _ coordinator: AVPlayerPlaybackCoordinator,
        identifierFor playerItem: AVPlayerItem
    ) -> String {
        Self.identifier(for: (playerItem.asset as? AVURLAsset)?.url)
    }
}

// MARK: - App plumbing

/// A group the app decided to join, waiting for the player.
struct GroupJoinRequest: Identifiable {
    let id = UUID()
    let group: any WatchGroup
}

/// Asks the channel list to show the player for a channel the group chose.
struct GroupPresentation: Equatable, Identifiable {
    let id = UUID()
    let channel: Channel
}

extension View {
    /// Opens the player when a SharePlay group starts or changes the channel.
    /// Applied to the channel list (iOS) / rail (tvOS), which own navigation.
    func presentsGroupPlayback(_ playerModel: PlayerModel, playingChannel: Binding<Channel?>) -> some View {
        task(id: playerModel.groupPresentation?.id) {
            guard let presentation = playerModel.groupPresentation else { return }
            playerModel.groupPresentationShown()
            // An open player follows the model's channel on its own.
            if playingChannel.wrappedValue == nil {
                playingChannel.wrappedValue = presentation.channel
            }
        }
    }
}
