import Foundation
import XCTest
import BowtieKit

// MARK: - URLProtocol stub (mirrors BowtieKitTests/TestSupport for SharedTests)

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

// MARK: - Fixtures

enum SharedFixtures {
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

    static func createdSessionJSON(
        viewerId: String = "viewer-1",
        playlistUrl: String = "/api/v1/stream/viewer-1/index.m3u8?token=abc",
        channelName: String = "WABC"
    ) -> Data {
        """
        {
          "viewerId": "\(viewerId)",
          "playlistUrl": "\(playlistUrl)",
          "session": {
            "videoCodec": "h264",
            "profile": "high",
            "backend": "ffmpeg",
            "channelName": "\(channelName)"
          }
        }
        """.data(using: .utf8)!
    }

    static let sampleChannel = Channel(
        id: 10,
        guideNumber: "4.1",
        name: "WABC",
        logoUrl: ""
    )

    static let sampleChannel2 = Channel(
        id: 20,
        guideNumber: "7.1",
        name: "WXYZ",
        logoUrl: ""
    )

    static let sampleCaps = ClientCaps(
        videoCodecs: ["h264", "hevc"],
        audioCodecs: ["aac", "ac3", "eac3"],
        maxHeight: 1080,
        profile: ""
    )
}

// MARK: - Request body helpers

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
            throw NSError(
                domain: "test",
                code: 1,
                userInfo: [NSLocalizedDescriptionKey: "missing body"]
            )
        }
        let obj = try JSONSerialization.jsonObject(with: data)
        guard let dict = obj as? [String: Any] else {
            throw NSError(
                domain: "test",
                code: 2,
                userInfo: [NSLocalizedDescriptionKey: "body not object"]
            )
        }
        return dict
    }

    func sessionCreateRequests() -> [URLRequest] {
        StubURLProtocol.recorded.filter {
            $0.httpMethod == "POST" && $0.url?.path == "/api/v1/sessions"
        }
    }

    func sessionDeleteRequests() -> [URLRequest] {
        StubURLProtocol.recorded.filter {
            $0.httpMethod == "DELETE" && ($0.url?.path.hasPrefix("/api/v1/sessions/") ?? false)
        }
    }
}
