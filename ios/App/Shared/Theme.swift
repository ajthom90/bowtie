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

    /// Channel numbers — SF Condensed, weight 700. Signature look: oversized.
    public static func channelNumber(_ size: CGFloat) -> Font {
        .system(size: size, weight: .bold).width(.condensed)
    }

    /// Readouts / technical values — SF Mono.
    public static func mono(_ size: CGFloat) -> Font {
        .system(size: size, design: .monospaced)
    }

    /// Primary body copy.
    public static func body(_ size: CGFloat = 17) -> Font {
        .system(size: size, weight: .regular)
    }

    /// Section / control labels.
    public static func label(_ size: CGFloat = 15) -> Font {
        .system(size: size, weight: .medium)
    }

    /// Screen titles.
    public static func title(_ size: CGFloat = 22) -> Font {
        .system(size: size, weight: .semibold)
    }

    // MARK: - Layout

    public static let cornerRadius: CGFloat = 10
    public static let fieldPadding: CGFloat = 14
    /// Default channel-row guide number size.
    public static let channelNumberSize: CGFloat = 28
}

// MARK: - Themed field chrome (keyboard-visible focus ring)

struct ThemedFieldModifier: ViewModifier {
    let isFocused: Bool

    func body(content: Content) -> some View {
        content
            .font(Theme.body())
            .foregroundStyle(Theme.text)
            .padding(Theme.fieldPadding)
            .background(Theme.surface)
            .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous)
                    .stroke(isFocused ? Theme.amber : Theme.line, lineWidth: isFocused ? 2 : 1)
            )
    }
}

extension View {
    /// Text field surface with amber focus ring when the keyboard/focus is active.
    func themedField(focused: Bool) -> some View {
        modifier(ThemedFieldModifier(isFocused: focused))
    }

    /// Full-screen charcoal background used by auth + list shells.
    func bowtieScreenBackground() -> some View {
        frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(Theme.bg.ignoresSafeArea())
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
