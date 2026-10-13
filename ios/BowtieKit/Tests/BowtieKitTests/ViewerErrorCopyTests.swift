import XCTest
@testable import BowtieKit

/// Viewers see plain words; the technical cause goes to the log.
final class ViewerErrorCopyTests: XCTestCase {
    private let unreachable = "Can't reach your Bowtie server. Check your connection and try again."
    private let generic = "Something went wrong. Try again."

    func testNetworkFailureIsCantReachServer() {
        XCTAssertEqual(
            ViewerErrorCopy.message(for: BowtieError.network("The request timed out.")),
            unreachable
        )
        XCTAssertEqual(ViewerErrorCopy.message(for: URLError(.notConnectedToInternet)), unreachable)
    }

    func testMalformedResponseIsSomethingWentWrong() {
        XCTAssertEqual(
            ViewerErrorCopy.message(for: BowtieError.badResponse("decode failed: The data couldn't be read")),
            generic
        )
    }

    func testUnknownErrorIsSomethingWentWrong() {
        struct Odd: Error {}
        XCTAssertEqual(ViewerErrorCopy.message(for: Odd()), generic)
    }

    func testServerMessageShownAsASentence() {
        XCTAssertEqual(
            ViewerErrorCopy.message(for: BowtieError.server(status: 503, message: "recording is not available")),
            "Recording is not available"
        )
        XCTAssertEqual(
            ViewerErrorCopy.message(for: BowtieError.parental("Blocked by parental controls (rated TV-MA)")),
            "Blocked by parental controls (rated TV-MA)"
        )
        XCTAssertEqual(
            ViewerErrorCopy.message(for: BowtieError.negotiationFailed("This device can't play this channel's format.")),
            "This device can't play this channel's format."
        )
    }

    func testServerErrorWithoutAMessageIsPlain() {
        XCTAssertEqual(ViewerErrorCopy.message(for: BowtieError.server(status: 500, message: "")), generic)
        // A proxy in front of Bowtie answering for it: Bowtie itself is out of reach.
        XCTAssertEqual(ViewerErrorCopy.message(for: BowtieError.server(status: 502, message: "")), unreachable)
        XCTAssertEqual(ViewerErrorCopy.message(for: BowtieError.negotiationFailed("")), generic)
    }

    func testSignedOutAndTunersBusyArePlain() {
        XCTAssertEqual(ViewerErrorCopy.message(for: BowtieError.unauthorized), "You've been signed out. Sign in again.")
        XCTAssertEqual(
            ViewerErrorCopy.message(for: BowtieError.tunersBusy([], otherInUse: 0)),
            "All tuners are in use. Try again in a few minutes."
        )
    }

    // MARK: - Client side: no HTTP status text or decoder detail in messages

    private func failure(status: Int, body: String) async -> BowtieError? {
        StubURLProtocol.reset()
        StubURLProtocol.handler = { _ in (status, Data(body.utf8), [:]) }
        let client = BowtieClient(
            server: TestFixtures.baseURL,
            store: InMemorySessionStore(),
            urlSession: TestFixtures.makeStubSession()
        )
        await client.setAccessTokenForTesting("access-1")
        defer { StubURLProtocol.reset() }
        do {
            _ = try await client.channels()
            return nil
        } catch {
            return error as? BowtieError
        }
    }

    func testBodylessServerErrorCarriesNoStatusText() async {
        let error = await failure(status: 500, body: "<html>Internal Server Error</html>")
        XCTAssertEqual(error, .server(status: 500, message: ""))
    }

    func testUndecodableSuccessIsBadResponse() async {
        let error = await failure(status: 200, body: "not json")
        guard case .badResponse? = error else {
            return XCTFail("expected badResponse, got \(String(describing: error))")
        }
    }
}
