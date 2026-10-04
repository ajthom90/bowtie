import SwiftUI
import BowtieKit

/// Guide search results in place of the channel list while the search field
/// has text. Tap a result for Watch / Record / Record Series.
struct GuideSearchResultsView: View {
    let model: GuideSearchModel
    let now: Date
    let flow: RecordFlow?
    let onWatch: (Channel) -> Void
    let openRecordings: () -> Void

    @State private var actionsFor: GuideSearchResult?

    var body: some View {
        content
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
    }

    @ViewBuilder
    private var content: some View {
        switch model.state {
        case .idle:
            message("Search upcoming programs by title, episode or description.", color: Theme.dim)
        case .searching:
            ProgressView()
                .tint(Theme.amber)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                .accessibilityLabel("Searching")
        case .empty:
            message("Nothing in the guide matches \u{201C}\(model.query)\u{201D}.", color: Theme.dim)
        case .failed(let text):
            message(text, color: Theme.alert)
        case .results(let results):
            list(results)
        }
    }

    private func message(_ text: String, color: Color) -> some View {
        Text(text)
            .font(Theme.body())
            .foregroundStyle(color)
            .multilineTextAlignment(.center)
            .padding(.horizontal, 32)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func list(_ results: [GuideSearchResult]) -> some View {
        List {
            ForEach(results) { result in
                row(result)
            }
        }
        .listStyle(.plain)
        .scrollContentBackground(.hidden)
        .refreshable { await model.refresh() }
    }

    private func row(_ result: GuideSearchResult) -> some View {
        let rowView = SearchResultRowView(result: result, now: now)
        return Button {
            actionsFor = result
        } label: {
            rowView
        }
        .buttonStyle(.plain)
        .listRowBackground(Theme.bg)
        .listRowSeparatorTint(Theme.line)
        .accessibilityElement(children: .combine)
        .accessibilityLabel(rowView.accessibilityText)
        .accessibilityHint("Watch or record")
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
}
