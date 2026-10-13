import SwiftUI
import BowtieKit

// MARK: - Series rule row (Recordings → Shows; `large` for tvOS)

struct RecordingRuleRowView: View {
    let rule: RecordingRule
    var large = false

    private var scale: CGFloat { large ? 1.6 : 1 }

    var body: some View {
        HStack(alignment: .center, spacing: 14 * scale) {
            Image(systemName: "square.stack.3d.down.right")
                .font(.system(size: 18 * scale))
                .foregroundStyle(Theme.amber)
                .accessibilityHidden(true)

            VStack(alignment: .leading, spacing: 4 * scale) {
                Text(rule.title.isEmpty ? "Untitled" : rule.title)
                    .font(Theme.label(17 * scale))
                    .foregroundStyle(Theme.text)
                    .lineLimit(1)
                Text(rule.detailLine)
                    .font(Theme.body(13 * scale))
                    .foregroundStyle(Theme.dim)
                    .lineLimit(1)
            }

            Spacer(minLength: 0)
        }
        .padding(.vertical, 8 * scale)
        .contentShape(Rectangle())
    }

    /// One VoiceOver phrase for the row.
    var accessibilityText: String {
        [rule.title, "Series", rule.detailLine].joined(separator: ", ")
    }
}

/// "Stop Recording This Show" confirmation, shared by the three Recordings screens.
extension View {
    func stopShowDialog(rule: Binding<RecordingRule?>, onStop: @escaping (RecordingRule) -> Void) -> some View {
        confirmationDialog(
            "Stop recording this show?",
            isPresented: Binding(
                get: { rule.wrappedValue != nil },
                set: { if !$0 { rule.wrappedValue = nil } }
            ),
            titleVisibility: .visible,
            presenting: rule.wrappedValue
        ) { rule in
            Button("Stop Recording \u{201C}\(rule.title)\u{201D}", role: .destructive) {
                onStop(rule)
            }
            Button("Cancel", role: .cancel) {}
        } message: { _ in
            Text("Its upcoming episodes are cancelled. Episodes already recorded stay.")
        }
    }
}

// MARK: - Guide search result row (`large` for tvOS)

struct SearchResultRowView: View {
    let result: GuideSearchResult
    let now: Date
    var large = false

    private var scale: CGFloat { large ? 1.6 : 1 }

    var body: some View {
        HStack(alignment: .center, spacing: 14 * scale) {
            VStack(alignment: .leading, spacing: 4 * scale) {
                titleLine
                if !result.subtitle.isEmpty {
                    Text(result.subtitle)
                        .font(Theme.body(14 * scale))
                        .foregroundStyle(Theme.text.opacity(0.85))
                        .lineLimit(1)
                }
                Text(whenLine)
                    .font(Theme.body(13 * scale))
                    .foregroundStyle(Theme.dim)
                    .lineLimit(1)
            }

            Spacer(minLength: 0)

            if result.isOnNow(at: now) {
                Text("On Now")
                    .font(Theme.label(12 * scale))
                    .foregroundStyle(Theme.signal)
                    .padding(.horizontal, 8 * scale)
                    .padding(.vertical, 4 * scale)
                    .background(Theme.signal.opacity(0.15))
                    .clipShape(Capsule())
                    .fixedSize()
            }
        }
        .padding(.vertical, 8 * scale)
        .contentShape(Rectangle())
    }

    private var titleLine: some View {
        HStack(spacing: 8 * scale) {
            Text(result.title.isEmpty ? "Untitled" : result.title)
                .font(Theme.label(17 * scale))
                .foregroundStyle(Theme.text)
                .lineLimit(1)
            if result.recording != nil {
                RecordingMarkDot(size: 10 * scale)
            }
            if result.isLocked {
                ParentalLockMark(rating: result.rating ?? "", size: 12 * scale)
            }
        }
    }

    /// "5.1 KSTP · Sun, Oct 5, 7:00 PM"
    var whenLine: String {
        let time = result.start.formatted(.dateTime.weekday(.abbreviated).month(.abbreviated).day().hour().minute())
        return "\(result.guideNumber) \(result.channelName) · \(time)"
    }

    /// One VoiceOver phrase for the row.
    var accessibilityText: String {
        var parts = [result.title]
        if !result.subtitle.isEmpty {
            parts.append(result.subtitle)
        }
        parts.append(whenLine)
        if result.isOnNow(at: now) {
            parts.append("On now")
        }
        if result.recording != nil {
            parts.append("Set to record")
        }
        if result.isLocked {
            parts.append(ParentalLockMark.accessibilityText(rating: result.rating ?? ""))
        }
        return parts.joined(separator: ", ")
    }
}

/// Watch (when on now), Record, Record Series for one search result: the
/// context menu and action sheet items on every platform.
struct SearchResultActions: View {
    let result: GuideSearchResult
    let now: Date
    let flow: RecordFlow?
    let onWatch: (Channel) -> Void
    let openRecordings: () -> Void

    var body: some View {
        if result.canWatch(at: now) {
            Button {
                onWatch(result.channel)
            } label: {
                Label("Watch", systemImage: "play.fill")
            }
        }
        if result.recording != nil {
            Button {
                openRecordings()
            } label: {
                Label("Recording Scheduled", systemImage: "checkmark.circle")
            }
        } else {
            Button {
                flow?.record(channel: result.channel, program: result.program)
            } label: {
                Label("Record", systemImage: "record.circle")
            }
            .disabled(flow == nil)
        }
        RecordSeriesButton(channel: result.channel, program: result.program, flow: flow)
    }
}
