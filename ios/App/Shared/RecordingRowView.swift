import SwiftUI
import BowtieKit

// MARK: - Recording row (shared look; `large` for tvOS)

struct RecordingRowView: View {
    let recording: Recording
    var large = false

    private var scale: CGFloat { large ? 1.6 : 1 }

    var body: some View {
        HStack(alignment: .center, spacing: 14 * scale) {
            VStack(alignment: .leading, spacing: 4 * scale) {
                HStack(spacing: 8 * scale) {
                    Text(recording.title.isEmpty ? "Untitled" : recording.title)
                        .font(Theme.label(17 * scale))
                        .foregroundStyle(Theme.text)
                        .lineLimit(1)
                    marks
                }

                if !recording.subtitle.isEmpty {
                    Text(recording.subtitle)
                        .font(Theme.body(14 * scale))
                        .foregroundStyle(Theme.text.opacity(0.85))
                        .lineLimit(1)
                }

                Text(whenLine)
                    .font(Theme.body(13 * scale))
                    .foregroundStyle(Theme.dim)
                    .lineLimit(1)

                if let detail = RecordingLogic.detailLine(recording) {
                    Text(detail)
                        .font(Theme.body(13 * scale))
                        .foregroundStyle(recording.status == .failed ? Theme.alert : Theme.amber)
                        .lineLimit(2)
                }

                if recording.status == .ready, recording.positionSec > 0 {
                    WatchedBar(fraction: RecordingLogic.watchedFraction(recording))
                        .frame(maxWidth: 180 * scale)
                }
            }

            Spacer(minLength: 0)

            RecordingStateBadge(recording: recording, scale: scale)
        }
        .padding(.vertical, 8 * scale)
        .contentShape(Rectangle())
    }

    /// Kept pin, series tag and parental lock after the title.
    @ViewBuilder
    private var marks: some View {
        if recording.protected {
            Image(systemName: "pin.fill")
                .font(.system(size: 12 * scale))
                .foregroundStyle(Theme.amber)
                .accessibilityLabel("Kept")
        }
        if recording.isSeries {
            SeriesTag(size: 10 * scale)
        }
        if recording.locked {
            ParentalLockMark(rating: recording.rating, size: 12 * scale)
        }
    }

    /// "5.1 KSTP · Sun, Oct 5, 7:00 PM · 33 min · 3.8 GB"
    private var whenLine: String {
        var parts = [recording.channelName, recording.start.formatted(.dateTime.weekday(.abbreviated).month(.abbreviated).day().hour().minute())]
        switch recording.status {
        case .ready, .converting:
            if recording.durationSec > 0 {
                parts.append(RecordingLogic.formatDuration(recording.durationSec))
            }
            if recording.sizeBytes > 0 {
                parts.append(RecordingLogic.formatSize(recording.sizeBytes))
            }
        default:
            let length = Int(recording.stop.timeIntervalSince(recording.start))
            if length > 0 {
                parts.append(RecordingLogic.formatDuration(length))
            }
        }
        if !recording.canManage, !recording.scheduledBy.isEmpty {
            parts.append("by \(recording.scheduledBy)")
        }
        return parts.joined(separator: " · ")
    }

    /// One VoiceOver phrase for the row.
    var accessibilityText: String {
        var parts = [recording.title, RecordingLogic.stateLabel(recording), whenLine]
        if let detail = RecordingLogic.detailLine(recording) {
            parts.append(detail)
        }
        if recording.protected {
            parts.append("Kept")
        }
        if recording.isSeries {
            parts.append("Series")
        }
        if recording.locked {
            parts.append(ParentalLockMark.accessibilityText(rating: recording.rating))
        }
        return parts.joined(separator: ", ")
    }
}

struct RecordingStateBadge: View {
    let recording: Recording
    var scale: CGFloat = 1

    var body: some View {
        HStack(spacing: 5 * scale) {
            if recording.status == .recording {
                Circle()
                    .fill(Theme.alert)
                    .frame(width: 7 * scale, height: 7 * scale)
            }
            Text(RecordingLogic.stateLabel(recording))
                .font(Theme.label(12 * scale))
                .lineLimit(1)
        }
        .foregroundStyle(color)
        .padding(.horizontal, 8 * scale)
        .padding(.vertical, 4 * scale)
        .background(color.opacity(0.15))
        .clipShape(Capsule())
        .fixedSize()
    }

    private var color: Color {
        switch RecordingLogic.badgeTone(recording) {
        case .neutral: return Theme.dim
        case .live: return Theme.alert
        case .good: return Theme.signal
        case .warning: return Theme.amber
        case .alert: return Theme.alert
        }
    }
}

private struct WatchedBar: View {
    let fraction: Double

    var body: some View {
        GeometryReader { geo in
            ZStack(alignment: .leading) {
                Capsule().fill(Theme.line)
                Capsule()
                    .fill(Theme.amber)
                    .frame(width: max(0, geo.size.width * fraction))
            }
        }
        .frame(height: 4)
        .accessibilityHidden(true)
    }
}
