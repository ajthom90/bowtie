import SwiftUI
import BowtieKit

/// Compact guide search result for the sidebar.
struct MacSearchResultRow: View {
    let result: GuideSearchResult
    let now: Date

    var body: some View {
        VStack(alignment: .leading, spacing: 1) {
            HStack(spacing: 4) {
                Text(result.title.isEmpty ? "Untitled" : result.title)
                    .font(Theme.label(13))
                    .lineLimit(1)
                if result.recording != nil {
                    RecordingMarkDot(size: 8)
                }
                if result.isLocked {
                    ParentalLockMark(rating: result.rating ?? "", size: 9)
                }
            }
            if !result.subtitle.isEmpty {
                Text(result.subtitle)
                    .font(Theme.body(11))
                    .foregroundStyle(Theme.text.opacity(0.85))
                    .lineLimit(1)
            }
            Text(timeLine)
                .font(Theme.body(11))
                .foregroundStyle(result.isOnNow(at: now) ? Theme.signal : Theme.dim)
                .lineLimit(1)
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel(SearchResultRowView(result: result, now: now).accessibilityText)
    }

    /// "5.1 KSTP · On now" or "5.1 KSTP · Sun 7:00 PM"
    private var timeLine: String {
        let when = result.isOnNow(at: now)
            ? "On now"
            : result.start.formatted(.dateTime.weekday(.abbreviated).hour().minute())
        return "\(result.guideNumber) \(result.channelName) · \(when)"
    }
}

/// The selected search result in the detail pane: what it is, and Watch /
/// Record / Record Series.
struct MacSearchResultDetail: View {
    let result: GuideSearchResult
    let now: Date
    let flow: RecordFlow?
    let onWatch: (Channel) -> Void
    let openRecordings: () -> Void

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                header
                if !result.isLocked, !result.description.isEmpty {
                    Text(result.description)
                        .font(Theme.body(14))
                        .foregroundStyle(Theme.text.opacity(0.9))
                        .fixedSize(horizontal: false, vertical: true)
                        .textSelection(.enabled)
                }
                actionButtons
            }
            .padding(28)
            .frame(maxWidth: 640, alignment: .leading)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .bowtieScreenBackground()
        .navigationTitle(result.title)
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                Text(result.title.isEmpty ? "Untitled" : result.title)
                    .font(Theme.title(24))
                    .foregroundStyle(Theme.text)
                if result.isLocked {
                    ParentalLockMark(rating: result.rating ?? "", size: 14)
                }
            }
            if !result.subtitle.isEmpty {
                Text(result.subtitle)
                    .font(Theme.label(16))
                    .foregroundStyle(Theme.text.opacity(0.85))
            }
            Text(whenLine)
                .font(Theme.body(13))
                .foregroundStyle(Theme.dim)
            if result.recording != nil {
                RecordingMarkDot(size: 11)
            }
        }
    }

    /// "5.1 KSTP · Sun, Oct 5, 7:00 – 7:30 PM"
    private var whenLine: String {
        let start = result.start.formatted(.dateTime.weekday(.abbreviated).month(.abbreviated).day().hour().minute())
        let stop = result.stop.formatted(.dateTime.hour().minute())
        var parts = ["\(result.guideNumber) \(result.channelName)", "\(start) – \(stop)"]
        if !result.category.isEmpty {
            parts.append(result.category)
        }
        return parts.joined(separator: " · ")
    }

    private var actionButtons: some View {
        HStack(spacing: 10) {
            if result.canWatch(at: now) {
                Button {
                    onWatch(result.channel)
                } label: {
                    Label("Watch", systemImage: "play.fill")
                }
                .keyboardShortcut(.defaultAction)
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
        .controlSize(.large)
        .padding(.top, 6)
    }
}
