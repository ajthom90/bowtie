import SwiftUI
import AppKit

/// What the menu commands can do in the focused main window. Published with
/// `focusedSceneValue` by `MacMainView` only once signed in, so on Connect /
/// Login and in the Settings window the items are disabled.
struct PlaybackActions {
    var togglePlay: (() -> Void)?
    var goLive: (() -> Void)?
    /// Recording player inside a commercial break.
    var skipAd: (() -> Void)?
    var channelUp: () -> Void
    var channelDown: () -> Void
    var toggleFullScreen: () -> Void
}

private struct PlaybackActionsKey: FocusedValueKey {
    typealias Value = PlaybackActions
}

extension FocusedValues {
    var playbackActions: PlaybackActions? {
        get { self[PlaybackActionsKey.self] }
        set { self[PlaybackActionsKey.self] = newValue }
    }
}

/// Playback menu: Space play/pause, L live, S skip ad, ⌘↑ / ⌘↓ channels, ⌘F full screen.
///
/// Space, L and S have no modifier, and AppKit offers key equivalents to the
/// menu before a text field sees the key. So each shortcut is attached only
/// while its action exists; with no action the item has no key equivalent and
/// typing a space or "l" into a field is untouched.
struct PlaybackCommands: Commands {
    @FocusedValue(\.playbackActions) private var actions

    var body: some Commands {
        // The Edit menu's Find group would claim ⌘F before the View menu.
        CommandGroup(replacing: .textEditing) {}

        CommandMenu("Playback") {
            Button("Play/Pause") { actions?.togglePlay?() }
                .keyboardShortcut(actions?.togglePlay == nil ? nil : KeyboardShortcut(.space, modifiers: []))
                .disabled(actions?.togglePlay == nil)

            Button("Go to Live") { actions?.goLive?() }
                .keyboardShortcut(actions?.goLive == nil ? nil : KeyboardShortcut("l", modifiers: []))
                .disabled(actions?.goLive == nil)

            Button("Skip Ad") { actions?.skipAd?() }
                .keyboardShortcut(actions?.skipAd == nil ? nil : KeyboardShortcut("s", modifiers: []))
                .disabled(actions?.skipAd == nil)

            Divider()

            Button("Channel Up") { actions?.channelUp() }
                .keyboardShortcut(actions == nil ? nil : KeyboardShortcut(.upArrow, modifiers: .command))
                .disabled(actions == nil)

            Button("Channel Down") { actions?.channelDown() }
                .keyboardShortcut(actions == nil ? nil : KeyboardShortcut(.downArrow, modifiers: .command))
                .disabled(actions == nil)
        }

        CommandGroup(after: .sidebar) {
            Button("Toggle Full Screen") { actions?.toggleFullScreen() }
                .keyboardShortcut(actions == nil ? nil : KeyboardShortcut("f", modifiers: .command))
                .disabled(actions == nil)
        }
    }
}
