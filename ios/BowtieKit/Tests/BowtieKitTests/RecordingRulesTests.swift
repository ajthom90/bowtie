import XCTest
@testable import BowtieKit

/// Series recording: `/recording-rules` client calls, the Shows tab and
/// "Record Series" scheduling.
@MainActor
final class RecordingRulesTests: XCTestCase {
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

    private func makeClient() async -> BowtieClient {
        let client = BowtieClient(
            server: TestFixtures.baseURL,
            store: store,
            urlSession: TestFixtures.makeStubSession()
        )
        await client.setAccessTokenForTesting("access-1")
        return client
    }

    nonisolated static func ruleJSON(
        id: Int64 = 4,
        title: String = "Jeopardy!",
        channelId: Int64 = 3,
        channelName: String = "5.1 KSTP",
        canManage: Bool = true
    ) -> String {
        """
        {"id":\(id),"title":"\(title)","seriesId":"SH001","channelId":\(channelId),
         "channelName":"\(channelName)","newOnly":true,"keepLatest":0,"scheduledBy":"andrew",
         "canManage":\(canManage),"createdAt":"2026-10-01T12:00:00Z"}
        """
    }

    nonisolated private static func list(_ items: [String]) -> Data {
        "[\(items.joined(separator: ","))]".data(using: .utf8)!
    }

    private func rules(_ model: RecordingsModel, file: StaticString = #filePath, line: UInt = #line) -> [RecordingRule] {
        guard case .loaded(let rules) = model.rulesState else {
            XCTFail("expected loaded rules, got \(model.rulesState)", file: file, line: line)
            return []
        }
        return rules
    }

    // MARK: - Client

    func testListRules() async throws {
        StubURLProtocol.handler = { _ in (200, Self.list([Self.ruleJSON(), Self.ruleJSON(id: 5, channelId: 0, channelName: "")]), [:]) }
        let client = await makeClient()

        let rules = try await client.recordingRules()

        XCTAssertEqual(rules.map(\.id), [4, 5])
        XCTAssertEqual(rules[0].title, "Jeopardy!")
        XCTAssertEqual(rules[0].channelLabel, "5.1 KSTP")
        XCTAssertEqual(rules[1].channelLabel, "Any channel")
        XCTAssertTrue(rules[0].newOnly)
        XCTAssertEqual(rules[0].createdAt, TestFixtures.iso("2026-10-01T12:00:00Z"))
        let request = try XCTUnwrap(StubURLProtocol.recorded.first)
        XCTAssertEqual(request.httpMethod, "GET")
        XCTAssertEqual(request.url?.path, "/api/v1/recording-rules")
        XCTAssertEqual(request.value(forHTTPHeaderField: "Authorization"), "Bearer access-1")
    }

    func testCreateRuleSendsProgramAndOptions() async throws {
        StubURLProtocol.handler = { _ in (201, Data(#"{"rule":\#(Self.ruleJSON()),"scheduled":3}"#.utf8), [:]) }
        let client = await makeClient()

        let created = try await client.createRecordingRule(
            channelId: 3,
            programStart: TestFixtures.iso("2026-10-05T00:00:00Z"),
            anyChannel: false,
            newOnly: true,
            keepLatest: 0
        )

        XCTAssertEqual(created.scheduled, 3)
        XCTAssertEqual(created.rule.id, 4)
        let request = try XCTUnwrap(StubURLProtocol.recorded.first)
        XCTAssertEqual(request.httpMethod, "POST")
        XCTAssertEqual(request.url?.path, "/api/v1/recording-rules")
        let body = try jsonBody(of: request)
        XCTAssertEqual(body["channelId"] as? Int, 3)
        XCTAssertEqual(body["programStart"] as? String, "2026-10-05T00:00:00Z")
        XCTAssertEqual(body["anyChannel"] as? Bool, false)
        XCTAssertEqual(body["newOnly"] as? Bool, true)
        XCTAssertEqual(body["keepLatest"] as? Int, 0)
    }

    func testDeleteRule() async throws {
        StubURLProtocol.handler = { _ in (204, Data(), [:]) }
        let client = await makeClient()

        try await client.deleteRecordingRule(id: 4)

        let request = try XCTUnwrap(StubURLProtocol.recorded.first)
        XCTAssertEqual(request.httpMethod, "DELETE")
        XCTAssertEqual(request.url?.path, "/api/v1/recording-rules/4")
    }

    // MARK: - Shows tab

    func testSelectShowsLoadsRules() async {
        StubURLProtocol.handler = { _ in (200, Self.list([Self.ruleJSON()]), [:]) }
        let model = RecordingsModel(client: await makeClient())

        await model.select(.shows)

        XCTAssertEqual(model.tab, .shows)
        XCTAssertEqual(rules(model).map(\.id), [4])
        XCTAssertEqual(StubURLProtocol.recorded.map { $0.url?.path }, ["/api/v1/recording-rules"])
    }

    func testRefreshOnShowsReloadsRulesNotRecordings() async {
        StubURLProtocol.handler = { _ in (200, Self.list([Self.ruleJSON()]), [:]) }
        let model = RecordingsModel(client: await makeClient())
        await model.select(.shows)

        await model.load()

        XCTAssertEqual(StubURLProtocol.recorded.count, 2)
        XCTAssertTrue(StubURLProtocol.recorded.allSatisfy { $0.url?.path == "/api/v1/recording-rules" })
    }

    func testNoShowsIsEmpty() async {
        StubURLProtocol.handler = { _ in (200, Data("[]".utf8), [:]) }
        let model = RecordingsModel(client: await makeClient())

        await model.select(.shows)

        XCTAssertEqual(model.rulesState, .empty)
    }

    func testShowsFailure() async {
        StubURLProtocol.handler = { _ in (500, Data(#"{"error":"failed to list rules"}"#.utf8), [:]) }
        let model = RecordingsModel(client: await makeClient())

        await model.select(.shows)

        XCTAssertEqual(model.rulesState, .failed("Failed to list rules"))
    }

    func testStopRecordingShowRemovesIt() async throws {
        StubURLProtocol.handler = { request in
            if request.httpMethod == "DELETE" { return (204, Data(), [:]) }
            return (200, Self.list([Self.ruleJSON(id: 4), Self.ruleJSON(id: 5)]), [:])
        }
        let model = RecordingsModel(client: await makeClient())
        await model.select(.shows)
        let rule = try XCTUnwrap(rules(model).first)

        await model.deleteRule(rule)

        XCTAssertEqual(rules(model).map(\.id), [5])
        XCTAssertNil(model.actionError)
        XCTAssertEqual(StubURLProtocol.recorded.last?.url?.path, "/api/v1/recording-rules/4")
    }

    func testStopRecordingShowForbiddenExplains() async throws {
        StubURLProtocol.handler = { request in
            if request.httpMethod == "DELETE" {
                return (403, Data(#"{"error":"only the person who set this up (or an admin) can stop it"}"#.utf8), [:])
            }
            return (200, Self.list([Self.ruleJSON(id: 4)]), [:])
        }
        let model = RecordingsModel(client: await makeClient())
        await model.select(.shows)
        let rule = try XCTUnwrap(rules(model).first)

        await model.deleteRule(rule)

        XCTAssertEqual(rules(model).map(\.id), [4])
        XCTAssertEqual(model.actionError, "Only the person who set this up (or an admin) can stop it")
    }

    func testLeavingShowsLoadsRecordingsAgain() async {
        StubURLProtocol.handler = { request in
            if request.url?.path == "/api/v1/recording-rules" {
                return (200, Self.list([Self.ruleJSON()]), [:])
            }
            return (200, RecordingFixtures.listJSON([RecordingFixtures.json(id: 9)]), [:])
        }
        let model = RecordingsModel(client: await makeClient())
        await model.select(.shows)

        await model.select(.recorded)

        guard case .loaded(let rows) = model.state else {
            return XCTFail("expected recordings, got \(model.state)")
        }
        XCTAssertEqual(rows.map(\.id), [9])
    }

    // MARK: - Record Series

    func testScheduleSeriesDefaultsToThisChannelNewEpisodes() async throws {
        StubURLProtocol.handler = { _ in (201, Data(#"{"rule":\#(Self.ruleJSON()),"scheduled":3}"#.utf8), [:]) }

        let outcome = await RecordingScheduler.scheduleSeries(
            client: await makeClient(),
            channelId: 3,
            programStart: TestFixtures.iso("2026-10-05T00:00:00Z")
        )

        guard case .scheduled(let rule, let count) = outcome else {
            return XCTFail("expected scheduled, got \(outcome)")
        }
        XCTAssertEqual(rule.id, 4)
        XCTAssertEqual(count, 3)
        let body = try jsonBody(of: XCTUnwrap(StubURLProtocol.recorded.first))
        XCTAssertEqual(body["anyChannel"] as? Bool, false)
        XCTAssertEqual(body["newOnly"] as? Bool, true)
        XCTAssertEqual(body["keepLatest"] as? Int, 0)
    }

    func testScheduleSeriesMissingProgram() async {
        StubURLProtocol.handler = { _ in (404, Data(#"{"error":"program not found"}"#.utf8), [:]) }

        let outcome = await RecordingScheduler.scheduleSeries(
            client: await makeClient(),
            channelId: 3,
            programStart: TestFixtures.iso("2026-10-05T00:00:00Z")
        )

        XCTAssertEqual(outcome, .failed("That program is no longer in the guide."))
    }

    func testSeriesScheduledCopy() {
        XCTAssertEqual(RecordingLogic.seriesScheduledTitle(count: 3), "Scheduled 3 episodes")
        XCTAssertEqual(RecordingLogic.seriesScheduledTitle(count: 1), "Scheduled 1 episode")
        XCTAssertEqual(RecordingLogic.seriesScheduledTitle(count: 0), "Series Recording Set")

        let message = RecordingLogic.seriesScheduledMessage(title: "Jeopardy!", channelName: "5.1 KSTP")
        XCTAssertTrue(message.contains("Jeopardy!"), message)
        XCTAssertTrue(message.contains("5.1 KSTP"), message)
        let any = RecordingLogic.seriesScheduledMessage(title: "Jeopardy!", channelName: "")
        XCTAssertTrue(any.contains("any channel"), any)
    }
}
