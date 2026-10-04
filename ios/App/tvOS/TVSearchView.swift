import SwiftUI
import BowtieKit

/// tvOS guide search: type with the on-screen keyboard; select a result for
/// Watch (when on now), Record or Record Series.
struct TVSearchView: View {
    let client: BowtieClient
    let onWatch: (Channel) -> Void
    let openRecordings: () -> Void
    /// The channel rail reloads to show new REC marks.
    let onScheduled: () -> Void

    @State private var model: GuideSearchModel?
    @State private var flow: RecordFlow?
    @State private var text = ""
    @State private var now = Date()
    @State private var actionsFor: GuideSearchResult?

    var body: some View {
        content
            .bowtieScreenBackground()
            .searchable(text: $text, prompt: "Search the guide")
            .task { setUp() }
            .task(id: text) {
                do {
                    try await Task.sleep(for: .milliseconds(400))
                } catch {
                    return
                }
                now = Date()
                await model?.search(text)
            }
            .confirmationDialog(
                actionsFor?.title ?? "",
                isPresented: Binding(
                    get: { actionsFor != nil },
                    set: { if !$0 { actionsFor = nil } }
                ),
                titleVisibility: .visible,
                presenting: actionsFor
            ) { result in
                actions(result)
                Button("Cancel", role: .cancel) {}
            } message: { result in
                Text(SearchResultRowView(result: result, now: now).whenLine)
            }
            .recordFlowAlerts(flow)
    }

    @ViewBuilder
    private var content: some View {
        switch model?.state {
        case .results(let results)?:
            List {
                ForEach(results) { result in
                    row(result)
                }
            }
            .listStyle(.plain)
        case .searching?:
            ProgressView()
                .tint(Theme.amber)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        case .empty?:
            message("Nothing in the guide matches \u{201C}\(model?.query ?? "")\u{201D}.", color: Theme.dim)
        case .failed(let text)?:
            message(text, color: Theme.alert)
        case .idle?, nil:
            message("Search upcoming programs by title, episode or description.", color: Theme.dim)
        }
    }

    private func message(_ text: String, color: Color) -> some View {
        Text(text)
            .font(Theme.body(24))
            .foregroundStyle(color)
            .multilineTextAlignment(.center)
            .padding(.horizontal, 80)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func row(_ result: GuideSearchResult) -> some View {
        let rowView = SearchResultRowView(result: result, now: now, large: true)
        return Button {
            actionsFor = result
        } label: {
            rowView
        }
        .listRowBackground(Theme.bg)
        .accessibilityElement(children: .combine)
        .accessibilityLabel(rowView.accessibilityText)
        .contextMenu {
            actions(result)
        }
    }

    private func actions(_ result: GuideSearchResult) -> some View {
        SearchResultActions(
            result: result,
            now: now,
            flow: flow,
            onWatch: onWatch,
            openRecordings: openRecordings
        )
    }

    private func setUp() {
        guard model == nil else { return }
        let search = GuideSearchModel(client: client)
        model = search
        flow = RecordFlow(client: client) { [onScheduled] in
            onScheduled()
            Task { await search.refresh() }
        }
    }
}
