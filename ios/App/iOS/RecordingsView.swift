import SwiftUI
import AVFoundation
import BowtieKit

/// DVR recordings: Upcoming / Recorded / Missed, with manage actions and VOD playback.
struct RecordingsView: View {
    let client: BowtieClient
    /// Live player: stopped before a recording plays (it may still be in PiP).
    @Bindable var playerModel: PlayerModel

    @State private var model: RecordingsModel?
    @State private var continueModel: ContinueWatchingModel?
    @State private var pendingResume: RecordingsModel.Playback?
    @State private var activePlayback: RecordingsModel.Playback?
    @State private var confirmDelete: Recording?
    @State private var confirmStopRule: RecordingRule?

    @Environment(\.scenePhase) private var scenePhase

    /// Upcoming states move on their own (recording → finishing up → recorded).
    private let refreshInterval: Duration = .seconds(30)

    var body: some View {
        VStack(spacing: 0) {
            if let model {
                if let continueModel, !continueModel.items.isEmpty {
                    ContinueWatchingShelf(
                        items: continueModel.items,
                        onPlay: { recording in Task { await resume(recording, model: model) } },
                        onRemove: { recording in
                            Task {
                                await continueModel.remove(recording)
                                await model.load()
                            }
                        }
                    )
                    .padding(.top, 10)
                }

                Picker("Show", selection: tabBinding(model)) {
                    ForEach(RecordingsTab.allCases) { tab in
                        Text(tab.title).tag(tab)
                    }
                }
                .pickerStyle(.segmented)
                .padding(.horizontal, 16)
                .padding(.vertical, 10)

                content(model)
            } else {
                loadingView
            }
        }
        .bowtieScreenBackground()
        .navigationTitle("Recordings")
        .navigationBarTitleDisplayMode(.inline)
        .toolbarBackground(Theme.bg, for: .navigationBar)
        .toolbarColorScheme(.dark, for: .navigationBar)
        .task {
            if model == nil {
                model = RecordingsModel(client: client)
                continueModel = ContinueWatchingModel(client: client)
            }
            await model?.load()
        }
        .task(id: continueModel != nil) {
            await continueModel?.load()
        }
        .bowtieToast(continueModel?.actionError) {
            continueModel?.actionError = nil
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
        .onChange(of: scenePhase) { _, phase in
            if phase == .active, activePlayback == nil {
                Task {
                    await model?.load()
                    await continueModel?.load()
                }
            }
        }
        .confirmationDialog(
            resumeTitle,
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
            "Delete this recording?",
            isPresented: Binding(
                get: { confirmDelete != nil },
                set: { if !$0 { confirmDelete = nil } }
            ),
            titleVisibility: .visible,
            presenting: confirmDelete
        ) { recording in
            Button("Delete \u{201C}\(recording.title)\u{201D}", role: .destructive) {
                Task {
                    await model?.delete(recording)
                    await continueModel?.load()
                }
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
        .fullScreenCover(item: $activePlayback, onDismiss: {
            Task {
                // The player's last position save, so the lists show it.
                await model?.waitForSaves()
                await model?.load()
                await continueModel?.load()
            }
        }) { playback in
            if let model {
                RecordingPlayerView(playback: playback, model: model)
            }
        }
    }

    private var resumeTitle: String {
        guard let playback = pendingResume else { return "" }
        return playback.recording.title.isEmpty ? "Resume?" : playback.recording.title
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
        if model.tab == .shows {
            showsContent(model)
        } else {
            recordingsContent(model)
        }
    }

    private func emptyView(_ message: String) -> some View {
        Text(message)
            .font(Theme.body())
            .foregroundStyle(Theme.dim)
            .multilineTextAlignment(.center)
            .padding(.horizontal, 32)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func failedView(_ message: String, model: RecordingsModel) -> some View {
        VStack(spacing: 16) {
            Text(message)
                .font(Theme.body())
                .foregroundStyle(Theme.alert)
                .multilineTextAlignment(.center)
                .padding(.horizontal, 32)
            Button {
                Task { await model.load() }
            } label: {
                Text("Try again")
                    .font(Theme.label(16))
                    .padding(.horizontal, 20)
                    .padding(.vertical, 10)
                    .background(Theme.raised)
                    .foregroundStyle(Theme.text)
                    .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
            }
            .buttonStyle(.plain)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    // MARK: - Shows

    @ViewBuilder
    private func showsContent(_ model: RecordingsModel) -> some View {
        switch model.rulesState {
        case .loading:
            loadingView
        case .empty:
            emptyView(RecordingsTab.shows.emptyMessage)
        case .failed(let message):
            failedView(message, model: model)
        case .loaded(let rules):
            List {
                ForEach(rules) { rule in
                    ruleRow(rule)
                }
            }
            .listStyle(.plain)
            .scrollContentBackground(.hidden)
            .refreshable { await model.load() }
            .stopShowDialog(rule: $confirmStopRule) { rule in
                Task { await model.deleteRule(rule) }
            }
        }
    }

    private func ruleRow(_ rule: RecordingRule) -> some View {
        let rowView = RecordingRuleRowView(rule: rule)
        return rowView
            .listRowBackground(Theme.bg)
            .listRowSeparatorTint(Theme.line)
            .accessibilityElement(children: .combine)
            .accessibilityLabel(rowView.accessibilityText)
            .swipeActions(edge: .trailing, allowsFullSwipe: false) {
                if rule.canManage {
                    Button {
                        confirmStopRule = rule
                    } label: {
                        Label("Stop Recording This Show", systemImage: "stop.circle")
                    }
                    .tint(Theme.alert)
                }
            }
            .contextMenu {
                if rule.canManage {
                    Button(role: .destructive) {
                        confirmStopRule = rule
                    } label: {
                        Label("Stop Recording This Show", systemImage: "stop.circle")
                    }
                }
            }
    }

    // MARK: - Recordings

    @ViewBuilder
    private func recordingsContent(_ model: RecordingsModel) -> some View {
        switch model.state {
        case .loading:
            loadingView
        case .empty:
            emptyView(model.tab.emptyMessage)
        case .failed(let message):
            failedView(message, model: model)
        case .loaded(let rows):
            List {
                ForEach(rows) { recording in
                    row(recording, model: model)
                }
            }
            .listStyle(.plain)
            .scrollContentBackground(.hidden)
            .refreshable {
                await model.load()
                await continueModel?.load()
            }
        }
    }

    private var loadingView: some View {
        ProgressView()
            .tint(Theme.amber)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .accessibilityLabel("Loading recordings")
    }

    private func row(_ recording: Recording, model: RecordingsModel) -> some View {
        let rowView = RecordingRowView(recording: recording)
        return Button {
            if recording.isPlayable {
                Task { await play(recording, model: model) }
            }
        } label: {
            rowView
        }
        .buttonStyle(.plain)
        .listRowBackground(Theme.bg)
        .listRowSeparatorTint(Theme.line)
        .accessibilityElement(children: .combine)
        .accessibilityLabel(rowView.accessibilityText)
        .accessibilityHint(recording.locked ? "Locked by parental controls" : (recording.isPlayable ? "Play this recording" : ""))
        .swipeActions(edge: .trailing, allowsFullSwipe: false) {
            if recording.canDelete {
                Button {
                    requestDelete(recording, model: model)
                } label: {
                    Label(recording.deleteActionTitle, systemImage: "trash")
                }
                .tint(Theme.alert)
            }
            if recording.canStop {
                Button {
                    Task { await model.stop(recording) }
                } label: {
                    Label("Stop and Keep", systemImage: "stop.circle")
                }
                .tint(Theme.amber)
            }
        }
        .swipeActions(edge: .leading, allowsFullSwipe: true) {
            if recording.canProtect {
                Button {
                    Task { await model.setProtected(recording, !recording.protected) }
                } label: {
                    Label(recording.protected ? "Don't Keep" : "Keep", systemImage: recording.protected ? "pin.slash" : "pin")
                }
                .tint(Theme.amber)
            }
        }
        .contextMenu {
            if recording.isPlayable {
                Button {
                    Task { await play(recording, model: model) }
                } label: {
                    Label("Play", systemImage: "play.fill")
                }
            }
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
    }

    // MARK: - Actions

    /// Upcoming recordings cancel right away; recorded files ask first.
    private func requestDelete(_ recording: Recording, model: RecordingsModel) {
        switch recording.status {
        case .scheduled, .waiting:
            Task { await model.delete(recording) }
        default:
            confirmDelete = recording
        }
    }

    /// Continue watching: straight to the saved position, no Resume prompt.
    private func resume(_ recording: Recording, model: RecordingsModel) async {
        guard let playback = await model.resume(recording, stopping: playerModel) else { return }
        activePlayback = playback
    }

    private func play(_ recording: Recording, model: RecordingsModel) async {
        // One stream at a time: a live session (maybe in PiP) ends, but only
        // once /play succeeds. On failure live TV keeps playing and the
        // error alert explains.
        guard let playback = await model.play(recording, beforeStart: {
            if playerModel.currentChannel != nil {
                await playerModel.stop()
            }
        }) else { return }
        if playback.resumeAt != nil {
            pendingResume = playback
        } else {
            activePlayback = playback
        }
    }
}
