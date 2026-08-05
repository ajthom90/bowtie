import SwiftUI

/// Broadcast design tokens shared by iOS and tvOS.
public enum Theme {
    // MARK: - Colors

    public static let bg = Color(hex: 0x101418)
    public static let surface = Color(hex: 0x1A2027)
    public static let raised = Color(hex: 0x232B34)
    public static let line = Color(hex: 0x2E3843)
    public static let text = Color(hex: 0xF2EFE8)
    public static let dim = Color(hex: 0x9BA5AE)
    public static let amber = Color(hex: 0xF0A428)
    public static let signal = Color(hex: 0x5DBB63)
    public static let alert = Color(hex: 0xE4574B)

    // MARK: - Fonts

    /// Channel numbers — SF Condensed, weight 700.
    public static func channelNumber(_ size: CGFloat) -> Font {
        .system(size: size, weight: .bold).width(.condensed)
    }

    /// Readouts / technical values — SF Mono.
    public static func mono(_ size: CGFloat) -> Font {
        .system(size: size, design: .monospaced)
    }
}

// MARK: - Color hex helper

private extension Color {
    init(hex: UInt32, opacity: Double = 1) {
        let r = Double((hex >> 16) & 0xFF) / 255
        let g = Double((hex >> 8) & 0xFF) / 255
        let b = Double(hex & 0xFF) / 255
        self.init(.sRGB, red: r, green: g, blue: b, opacity: opacity)
    }
}
