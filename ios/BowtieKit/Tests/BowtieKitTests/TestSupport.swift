import Foundation
import XCTest
@testable import BowtieKit

// MARK: - URLProtocol stub (shared by BowtieClient + ChannelListModel tests)

/// Records requests and returns scripted responses.
final class StubURLProtocol: URLProtocol, @unchecked Sendable {
    nonisolated(unsafe) static var recorded: [URLRequest] = []
    nonisolated(unsafe) static var handler: (@Sendable (URLRequest) throws -> (Int, Data, [String: String]))?

    static func reset() {
        recorded = []
        handler = nil
    }

    override class func canInit(with request: URLRequest) -> Bool { true }

    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        Self.recorded.append(request)
        do {
            guard let handler = Self.handler else {
                throw URLError(.badServerResponse)
            }
            let (status, data, headers) = try handler(request)
            let response = HTTPURLResponse(
                url: request.url!,
                statusCode: status,
                httpVersion: "HTTP/1.1",
                headerFields: headers.merging(["Content-Type": "application/json"]) { _, new in new }
            )!
            client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: data)
            client?.urlProtocolDidFinishLoading(self)
        } catch {
            client?.urlProtocol(self, didFailWithError: error)
        }
    }

    override func stopLoading() {}
}

// MARK: - Shared client test helpers

enum TestFixtures {
    static let baseURL = URL(string: "http://test.bowtie.local:8400")!

    static func makeStubSession() -> URLSession {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [StubURLProtocol.self]
        return URLSession(configuration: config)
    }

    static func tokenPairJSON(
        access: String = "access-1",
        refresh: String = "refresh-1"
    ) -> Data {
        """
        {
          "accessToken": "\(access)",
          "refreshToken": "\(refresh)",
          "user": {
            "id": 1,
            "username": "alice",
            "role": "viewer",
            "maxQuality": "high"
          }
        }
        """.data(using: .utf8)!
    }

    static let sampleUserJSON = """
    {
      "id": 1,
      "username": "alice",
      "role": "viewer",
      "maxQuality": "high"
    }
    """

    static func iso(_ string: String) -> Date {
        ISO8601DateFormatter().date(from: string)!
    }

    static func program(
        start: String,
        stop: String,
        title: String,
        subtitle: String = "",
        description: String = "",
        category: String = ""
    ) -> GuideProgram {
        GuideProgram(
            start: iso(start),
            stop: iso(stop),
            title: title,
            subtitle: subtitle,
            description: description,
            category: category
        )
    }

    static func channelJSON(_ channels: [(id: Int64, number: String, name: String)]) -> Data {
        let items = channels.map { ch in
            """
            {"id":\(ch.id),"guideNumber":"\(ch.number)","name":"\(ch.name)","logoUrl":""}
            """
        }.joined(separator: ",")
        return "[\(items)]".data(using: .utf8)!
    }

    static func guideJSON(
        _ entries: [(channelId: Int64, number: String, name: String, programs: [(start: String, stop: String, title: String)])]
    ) -> Data {
        let channels = entries.map { e in
            let progs = e.programs.map { p in
                """
                {
                  "start":"\(p.start)",
                  "stop":"\(p.stop)",
                  "title":"\(p.title)",
                  "subtitle":"",
                  "description":"",
                  "category":""
                }
                """
            }.joined(separator: ",")
            return """
            {
              "channelId":\(e.channelId),
              "guideNumber":"\(e.number)",
              "name":"\(e.name)",
              "logoUrl":"",
              "programs":[\(progs)]
            }
            """
        }.joined(separator: ",")
        return "[\(channels)]".data(using: .utf8)!
    }
}

extension XCTestCase {
    func jsonBody(of request: URLRequest) throws -> [String: Any] {
        guard let data = request.httpBody ?? request.httpBodyStream.flatMap({ stream in
            let buffer = NSMutableData()
            let s = stream
            s.open()
            defer { s.close() }
            let chunk = UnsafeMutablePointer<UInt8>.allocate(capacity: 1024)
            defer { chunk.deallocate() }
            while s.hasBytesAvailable {
                let n = s.read(chunk, maxLength: 1024)
                if n > 0 { buffer.append(chunk, length: n) }
                else { break }
            }
            return buffer as Data
        }) else {
            throw NSError(domain: "test", code: 1, userInfo: [NSLocalizedDescriptionKey: "missing body"])
        }
        let obj = try JSONSerialization.jsonObject(with: data)
        guard let dict = obj as? [String: Any] else {
            throw NSError(domain: "test", code: 2, userInfo: [NSLocalizedDescriptionKey: "body not object"])
        }
        return dict
    }
}
