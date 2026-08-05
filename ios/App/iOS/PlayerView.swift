import SwiftUI
import BowtieKit

/// Player shell for Task 6 — real AVKit work is Task 7.
///
/// Black screen + channel identity + Back, which calls `playerModel.stop()` so
/// session teardown runs on real leave. Navigation lands here so ChannelList
/// compiles end-to-end.
struct PlayerView: View {
    let channel: Channel
    var playerModel: PlayerModel

    @Environment(\.dismiss) private var dismiss
    @State private var isStopping = false

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()

            VStack(spacing: 20) {
                Text(channel.guideNumber)
                    .font(Theme.channelNumber(56))
                    .foregroundStyle(Theme.amber)
                    .accessibilityLabel("Channel \(channel.guideNumber)")

                Text(channel.name)
                    .font(Theme.title(22))
                    .foregroundStyle(Theme.text)
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, 24)
                    .accessibilityLabel(channel.name)

                stateCaption
                    .font(Theme.mono(13))
                    .foregroundStyle(Theme.dim)
                    .multilineTextAlignment(.center)
                    .padding(.horizontal, 32)

                Button {
                    Task { await leave() }
                } label: {
                    Text("Back")
                        .font(Theme.label(17))
                        .frame(minWidth: 120)
                        .padding(.vertical, 12)
                        .padding(.horizontal, 20)
                        .background(Theme.raised)
                        .foregroundStyle(Theme.text)
                        .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
                }
                .buttonStyle(.plain)
                .disabled(isStopping)
                .accessibilityLabel("Back")
                .accessibilityHint("Stop playback and return to the channel list")
                .padding(.top, 12)
            }
        }
        .navigationBarBackButtonHidden(true)
        .toolbar {
            ToolbarItem(placement: .topBarLeading) {
                Button("Back") {
                    Task { await leave() }
                }
                .foregroundStyle(Theme.amber)
                .disabled(isStopping)
                .accessibilityLabel("Back")
            }
        }
        .toolbarBackground(Color.black, for: .navigationBar)
        .toolbarColorScheme(.dark, for: .navigationBar)
        .statusBarHidden(true)
    }

    @ViewBuilder
    private var stateCaption: some View {
        switch playerModel.state {
        case .idle:
            Text("Idle")
        case .starting:
            Text("Starting…")
        case .playing:
            Text("Playing (stub)")
        case .stalled:
            Text("Stalled…")
        case .failed(let message):
            Text(message)
                .foregroundStyle(Theme.alert)
        case .tunersBusy:
            Text("All tuners are in use")
                .foregroundStyle(Theme.alert)
        }
    }

    @MainActor
    private func leave() async {
        guard !isStopping else { return }
        isStopping = true
        await playerModel.stop()
        dismiss()
    }
}

#Preview {
    NavigationStack {
        PlayerView(
            channel: Channel(id: 1, guideNumber: "7.1", name: "Local News", logoUrl: ""),
            playerModel: PlayerModel(
                client: BowtieClient(
                    server: URL(string: "http://127.0.0.1:8400")!,
                    store: InMemorySessionStore()
                ),
                caps: Caps.make(maxHeight: 1080)
            )
        )
    }
}
