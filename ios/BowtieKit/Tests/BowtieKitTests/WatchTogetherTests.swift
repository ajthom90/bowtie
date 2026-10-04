import XCTest
import GroupActivities
@testable import BowtieKit

// MARK: - Activity payload

final class WatchChannelActivityTests: XCTestCase {
    private let sample = WatchChannelActivity(
        serverId: "srv-123",
        serverName: "Living Room",
        channelId: 42,
        channelName: "WABC",
        sessionId: "sess-9"
    )

    func testCodableRoundTrip() throws {
        let data = try JSONEncoder().encode(sample)
        let decoded = try JSONDecoder().decode(WatchChannelActivity.self, from: data)
        XCTAssertEqual(decoded, sample)
    }

    func testWireFieldNames() throws {
        let data = try JSONEncoder().encode(sample)
        let obj = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        XCTAssertEqual(
            Set(obj.keys),
            Set(["serverId", "serverName", "channelId", "channelName", "sessionId"])
        )
        XCTAssertEqual(obj["serverId"] as? String, "srv-123")
        XCTAssertEqual(obj["channelId"] as? Int, 42)
        XCTAssertEqual(obj["sessionId"] as? String, "sess-9")
    }

    func testMetadataIsWatchTogetherTitledByChannel() {
        let metadata = sample.metadata
        XCTAssertEqual(metadata.title, "WABC")
        XCTAssertEqual(metadata.type, .watchTogether)
        XCTAssertNil(metadata.fallbackURL)
    }

    func testActivityIdentifierIsFixed() {
        // Explicit: iOS and tvOS must agree, and `swift test` has no bundle ID.
        XCTAssertEqual(WatchChannelActivity.activityIdentifier, "app.bowtie.WatchChannelActivity")
    }

    func testFollowingChannelKeepsServer() {
        let next = sample.following(channelId: 7, channelName: "WXYZ", sessionId: "sess-10")
        XCTAssertEqual(next.serverId, "srv-123")
        XCTAssertEqual(next.serverName, "Living Room")
        XCTAssertEqual(next.channelId, 7)
        XCTAssertEqual(next.channelName, "WXYZ")
        XCTAssertEqual(next.sessionId, "sess-10")
    }
}

// MARK: - Join decision

final class WatchTogetherDecisionTests: XCTestCase {
    private let lan = URL(string: "http://192.168.1.10:8400")!
    private let tailnet = URL(string: "http://bowtie.tail1234.ts.net:8400")!
    private let other = URL(string: "http://other.example:8400")!

    private func decide(
        serverId: String = "srv-1",
        serverName: String = "Living Room",
        current: WatchTogetherServer?,
        saved: [WatchTogetherServer] = []
    ) -> WatchTogetherDecision {
        WatchTogetherDecision.decide(
            serverId: serverId,
            serverName: serverName,
            current: current,
            saved: saved
        )
    }

    func testCurrentServerSignedInJoins() {
        let d = decide(current: .init(url: lan, serverId: "srv-1", isSignedIn: true))
        XCTAssertEqual(d, .join)
    }

    func testSavedServerWithLoginSwitches() {
        let d = decide(
            current: .init(url: other, serverId: "srv-other", isSignedIn: true),
            saved: [
                .init(url: other, serverId: "srv-other", isSignedIn: true),
                .init(url: tailnet, serverId: "srv-1", isSignedIn: true),
            ]
        )
        XCTAssertEqual(d, .switchServer(tailnet))
    }

    func testSameServerOtherURLWithLoginWhenCurrentSignedOut() {
        // Signed out on the LAN URL but signed in through Tailscale: same server.
        let d = decide(
            current: .init(url: lan, serverId: "srv-1", isSignedIn: false),
            saved: [
                .init(url: lan, serverId: "srv-1", isSignedIn: false),
                .init(url: tailnet, serverId: "srv-1", isSignedIn: true),
            ]
        )
        XCTAssertEqual(d, .switchServer(tailnet))
    }

    func testNoCurrentServerUsesSaved() {
        let d = decide(current: nil, saved: [.init(url: lan, serverId: "srv-1", isSignedIn: true)])
        XCTAssertEqual(d, .switchServer(lan))
    }

    func testSavedMatchWithoutLoginAsksToSignIn() {
        let d = decide(
            current: .init(url: other, serverId: "srv-other", isSignedIn: true),
            saved: [.init(url: lan, serverId: "srv-1", isSignedIn: false)]
        )
        XCTAssertEqual(d, .signIn(message: "Sign in to Living Room to watch together"))
    }

    func testCurrentMatchSignedOutAndNoOtherLoginAsksToSignIn() {
        let d = decide(current: .init(url: lan, serverId: "srv-1", isSignedIn: false))
        XCTAssertEqual(d, .signIn(message: "Sign in to Living Room to watch together"))
    }

    func testNoMatchAsksToSignIn() {
        let d = decide(
            current: .init(url: other, serverId: "srv-other", isSignedIn: true),
            saved: [.init(url: other, serverId: "srv-other", isSignedIn: true)]
        )
        XCTAssertEqual(d, .signIn(message: "Sign in to Living Room to watch together"))
    }

    func testUnknownServerIdNeverMatches() {
        // Servers older than SharePlay support report no serverId.
        let d = decide(
            current: .init(url: lan, serverId: nil, isSignedIn: true),
            saved: [.init(url: tailnet, serverId: nil, isSignedIn: true)]
        )
        XCTAssertEqual(d, .signIn(message: "Sign in to Living Room to watch together"))
    }

    func testEmptyActivityServerIdNeverMatches() {
        let d = decide(
            serverId: "",
            current: .init(url: lan, serverId: "", isSignedIn: true)
        )
        XCTAssertEqual(d, .signIn(message: "Sign in to Living Room to watch together"))
    }

    func testFirstSavedLoginWins() {
        let d = decide(
            current: nil,
            saved: [
                .init(url: lan, serverId: "srv-1", isSignedIn: false),
                .init(url: tailnet, serverId: "srv-1", isSignedIn: true),
                .init(url: other, serverId: "srv-1", isSignedIn: true),
            ]
        )
        XCTAssertEqual(d, .switchServer(tailnet))
    }

    func testBlankServerNameFallsBack() {
        let d = decide(serverName: "  ", current: nil)
        XCTAssertEqual(d, .signIn(message: "Sign in to the sharer's Bowtie server to watch together"))
    }
}
