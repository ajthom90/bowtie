import XCTest
@testable import BowtieKit

/// `POST …/heartbeat?signal=1`: the antenna reading for the weak-signal note.
final class HeartbeatSignalTests: XCTestCase {
    private var store: InMemorySessionStore!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
        store = InMemorySessionStore()
    }

    override func tearDown() {
        StubURLProtocol.reset()
        super.tearDown()
    }

    private func beat(status: Int, body: String) async -> Result<SessionSignal?, BowtieError> {
        StubURLProtocol.handler = { _ in (status, Data(body.utf8), [:]) }
        let client = BowtieClient(server: TestFixtures.baseURL, store: store, urlSession: TestFixtures.makeStubSession())
        return await client.heartbeat(viewerId: "v1", token: "tok")
    }

    func testAsksForTheSignal() async {
        _ = await beat(status: 204, body: "")
        let items = URLComponents(url: StubURLProtocol.recorded[0].url!, resolvingAgainstBaseURL: false)?.queryItems ?? []
        XCTAssertEqual(items.first(where: { $0.name == "signal" })?.value, "1")
        XCTAssertEqual(items.first(where: { $0.name == "token" })?.value, "tok")
    }

    func testWeakSignalParses() async {
        let result = await beat(
            status: 200,
            body: #"{"signal":{"strength":96,"quality":46,"symbolQuality":0,"weak":true}}"#
        )
        XCTAssertEqual(
            try? result.get(),
            SessionSignal(strength: 96, quality: 46, symbolQuality: 0, weak: true)
        )
    }

    func testNullSignalIsUnknown() async {
        let result = await beat(status: 200, body: #"{"signal":null}"#)
        XCTAssertEqual(try? result.get(), .some(nil))
    }

    func testOlderServer204IsSuccessWithUnknownSignal() async {
        let result = await beat(status: 204, body: "")
        XCTAssertEqual(try? result.get(), .some(nil))
    }

    func testUnexpected2xxBodyIsStillSuccess() async {
        let result = await beat(status: 200, body: "ok")
        XCTAssertEqual(try? result.get(), .some(nil))
    }

    func testParentalRefusalSurfaces() async {
        let result = await beat(
            status: 403,
            body: #"{"error":"Blocked by parental controls (rated TV-14)","code":"parental"}"#
        )
        guard case .failure(let error) = result else {
            return XCTFail("expected a refusal, got \(result)")
        }
        XCTAssertEqual(error, .parental("Blocked by parental controls (rated TV-14)"))
    }
}

/// How a reading reads in the player: quality first (strength can be high
/// while the picture breaks up).
final class SessionSignalCopyTests: XCTestCase {
    private let reading = SessionSignal(strength: 96, quality: 46, symbolQuality: 0, weak: true)

    func testStatsLine() {
        XCTAssertEqual(reading.statsLine, "Signal quality 46% · strength 96% · error-free 0%")
    }

    func testWeakNoteLeadsWithQuality() {
        XCTAssertEqual(reading.weakNote, "Weak signal (46%) — the picture may break up.")
    }

    func testPercentsAreClampedToARealRange() {
        let odd = SessionSignal(strength: 140, quality: -3, symbolQuality: 100, weak: false)
        XCTAssertEqual(odd.statsLine, "Signal quality 0% · strength 100% · error-free 100%")
    }
}
