import Foundation

public enum ServerURL {
    /// Adds `http://` when schemeless, strips trailing `/`, rejects empty/garbage.
    public static func normalize(_ raw: String) -> URL? {
        var trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }

        if !trimmed.contains("://") {
            trimmed = "http://" + trimmed
        }

        guard var components = URLComponents(string: trimmed),
              let host = components.host,
              !host.isEmpty,
              let scheme = components.scheme,
              !scheme.isEmpty
        else {
            return nil
        }

        // Reject schemes that are not http(s) for a media server base.
        let lower = scheme.lowercased()
        guard lower == "http" || lower == "https" else { return nil }

        var path = components.path
        while path.hasSuffix("/") {
            path = String(path.dropLast())
        }
        components.path = path

        return components.url
    }

    /// `GET {url}/healthz` must return HTTP 200 within `timeout`.
    public static func validate(_ url: URL, timeout: TimeInterval = 2) async -> Bool {
        let healthURL = url.appendingPathComponent("healthz")
        var request = URLRequest(url: healthURL)
        request.httpMethod = "GET"
        request.timeoutInterval = timeout

        do {
            let (_, response) = try await URLSession.shared.data(for: request)
            guard let http = response as? HTTPURLResponse else { return false }
            return http.statusCode == 200
        } catch {
            return false
        }
    }

    /// Resolves a server-relative path against `base`, preserving the path's query
    /// (required for HLS `?token=` auth — never drop the token).
    public static func resolve(path: String, against base: URL) -> URL {
        if let resolved = URL(string: path, relativeTo: base) {
            return resolved.absoluteURL
        }
        // Path should always be a valid relative or absolute URL string from the API.
        // Fall back without dropping an absolute path's query when possible.
        if let absolute = URL(string: path), absolute.scheme != nil {
            return absolute
        }
        return base.appendingPathComponent(path)
    }
}
