import SwiftUI
import BowtieKit

/// "Record this program" from the channel list: schedules, then explains the
/// result, offering "Record Anyway" on a tuner conflict.
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
        }
        if let program = nowNext.next {
            item(
                program: program,
                label: program.title.isEmpty ? "Record Next Program" : "Record Next: \(program.title)"
            )
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
