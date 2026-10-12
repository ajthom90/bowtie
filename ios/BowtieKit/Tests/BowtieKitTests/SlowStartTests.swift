import XCTest
@testable import BowtieKit

/// A channel start can take a while: createSession outwaits the server's own
/// start bound, and the player's "Starting…" turns into a plain-words note.
final class SlowStartTests: XCTestCase {
    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
    }

    override func tearDown() {
        StubURLProtocol.reset()
        super.tearDown()
    }

    func testStartingCopy() {
        XCTAssertEqual(StartingCopy.text(slow: false), "Starting…")
        XCTAssertEqual(
            StartingCopy.text(slow: true),
            "Still starting — the stream is buffering. This can take a little while."
        )
        XCTAssertEqual(StartingCopy.slowAfter, .seconds(5))
    }

    /// Server bound: device answer 12 s + first playlist 15 s + one simpler
    /// layout 15 s. Waiting less turned a slow start into "can't reach".
    func testCreateSessionWaitsLongerThanTheServerTakesToStart() async throws {
        XCTAssertGreaterThan(BowtieClient.sessionStartTimeout, 12 + 15 + 15)

        let client = BowtieClient(
            server: TestFixtures.baseURL,
            store: InMemorySessionStore(),
            urlSession: TestFixtures.makeStubSession()
        )
        await client.setAccessTokenForTesting("access-1")
        StubURLProtocol.handler = { _ in
            (200, Data(#"{"viewerId":"v1","playlistUrl":"/api/v1/stream/v1/index.m3u8?token=t"}"#.utf8), [:])
        }
        let caps = ClientCaps(videoCodecs: ["h264"], audioCodecs: ["aac"], maxHeight: 1080, profile: "")
        _ = try await client.createSession(channelId: 7, caps: caps)

        let create = try XCTUnwrap(StubURLProtocol.recorded.first { $0.url?.path == "/api/v1/sessions" })
        XCTAssertEqual(create.timeoutInterval, BowtieClient.sessionStartTimeout)
    }
}
