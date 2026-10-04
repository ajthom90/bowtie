import Foundation

/// Remembered audio language and captions choice; nil = never chosen (leave
/// the player's and the system accessibility defaults alone).
public struct MediaPrefs: Codable, Equatable, Sendable {
    public var audioLanguage: String?
    public var captionsOn: Bool?

    public init(audioLanguage: String? = nil, captionsOn: Bool? = nil) {
        self.audioLanguage = audioLanguage
        self.captionsOn = captionsOn
    }

    public static let defaultsKey = "bowtie.mediaPrefs"

    public static func load(from defaults: UserDefaults = .standard) -> MediaPrefs {
        guard let data = defaults.data(forKey: defaultsKey),
              let prefs = try? JSONDecoder().decode(MediaPrefs.self, from: data) else {
            return MediaPrefs()
        }
        return prefs
    }

    public func save(to defaults: UserDefaults = .standard) {
        if let data = try? JSONEncoder().encode(self) {
            defaults.set(data, forKey: Self.defaultsKey)
        }
    }

    public static func pickIndex(languages: [String?], preferred: String?) -> Int? {
        guard let preferred else { return nil }
        let want = primary(preferred)
        return languages.firstIndex { $0.map(primary) == want }
    }

    private static func primary(_ tag: String) -> String {
        tag.lowercased().split(separator: "-").first.map(String.init) ?? tag.lowercased()
    }
}
