import SwiftUI
import BowtieKit

/// "Record This Program" / "Record Series" from the channel list and search:
/// schedules, then explains the result, offering "Record Anyway" on a tuner
/// conflict.
@Observable
@MainActor
final class RecordFlow {
    struct Request: Equatable {
        let channelId: Int64
        let channelName: String
        let programStart: Date
        let title: String
    }

    enum Notice {
        case scheduled(title: String, message: String)
        case conflict(Request, message: String)
        case failed(String)
    }

    var notice: Notice?
    private(set) var isWorking = false

    private let client: BowtieClient
    private let onScheduled: () -> Void

    init(client: BowtieClient, onScheduled: @escaping () -> Void) {
        self.client = client
        self.onScheduled = onScheduled
    }

    func record(channel: Channel, program: GuideProgram) {
        let request = Request(
            channelId: channel.id,
            channelName: channel.name,
            // Exact guide start: the server looks the program up by it.
            programStart: program.start,
            title: program.title.isEmpty ? "This program" : program.title
        )
        Task { await schedule(request, force: false) }
    }

    func recordAnyway(_ request: Request) {
        Task { await schedule(request, force: true) }
    }

    /// Every new episode of the program's show on this channel.
    func recordSeries(channel: Channel, program: GuideProgram) {
        let request = Request(
            channelId: channel.id,
            channelName: channel.name,
            programStart: program.start,
            title: program.title
        )
        Task { await scheduleSeries(request) }
    }

    private func scheduleSeries(_ request: Request) async {
        guard !isWorking else { return }
        isWorking = true
        defer { isWorking = false }
        let outcome = await RecordingScheduler.scheduleSeries(
            client: client,
            channelId: request.channelId,
            programStart: request.programStart
        )
        switch outcome {
        case .scheduled(let rule, let count):
            notice = .scheduled(
                title: RecordingLogic.seriesScheduledTitle(count: count),
                message: RecordingLogic.seriesScheduledMessage(
                    title: rule.title.isEmpty ? request.title : rule.title,
                    channelName: seriesChannelName(rule: rule, request: request)
                )
            )
            onScheduled()
        case .failed(let message):
            notice = .failed(message)
        }
    }

    /// "" (any channel) for an any-channel rule.
    private func seriesChannelName(rule: RecordingRule, request: Request) -> String {
        if rule.anyChannel { return "" }
        return rule.channelName.isEmpty ? request.channelName : rule.channelName
    }

    private func schedule(_ request: Request, force: Bool) async {
        guard !isWorking else { return }
        isWorking = true
        defer { isWorking = false }
        let outcome = await RecordingScheduler.schedule(
            client: client,
            channelId: request.channelId,
            programStart: request.programStart,
            force: force
        )
        switch outcome {
        case .scheduled(let recording, let warnings):
            var message = "\u{201C}\(recording.title)\u{201D} on \(recording.channelName) will record."
            if !warnings.isEmpty {
                message += "\n\n" + warnings.joined(separator: "\n")
            }
            notice = .scheduled(title: "Recording Scheduled", message: message)
            onScheduled()
        case .conflict(let tunerCount, let conflicts):
            notice = .conflict(
                request,
                message: RecordingLogic.conflictMessage(tunerCount: tunerCount, conflicts: conflicts)
            )
        case .failed(let message):
            notice = .failed(message)
        }
    }
}

extension View {
    /// Alerts for `RecordFlow` results.
    func recordFlowAlerts(_ flow: RecordFlow?) -> some View {
        modifier(RecordFlowAlerts(flow: flow))
    }
}

private struct RecordFlowAlerts: ViewModifier {
    var flow: RecordFlow?

    private var isPresented: Binding<Bool> {
        Binding(
            get: { flow?.notice != nil },
            set: { if !$0 { flow?.notice = nil } }
        )
    }

    private var title: String {
        switch flow?.notice {
        case .scheduled(let title, _): return title
        case .conflict: return "Not Enough Tuners"
        case .failed: return "Couldn't Schedule"
        case nil: return ""
        }
    }

    func body(content: Content) -> some View {
        content.alert(title, isPresented: isPresented, presenting: flow?.notice) { notice in
            switch notice {
            case .conflict(let request, _):
                Button("Record Anyway") { flow?.recordAnyway(request) }
                Button("Cancel", role: .cancel) {}
            case .scheduled, .failed:
                Button("OK", role: .cancel) {}
            }
        } message: { notice in
            switch notice {
            case .scheduled(_, let message), .conflict(_, let message), .failed(let message):
                Text(message)
            }
        }
    }
}

/// Context-menu items for scheduling a channel row's now / next program.
struct RecordMenuItems: View {
    let channel: Channel
    let nowNext: GuideLogic.NowNext
    let flow: RecordFlow?
    let openRecordings: () -> Void

    var body: some View {
        if let program = nowNext.now {
            item(program: program, label: "Record This Program")
            RecordSeriesButton(channel: channel, program: program, flow: flow, label: "Record Series")
        }
        if let program = nowNext.next {
            item(
                program: program,
                label: program.title.isEmpty ? "Record Next Program" : "Record Next: \(program.title)"
            )
            // Back-to-back episodes of one show need only one series item.
            if program.title != nowNext.now?.title {
                RecordSeriesButton(
                    channel: channel,
                    program: program,
                    flow: flow,
                    label: "Record Series: \(program.title)"
                )
            }
        }
        Button {
            openRecordings()
        } label: {
            Label("Recordings", systemImage: "recordingtape")
        }
    }

    @ViewBuilder
    private func item(program: GuideProgram, label: String) -> some View {
        if program.recording != nil {
            Button {
                openRecordings()
            } label: {
                Label(
                    program.title.isEmpty ? "Recording Scheduled" : "Recording: \(program.title)",
                    systemImage: "checkmark.circle"
                )
            }
        } else {
            Button {
                flow?.record(channel: channel, program: program)
            } label: {
                Label(label, systemImage: "record.circle")
            }
            .disabled(flow == nil)
        }
    }
}

/// "Record Series": this channel, new episodes only. Offered even when this
/// airing is already set to record (the show's later episodes aren't). Hidden
/// for an untitled program (nothing to match on).
struct RecordSeriesButton: View {
    let channel: Channel
    let program: GuideProgram
    let flow: RecordFlow?
    var label = "Record Series"

    var body: some View {
        if !program.title.isEmpty {
            Button {
                flow?.recordSeries(channel: channel, program: program)
            } label: {
                Label(label, systemImage: "square.stack.3d.down.right")
            }
            .disabled(flow == nil)
        }
    }
}

/// Lock and rating for a program or recording parental controls block.
struct ParentalLockMark: View {
    let rating: String
    var size: CGFloat = 12

    var body: some View {
        HStack(spacing: size * 0.3) {
            Image(systemName: "lock.fill")
                .font(.system(size: size * 0.9, weight: .semibold))
            if !rating.isEmpty {
                Text(rating)
                    .font(.system(size: size, weight: .semibold))
                    .lineLimit(1)
            }
        }
        .foregroundStyle(Theme.dim)
        .fixedSize()
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(Self.accessibilityText(rating: rating))
    }

    static func accessibilityText(rating: String) -> String {
        rating.isEmpty ? "Locked by parental controls" : "Locked by parental controls, rated \(rating)"
    }
}

/// Small "SERIES" tag for a recording a series rule scheduled.
struct SeriesTag: View {
    var size: CGFloat = 11

    var body: some View {
        Text("Series")
            .font(.system(size: size, weight: .bold))
            .textCase(.uppercase)
            .foregroundStyle(Theme.amber)
            .padding(.horizontal, size * 0.5)
            .padding(.vertical, size * 0.15)
            .overlay(Capsule().stroke(Theme.amber.opacity(0.6), lineWidth: 1))
            .fixedSize()
            .accessibilityLabel("Series")
    }
}

/// Small red "REC" mark for a guide program that is scheduled or recorded.
struct RecordingMarkDot: View {
    var size: CGFloat = 12

    var body: some View {
        HStack(spacing: size * 0.3) {
            Circle()
                .fill(Theme.alert)
                .frame(width: size * 0.7, height: size * 0.7)
            Text("REC")
                .font(.system(size: size, weight: .bold))
                .foregroundStyle(Theme.alert)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Set to record")
    }
}
