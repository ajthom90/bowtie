import SwiftUI
import BowtieKit

/// Debug-style stats-for-nerds panel: session meta + live access-log samples.
///
/// SF Mono, amber-on-dark. Missing values render as "—".
struct StatsOverlay: View {
    let meta: SessionInfoMeta?
    /// Indicated bitrate from `AVPlayerItemAccessLogEvent.indicatedBitrate` (bits/s).
    let indicatedBitrate: Double?
    /// Cumulative dropped video frames from the last access-log event.
    let droppedFrames: Int?

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            row(label: "Codec", value: display(meta?.videoCodec))
            row(label: "Profile", value: display(meta?.profile))
            row(label: "Backend", value: display(meta?.backend))
            row(label: "Bitrate", value: formatBitrate(indicatedBitrate))
            row(label: "Dropped", value: droppedFrames.map(String.init) ?? "—")
        }
        .font(Theme.mono(12))
        .foregroundStyle(Theme.amber)
        .padding(12)
        .frame(maxWidth: 280, alignment: .leading)
        .background(Theme.bg.opacity(0.88))
        .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous)
                .stroke(Theme.line, lineWidth: 1)
        )
        .accessibilityElement(children: .combine)
        .accessibilityLabel(accessibilitySummary)
    }

    private func row(label: String, value: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Text(label)
                .foregroundStyle(Theme.dim)
                .frame(width: 72, alignment: .leading)
            Text(value)
                .lineLimit(1)
                .minimumScaleFactor(0.8)
        }
    }

    private func display(_ value: String?) -> String {
        guard let value, !value.isEmpty else { return "—" }
        return value
    }

    private func formatBitrate(_ bps: Double?) -> String {
        guard let bps, bps.isFinite, bps > 0 else { return "—" }
        if bps >= 1_000_000 {
            return String(format: "%.2f Mbps", bps / 1_000_000)
        }
        if bps >= 1_000 {
            return String(format: "%.0f kbps", bps / 1_000)
        }
        return String(format: "%.0f bps", bps)
    }

    private var accessibilitySummary: String {
        [
            "Codec \(display(meta?.videoCodec))",
            "Profile \(display(meta?.profile))",
            "Backend \(display(meta?.backend))",
            "Bitrate \(formatBitrate(indicatedBitrate))",
            "Dropped frames \(droppedFrames.map(String.init) ?? "none")",
        ].joined(separator: ", ")
    }
}

#Preview {
    ZStack {
        Color.black
        StatsOverlay(
            meta: SessionInfoMeta(
                videoCodec: "h264",
                profile: "high",
                backend: "ffmpeg",
                channelName: "WABC"
            ),
            indicatedBitrate: 4_200_000,
            droppedFrames: 3
        )
        .padding()
    }
}
