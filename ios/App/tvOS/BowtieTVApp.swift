import SwiftUI
import BowtieKit

@main
struct BowtieTVApp: App {
    @State private var appModel = AppModel(store: KeychainSessionStore())

    var body: some Scene {
        WindowGroup {
            TVRootView(appModel: appModel)
                .preferredColorScheme(.dark)
        }
    }
}

// MARK: - Root

/// Routes on `AppModel.phase`. Owns `PlayerModel` so leaving `.ready`
/// (sign-out / change-server) can stop a live session even as the rail tears down.
struct TVRootView: View {
    @Bindable var appModel: AppModel
    @State private var playerModel: PlayerModel?

    var body: some View {
        Group {
            switch appModel.phase {
            case .connect:
                ConnectView(appModel: appModel)
            case .login:
                LoginView(appModel: appModel)
            case .checking:
                TVCheckingView()
            case .ready:
                if let playerModel {
                    ChannelRailView(appModel: appModel, playerModel: playerModel)
                } else {
                    TVCheckingView()
                }
            }
        }
        .animation(.easeInOut(duration: 0.18), value: appModel.phase)
        .task(id: appModel.phase) {
            switch appModel.phase {
            case .checking:
                await appModel.start()
            case .ready:
                ensurePlayerModel()
            case .connect, .login:
                break
            }
        }
        .onChange(of: appModel.phase) { previous, phase in
            // Real leave: stop playback when auth shell replaces the guide.
            if previous == .ready && phase != .ready {
                let model = playerModel
                playerModel = nil
                if let model {
                    Task { await model.stop() }
                }
            }
        }
    }

    private func ensurePlayerModel() {
        guard playerModel == nil, let client = appModel.client else { return }
        playerModel = PlayerModel(client: client, caps: Caps.current())
    }
}

// MARK: - Checking

private struct TVCheckingView: View {
    var body: some View {
        VStack(spacing: 20) {
            Text("Bowtie")
                .font(Theme.channelNumber(64))
                .foregroundStyle(Theme.amber)
            ProgressView()
                .tint(Theme.amber)
            Text("Signing in…")
                .font(Theme.body(22))
                .foregroundStyle(Theme.dim)
        }
        .bowtieScreenBackground()
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Signing in")
    }
}

#Preview("Connect") {
    TVRootView(appModel: AppModel(store: InMemorySessionStore()))
}
