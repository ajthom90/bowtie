import SwiftUI
import BowtieKit

struct EditRecordingRuleView: View {
    let rule: RecordingRule
    @Bindable var model: RecordingsModel
    @Environment(\.dismiss) private var dismiss
    @State private var newOnly: Bool
    @State private var channelId: Int64
    @State private var keepLatest: Int
    @State private var channels: [Int64: String] = [:]
    @State private var busy = false

    init(rule: RecordingRule, model: RecordingsModel) {
        self.rule = rule
        self.model = model
        _newOnly = State(initialValue: rule.newOnly)
        _channelId = State(initialValue: rule.channelId)
        _keepLatest = State(initialValue: rule.keepLatest)
        _channels = State(initialValue: rule.channelId == 0 ? [:] : [rule.channelId: rule.channelName])
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                Text("Edit \(rule.title)").font(.headline)
                Picker("Episodes", selection: $newOnly) {
                    Text("New episodes only").tag(true)
                    Text("All episodes").tag(false)
                }
                Picker("Channel", selection: $channelId) {
                    Text("Any channel").tag(Int64(0))
                    ForEach(channels.keys.sorted(), id: \.self) { id in
                        Text(channels[id] ?? "Channel \(id)").tag(id)
                    }
                }
                Text(keepLatest == 0 ? "Keep all" : "Keep latest \(keepLatest)")
                HStack {
                    Button("Keep all") { keepLatest = 0 }
                    Button("−") { keepLatest = max(1, keepLatest - 1) }
                        .accessibilityLabel("Keep fewer episodes")
                    Button("+") { if keepLatest < Int.max { keepLatest += 1 } }
                        .accessibilityLabel("Keep more episodes")
                }
                Text("The keep limit applies when new episodes finish recording. Saving deletes no recorded episodes.")
                    .font(.caption)
                if let error = model.actionError { Text(error).foregroundStyle(.red) }
                HStack {
                    Button("Cancel") { dismiss() }
                    Button(busy ? "Saving…" : "Save") {
                        busy = true
                        model.actionError = nil
                        Task {
                            if await model.updateRule(rule, newOnly: newOnly, channelId: channelId, keepLatest: keepLatest) { dismiss() }
                            busy = false
                        }
                    }
                }
            }
            .padding(30)
            .disabled(busy)
        }
        .interactiveDismissDisabled(busy)
        .task { channels = await model.ruleChannels(rule) }
    }
}
