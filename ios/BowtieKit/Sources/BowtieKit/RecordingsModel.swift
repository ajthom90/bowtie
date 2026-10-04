import Foundation
import Observation

/// Recordings screen state: one tab's list, manage actions and playback start.
@Observable
@MainActor
public final class RecordingsModel {
    public enum LoadState: Equatable {
        case loading
        case loaded([Recording])
        case empty
        case failed(String)
    }

    /// What the player needs to start a recording.
    public struct Playback: Identifiable, Hashable, Sendable {
        public let recording: Recording
        /// Absolute, token-signed HLS VOD URL (no bearer needed).
        public let url: URL
        public let durationSec: Int
        /// Saved position worth resuming from, or nil to start at the beginning.
        public let resumeAt: Int?
        /// Where the player should begin.
        public let startSec: Int

        public var id: Int64 { recording.id }

        public init(recording: Recording, url: URL, durationSec: Int, resumeAt: Int?, startSec: Int = 0) {
            self.recording = recording
            self.url = url
            self.durationSec = durationSec
            self.resumeAt = resumeAt
            self.startSec = startSec
        }

        public func starting(at seconds: Int) -> Playback {
            Playback(recording: recording, url: url, durationSec: durationSec, resumeAt: resumeAt, startSec: max(0, seconds))
        }
    }

    public private(set) var tab: RecordingsTab
    public private(set) var state: LoadState = .loading
    /// Last failed action (delete, stop, keep, play), for an alert.
    public var actionError: String?

    private let client: BowtieClient
    /// Bumped per load so a slow response for an old tab can't land.
    private var generation: UInt64 = 0

    public init(client: BowtieClient, tab: RecordingsTab = .recorded) {
        self.client = client
        self.tab = tab
    }

    // MARK: - Loading

    /// Switches tab (clearing the old tab's rows) and loads it.
    public func select(_ tab: RecordingsTab) async {
        if tab != self.tab {
            self.tab = tab
            state = .loading
        }
        await load()
    }

    public func load() async {
        generation &+= 1
        let gen = generation
        let tab = self.tab
        // A refresh of the same tab keeps its rows on screen.
        if case .loaded = state {} else {
            state = .loading
        }
        do {
            let rows = try await client.recordings(filter: tab.filter)
            guard gen == generation else { return }
            state = rows.isEmpty ? .empty : .loaded(rows)
        } catch {
            guard gen == generation else { return }
            state = .failed(RecordingErrorCopy.message(for: error))
        }
    }

    // MARK: - Manage

    public func delete(_ recording: Recording) async {
        do {
            try await client.deleteRecording(id: recording.id)
            remove(id: recording.id)
        } catch {
            actionError = RecordingErrorCopy.message(for: error)
        }
    }

    public func stop(_ recording: Recording) async {
        do {
            try await client.stopRecording(id: recording.id)
            await load()
        } catch {
            actionError = RecordingErrorCopy.message(for: error)
        }
    }

    public func setProtected(_ recording: Recording, _ protected: Bool) async {
        do {
            let updated = try await client.setRecordingProtected(id: recording.id, protected: protected)
            replace(updated)
        } catch {
            actionError = RecordingErrorCopy.message(for: error)
        }
    }

    // MARK: - Playback

    /// Asks the server for a playback URL; the resume decision uses its fresh position.
    public func play(_ recording: Recording) async -> Playback? {
        do {
            let play = try await client.playRecording(id: recording.id)
            let url = ServerURL.resolve(path: play.playlistUrl, against: client.serverURL)
            let duration = play.durationSec > 0 ? play.durationSec : recording.durationSec
            return Playback(
                recording: recording,
                url: url,
                durationSec: duration,
                resumeAt: RecordingLogic.resumePosition(positionSec: play.positionSec, durationSec: duration)
            )
        } catch {
            actionError = RecordingErrorCopy.message(for: error)
            return nil
        }
    }

    /// Best-effort save of the player's position (every 15 s and on dismiss).
    public func savePosition(recordingId: Int64, seconds: Double) async {
        guard let sec = RecordingLogic.positionToSave(seconds: seconds) else { return }
        try? await client.saveRecordingPosition(id: recordingId, positionSec: sec)
    }

    // MARK: - Rows

    private func remove(id: Int64) {
        guard case .loaded(let rows) = state else { return }
        let kept = rows.filter { $0.id != id }
        state = kept.isEmpty ? .empty : .loaded(kept)
    }

    private func replace(_ recording: Recording) {
        guard case .loaded(let rows) = state else { return }
        state = .loaded(rows.map { $0.id == recording.id ? recording : $0 })
    }
}

// MARK: - Scheduling

public enum ScheduleOutcome: Equatable, Sendable {
    /// `warnings` are messages to show (e.g. this uses the last free tuner).
    case scheduled(Recording, warnings: [String])
    /// Not enough tuners; offer "Record anyway" (schedule again with `force`).
    case conflict(tunerCount: Int, conflicts: [Recording])
    case failed(String)
}

/// Schedules a guide program for recording (channel list context menu).
public enum RecordingScheduler {
    public static func schedule(
        client: BowtieClient,
        channelId: Int64,
        programStart: Date,
        force: Bool = false
    ) async -> ScheduleOutcome {
        do {
            let result = try await client.scheduleRecording(
                channelId: channelId,
                programStart: programStart,
                force: force
            )
            return .scheduled(result.recording, warnings: result.warnings.map(\.message))
        } catch BowtieError.recordingConflict(let tunerCount, let conflicts, _) {
            return .conflict(tunerCount: tunerCount, conflicts: conflicts)
        } catch BowtieError.notFound {
            return .failed("That program is no longer in the guide.")
        } catch {
            return .failed(RecordingErrorCopy.message(for: error))
        }
    }
}

// MARK: - Error copy

enum RecordingErrorCopy {
    static func message(for error: Error) -> String {
        guard let error = error as? BowtieError else {
            return error.localizedDescription
        }
        switch error {
        case .unauthorized:
            return "Signed out"
        case .tunersBusy:
            return "All tuners are in use"
        case .negotiationFailed(let message):
            return sentence(message)
        case .recordingConflict(_, _, let message):
            return sentence(message)
        case .notFound:
            return "That recording is gone."
        case .server(_, let message):
            return sentence(message)
        case .network(let message):
            return message
        case .invalidServerURL:
            return "Invalid server URL"
        }
    }

    /// Server errors are lowercase phrases; show them as sentences.
    private static func sentence(_ message: String) -> String {
        message.prefix(1).uppercased() + message.dropFirst()
    }
}
