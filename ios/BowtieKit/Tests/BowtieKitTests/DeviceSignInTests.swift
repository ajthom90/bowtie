import XCTest
@testable import BowtieKit

/// Quick sign-in for TV apps: the client calls and the polling model.
@MainActor
final class DeviceSignInTests: XCTestCase {
    private var store: InMemorySessionStore!

    override func setUp() {
        super.setUp()
        StubURLProtocol.reset()
        store = InMemorySessionStore()
        store.save(server: TestFixtures.baseURL, refreshToken: nil)
    }

    override func tearDown() {
        StubURLProtocol.reset()
        super.tearDown()
    }

    private func makeClient() -> BowtieClient {
        BowtieClient(server: TestFixtures.baseURL, store: store, urlSession: TestFixtures.makeStubSession())
    }

    nonisolated private static func codeJSON(
        deviceCode: String = "dev-secret-1",
        userCode: String = "BCDF-2345",
        expiresIn: Int = 600,
        interval: Int = 5
    ) -> Data {
        let raw = userCode.replacingOccurrences(of: "-", with: "")
        return """
        {"deviceCode":"\(deviceCode)","userCode":"\(userCode)",
         "verifyUrl":"http://other-host:8400/link?code=\(raw)",
         "qrUrl":"/api/v1/auth/device/qr/\(raw).png","expiresIn":\(expiresIn),"interval":\(interval)}
        """.data(using: .utf8)!
    }

    nonisolated private static let pending = Data(#"{"error":"authorization_pending"}"#.utf8)
    nonisolated private static let expired = Data(#"{"error":"expired_token"}"#.utf8)

    nonisolated private static func isStart(_ request: URLRequest) -> Bool {
        request.url?.path == "/api/v1/auth/device"
    }

    nonisolated private static func isPoll(_ request: URLRequest) -> Bool {
        request.url?.path == "/api/v1/auth/device/token"
    }

    /// Scripted poll answers in order; the last one repeats.
    private func script(polls: [(Int, Data)], start: [Data] = [codeJSON()]) {
        let state = ScriptState(polls: polls, starts: start)
        StubURLProtocol.handler = { request in
            if Self.isStart(request) {
                return (200, state.nextStart(), [:])
            }
            if Self.isPoll(request) {
                let (status, data) = state.nextPoll()
                return (status, data, [:])
            }
            return (404, Data(), [:])
        }
    }

    private func pollRequests() -> [URLRequest] {
        StubURLProtocol.recorded.filter(Self.isPoll)
    }

    // MARK: - Client

    func testStartSendsDeviceNameWithoutBearer() async throws {
        script(polls: [(428, Self.pending)])
        let client = makeClient()
        await client.setAccessTokenForTesting("stale")

        let code = try await client.startDeviceSignIn(deviceName: "Living Room")

        XCTAssertEqual(code.userCode, "BCDF-2345")
        XCTAssertEqual(code.deviceCode, "dev-secret-1")
        XCTAssertEqual(code.qrUrl, "/api/v1/auth/device/qr/BCDF2345.png")
        XCTAssertEqual(code.interval, 5)
        XCTAssertEqual(code.expiresIn, 600)
        let request = try XCTUnwrap(StubURLProtocol.recorded.first)
        XCTAssertEqual(request.httpMethod, "POST")
        XCTAssertNil(request.value(forHTTPHeaderField: "Authorization"))
        XCTAssertEqual(try jsonBody(of: request)["deviceName"] as? String, "Living Room")
    }

    func testPollPendingApprovedExpired() async throws {
        script(polls: [(428, Self.pending), (200, TestFixtures.tokenPairJSON(access: "a-9", refresh: "r-9")), (410, Self.expired)])
        let client = makeClient()

        let first = try await client.pollDeviceSignIn(deviceCode: "dev-secret-1")
        XCTAssertEqual(first, .pending)
        XCTAssertNil(store.loadRefreshToken())

        let second = try await client.pollDeviceSignIn(deviceCode: "dev-secret-1")
        XCTAssertEqual(second, .approved(User(id: 1, username: "alice", role: "viewer", maxQuality: "high")))
        XCTAssertEqual(store.loadRefreshToken(), "r-9", "the refresh token is saved like a password sign-in")
        let current = await client.currentUser
        XCTAssertEqual(current?.username, "alice")

        let third = try await client.pollDeviceSignIn(deviceCode: "dev-secret-1")
        XCTAssertEqual(third, .expired)

        let poll = try XCTUnwrap(pollRequests().first)
        XCTAssertEqual(poll.httpMethod, "POST")
        XCTAssertEqual(try jsonBody(of: poll)["deviceCode"] as? String, "dev-secret-1")
        XCTAssertNil(poll.value(forHTTPHeaderField: "Authorization"))
    }

    // MARK: - Model

    private func makeModel(
        client: BowtieClient,
        now: @escaping () -> Date = { Date(timeIntervalSince1970: 1_000) },
        sleeps: SleepLog? = nil
    ) -> DeviceSignInModel {
        let sleeps = sleeps ?? SleepLog()
        return DeviceSignInModel(
            client: client,
            deviceName: "Apple TV",
            sleep: { duration in sleeps.record(duration) },
            now: now
        )
    }

    func testPollsUntilApprovedThenSignsIn() async {
        script(polls: [(428, Self.pending), (428, Self.pending), (200, TestFixtures.tokenPairJSON(refresh: "r-tv"))])
        let sleeps = SleepLog()
        let model = makeModel(client: makeClient(), sleeps: sleeps)

        await model.start()

        XCTAssertEqual(model.state, .signedIn(User(id: 1, username: "alice", role: "viewer", maxQuality: "high")))
        XCTAssertEqual(pollRequests().count, 3)
        XCTAssertEqual(sleeps.durations, [.seconds(5), .seconds(5), .seconds(5)], "waits `interval` before each poll")
        XCTAssertEqual(store.loadRefreshToken(), "r-tv")
    }

    func testWaitingShowsCodeQRAndLinkOnTheConnectedServer() async {
        let client = makeClient()
        let model = makeModel(client: client)
        var seen: DeviceSignInModel.Code?
        script(polls: [(428, Self.pending), (410, Self.expired)])
        model.onWaiting = { seen = $0 }

        await model.start()

        let code = try? XCTUnwrap(seen)
        XCTAssertEqual(code?.userCode, "BCDF-2345")
        XCTAssertEqual(code?.qrURL.absoluteString, "http://test.bowtie.local:8400/api/v1/auth/device/qr/BCDF2345.png")
        // The server's verifyUrl uses its request host; the TV shows the URL it reached.
        XCTAssertEqual(code?.linkURL.absoluteString, "http://test.bowtie.local:8400/link")
        XCTAssertEqual(code?.expiresAt, Date(timeIntervalSince1970: 1_600))
    }

    func testServerExpiryEndsInExpired() async {
        script(polls: [(428, Self.pending), (410, Self.expired)])
        let model = makeModel(client: makeClient())

        await model.start()

        XCTAssertEqual(model.state, .expired)
        XCTAssertEqual(pollRequests().count, 2)
    }

    func testLocalDeadlineEndsInExpiredWithoutPollingForever() async {
        script(polls: [(428, Self.pending)], start: [Self.codeJSON(expiresIn: 12, interval: 5)])
        let clock = SteppingNow(start: Date(timeIntervalSince1970: 1_000), step: 5)
        let model = makeModel(client: makeClient(), now: { clock.next() })

        await model.start()

        XCTAssertEqual(model.state, .expired)
        XCTAssertLessThanOrEqual(pollRequests().count, 3)
    }

    func testRestartAfterExpiryGetsANewCode() async {
        script(
            polls: [(410, Self.expired), (200, TestFixtures.tokenPairJSON())],
            start: [Self.codeJSON(userCode: "AAAA-1111"), Self.codeJSON(deviceCode: "dev-2", userCode: "BBBB-2222")]
        )
        let model = makeModel(client: makeClient())
        var codes: [String] = []
        model.onWaiting = { codes.append($0.userCode) }

        await model.start()
        XCTAssertEqual(model.state, .expired)

        await model.start()

        XCTAssertEqual(codes, ["AAAA-1111", "BBBB-2222"])
        if case .signedIn = model.state {} else { XCTFail("expected signedIn, got \(model.state)") }
        XCTAssertEqual(try? jsonBody(of: pollRequests().last!)["deviceCode"] as? String, "dev-2")
    }

    func testStartFailureShowsServerMessage() async {
        StubURLProtocol.handler = { _ in (429, Data(#"{"error":"too many sign-ins in progress"}"#.utf8), [:]) }
        let model = makeModel(client: makeClient())

        await model.start()

        XCTAssertEqual(model.state, .failed("Too many sign-ins in progress"))
    }

    func testTransientPollErrorKeepsPolling() async {
        script(polls: [(500, Data(#"{"error":"boom"}"#.utf8)), (200, TestFixtures.tokenPairJSON())])
        let model = makeModel(client: makeClient())

        await model.start()

        if case .signedIn = model.state {} else { XCTFail("expected signedIn, got \(model.state)") }
        XCTAssertEqual(pollRequests().count, 2)
    }

    func testZeroIntervalFallsBackToFiveSeconds() async {
        script(polls: [(200, TestFixtures.tokenPairJSON())], start: [Self.codeJSON(interval: 0)])
        let sleeps = SleepLog()
        let model = makeModel(client: makeClient(), sleeps: sleeps)

        await model.start()

        XCTAssertEqual(sleeps.durations, [.seconds(5)])
    }

    func testCancelStopsPollingAndReturnsToIdle() async {
        script(polls: [(428, Self.pending)])
        let sleeps = SleepLog()
        let model = makeModel(client: makeClient(), sleeps: sleeps)
        sleeps.onSleep = { count in
            if count == 3 { model.cancel() }
        }

        await model.start()

        XCTAssertEqual(model.state, .idle)
        XCTAssertLessThanOrEqual(pollRequests().count, 3)
    }
}

// MARK: - Helpers

private final class ScriptState: @unchecked Sendable {
    private let lock = NSLock()
    private var polls: [(Int, Data)]
    private var starts: [Data]

    init(polls: [(Int, Data)], starts: [Data]) {
        self.polls = polls
        self.starts = starts
    }

    func nextPoll() -> (Int, Data) {
        lock.lock()
        defer { lock.unlock() }
        return polls.count > 1 ? polls.removeFirst() : polls[0]
    }

    func nextStart() -> Data {
        lock.lock()
        defer { lock.unlock() }
        return starts.count > 1 ? starts.removeFirst() : starts[0]
    }
}

@MainActor
final class SleepLog {
    private(set) var durations: [Duration] = []
    var onSleep: ((Int) -> Void)?

    func record(_ duration: Duration) {
        durations.append(duration)
        onSleep?(durations.count)
    }
}

/// `now` that moves forward `step` seconds on every read.
@MainActor
private final class SteppingNow {
    private var current: Date
    private let step: TimeInterval

    init(start: Date, step: TimeInterval) {
        current = start
        self.step = step
    }

    func next() -> Date {
        defer { current = current.addingTimeInterval(step) }
        return current
    }
}
