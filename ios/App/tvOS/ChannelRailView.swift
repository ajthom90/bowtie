import SwiftUI
import BowtieKit

/// Focus-driven channel rail — tvOS home when `AppModel.phase == .ready`.
///
/// Condensed rows (number + name + now/next). Play on select. Default focus
/// effects only — no custom scale fights the system.
struct ChannelRailView: View {
    @Bindable var appModel: AppModel
    @Bindable var playerModel: PlayerModel

    @State private var listModel: ChannelListModel?
    @State private var playingChannel: Channel?
    @State private var showSettings = false
    @State private var showRecordings = false
    @State private var showSearch = false
    @State private var recordFlow: RecordFlow?
    @State private var now = Date()
    /// Continue watching: its shelf, and the recordings model that plays from it.
    @State private var continueModel: ContinueWatchingModel?
    @State private var recordingsModel: RecordingsModel?
    @State private var activePlayback: ContinuePlayback?

    @Environment(\.scenePhase) private var scenePhase

    /// Spec-mandated empty copy (verbatim).
    static let emptyCopy = "No channels yet. Ask your admin to enable some."

    /// Busy tuners re-check; a full reload once the list is 5 minutes old.
    private let recheckInterval: Duration = .seconds(30)
    private let clockTick: Duration = .seconds(30)

    var body: some View {
        NavigationStack {
            content
                .navigationTitle("Channels")
                .toolbar {
                    ToolbarItem(placement: .primaryAction) {
                        Button {
                            showSearch = true
                        } label: {
                            Label("Search", systemImage: "magnifyingglass")
                                .labelStyle(.titleAndIcon)
                                .foregroundStyle(Theme.amber)
                        }
                        .accessibilityLabel("Search")
                        .accessibilityHint("Search the guide")
                    }
                    ToolbarItem(placement: .primaryAction) {
                        Button {
                            showRecordings = true
                        } label: {
                            Label("Recordings", systemImage: "recordingtape")
                                .labelStyle(.titleAndIcon)
                                .foregroundStyle(Theme.amber)
                        }
                        .accessibilityLabel("Recordings")
                        .accessibilityHint("Open your recordings")
                    }
                    ToolbarItem(placement: .primaryAction) {
                        Button {
                            showSettings = true
                        } label: {
                            Label("Settings", systemImage: "gearshape")
                                .labelStyle(.titleAndIcon)
                                .foregroundStyle(Theme.amber)
                        }
                        .accessibilityLabel("Settings")
                        .accessibilityHint("Open server and account settings")
                    }
                }
                .navigationDestination(item: $playingChannel) { channel in
                    if let serverURL = appModel.serverURL {
                        TVPlayerView(
                            channel: channel,
                            serverURL: serverURL,
                            maxQuality: appModel.user?.maxQuality ?? "",
                            nowTitle: nowTitle(for: channel),
                            programEnd: { programEnd(for: $0) },
                            playerModel: playerModel
                        )
                    } else {
                        VStack(spacing: 20) {
                            Text("Not connected to a server")
                                .font(Theme.body(22))
                                .foregroundStyle(Theme.alert)
                            Button("Back") { playingChannel = nil }
                                .foregroundStyle(Theme.amber)
                        }
                        .bowtieScreenBackground()
                    }
                }
                .navigationDestination(isPresented: $showRecordings) {
                    if let client = appModel.client {
                        TVRecordingsView(client: client, playerModel: playerModel)
                    }
                }
                .navigationDestination(isPresented: $showSearch) {
                    searchDestination
                }
                .navigationDestination(item: $activePlayback) { item in
                    if let recordingsModel {
                        TVRecordingPlayerView(playback: item.playback, model: recordingsModel)
                    }
                }
                .alert(
                    "Something Went Wrong",
                    isPresented: Binding(
                        get: { recordingsModel?.actionError != nil },
                        set: { if !$0 { recordingsModel?.actionError = nil } }
                    )
                ) {
                    Button("OK", role: .cancel) {}
                } message: {
                    Text(recordingsModel?.actionError ?? "")
                }
                .recordFlowAlerts(recordFlow)
                .presentsGroupPlayback(playerModel, playingChannel: $playingChannel)
                .sheet(isPresented: $showSettings) {
                    NavigationStack {
                        SettingsView(appModel: appModel)
                            .toolbar {
                                ToolbarItem(placement: .cancellationAction) {
                                    Button("Done") { showSettings = false }
                                        .foregroundStyle(Theme.amber)
                                        .accessibilityLabel("Done")
                                }
                            }
                    }
                }
        }
        .preferredColorScheme(.dark)
        .task {
            await ensureListModel()
            await listModel?.load()
        }
        // Alongside the channel list, not after it.
        .task(id: continueModel != nil) {
            await continueModel?.load()
        }
        .task(id: listModel != nil) {
            guard listModel != nil else { return }
            while !Task.isCancelled {
                do {
                    try await Task.sleep(for: recheckInterval)
                } catch {
                    return
                }
                // Behind the player the list isn't visible.
                guard playingChannel == nil else { continue }
                await listModel?.recheckTuners()
            }
        }
        .task {
            while !Task.isCancelled {
                now = Date()
                do {
                    try await Task.sleep(for: clockTick)
                } catch {
                    return
                }
            }
        }
        // Back from the background: tuners may have freed up (or filled).
        .onChange(of: scenePhase) { _, phase in
            if phase == .active {
                Task { await listModel?.recheckTuners() }
            }
        }
        .onChange(of: playerModel.channelsStaleGeneration) { _, _ in
            Task { await listModel?.load() }
        }
        // Back from the player: the server may have just recorded a watch.
        .onChange(of: playingChannel) { old, new in
            if old != nil, new == nil {
                Task { await listModel?.refreshRecents() }
            }
        }
        // Back from a recording: it moves to the front (or leaves if finished).
        .onChange(of: activePlayback) { old, new in
            if old != nil, new == nil {
                Task {
                    await recordingsModel?.waitForSaves()
                    await continueModel?.load()
                }
            }
        }
        // Back from Recordings: something may have been watched or deleted there.
        .onChange(of: showRecordings) { old, new in
            if old, !new {
                Task { await continueModel?.load() }
            }
        }
    }

    // MARK: - Search

    @ViewBuilder
    private var searchDestination: some View {
        if let client = appModel.client {
            TVSearchView(
                client: client,
                onWatch: { open(channel: $0) },
                openRecordings: {
                    showSearch = false
                    showRecordings = true
                },
                onScheduled: { Task { await listModel?.load() } }
            )
        }
    }

    // MARK: - Content

    @ViewBuilder
    private var content: some View {
        Group {
            if let listModel {
                switch listModel.state {
                case .loading:
                    loadingView
                case .empty:
                    emptyView
                case .failed(let message):
                    failedView(message: message, model: listModel)
                case .loaded(let rows):
                    railView(rows: rows, model: listModel)
                }
            } else {
                loadingView
            }
        }
        .bowtieScreenBackground()
    }

    private var loadingView: some View {
        VStack(spacing: 16) {
            ProgressView()
                .tint(Theme.amber)
            Text("Loading channels…")
                .font(Theme.body(22))
                .foregroundStyle(Theme.dim)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Loading channels")
    }

    private var emptyView: some View {
        Text(Self.emptyCopy)
            .font(Theme.body(24))
            .foregroundStyle(Theme.dim)
            .multilineTextAlignment(.center)
            .padding(.horizontal, 80)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .accessibilityLabel(Self.emptyCopy)
    }

    private func failedView(message: String, model: ChannelListModel) -> some View {
        VStack(spacing: 24) {
            Text(message)
                .font(Theme.body(24))
                .foregroundStyle(Theme.alert)
                .multilineTextAlignment(.center)
                .padding(.horizontal, 80)
                .accessibilityLabel(message)

            Button {
                Task { await model.load() }
            } label: {
                Text("Try again")
                    .font(Theme.label(22))
            }
            .accessibilityLabel("Try again")
            .accessibilityHint("Reload the channel list")
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .focusSection()
    }

    /// Continue watching, recent cards, then the category chips (each its own
    /// focus section, so Up from the rail lands on the chips), then the rail;
    /// favorites lead it.
    private func railView(rows: [ChannelListModel.Row], model: ChannelListModel) -> some View {
        let visible = model.filteredRows(at: now)
        return VStack(alignment: .leading, spacing: 8) {
            if let continueModel, !continueModel.items.isEmpty {
                ContinueWatchingShelf(
                    items: continueModel.items,
                    onPlay: { recording in Task { await resume(recording) } },
                    onRemove: { recording in Task { await continueModel.remove(recording) } }
                )
                .focusSection()
            }

            if model.showsRecents {
                RecentCardRow(
                    channels: model.recentChannels,
                    playingID: playerModel.currentChannel?.id,
                    onSelect: { open(channel: $0) },
                    onClear: { Task { await model.clearRecents() } }
                )
                .focusSection()
            }

            GuideFilterBar(selection: Bindable(model).filter)
                .focusSection()

            // Busy channels are left out of the rail (and Recent).
            if let note = model.tunersBusyNote {
                TunersBusyNote(text: note)
                    .padding(.horizontal, 20)
                    .padding(.vertical, 8)
            }

            if model.allTunersBusy {
                Spacer(minLength: 0)
            } else if visible.isEmpty, model.filter != .all {
                GuideFilterEmptyView(filter: model.filter) { model.filter = .all }
                    .frame(maxHeight: .infinity)
                    .focusSection()
            } else {
                List {
                    ForEach(visible) { row in
                        // Once per row: the view and its VoiceOver label read the same answer.
                        let highlight = model.highlight(for: row, at: now)
                        Button {
                            open(channel: row.channel)
                        } label: {
                            RailRowView(
                                row: row,
                                now: now,
                                isPlaying: playerModel.currentChannel?.id == row.channel.id,
                                highlight: highlight
                            )
                            .opacity(row.channel.hasNoSignal ? 0.55 : 1)
                        }
                        // Default button style → system focus scale / highlight.
                        // Click-and-hold Select opens the menu.
                        .contextMenu {
                            if model.supportsFavorites {
                                favoriteButton(for: row.channel, model: model)
                            }
                            RecordMenuItems(
                                channel: row.channel,
                                nowNext: row.nowNext,
                                flow: recordFlow,
                                openRecordings: { showRecordings = true }
                            )
                        }
                        .listRowBackground(Theme.bg)
                        .accessibilityElement(children: .combine)
                        .accessibilityLabel(accessibilityLabel(for: row, highlight: highlight))
                        .accessibilityHint("Play this channel. Press and hold to favorite or record.")
                        .accessibilityActions {
                            if model.supportsFavorites {
                                favoriteButton(for: row.channel, model: model)
                            }
                        }
                    }
                }
                .listStyle(.plain)
                .focusSection()
            }
        }
        .bowtieToast(model.actionError ?? continueModel?.actionError) {
            if model.actionError != nil {
                model.dismissActionError()
            } else {
                continueModel?.actionError = nil
            }
        }
    }

    /// "Favorite" / "Unfavorite" — shared by the context menu and VoiceOver.
    private func favoriteButton(for channel: Channel, model: ChannelListModel) -> some View {
        Button {
            Task { await model.toggleFavorite(channelId: channel.id) }
        } label: {
            if channel.isFavorite {
                Label("Unfavorite", systemImage: "star.slash")
            } else {
                Label("Favorite", systemImage: "star")
            }
        }
    }

    // MARK: - Actions

    private func ensureListModel() async {
        guard listModel == nil, let client = appModel.client else { return }
        let model = ChannelListModel(client: client)
        listModel = model
        continueModel = ContinueWatchingModel(client: client)
        recordingsModel = RecordingsModel(client: client)
        // Reload after scheduling so the program shows its REC mark.
        recordFlow = RecordFlow(client: client) { Task { await model.load() } }
    }

    /// Continue watching: plays the recording from where it was left.
    private func resume(_ recording: Recording) async {
        guard let recordingsModel,
              let playback = await recordingsModel.resume(recording, stopping: playerModel)
        else { return }
        activePlayback = ContinuePlayback(playback: playback)
    }

    private func open(channel: Channel) {
        playingChannel = channel
        Task {
            await playerModel.play(channel: channel)
        }
    }

    private func nowTitle(for channel: Channel) -> String? {
        guard case .loaded(let rows) = listModel?.state,
              let row = rows.first(where: { $0.channel.id == channel.id }),
              let title = row.nowNext.now?.title,
              !title.isEmpty
        else {
            return nil
        }
        return title
    }

    /// When the program now on `channel` ends, if the guide knows (sleep timer).
    private func programEnd(for channel: Channel) -> Date? {
        guard case .loaded(let rows) = listModel?.state else { return nil }
        return rows.first(where: { $0.channel.id == channel.id })?.nowNext.now?.stop
    }

    private func accessibilityLabel(for row: ChannelListModel.Row, highlight: GuideFilter.RowHighlight) -> String {
        var parts = [
            "Channel \(row.channel.guideNumber)",
            row.channel.name,
        ]
        if row.channel.isFavorite {
            parts.append("Favorite")
        }
        if row.channel.hasNoSignal {
            parts.append("No signal last time")
        }
        if let nowTitle = row.nowNext.now?.title, !nowTitle.isEmpty {
            parts.append("Now \(nowTitle)")
        }
        if row.nowNext.now?.recording != nil {
            parts.append("Set to record")
        }
        if let now = row.nowNext.now, now.isLocked {
            parts.append(ParentalLockMark.accessibilityText(rating: now.rating ?? ""))
        }
        if let nextTitle = row.nowNext.next?.title, !nextTitle.isEmpty {
            parts.append("Next \(nextTitle)")
        }
        if let later = highlight.later {
            parts.append(GuideFilterLaterLine.accessibilityText(later))
        }
        if playerModel.currentChannel?.id == row.channel.id {
            parts.append("Playing")
        }
        return parts.joined(separator: ", ")
    }
}

// MARK: - Continue watching destination

/// A recording started from Continue watching. Its own type, so this
/// destination doesn't clash with Recordings' `Playback` destination further
/// down the same navigation stack.
private struct ContinuePlayback: Hashable, Identifiable {
    let playback: RecordingsModel.Playback

    var id: Int64 { playback.id }
}

// MARK: - Condensed rail row

private struct RailRowView: View {
    let row: ChannelListModel.Row
    let now: Date
    let isPlaying: Bool
    /// Category filter: lines outside it are dimmed; a later match may show.
    var highlight = GuideFilter.RowHighlight(nowMatches: true, nextMatches: true, later: nil)

    var body: some View {
        HStack(alignment: .center, spacing: 28) {
            Text(row.channel.guideNumber)
                .font(Theme.channelNumber(44))
                .foregroundStyle(isPlaying ? Theme.amber : Theme.text)
                .frame(minWidth: 110, alignment: .trailing)
                .lineLimit(1)
                .minimumScaleFactor(0.7)

            VStack(alignment: .leading, spacing: 6) {
                HStack(spacing: 10) {
                    Text(row.channel.name)
                        .font(Theme.label(28))
                        .foregroundStyle(Theme.text)
                        .lineLimit(1)

                    if row.channel.isFavorite {
                        Image(systemName: "star.fill")
                            .font(.system(size: 22, weight: .semibold))
                            .foregroundStyle(Theme.amber)
                            .accessibilityHidden(true)
                    }
                }

                if row.channel.hasNoSignal {
                    // Last tune got no signal; still tappable (signal may return).
                    Text("No signal")
                        .font(Theme.label(18))
                        .foregroundStyle(Theme.alert)
                        .textCase(.uppercase)
                }

                HStack(spacing: 16) {
                    if let program = row.nowNext.now {
                        Text(program.title.isEmpty ? "On now" : program.title)
                            .font(Theme.body(20))
                            .foregroundStyle(Theme.text.opacity(0.92))
                            .lineLimit(1)
                            .opacity(highlight.nowMatches ? 1 : 0.4)

                        if program.recording != nil {
                            RecordingMarkDot(size: 16)
                        }

                        if program.isLocked {
                            ParentalLockMark(rating: program.rating ?? "", size: 18)
                        }

                        // Compact progress for the current program.
                        GeometryReader { geo in
                            ZStack(alignment: .leading) {
                                Capsule().fill(Theme.line)
                                Capsule()
                                    .fill(Theme.amber)
                                    .frame(width: max(0, geo.size.width * Self.progress(for: program, at: now)))
                            }
                        }
                        .frame(width: 120, height: 6)
                        .accessibilityHidden(true)
                    } else {
                        Text("No guide data")
                            .font(Theme.body(20))
                            .foregroundStyle(Theme.dim)
                            .lineLimit(1)
                    }

                    if let next = row.nowNext.next {
                        Text(next.title.isEmpty ? "Up next" : "Next: \(next.title)")
                            .font(Theme.body(18))
                            .foregroundStyle(Theme.dim)
                            .lineLimit(1)
                            .opacity(highlight.nextMatches ? 1 : 0.4)
                    }
                }

                if let later = highlight.later {
                    GuideFilterLaterLine(program: later, size: 18)
                }
            }

            Spacer(minLength: 0)
        }
        .padding(.vertical, 12)
        .contentShape(Rectangle())
    }

    static func progress(for program: GuideProgram, at date: Date) -> Double {
        let total = program.stop.timeIntervalSince(program.start)
        guard total > 0 else { return 0 }
        let elapsed = date.timeIntervalSince(program.start)
        return min(1, max(0, elapsed / total))
    }
}

// MARK: - Recent cards

/// Row of recently watched channels above the rail; Select plays the channel.
private struct RecentCardRow: View {
    let channels: [Channel]
    let playingID: Int64?
    let onSelect: (Channel) -> Void
    let onClear: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("Recent")
                .font(Theme.label(24))
                .foregroundStyle(Theme.dim)
                .textCase(.uppercase)
                .padding(.horizontal, 80)
                .accessibilityAddTraits(.isHeader)

            ScrollView(.horizontal, showsIndicators: false) {
                LazyHStack(spacing: 40) {
                    ForEach(channels) { channel in
                        Button {
                            onSelect(channel)
                        } label: {
                            card(for: channel)
                        }
                        .buttonStyle(.card)
                        .contextMenu {
                            Button(role: .destructive) {
                                onClear()
                            } label: {
                                Label("Clear Recent", systemImage: "clock.arrow.circlepath")
                            }
                        }
                        .accessibilityLabel(accessibilityLabel(for: channel))
                        .accessibilityHint("Play this channel")
                    }
                }
                // Room for the card focus lift so it isn't clipped.
                .padding(.horizontal, 80)
                .padding(.vertical, 24)
            }
            .scrollClipDisabled()
        }
    }

    private func card(for channel: Channel) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(channel.guideNumber)
                .font(Theme.channelNumber(36))
                .foregroundStyle(playingID == channel.id ? Theme.amber : Theme.text)
                .lineLimit(1)
                .minimumScaleFactor(0.7)
            Text(channel.name)
                .font(Theme.label(22))
                .foregroundStyle(Theme.text)
                .lineLimit(1)
        }
        .padding(.horizontal, 24)
        .padding(.vertical, 18)
        .frame(width: 260, alignment: .leading)
        .background(Theme.raised)
    }

    private func accessibilityLabel(for channel: Channel) -> String {
        var parts = ["Channel \(channel.guideNumber)", channel.name]
        if playingID == channel.id {
            parts.append("Playing")
        }
        return parts.joined(separator: ", ")
    }
}

#Preview {
    let store = InMemorySessionStore()
    store.save(server: URL(string: "http://192.168.1.50:8400")!, refreshToken: "tok")
    let app = AppModel(store: store)
    let client = BowtieClient(
        server: URL(string: "http://192.168.1.50:8400")!,
        store: store
    )
    return ChannelRailView(
        appModel: app,
        playerModel: PlayerModel(client: client, caps: Caps.make(maxHeight: 2160))
    )
}
