import XCTest
@testable import BowtieKit

final class ModelsTests: XCTestCase {
    private var decoder: JSONDecoder!

    override func setUp() {
        super.setUp()
        decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
    }

    func testDecodeTokenPair() throws {
        let json = """
        {
          "accessToken": "access-xyz",
          "refreshToken": "refresh-abc",
          "user": {
            "id": 42,
            "username": "alice",
            "role": "viewer",
            "maxQuality": "high"
          }
        }
        """.data(using: .utf8)!

        let pair = try decoder.decode(TokenPair.self, from: json)
        XCTAssertEqual(pair.accessToken, "access-xyz")
        XCTAssertEqual(pair.refreshToken, "refresh-abc")
        XCTAssertEqual(pair.user.id, 42)
        XCTAssertEqual(pair.user.username, "alice")
        XCTAssertEqual(pair.user.role, "viewer")
        XCTAssertEqual(pair.user.maxQuality, "high")
    }

    func testDecodeGuideChannelRFC3339Dates() throws {
        let json = """
        {
          "channelId": 7,
          "guideNumber": "4.1",
          "name": "WABC",
          "logoUrl": "https://example.com/wabc.png",
          "programs": [
            {
              "start": "2024-06-15T20:00:00Z",
              "stop": "2024-06-15T21:00:00Z",
              "title": "Evening News",
              "subtitle": "Local",
              "description": "Nightly broadcast",
              "category": "News"
            }
          ]
        }
        """.data(using: .utf8)!

        let channel = try decoder.decode(GuideChannel.self, from: json)
        XCTAssertEqual(channel.channelId, 7)
        XCTAssertEqual(channel.guideNumber, "4.1")
        XCTAssertEqual(channel.name, "WABC")
        XCTAssertEqual(channel.logoUrl, "https://example.com/wabc.png")
        XCTAssertEqual(channel.programs.count, 1)

        let program = channel.programs[0]
        XCTAssertEqual(program.title, "Evening News")
        XCTAssertEqual(program.subtitle, "Local")
        XCTAssertEqual(program.description, "Nightly broadcast")
        XCTAssertEqual(program.category, "News")

        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(secondsFromGMT: 0)!
        let start = calendar.date(from: DateComponents(year: 2024, month: 6, day: 15, hour: 20))!
        let stop = calendar.date(from: DateComponents(year: 2024, month: 6, day: 15, hour: 21))!
        XCTAssertEqual(program.start, start)
        XCTAssertEqual(program.stop, stop)
    }

    func testDecodeCreatedSessionWithSessionMeta() throws {
        let json = """
        {
          "viewerId": "vid-1",
          "playlistUrl": "/api/v1/stream/vid-1/index.m3u8?token=tok",
          "session": {
            "videoCodec": "h264",
            "profile": "high",
            "backend": "videotoolbox",
            "channelName": "WABC"
          }
        }
        """.data(using: .utf8)!

        let created = try decoder.decode(CreatedSession.self, from: json)
        XCTAssertEqual(created.viewerId, "vid-1")
        XCTAssertEqual(created.playlistUrl, "/api/v1/stream/vid-1/index.m3u8?token=tok")
        XCTAssertEqual(created.session?.videoCodec, "h264")
        XCTAssertEqual(created.session?.profile, "high")
        XCTAssertEqual(created.session?.backend, "videotoolbox")
        XCTAssertEqual(created.session?.channelName, "WABC")
    }

    func testDecodeCreatedSessionWithoutSession() throws {
        let json = """
        {
          "viewerId": "vid-2",
          "playlistUrl": "/api/v1/stream/vid-2/index.m3u8?token=xyz"
        }
        """.data(using: .utf8)!

        let created = try decoder.decode(CreatedSession.self, from: json)
        XCTAssertEqual(created.viewerId, "vid-2")
        XCTAssertEqual(created.playlistUrl, "/api/v1/stream/vid-2/index.m3u8?token=xyz")
        XCTAssertNil(created.session)
    }

    func testDecodeChannel() throws {
        let json = """
        {
          "id": 3,
          "guideNumber": "7.1",
          "name": "WXYZ",
          "logoUrl": ""
        }
        """.data(using: .utf8)!

        let channel = try decoder.decode(Channel.self, from: json)
        XCTAssertEqual(channel.id, 3)
        XCTAssertEqual(channel.guideNumber, "7.1")
        XCTAssertEqual(channel.name, "WXYZ")
        XCTAssertEqual(channel.logoUrl, "")
    }

    func testDecodeActiveSessionSummary() throws {
        // Wire shape may include extra SessionInfo fields; we only model the summary.
        let json = """
        {
          "channelName": "WABC",
          "viewers": [
            { "username": "bob" },
            { "username": "carol" }
          ]
        }
        """.data(using: .utf8)!

        let summary = try decoder.decode(ActiveSessionSummary.self, from: json)
        XCTAssertEqual(summary.channelName, "WABC")
        XCTAssertEqual(summary.viewers.map(\.username), ["bob", "carol"])
    }

    func testBowtieErrorEquatable() {
        XCTAssertEqual(BowtieError.unauthorized, BowtieError.unauthorized)
        XCTAssertEqual(BowtieError.notFound, BowtieError.notFound)
        XCTAssertEqual(BowtieError.invalidServerURL, BowtieError.invalidServerURL)
        XCTAssertEqual(
            BowtieError.negotiationFailed("no"),
            BowtieError.negotiationFailed("no")
        )
        XCTAssertNotEqual(
            BowtieError.server(status: 500, message: "x"),
            BowtieError.server(status: 502, message: "x")
        )
        XCTAssertEqual(
            BowtieError.tunersBusy([]),
            BowtieError.tunersBusy([])
        )
    }
}
