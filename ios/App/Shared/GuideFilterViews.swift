import SwiftUI
import BowtieKit

/// Category chips (All · Sports · Movies · News · Kids · New) above the
/// channel list. Each chip is a toggle: VoiceOver reads the chosen one as
/// "Selected".
struct GuideFilterBar: View {
    @Binding var selection: GuideFilter

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: spacing) {
                ForEach(GuideFilter.allCases) { filter in
                    chip(filter)
                }
            }
            .padding(.horizontal, horizontalPadding)
            .padding(.vertical, verticalPadding)
        }
        #if os(tvOS)
        // Room for the focus lift.
        .scrollClipDisabled()
        #endif
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Show programs")
    }

    private func chip(_ filter: GuideFilter) -> some View {
        let on = selection == filter
        return Button {
            selection = filter
        } label: {
            #if os(tvOS)
            // System button style gives the focus platter; amber + check marks the choice.
            HStack(spacing: 8) {
                if on {
                    Image(systemName: "checkmark")
                        .accessibilityHidden(true)
                }
                Text(filter.label)
            }
            .font(Theme.label(24))
            .foregroundStyle(on ? Theme.amber : Theme.text)
            #else
            Text(filter.label)
                .font(Theme.label(fontSize))
                .foregroundStyle(on ? Theme.bg : Theme.dim)
                .padding(.horizontal, 14)
                .padding(.vertical, chipVerticalPadding)
                .background(on ? Theme.amber : Color.clear, in: Capsule())
                .overlay(Capsule().stroke(on ? Theme.amber : Theme.line, lineWidth: 1))
                .contentShape(Capsule())
            #endif
        }
        #if !os(tvOS)
        .buttonStyle(.plain)
        #endif
        .accessibilityLabel(filter.label)
        .accessibilityAddTraits(on ? [.isSelected] : [])
        .accessibilityHint(on ? "" : "Show only \(filter == .all ? "every channel" : filter.label.lowercased())")
    }

    #if os(tvOS)
    private let spacing: CGFloat = 24
    private let horizontalPadding: CGFloat = 80
    private let verticalPadding: CGFloat = 16
    #elseif os(macOS)
    private let spacing: CGFloat = 6
    private let horizontalPadding: CGFloat = 10
    private let verticalPadding: CGFloat = 6
    private let fontSize: CGFloat = 12
    private let chipVerticalPadding: CGFloat = 4
    #else
    private let spacing: CGFloat = 8
    private let horizontalPadding: CGFloat = 16
    private let verticalPadding: CGFloat = 8
    private let fontSize: CGFloat = 14
    // 44pt touch target with the vertical padding above.
    private let chipVerticalPadding: CGFloat = 8
    #endif
}

/// "No sports on in this time window" with a way back to every channel.
struct GuideFilterEmptyView: View {
    let filter: GuideFilter
    let onShowAll: () -> Void

    var body: some View {
        VStack(spacing: 16) {
            Text(filter.emptyCopy)
                .font(Theme.body(textSize))
                .foregroundStyle(Theme.dim)
                .multilineTextAlignment(.center)
            Button("Show all channels", action: onShowAll)
                .font(Theme.label(textSize))
                .foregroundStyle(Theme.amber)
        }
        .padding(32)
        .frame(maxWidth: .infinity)
    }

    #if os(tvOS)
    private let textSize: CGFloat = 26
    #elseif os(macOS)
    private let textSize: CGFloat = 13
    #else
    private let textSize: CGFloat = 16
    #endif
}

/// "Later: Title · 8:00 PM" when neither now nor next matches the filter.
struct GuideFilterLaterLine: View {
    let program: GuideProgram
    let size: CGFloat

    var body: some View {
        Text("Later: \(program.title) · \(program.start.formatted(date: .omitted, time: .shortened))")
            .font(Theme.body(size))
            .foregroundStyle(Theme.amber)
            .lineLimit(1)
    }

    static func accessibilityText(_ program: GuideProgram) -> String {
        "Later \(program.title) at \(program.start.formatted(date: .omitted, time: .shortened))"
    }
}
