import SwiftUI
import AppKit
import BowtieKit

@main
struct BowtieMacApp: App {
    @NSApplicationDelegateAdaptor(MacAppDelegate.self) private var appDelegate
    @State private var appModel = AppModel(store: KeychainSessionStore())

    var body: some Scene {
        // One window: each window would otherwise hold its own live session.
        Window("Bowtie", id: "main") {
            MacRootView(appModel: appModel)
                .frame(minWidth: 760, minHeight: 460)
                .preferredColorScheme(.dark)
                .tint(Theme.amber)
        }
        .defaultSize(width: 1200, height: 720)
        .commands {
            PlaybackCommands()
        }

        Settings {
            MacSettingsView(appModel: appModel)
                .preferredColorScheme(.dark)
                .tint(Theme.amber)
        }
    }
}

// MARK: - App delegate (quit releases the tuner)

@MainActor
final class MacAppDelegate: NSObject, NSApplicationDelegate {
    /// Set by the main view: stops the live session and saves the recording
    /// position before the app quits.
    static var onTerminate: (@MainActor () async -> Void)?

    /// Upper bound on how long quitting waits for the server.
    private static let terminateTimeout: Duration = .seconds(2)

    func applicationShouldTerminateAfterLastWindowClosed(_ sender: NSApplication) -> Bool {
        true
    }

    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        guard let onTerminate = Self.onTerminate else { return .terminateNow }
        Self.onTerminate = nil
        let reply = TerminateReply(sender)
        Task { @MainActor in
            await onTerminate()
            reply.send()
        }
        Task { @MainActor in
            try? await Task.sleep(for: Self.terminateTimeout)
            reply.send()
        }
        return .terminateLater
    }
}

/// Answers `applicationShouldTerminate` once, whichever finishes first.
@MainActor
private final class TerminateReply {
    private let app: NSApplication
    private var sent = false

    init(_ app: NSApplication) {
        self.app = app
    }

    func send() {
        guard !sent else { return }
        sent = true
        app.reply(toApplicationShouldTerminate: true)
    }
}

// MARK: - Root

/// Routes on `AppModel.phase`, like the iOS and tvOS roots. Owns `PlayerModel`
/// so leaving `.ready` (sign-out / change-server) stops the live session.
struct MacRootView: View {
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
                MacCheckingView()
            case .ready:
                if let playerModel {
                    MacMainView(appModel: appModel, playerModel: playerModel)
                } else {
                    MacCheckingView()
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
            // Real leave: stop playback when the auth shell replaces the main view.
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

private struct MacCheckingView: View {
    var body: some View {
        VStack(spacing: 16) {
            Text("Bowtie")
                .font(Theme.channelNumber(40))
                .foregroundStyle(Theme.amber)
            ProgressView()
                .tint(Theme.amber)
            Text("Signing in…")
                .font(Theme.body(15))
                .foregroundStyle(Theme.dim)
        }
        .bowtieScreenBackground()
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Signing in")
    }
}

// MARK: - Settings scene

/// The app's Settings window (⌘,): the shared server + account screen once
/// signed in; before that, a pointer back to the main window.
struct MacSettingsView: View {
    @Bindable var appModel: AppModel

    var body: some View {
        Group {
            if appModel.phase == .ready {
                SettingsView(appModel: appModel)
            } else {
                VStack(spacing: 10) {
                    Text("Not signed in")
                        .font(Theme.title(18))
                        .foregroundStyle(Theme.text)
                    Text(appModel.serverURL?.absoluteString ?? "Connect to a server in the Bowtie window.")
                        .font(Theme.body(14))
                        .foregroundStyle(Theme.dim)
                        .multilineTextAlignment(.center)
                }
                .padding(32)
                .bowtieScreenBackground()
            }
        }
        .frame(width: 520, height: 560)
    }
}

#Preview("Connect") {
    MacRootView(appModel: AppModel(store: InMemorySessionStore()))
        .frame(width: 900, height: 600)
}
