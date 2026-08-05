import Foundation

#if canImport(UIKit)
import UIKit
#endif

/// Client capability reporting for session negotiation.
///
/// Pure construction lives in `make(maxHeight:)`. Platform probes are isolated
/// behind `#if canImport(UIKit)` so macOS unit tests never need a screen.
public enum Caps {
    /// Pure capability set used by tests and by `current()`.
    /// Always advertises h264+hevc and aac+ac3+eac3; profile is empty (Auto).
    public static func make(maxHeight: Int) -> ClientCaps {
        ClientCaps(
            videoCodecs: ["h264", "hevc"],
            audioCodecs: ["aac", "ac3", "eac3"],
            maxHeight: maxHeight,
            profile: ""
        )
    }

    #if canImport(UIKit)
    /// Thin platform wrapper: maxHeight from the main screen.
    /// tvOS reports 2160 when the display is taller than 1080, otherwise 1080.
    /// iOS/iPadOS always reports 1080 (v1 cap).
    public static func current() -> ClientCaps {
        #if os(tvOS)
        let displayHeight = Int(
            max(UIScreen.main.nativeBounds.width, UIScreen.main.nativeBounds.height)
        )
        let maxHeight = displayHeight > 1080 ? 2160 : 1080
        return make(maxHeight: maxHeight)
        #else
        return make(maxHeight: 1080)
        #endif
    }
    #endif
}
