import SwiftUI
import AppKit
import BowtieKit

/// DVR recordings in the detail pane: Upcoming / Recorded / Missed, manage
/// actions in a context menu, and VOD playback (with resume) in place of the
/// list. The playing recording's controller is owned by the main view so the
/// Play/Pause command reaches it.
struct MacRecordingsView: View {
    let client: BowtieClient
    @Bindable var playerModel: PlayerModel
    @Binding var activeRecording: RecordingPlayerController?

    @State private var model: RecordingsModel?
    @State private var pendingResume: RecordingsModel.Playback?
    @State private var confirmDelete: Recording?
    @State private var confirmStopRule: RecordingRule?

    /// Upcoming states move on their own (recording → finishing up → recorded).
    private let refreshInterval: Duration = .seconds(30)

    var body: some View {
        Group {
            if let controller = activeRecording {
                MacRecordingPlayerView(controller: controller) {
                    closePlayer()
                }
                .id(controller.playback.recording.id)
            } else if let model {
                list(model)
            } else {
                loadingView
            }
        }
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
                if activeRecording == nil {
                    await model?.load()
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
                    start(playback.starting(at: at))
                }
                .keyboardShortcut(.defaultAction)
            }
            Button("Start Over") {
                start(playback.starting(at: 0))
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

    private var resumeTitle: String {
        guard let playback = pendingResume else { return "" }
        return playback.recording.title.isEmpty ? "Resume?" : playback.recording.title
    }

    // MARK: - List

    private func list(_ model: RecordingsModel) -> some View {
        VStack(spacing: 0) {
            Picker("Show", selection: tabBinding(model)) {
                ForEach(RecordingsTab.allCases) { tab in
                    Text(tab.title).tag(tab)
                }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .frame(maxWidth: 440)
            .padding(.vertical, 12)

            content(model)
        }
        .bowtieScreenBackground()
        .toolbar {
            ToolbarItem {
                Button {
                    Task { await model.load() }
                } label: {
                    Label("Reload", systemImage: "arrow.clockwise")
                }
                .help("Reload recordings")
            }
        }
    }

    private func tabBinding(_ model: RecordingsModel) -> Binding<RecordingsTab> {
        Binding(
            get: { model.tab },
            set: { tab in Task { await model.select(tab) } }
        )
    }

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
            Button("Try Again") {
                Task { await model.load() }
            }
            .controlSize(.large)
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
            .scrollContentBackground(.hidden)
            .stopShowDialog(rule: $confirmStopRule) { rule in
                Task { await model.deleteRule(rule) }
            }
        }
    }

    private func ruleRow(_ rule: RecordingRule) -> some View {
        let rowView = RecordingRuleRowView(rule: rule)
        return rowView
            .accessibilityElement(children: .combine)
            .accessibilityLabel(rowView.accessibilityText)
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
            .scrollContentBackground(.hidden)
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
        .help(recording.locked ? "Locked by parental controls" : (recording.isPlayable ? "Play" : ""))
        .accessibilityElement(children: .combine)
        .accessibilityLabel(rowView.accessibilityText)
        .accessibilityHint(recording.isPlayable ? "Play this recording" : "")
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
                Divider()
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
            start(playback)
        }
    }

    private func start(_ playback: RecordingsModel.Playback) {
        guard let model else { return }
        activeRecording?.finish()
        activeRecording = RecordingPlayerController(playback: playback, model: model)
    }

    private func closePlayer() {
        activeRecording?.finish()
        activeRecording = nil
        Task { await model?.load() }
    }
}

// MARK: - VOD player

/// One recording: AVKit floating transport with full scrubbing, a title bar
/// and a Done button. Resume / periodic position saves come from the shared
/// `RecordingPlayerController`.
struct MacRecordingPlayerView: View {
    let controller: RecordingPlayerController
    let onDone: () -> Void

    @State private var showChrome = true
    @State private var hideChromeTask: Task<Void, Never>?

    private static let chromeHideDelay: Duration = .seconds(3)

    var body: some View {
        ZStack {
            Color.black

            MacPlayerSurface(player: controller.player, allowsPictureInPicture: false)

            if showChrome || controller.errorMessage != nil {
                chrome
                    .transition(.opacity)
            }
        }
        .onContinuousHover { phase in
            if case .active = phase {
                bumpChrome()
            }
        }
        .onAppear {
            controller.start()
            bumpChrome()
        }
        .onDisappear {
            hideChromeTask?.cancel()
            controller.finish()
        }
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.willResignActiveNotification)) { _ in
            controller.saveNow()
        }
    }

    private var chrome: some View {
        VStack(spacing: 0) {
            HStack(alignment: .center, spacing: 12) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(controller.playback.recording.title)
                        .font(Theme.label(16))
                        .foregroundStyle(Theme.text)
                        .lineLimit(1)
                    Text(subtitle)
                        .font(Theme.body(13))
                        .foregroundStyle(Theme.dim)
                        .lineLimit(1)
                }
                Spacer(minLength: 0)
                Button("Done") {
                    onDone()
                }
                .keyboardShortcut(.cancelAction)
                .help("Close the player (Esc)")
            }
            .padding(.horizontal, 16)
            .padding(.top, 12)
            .padding(.bottom, 24)
            .background(
                LinearGradient(
                    colors: [Color.black.opacity(0.75), Color.black.opacity(0)],
                    startPoint: .top,
                    endPoint: .bottom
                )
                .allowsHitTesting(false)
            )

            Spacer()

            if let error = controller.errorMessage {
                Text(error)
                    .font(Theme.body())
                    .foregroundStyle(Theme.alert)
                    .padding(16)
                    .background(Theme.bg.opacity(0.9))
                    .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
                Spacer()
            }
        }
        .animation(.easeInOut(duration: 0.2), value: showChrome)
    }

    private var subtitle: String {
        let recording = controller.playback.recording
        return recording.subtitle.isEmpty ? recording.channelName : "\(recording.subtitle) · \(recording.channelName)"
    }

    private func bumpChrome() {
        if !showChrome {
            showChrome = true
        }
        hideChromeTask?.cancel()
        hideChromeTask = Task { @MainActor in
            do {
                try await Task.sleep(for: Self.chromeHideDelay)
            } catch {
                return
            }
            withAnimation(.easeOut(duration: 0.25)) {
                showChrome = false
            }
        }
    }
}
