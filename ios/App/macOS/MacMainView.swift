import SwiftUI
import AppKit
import BowtieKit

/// Sidebar row identity. A channel can appear in Recent and in Favorites /
/// Channels at once, so its section is part of the tag.
enum SidebarItem: Hashable {
    case recordings
    case channel(Int64, ChannelSection)
    /// A guide search result (`GuideSearchResult.id`).
    case search(String)
}

enum ChannelSection: Hashable {
    case recent
    case favorites
    case all
}

/// Signed-in window: channels (Recent, Favorites, Channels) and Recordings in
/// the sidebar; live TV or recordings in the detail pane.
struct MacMainView: View {
    @Bindable var appModel: AppModel
    @Bindable var playerModel: PlayerModel

    @State private var listModel: ChannelListModel?
    @State private var recordFlow: RecordFlow?
    @State private var searchModel: GuideSearchModel?
    @State private var searchText = ""
    @State private var now = Date()
    @State private var selection: SidebarItem?
    @State private var columnVisibility: NavigationSplitViewVisibility = .all
    /// Live AVPlayer owner; lives here so the commands and PiP-end stop work
    /// even while the live view is off screen.
    @State private var bridge = PlayerBridge()
    @State private var activeRecording: RecordingPlayerController?

    /// Spec-mandated empty copy (verbatim).
    static let emptyCopy = "No channels yet. Ask your admin to enable some."

    private let refreshInterval: Duration = .seconds(5 * 60)

    var body: some View {
        NavigationSplitView(columnVisibility: $columnVisibility) {
            sidebar
                .navigationSplitViewColumnWidth(min: 220, ideal: 270, max: 380)
        } detail: {
            detail
        }
        .recordFlowAlerts(recordFlow)
        .focusedSceneValue(\.playbackActions, playbackActions)
        .task {
            await ensureListModel()
            await listModel?.load()
        }
        // Auto-refresh every 5 minutes while signed in.
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
        .onAppear {
            MacAppDelegate.onTerminate = { [playerModel, bridge] in
                activeRecording?.finish()
                bridge.replacePlayer(nil)
                await playerModel.stop()
            }
        }
        .onDisappear {
            MacAppDelegate.onTerminate = nil
            activeRecording?.finish()
            activeRecording = nil
        }
        .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
            Task { await listModel?.refreshIfStale() }
        }
        // Player create-session 404 → reload channel list (disabled/unknown channel).
        .onChange(of: playerModel.channelsStaleGeneration) { _, _ in
            Task { await listModel?.load() }
        }
        // A channel change may have recorded a watch of the previous one.
        .onChange(of: playerModel.currentChannel?.id) { _, _ in
            Task { await listModel?.refreshRecents() }
        }
        .onChange(of: selection) { old, new in
            selectionChanged(from: old, to: new)
        }
        // PiP ended after the live view was left (e.g. for Recordings).
        .onChange(of: bridge.pipDidEndAndShouldStop) { _, shouldStop in
            if shouldStop {
                bridge.pipDidEndAndShouldStop = false
                bridge.replacePlayer(nil)
                if !isLiveSelected {
                    Task { await playerModel.stop() }
                }
            }
        }
        // ⌘F full screen hides the sidebar; leaving brings it back.
        .onReceive(NotificationCenter.default.publisher(for: NSWindow.willEnterFullScreenNotification)) { _ in
            withAnimation { columnVisibility = .detailOnly }
        }
        .onReceive(NotificationCenter.default.publisher(for: NSWindow.willExitFullScreenNotification)) { _ in
            withAnimation { columnVisibility = .all }
        }
    }

    // MARK: - Sidebar

    @ViewBuilder
    private var sidebar: some View {
        Group {
            if let listModel {
                switch listModel.state {
                case .loading:
                    ProgressView()
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                        .accessibilityLabel("Loading channels")
                case .failed(let message):
                    VStack(spacing: 12) {
                        Text(message)
                            .font(Theme.body(14))
                            .foregroundStyle(Theme.alert)
                            .multilineTextAlignment(.center)
                        Button("Try Again") {
                            Task { await listModel.load() }
                        }
                    }
                    .padding()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                case .empty, .loaded:
                    if isSearching, let searchModel {
                        searchList(searchModel)
                    } else {
                        channelList(listModel)
                    }
                }
            } else {
                ProgressView()
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .searchable(text: $searchText, placement: .sidebar, prompt: "Search the guide")
        // Debounced: search once typing pauses.
        .task(id: searchText) {
            do {
                try await Task.sleep(for: .milliseconds(300))
            } catch {
                return
            }
            now = Date()
            await searchModel?.search(searchText)
        }
        .navigationTitle("Bowtie")
        .toolbar {
            ToolbarItem {
                Button {
                    Task { await listModel?.load() }
                } label: {
                    Label("Reload Channels", systemImage: "arrow.clockwise")
                }
                .help("Reload channels and guide")
            }
        }
    }

    private func channelList(_ model: ChannelListModel) -> some View {
        List(selection: $selection) {
            Section("Library") {
                Label("Recordings", systemImage: "recordingtape")
                    .tag(SidebarItem.recordings)
            }

            if model.showsRecents {
                Section {
                    ForEach(model.recentChannels) { channel in
                        channelRow(channel, nowNext: nowNext(for: channel.id), section: .recent, model: model)
                            .contextMenu {
                                Button("Clear Recent") {
                                    Task { await model.clearRecents() }
                                }
                            }
                    }
                } header: {
                    Text("Recent")
                }
            }

            let favorites = model.favoriteRows
            if !favorites.isEmpty {
                Section("Favorites") {
                    ForEach(favorites) { row in
                        rowWithMenu(row, section: .favorites, model: model)
                    }
                }
            }

            Section("Channels") {
                if case .empty = model.state {
                    Text(Self.emptyCopy)
                        .font(Theme.body(13))
                        .foregroundStyle(Theme.dim)
                } else {
                    ForEach(model.otherRows) { row in
                        rowWithMenu(row, section: .all, model: model)
                    }
                }
            }
        }
        .listStyle(.sidebar)
        .bowtieToast(model.actionError) {
            model.dismissActionError()
        }
    }

    // MARK: - Search

    private var isSearching: Bool {
        !searchText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    private func searchList(_ model: GuideSearchModel) -> some View {
        List(selection: $selection) {
            Section("Search Results") {
                searchSectionContent(model)
            }
        }
        .listStyle(.sidebar)
    }

    @ViewBuilder
    private func searchSectionContent(_ model: GuideSearchModel) -> some View {
        switch model.state {
        case .idle, .searching:
            ProgressView()
                .controlSize(.small)
                .frame(maxWidth: .infinity)
        case .empty:
            Text("Nothing in the guide matches \u{201C}\(model.query)\u{201D}.")
                .font(Theme.body(12))
                .foregroundStyle(Theme.dim)
        case .failed(let message):
            Text(message)
                .font(Theme.body(12))
                .foregroundStyle(Theme.alert)
        case .results(let results):
            ForEach(results) { result in
                MacSearchResultRow(result: result, now: now)
                    .tag(SidebarItem.search(result.id))
                    .contextMenu {
                        SearchResultActions(
                            result: result,
                            now: now,
                            flow: recordFlow,
                            onWatch: { watch($0) },
                            openRecordings: { selection = .recordings }
                        )
                    }
            }
        }
    }

    /// "Watch" from search: select the channel like a sidebar click.
    private func watch(_ channel: Channel) {
        selection = .channel(channel.id, channel.isFavorite ? .favorites : .all)
    }

    private func searchResult(id: String) -> GuideSearchResult? {
        searchModel?.results.first { $0.id == id }
    }

    private func rowWithMenu(_ row: ChannelListModel.Row, section: ChannelSection, model: ChannelListModel) -> some View {
        channelRow(row.channel, nowNext: row.nowNext, section: section, model: model)
            .contextMenu {
                if model.supportsFavorites {
                    Button {
                        Task { await model.toggleFavorite(channelId: row.channel.id) }
                    } label: {
                        if row.channel.isFavorite {
                            Label("Unfavorite", systemImage: "star.slash")
                        } else {
                            Label("Favorite", systemImage: "star")
                        }
                    }
                    Divider()
                }
                RecordMenuItems(
                    channel: row.channel,
                    nowNext: row.nowNext,
                    flow: recordFlow,
                    openRecordings: { selection = .recordings }
                )
            }
    }

    private func channelRow(
        _ channel: Channel,
        nowNext: GuideLogic.NowNext?,
        section: ChannelSection,
        model: ChannelListModel
    ) -> some View {
        let isPlaying = playerModel.currentChannel?.id == channel.id
        return HStack(spacing: 10) {
            Text(channel.guideNumber)
                .font(Theme.channelNumber(17))
                .foregroundStyle(isPlaying ? Theme.amber : Theme.text)
                .frame(minWidth: 44, alignment: .trailing)
                .lineLimit(1)
                .minimumScaleFactor(0.7)

            VStack(alignment: .leading, spacing: 1) {
                HStack(spacing: 4) {
                    Text(channel.name)
                        .font(Theme.label(13))
                        .lineLimit(1)
                    if section != .favorites, channel.isFavorite {
                        Image(systemName: "star.fill")
                            .font(.system(size: 9, weight: .semibold))
                            .foregroundStyle(Theme.amber)
                            .accessibilityHidden(true)
                    }
                }
                if channel.hasNoSignal {
                    Text("No signal")
                        .font(Theme.label(10))
                        .foregroundStyle(Theme.alert)
                        .textCase(.uppercase)
                } else if let now = nowNext?.now {
                    HStack(spacing: 4) {
                        Text(now.title.isEmpty ? "On now" : now.title)
                            .font(Theme.body(11))
                            .foregroundStyle(Theme.dim)
                            .lineLimit(1)
                        if now.recording != nil {
                            RecordingMarkDot(size: 8)
                        }
                        if now.isLocked {
                            ParentalLockMark(rating: now.rating ?? "", size: 9)
                        }
                    }
                }
            }
        }
        .opacity(channel.hasNoSignal ? 0.6 : 1)
        .tag(SidebarItem.channel(channel.id, section))
        .help(helpText(channel: channel, nowNext: nowNext))
        .accessibilityElement(children: .combine)
        .accessibilityLabel(accessibilityLabel(channel: channel, nowNext: nowNext, isPlaying: isPlaying))
    }

    // MARK: - Detail

    @ViewBuilder
    private var detail: some View {
        switch selection {
        case .recordings:
            if let client = appModel.client {
                MacRecordingsView(client: client, playerModel: playerModel, activeRecording: $activeRecording)
            }
        case .search(let id):
            if let result = searchResult(id: id) {
                MacSearchResultDetail(
                    result: result,
                    now: now,
                    flow: recordFlow,
                    onWatch: { watch($0) },
                    openRecordings: { selection = .recordings }
                )
            } else {
                Text("No longer in the results")
                    .font(Theme.body(14))
                    .foregroundStyle(Theme.dim)
                    .bowtieScreenBackground()
            }
        case .channel:
            if let serverURL = appModel.serverURL {
                MacLivePlayerView(
                    serverURL: serverURL,
                    maxQuality: appModel.user?.maxQuality ?? "",
                    nowTitle: playerModel.currentChannel.flatMap { nowNext(for: $0.id)?.now?.title },
                    playerModel: playerModel,
                    bridge: bridge,
                    onLeave: { selection = nil }
                )
                .navigationTitle(playerModel.currentChannel.map { "\($0.guideNumber) \($0.name)" } ?? "Live TV")
            }
        case nil:
            VStack(spacing: 10) {
                Image(systemName: "tv")
                    .font(.system(size: 40, weight: .light))
                    .foregroundStyle(Theme.dim)
                Text("Pick a channel")
                    .font(Theme.title(18))
                    .foregroundStyle(Theme.text)
                Text("⌘↑ and ⌘↓ change channels.")
                    .font(Theme.body(13))
                    .foregroundStyle(Theme.dim)
            }
            .bowtieScreenBackground()
            .navigationTitle("Bowtie")
        }
    }

    // MARK: - Selection / commands

    private var isLiveSelected: Bool {
        if case .channel = selection { return true }
        return false
    }

    private func selectionChanged(from old: SidebarItem?, to new: SidebarItem?) {
        if old == .recordings, new != .recordings {
            activeRecording?.finish()
            activeRecording = nil
        }
        guard case .channel(let id, _) = new,
              let channel = listModel?.rows.first(where: { $0.channel.id == id })?.channel
                ?? listModel?.recentChannels.first(where: { $0.id == id })
                ?? searchModel?.results.first(where: { $0.channelId == id })?.channel
        else {
            return
        }
        // Same channel from another section: keep the session unless it ended.
        if playerModel.currentChannel?.id == id {
            switch playerModel.state {
            case .idle, .failed, .tunersBusy:
                break
            default:
                return
            }
        }
        Task { await playerModel.play(channel: channel) }
    }

    /// Next / previous in the sidebar's order (favorites, then the rest), wrapping.
    private func stepChannel(by delta: Int) {
        guard let rows = listModel?.rows, !rows.isEmpty else { return }
        let current = playerModel.currentChannel?.id
        let index = rows.firstIndex { $0.channel.id == current }
        let next: Int
        if let index {
            next = (index + delta + rows.count) % rows.count
        } else {
            next = delta > 0 ? 0 : rows.count - 1
        }
        let channel = rows[next].channel
        selection = .channel(channel.id, channel.isFavorite ? .favorites : .all)
    }

    private var playbackActions: PlaybackActions {
        let livePlayer = isLiveSelected ? bridge.player : nil
        let vodPlayer = selection == .recordings ? activeRecording?.player : nil
        let player = livePlayer ?? vodPlayer
        return PlaybackActions(
            togglePlay: player.map { player in
                {
                    if player.timeControlStatus == .paused {
                        player.play()
                    } else {
                        player.pause()
                    }
                }
            },
            goLive: livePlayer == nil ? nil : { bridge.jumpToLive() },
            channelUp: { stepChannel(by: 1) },
            channelDown: { stepChannel(by: -1) },
            toggleFullScreen: {
                (NSApp.keyWindow ?? NSApp.mainWindow)?.toggleFullScreen(nil)
            }
        )
    }

    // MARK: - Helpers

    private func ensureListModel() async {
        guard listModel == nil, let client = appModel.client else { return }
        let model = ChannelListModel(client: client)
        let search = GuideSearchModel(client: client)
        listModel = model
        searchModel = search
        // Reload after scheduling so the program shows its REC mark.
        recordFlow = RecordFlow(client: client) {
            Task {
                await model.load()
                if !search.query.isEmpty {
                    await search.refresh()
                }
            }
        }
    }

    private func nowNext(for channelId: Int64) -> GuideLogic.NowNext? {
        listModel?.rows.first { $0.channel.id == channelId }?.nowNext
    }

    private func helpText(channel: Channel, nowNext: GuideLogic.NowNext?) -> String {
        var lines = ["\(channel.guideNumber) \(channel.name)"]
        if let now = nowNext?.now, !now.title.isEmpty {
            lines.append("Now: \(now.title)" + (now.isLocked ? " (locked by parental controls)" : ""))
        }
        if let next = nowNext?.next, !next.title.isEmpty {
            lines.append("Next: \(next.title)")
        }
        return lines.joined(separator: "\n")
    }

    private func accessibilityLabel(channel: Channel, nowNext: GuideLogic.NowNext?, isPlaying: Bool) -> String {
        var parts = ["Channel \(channel.guideNumber)", channel.name]
        if channel.isFavorite {
            parts.append("Favorite")
        }
        if channel.hasNoSignal {
            parts.append("No signal last time")
        }
        if let title = nowNext?.now?.title, !title.isEmpty {
            parts.append("Now \(title)")
        }
        if nowNext?.now?.recording != nil {
            parts.append("Set to record")
        }
        if let now = nowNext?.now, now.isLocked {
            parts.append(ParentalLockMark.accessibilityText(rating: now.rating ?? ""))
        }
        if let title = nowNext?.next?.title, !title.isEmpty {
            parts.append("Next \(title)")
        }
        if isPlaying {
            parts.append("Playing")
        }
        return parts.joined(separator: ", ")
    }
}
