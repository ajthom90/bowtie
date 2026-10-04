import SwiftUI
import BowtieKit

/// tvOS Recordings screen: Upcoming / Recorded / Missed. Select plays a
/// recording (or opens its actions); press and hold for the same actions.
struct TVRecordingsView: View {
    let client: BowtieClient
    /// Live player: stopped before a recording plays.
    @Bindable var playerModel: PlayerModel

    @State private var model: RecordingsModel?
    @State private var pendingResume: RecordingsModel.Playback?
    @State private var activePlayback: RecordingsModel.Playback?
    @State private var actionsFor: Recording?
    @State private var confirmDelete: Recording?

    private let refreshInterval: Duration = .seconds(30)

    var body: some View {
        VStack(spacing: 0) {
            if let model {
                Picker("Show", selection: tabBinding(model)) {
                    ForEach(RecordingsTab.allCases) { tab in
                        Text(tab.title).tag(tab)
                    }
                }
                .pickerStyle(.segmented)
                .padding(.horizontal, 80)
                .padding(.vertical, 20)
                .focusSection()

                content(model)
            } else {
                loadingView
            }
        }
        .bowtieScreenBackground()
        .navigationTitle("Recordings")
        .task {
            if model == nil {
                model = RecordingsModel(client: client)
            }
            await model?.load()
        }
        .task(id: model != nil) {
            while !Task.isCancelled {
                do {
                    try await Task.sleep(for: refreshInterval)
                } catch {
                    return
                }
                if activePlayback == nil {
                    await model?.load()
                }
            }
        }
        .onChange(of: activePlayback) { previous, current in
            // Back from the player: show the new resume position.
            if previous != nil, current == nil {
                Task { await model?.load() }
            }
        }
        .navigationDestination(item: $activePlayback) { playback in
            if let model {
                TVRecordingPlayerView(playback: playback, model: model)
            }
        }
        .confirmationDialog(
            pendingResume?.recording.title ?? "",
            isPresented: Binding(
                get: { pendingResume != nil },
                set: { if !$0 { pendingResume = nil } }
            ),
            titleVisibility: .visible,
            presenting: pendingResume
        ) { playback in
            if let at = playback.resumeAt {
                Button("Resume from \(RecordingLogic.formatTimestamp(at))") {
                    activePlayback = playback.starting(at: at)
                }
            }
            Button("Start Over") {
                activePlayback = playback.starting(at: 0)
            }
            Button("Cancel", role: .cancel) {}
        }
        .confirmationDialog(
            actionsFor?.title ?? "",
            isPresented: Binding(
                get: { actionsFor != nil },
                set: { if !$0 { actionsFor = nil } }
            ),
            titleVisibility: .visible,
            presenting: actionsFor
        ) { recording in
            if let model {
                actionButtons(recording, model: model)
            }
            Button("Cancel", role: .cancel) {}
        } message: { recording in
            Text(RecordingLogic.detailLine(recording) ?? RecordingLogic.stateLabel(recording))
        }
        .confirmationDialog(
            "Delete this recording?",
            isPresented: Binding(
                get: { confirmDelete != nil },
                set: { if !$0 { confirmDelete = nil } }
            ),
            titleVisibility: .visible,
            presenting: confirmDelete
        ) { recording in
            Button("Delete \u{201C}\(recording.title)\u{201D}", role: .destructive) {
                Task { await model?.delete(recording) }
            }
            Button("Cancel", role: .cancel) {}
        } message: { _ in
            Text("The recording is removed from the server for everyone.")
        }
        .alert(
            "Something Went Wrong",
            isPresented: Binding(
                get: { model?.actionError != nil },
                set: { if !$0 { model?.actionError = nil } }
            )
        ) {
            Button("OK", role: .cancel) {}
        } message: {
            Text(model?.actionError ?? "")
        }
    }

    private func tabBinding(_ model: RecordingsModel) -> Binding<RecordingsTab> {
        Binding(
            get: { model.tab },
            set: { tab in Task { await model.select(tab) } }
        )
    }

    // MARK: - Content

    @ViewBuilder
    private func content(_ model: RecordingsModel) -> some View {
        switch model.state {
        case .loading:
            loadingView
        case .empty:
            Text(model.tab.emptyMessage)
                .font(Theme.body(24))
                .foregroundStyle(Theme.dim)
                .multilineTextAlignment(.center)
                .padding(.horizontal, 80)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        case .failed(let message):
            VStack(spacing: 24) {
                Text(message)
                    .font(Theme.body(24))
                    .foregroundStyle(Theme.alert)
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, 80)
                Button {
                    Task { await model.load() }
                } label: {
                    Text("Try again")
                        .font(Theme.label(22))
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .focusSection()
        case .loaded(let rows):
            List {
                ForEach(rows) { recording in
                    row(recording, model: model)
                }
            }
            .listStyle(.plain)
            .focusSection()
        }
    }

    private var loadingView: some View {
        ProgressView()
            .tint(Theme.amber)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .accessibilityLabel("Loading recordings")
    }

    private func row(_ recording: Recording, model: RecordingsModel) -> some View {
        let rowView = RecordingRowView(recording: recording, large: true)
        return Button {
            if recording.isPlayable {
                Task { await play(recording, model: model) }
            } else if hasActions(recording) {
                actionsFor = recording
            }
        } label: {
            rowView
        }
        .listRowBackground(Theme.bg)
        .accessibilityElement(children: .combine)
        .accessibilityLabel(rowView.accessibilityText)
        .contextMenu {
            if recording.isPlayable {
                Button {
                    Task { await play(recording, model: model) }
                } label: {
                    Label("Play", systemImage: "play.fill")
                }
            }
            actionButtons(recording, model: model)
        }
    }

    @ViewBuilder
    private func actionButtons(_ recording: Recording, model: RecordingsModel) -> some View {
        if recording.canStop {
            Button {
                Task { await model.stop(recording) }
            } label: {
                Label("Stop and Keep", systemImage: "stop.circle")
            }
        }
        if recording.canProtect {
            Button {
                Task { await model.setProtected(recording, !recording.protected) }
            } label: {
                Label(
                    recording.protected ? "Don't Keep" : "Keep (Never Auto-Delete)",
                    systemImage: recording.protected ? "pin.slash" : "pin"
                )
            }
        }
        if recording.canDelete {
            Button(role: .destructive) {
                requestDelete(recording, model: model)
            } label: {
                Label(recording.deleteActionTitle, systemImage: "trash")
            }
        }
    }

    // MARK: - Actions

    private func hasActions(_ recording: Recording) -> Bool {
        recording.canStop || recording.canProtect || recording.canDelete
    }

    private func requestDelete(_ recording: Recording, model: RecordingsModel) {
        switch recording.status {
        case .scheduled, .waiting:
            Task { await model.delete(recording) }
        default:
            confirmDelete = recording
        }
    }

    private func play(_ recording: Recording, model: RecordingsModel) async {
        if playerModel.currentChannel != nil {
            await playerModel.stop()
        }
        guard let playback = await model.play(recording) else { return }
        if playback.resumeAt != nil {
            pendingResume = playback
        } else {
            activePlayback = playback
        }
    }
}
