import SwiftUI
import BowtieKit

/// Channel list with now/next guide rows. Home screen when `AppModel.phase == .ready`.
struct ChannelListView: View {
    @Bindable var appModel: AppModel
    /// Session-replace player owned by the root so sign-out / change-server can stop it.
    @Bindable var playerModel: PlayerModel

    @State private var listModel: ChannelListModel?
    @State private var playingChannel: Channel?
    @State private var showSettings = false
    @State private var showRecordings = false
    @State private var recordFlow: RecordFlow?
    @State private var now = Date()

    @Environment(\.scenePhase) private var scenePhase

    /// Spec-mandated empty copy (verbatim).
    static let emptyCopy = "No channels yet. Ask your admin to enable some."

    private let refreshInterval: Duration = .seconds(5 * 60)
    private let clockTick: Duration = .seconds(30)

    var body: some View {
        NavigationStack {
            content
                .navigationTitle("Channels")
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .topBarLeading) {
                        Button {
                            showRecordings = true
                        } label: {
                            Image(systemName: "recordingtape")
                                .foregroundStyle(Theme.amber)
                        }
                        .accessibilityLabel("Recordings")
                        .accessibilityHint("Open your recordings")
                    }
                    ToolbarItem(placement: .topBarTrailing) {
                        Button {
                            showSettings = true
                        } label: {
                            Image(systemName: "gearshape")
                                .foregroundStyle(Theme.amber)
                        }
                        .accessibilityLabel("Settings")
                        .accessibilityHint("Open server and account settings")
                    }
                }
                .toolbarBackground(Theme.bg, for: .navigationBar)
                .toolbarColorScheme(.dark, for: .navigationBar)
                .navigationDestination(item: $playingChannel) { channel in
                    if let serverURL = appModel.serverURL {
                        PlayerView(
                            channel: channel,
                            serverURL: serverURL,
                            maxQuality: appModel.user?.maxQuality ?? "",
                            nowTitle: nowTitle(for: channel),
                            playerModel: playerModel
                        )
                    } else {
                        // Ready phase always has a server; defensive failed copy.
                        VStack(spacing: 16) {
                            Text("Not connected to a server")
                                .font(Theme.body())
                                .foregroundStyle(Theme.alert)
                            Button("Back") { playingChannel = nil }
                                .foregroundStyle(Theme.amber)
                        }
                        .bowtieScreenBackground()
                    }
                }
                .navigationDestination(isPresented: $showRecordings) {
                    if let client = appModel.client {
                        RecordingsView(client: client, playerModel: playerModel)
                    }
                }
                .recordFlowAlerts(recordFlow)
                .sheet(isPresented: $showSettings) {
                    NavigationStack {
                        SettingsView(appModel: appModel)
                            .toolbar {
                                ToolbarItem(placement: .topBarTrailing) {
                                    Button("Done") { showSettings = false }
                                        .foregroundStyle(Theme.amber)
                                        .accessibilityLabel("Done")
                                }
                            }
                    }
                    .presentationDragIndicator(.visible)
                }
        }
        .preferredColorScheme(.dark)
        .task {
            await ensureListModel()
            await listModel?.load()
        }
        // Auto-refresh every 5 minutes while the list is visible.
        .task(id: listModel != nil) {
            guard listModel != nil else { return }
            while !Task.isCancelled {
                do {
                    try await Task.sleep(for: refreshInterval)
                } catch {
                    return
                }
                await listModel?.refreshIfStale()
            }
        }
        // Advance "now" so progress capsules keep moving while on screen.
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
        .onChange(of: scenePhase) { _, phase in
            if phase == .active {
                Task { await listModel?.refreshIfStale() }
            }
        }
        // Player create-session 404 → reload channel list (disabled/unknown channel).
        .onChange(of: playerModel.channelsStaleGeneration) { _, _ in
            Task { await listModel?.load() }
        }
        // Back from the player: the server may have just recorded a watch.
        .onChange(of: playingChannel) { old, new in
            if old != nil, new == nil {
                Task { await listModel?.refreshRecents() }
            }
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
                    listView(rows: rows, model: listModel)
                }
            } else {
                loadingView
            }
        }
        .bowtieScreenBackground()
    }

    private var loadingView: some View {
        VStack(spacing: 12) {
            ProgressView()
                .tint(Theme.amber)
            Text("Loading channels…")
                .font(Theme.body(15))
                .foregroundStyle(Theme.dim)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Loading channels")
    }

    private var emptyView: some View {
        VStack(spacing: 12) {
            Text(Self.emptyCopy)
                .font(Theme.body())
                .foregroundStyle(Theme.dim)
                .multilineTextAlignment(.center)
                .padding(.horizontal, 32)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .accessibilityLabel(Self.emptyCopy)
    }

    private func failedView(message: String, model: ChannelListModel) -> some View {
        VStack(spacing: 16) {
            Text(message)
                .font(Theme.body())
                .foregroundStyle(Theme.alert)
                .multilineTextAlignment(.center)
                .padding(.horizontal, 32)
                .accessibilityLabel(message)

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
            .accessibilityLabel("Try again")
            .accessibilityHint("Reload the channel list")
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func listView(rows: [ChannelListModel.Row], model: ChannelListModel) -> some View {
        List {
            if model.showsRecents {
                Section {
                    RecentChipRow(
                        channels: model.recentChannels,
                        playingID: playerModel.currentChannel?.id,
                        onSelect: { open(channel: $0) },
                        onClear: { Task { await model.clearRecents() } }
                    )
                    .listRowInsets(EdgeInsets())
                    .listRowBackground(Theme.bg)
                    .listRowSeparator(.hidden)
                } header: {
                    sectionHeader("Recent")
                }
            }

            let favorites = model.favoriteRows
            if favorites.isEmpty {
                ForEach(rows) { row in
                    channelRow(row, model: model)
                }
            } else {
                Section {
                    ForEach(favorites) { row in
                        channelRow(row, model: model)
                    }
                } header: {
                    sectionHeader("Favorites")
                }
                Section {
                    ForEach(model.otherRows) { row in
                        channelRow(row, model: model)
                    }
                } header: {
                    sectionHeader("Channels")
                }
            }
        }
        .listStyle(.plain)
        .scrollContentBackground(.hidden)
        .refreshable {
            await model.load()
        }
        .bowtieToast(model.actionError) {
            model.dismissActionError()
        }
    }

    private func sectionHeader(_ title: String) -> some View {
        Text(title)
            .font(Theme.label(13))
            .foregroundStyle(Theme.dim)
            .textCase(.uppercase)
            .accessibilityAddTraits(.isHeader)
    }

    private func channelRow(_ row: ChannelListModel.Row, model: ChannelListModel) -> some View {
        Button {
            open(channel: row.channel)
        } label: {
            ChannelRowView(
                row: row,
                now: now,
                isPlaying: playerModel.currentChannel?.id == row.channel.id
            )
            .opacity(row.channel.hasNoSignal ? 0.55 : 1)
        }
        .buttonStyle(.plain)
        .listRowBackground(Theme.bg)
        .listRowSeparatorTint(Theme.line)
        .swipeActions(edge: .leading, allowsFullSwipe: true) {
            if model.supportsFavorites {
                favoriteButton(for: row.channel, model: model)
                    .tint(Theme.amber)
            }
        }
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
        .accessibilityElement(children: .combine)
        .accessibilityLabel(accessibilityLabel(for: row))
        .accessibilityHint("Play this channel")
        .accessibilityAddTraits(.isButton)
        .accessibilityActions {
            if model.supportsFavorites {
                favoriteButton(for: row.channel, model: model)
            }
        }
    }

    /// "Favorite" / "Unfavorite" — shared by the swipe action, context menu and VoiceOver.
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
        // Reload after scheduling so the program shows its REC mark.
        recordFlow = RecordFlow(client: client) { Task { await model.load() } }
    }

    private func open(channel: Channel) {
        playingChannel = channel
        Task {
            await playerModel.play(channel: channel)
        }
    }

    /// Snapshot now-title at open time for the player chrome overlay.
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

    private func accessibilityLabel(for row: ChannelListModel.Row) -> String {
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
        if let nextTitle = row.nowNext.next?.title, !nextTitle.isEmpty {
            parts.append("Next \(nextTitle)")
        }
        if playerModel.currentChannel?.id == row.channel.id {
            parts.append("Playing")
        }
        return parts.joined(separator: ", ")
    }
}

// MARK: - Row

private struct ChannelRowView: View {
    let row: ChannelListModel.Row
    let now: Date
    let isPlaying: Bool

    var body: some View {
        HStack(alignment: .center, spacing: 14) {
            Text(row.channel.guideNumber)
                .font(Theme.channelNumber(Theme.channelNumberSize))
                .foregroundStyle(isPlaying ? Theme.amber : Theme.text)
                .frame(minWidth: 64, alignment: .trailing)
                .lineLimit(1)
                .minimumScaleFactor(0.7)

            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 6) {
                    Text(row.channel.name)
                        .font(Theme.label(17))
                        .foregroundStyle(Theme.text)
                        .lineLimit(1)

                    if row.channel.isFavorite {
                        Image(systemName: "star.fill")
                            .font(.system(size: 12, weight: .semibold))
                            .foregroundStyle(Theme.amber)
                            .accessibilityHidden(true)
                    }
                }

                if row.channel.hasNoSignal {
                    // Last tune got no signal; still tappable (signal may return).
                    Text("No signal")
                        .font(Theme.label(12))
                        .foregroundStyle(Theme.alert)
                        .textCase(.uppercase)
                }

                if let program = row.nowNext.now {
                    VStack(alignment: .leading, spacing: 4) {
                        HStack(spacing: 6) {
                            Text(program.title.isEmpty ? "On now" : program.title)
                                .font(Theme.body(14))
                                .foregroundStyle(Theme.text.opacity(0.92))
                                .lineLimit(1)
                            if program.recording != nil {
                                RecordingMarkDot(size: 10)
                            }
                        }

                        ProgressCapsule(progress: Self.progress(for: program, at: now))
                            .frame(maxWidth: 180)
                    }
                } else {
                    Text("No guide data")
                        .font(Theme.body(14))
                        .foregroundStyle(Theme.dim)
                        .lineLimit(1)
                }

                if let next = row.nowNext.next {
                    Text(next.title.isEmpty ? "Up next" : "Next: \(next.title)")
                        .font(Theme.body(13))
                        .foregroundStyle(Theme.dim)
                        .lineLimit(1)
                }
            }

            Spacer(minLength: 0)

            Image(systemName: "chevron.right")
                .font(.system(size: 12, weight: .semibold))
                .foregroundStyle(Theme.dim.opacity(0.7))
                .accessibilityHidden(true)
        }
        .padding(.vertical, 8)
        .contentShape(Rectangle())
    }

    static func progress(for program: GuideProgram, at date: Date) -> Double {
        let total = program.stop.timeIntervalSince(program.start)
        guard total > 0 else { return 0 }
        let elapsed = date.timeIntervalSince(program.start)
        return min(1, max(0, elapsed / total))
    }
}

// MARK: - Recent chips

/// Horizontal strip of recently watched channels; a tap plays like a row tap.
private struct RecentChipRow: View {
    let channels: [Channel]
    let playingID: Int64?
    let onSelect: (Channel) -> Void
    let onClear: () -> Void

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 10) {
                ForEach(channels) { channel in
                    Button {
                        onSelect(channel)
                    } label: {
                        chip(for: channel)
                    }
                    .buttonStyle(.plain)
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
            .padding(.horizontal, 16)
            .padding(.vertical, 8)
        }
    }

    private func chip(for channel: Channel) -> some View {
        HStack(spacing: 8) {
            Text(channel.guideNumber)
                .font(Theme.channelNumber(17))
                .foregroundStyle(playingID == channel.id ? Theme.amber : Theme.text)
            Text(channel.name)
                .font(Theme.label(15))
                .foregroundStyle(Theme.text)
                .lineLimit(1)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 9)
        .background(Theme.raised, in: Capsule())
        .overlay(Capsule().stroke(Theme.line, lineWidth: 1))
        .contentShape(Capsule())
    }

    private func accessibilityLabel(for channel: Channel) -> String {
        var parts = ["Channel \(channel.guideNumber)", channel.name]
        if playingID == channel.id {
            parts.append("Playing")
        }
        return parts.joined(separator: ", ")
    }
}

/// Amber fill on a charcoal track for now-playing progress.
private struct ProgressCapsule: View {
    let progress: Double

    var body: some View {
        GeometryReader { geo in
            ZStack(alignment: .leading) {
                Capsule()
                    .fill(Theme.line)
                Capsule()
                    .fill(Theme.amber)
                    .frame(width: max(0, geo.size.width * progress))
            }
        }
        .frame(height: 4)
        .accessibilityHidden(true)
    }
}

// MARK: - Preview

#Preview {
    let store = InMemorySessionStore()
    store.save(server: URL(string: "http://192.168.1.50:8400")!, refreshToken: "tok")
    let app = AppModel(store: store)
    let client = BowtieClient(
        server: URL(string: "http://192.168.1.50:8400")!,
        store: store
    )
    return ChannelListView(
        appModel: app,
        playerModel: PlayerModel(client: client, caps: Caps.make(maxHeight: 1080))
    )
}
