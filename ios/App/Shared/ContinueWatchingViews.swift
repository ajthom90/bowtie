import SwiftUI
import BowtieKit

// MARK: - Shelf

/// "Continue watching": a horizontal row of part-watched recordings. Select
/// resumes; the context menu (press and hold on tvOS) removes one. Callers
/// show it only when `items` isn't empty.
struct ContinueWatchingShelf: View {
    let items: [Recording]
    /// Draw the "Continue watching" title (off when a list section header has it).
    var showsTitle = true
    let onPlay: (Recording) -> Void
    let onRemove: (Recording) -> Void

    static let title = "Continue watching"
    static let removeTitle = "Remove from Continue watching"

    #if os(tvOS)
    private let scale: CGFloat = 1.5
    private let spacing: CGFloat = 40
    private let inset: CGFloat = 80
    #else
    private let scale: CGFloat = 1
    private let spacing: CGFloat = 12
    private let inset: CGFloat = 16
    #endif

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            if showsTitle {
                Text(Self.title)
                    #if os(tvOS)
                    .font(Theme.label(24))
                    #else
                    .font(Theme.label(13))
                    #endif
                    .foregroundStyle(Theme.dim)
                    .textCase(.uppercase)
                    .padding(.horizontal, inset)
                    .accessibilityAddTraits(.isHeader)
            }

            ScrollView(.horizontal, showsIndicators: false) {
                LazyHStack(spacing: spacing) {
                    ForEach(items) { recording in
                        card(recording)
                    }
                }
                .padding(.horizontal, inset)
                #if os(tvOS)
                // Room for the card focus lift so it isn't clipped.
                .padding(.vertical, 24)
                #else
                .padding(.vertical, 8)
                #endif
            }
            #if os(tvOS)
            .scrollClipDisabled()
            #endif
        }
    }

    private func card(_ recording: Recording) -> some View {
        let cardView = ContinueWatchingCard(recording: recording, scale: scale)
        return Button {
            onPlay(recording)
        } label: {
            cardView
        }
        #if os(tvOS)
        .buttonStyle(.card)
        #else
        .buttonStyle(.plain)
        #endif
        .contextMenu {
            Button {
                onPlay(recording)
            } label: {
                Label("Resume", systemImage: "play.fill")
            }
            Button(role: .destructive) {
                onRemove(recording)
            } label: {
                Label(Self.removeTitle, systemImage: "minus.circle")
            }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(cardView.accessibilityText)
        .accessibilityHint("Resume this recording")
        .accessibilityAddTraits(.isButton)
        .accessibilityAction(named: Self.removeTitle) {
            onRemove(recording)
        }
    }
}

// MARK: - Card

/// Title, episode (or channel), how far in, and how long is left.
struct ContinueWatchingCard: View {
    let recording: Recording
    var scale: CGFloat = 1

    var body: some View {
        VStack(alignment: .leading, spacing: 6 * scale) {
            Text(title)
                .font(Theme.label(16 * scale))
                .foregroundStyle(Theme.text)
                .lineLimit(1)
            Text(secondLine)
                .font(Theme.body(13 * scale))
                .foregroundStyle(Theme.dim)
                .lineLimit(1)
            WatchedBar(fraction: ContinueWatching.progress(recording))
            Text(ContinueWatching.remainingText(recording))
                .font(Theme.label(12 * scale))
                .foregroundStyle(Theme.amber)
                .lineLimit(1)
        }
        .padding(.horizontal, 14 * scale)
        .padding(.vertical, 12 * scale)
        .frame(width: 220 * scale, alignment: .leading)
        .background(Theme.raised, in: RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
        .contentShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
    }

    private var title: String {
        recording.title.isEmpty ? "Untitled" : recording.title
    }

    private var secondLine: String {
        recording.subtitle.isEmpty ? recording.channelName : recording.subtitle
    }

    var accessibilityText: String {
        [title, secondLine, ContinueWatching.remainingText(recording)]
            .filter { !$0.isEmpty }
            .joined(separator: ", ")
    }
}

// MARK: - Resume

extension RecordingsModel {
    /// Continue watching: asks `/play` first; live TV stops only once that
    /// succeeds (`play(beforeStart:)`), and playback picks up at the saved
    /// position without asking. Nil (with `actionError`) when `/play` fails.
    func resume(_ recording: Recording, stopping playerModel: PlayerModel) async -> Playback? {
        guard let playback = await play(recording, beforeStart: {
            if playerModel.currentChannel != nil {
                await playerModel.stop()
            }
        }) else { return nil }
        return playback.starting(at: playback.resumeAt ?? 0)
    }
}
