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

    /// Server bound: connect 5 s + device answer 12 s + track probe 3 s +
    /// first playlist 15 s + one simpler layout 15 s ≈ 50 s. Waiting less
    /// turned a slow start into "can't reach".
    func testCreateSessionWaitsLongerThanTheServerTakesToStart() {
        XCTAssertEqual(BowtieClient.sessionStartTimeout, 60)
        XCTAssertGreaterThan(BowtieClient.sessionStartTimeout, 5 + 12 + 3 + 15 + 15)
    }

    /// The wait is set on the createSession request itself (not URLRequest's
    /// default, which also happens to be 60 s), and only there.
    func testCreateSessionSetsItsWaitOnTheRequest() async throws {
        let client = BowtieClient(
            server: TestFixtures.baseURL,
            store: InMemorySessionStore(),
            urlSession: TestFixtures.makeStubSession(),
            sessionStartWait: 7
        )
        await client.setAccessTokenForTesting("access-1")
        StubURLProtocol.handler = { request in
            if request.url?.path == "/api/v1/sessions" {
                return (200, Data(#"{"viewerId":"v1","playlistUrl":"/api/v1/stream/v1/index.m3u8?token=t"}"#.utf8), [:])
            }
            return (200, Data("[]".utf8), [:])
        }
        let caps = ClientCaps(videoCodecs: ["h264"], audioCodecs: ["aac"], maxHeight: 1080, profile: "")
        _ = try await client.createSession(channelId: 7, caps: caps)
        _ = try await client.channels()

        let create = try XCTUnwrap(StubURLProtocol.recorded.first { $0.url?.path == "/api/v1/sessions" })
        XCTAssertEqual(create.timeoutInterval, 7)
        let channels = try XCTUnwrap(StubURLProtocol.recorded.first { $0.url?.path == "/api/v1/channels" })
        XCTAssertNotEqual(channels.timeoutInterval, 7)
    }
}
