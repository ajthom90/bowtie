import Foundation

/// Pure now/next guide derivation and quality-ladder helpers.
public enum GuideLogic {
    public struct NowNext: Equatable, Sendable {
        public let now: GuideProgram?
        public let next: GuideProgram?

        public init(now: GuideProgram?, next: GuideProgram?) {
            self.now = now
            self.next = next
        }
    }

    private static let qualityLadder = ["original", "high", "medium", "low"]

    /// `now`: first program with `start <= date < stop` (stop exclusive).
    /// `next`: earliest program with `start >= now.stop`, or if there is no now,
    /// the earliest program with `start > date`.
    public static func nowNext(programs: [GuideProgram], at date: Date) -> NowNext {
        let now = programs.first { program in
            program.start <= date && date < program.stop
        }

        let next: GuideProgram?
        if let now {
            next = programs
                .filter { $0.start >= now.stop }
                .min(by: { $0.start < $1.start })
        } else {
            next = programs
                .filter { $0.start > date }
                .min(by: { $0.start < $1.start })
        }

        return NowNext(now: now, next: next)
    }

    /// Profiles the user may select, given their `maxQuality` cap.
    /// - `""` (unlimited) → full ladder
    /// - known rung → that rung and every lower rung
    /// - unknown → full ladder (defensive)
    public static func allowedProfiles(maxQuality: String) -> [String] {
        if maxQuality.isEmpty {
            return qualityLadder
        }
        guard let index = qualityLadder.firstIndex(of: maxQuality) else {
            return qualityLadder
        }
        return Array(qualityLadder[index...])
    }
}
