import Foundation

public enum PairingParse: Equatable {
    case incomplete
    case complete(HTTPRequest)
    case tooLarge
    case bad
}

/// Minimal HTTP/1.1 request parser for the pairing page: request line, headers,
/// and an urlencoded body sized by `Content-Length`. Callers keep appending reads
/// until the result is no longer `.incomplete`.
public struct HTTPRequest: Equatable, Sendable {
    public let method: String
    public let path: String
    public let query: [String: String]
    public let form: [String: String]

    public static func parse(_ data: Data, limit: Int = 8192) -> PairingParse {
        if data.count > limit { return .tooLarge }
        let separator = Data("\r\n\r\n".utf8)
        guard let end = data.range(of: separator) else { return .incomplete }

        let head = String(decoding: data[data.startIndex..<end.lowerBound], as: UTF8.self)
        var lines = head.components(separatedBy: "\r\n")
        let requestLine = lines.removeFirst().split(separator: " ", omittingEmptySubsequences: true)
        guard requestLine.count == 3, requestLine[2].hasPrefix("HTTP/") else { return .bad }

        var contentLength: Int?
        for line in lines {
            guard let colon = line.firstIndex(of: ":") else { continue }
            let name = line[..<colon].trimmingCharacters(in: .whitespaces)
            guard name.caseInsensitiveCompare("Content-Length") == .orderedSame else { continue }
            let value = line[line.index(after: colon)...].trimmingCharacters(in: .whitespaces)
            // Plain digits only (no sign); an absurdly long run overflows Int and is too large.
            guard !value.isEmpty, value.utf8.allSatisfy({ $0 >= 48 && $0 <= 57 }) else { return .bad }
            let n = Int(value) ?? Int.max
            if let existing = contentLength, existing != n { return .bad }
            contentLength = n
        }
        let length = contentLength ?? 0
        if length > limit { return .tooLarge }

        let bodyStart = end.upperBound
        if bodyStart - data.startIndex + length > limit { return .tooLarge }
        if data.endIndex - bodyStart < length { return .incomplete }
        let body = String(decoding: data[bodyStart..<(bodyStart + length)], as: UTF8.self)

        let target = String(requestLine[1])
        let parts = target.split(separator: "?", maxSplits: 1, omittingEmptySubsequences: false)
        return .complete(HTTPRequest(
            method: String(requestLine[0]),
            path: String(parts[0]),
            query: parts.count > 1 ? decodeForm(String(parts[1])) : [:],
            form: decodeForm(body)
        ))
    }

    /// `application/x-www-form-urlencoded`: `+` is a space, `%XX` decoded once as UTF-8.
    static func decodeForm(_ s: String) -> [String: String] {
        var out: [String: String] = [:]
        for pair in s.split(separator: "&") {
            let kv = pair.split(separator: "=", maxSplits: 1, omittingEmptySubsequences: false)
            let key = decode(String(kv[0]))
            if out[key] == nil { out[key] = kv.count > 1 ? decode(String(kv[1])) : "" }
        }
        return out
    }

    private static func decode(_ s: String) -> String {
        var bytes: [UInt8] = []
        let src = Array(s.utf8)
        var i = 0
        while i < src.count {
            let c = src[i]
            if c == UInt8(ascii: "+") {
                bytes.append(UInt8(ascii: " "))
            } else if c == UInt8(ascii: "%"), i + 2 < src.count, let hi = hex(src[i + 1]), let lo = hex(src[i + 2]) {
                bytes.append(hi << 4 | lo)
                i += 2
            } else {
                bytes.append(c)
            }
            i += 1
        }
        return String(decoding: bytes, as: UTF8.self)
    }

    private static func hex(_ b: UInt8) -> UInt8? {
        switch b {
        case 48...57: return b - 48
        case 97...102: return b - 87
        case 65...70: return b - 55
        default: return nil
        }
    }
}
