import XCTest
@testable import BowtieKit

final class RecordingLogicTests: XCTestCase {
    // MARK: - Decoding

    func testRecordingDecodesOpenAPIShape() throws {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        let rec = try decoder.decode(
            Recording.self,
            from: RecordingFixtures.json(state: "failed", failure: "noTuner", positionSec: 412).data(using: .utf8)!
        )
        XCTAssertEqual(rec.id, 7)
        XCTAssertEqual(rec.title, "Jeopardy!")
        XCTAssertEqual(rec.channelId, 3)
        XCTAssertEqual(rec.channelName, "5.1 KSTP")
        XCTAssertEqual(rec.start, TestFixtures.iso("2026-10-05T00:00:00Z"))
        XCTAssertEqual(rec.status, .failed)
        XCTAssertEqual(rec.failure, "noTuner")
        XCTAssertEqual(rec.positionSec, 412)
        XCTAssertEqual(rec.sizeBytes, 3_800_000_000)
        XCTAssertEqual(rec.scheduledBy, "andrew")
        XCTAssertTrue(rec.canManage)
        XCTAssertEqual(rec, RecordingFixtures.recording(state: "failed", failure: "noTuner", positionSec: 412))
    }

    func testUnknownStateDecodesAsUnknownStatus() {
        XCTAssertEqual(RecordingFixtures.recording(state: "cancelled").status, .unknown)
    }

    func testGuideProgramDecodesRecordingMark() throws {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        let json = """
        {"start":"2026-10-05T00:00:00Z","stop":"2026-10-05T00:30:00Z","title":"Jeopardy!",
         "subtitle":"","description":"","category":"","recording":{"id":7,"state":"scheduled"}}
        """
        let program = try decoder.decode(GuideProgram.self, from: json.data(using: .utf8)!)
        XCTAssertEqual(program.recording, RecordingMark(id: 7, state: "scheduled"))

        let bare = """
        {"start":"2026-10-05T00:00:00Z","stop":"2026-10-05T00:30:00Z","title":"News",
         "subtitle":"","description":"","category":""}
        """
        XCTAssertNil(try decoder.decode(GuideProgram.self, from: bare.data(using: .utf8)!).recording)
    }

    // MARK: - Actions allowed

    func testActionsRequireCanManage() {
        let other = RecordingFixtures.recording(state: "recording", canManage: false)
        XCTAssertFalse(other.canDelete)
        XCTAssertFalse(other.canStop)
        XCTAssertFalse(other.canProtect)

        let mine = RecordingFixtures.recording(state: "recording", canManage: true)
        XCTAssertTrue(mine.canDelete)
        XCTAssertTrue(mine.canStop)
    }

    func testStopOnlyWhileRecording() {
        for state in ["scheduled", "waiting", "converting", "ready", "failed"] {
            XCTAssertFalse(RecordingFixtures.recording(state: state).canStop, state)
        }
    }

    func testProtectOnlyForRecordedItems() {
        XCTAssertTrue(RecordingFixtures.recording(state: "ready").canProtect)
        XCTAssertTrue(RecordingFixtures.recording(state: "converting").canProtect)
        XCTAssertFalse(RecordingFixtures.recording(state: "scheduled").canProtect)
        XCTAssertFalse(RecordingFixtures.recording(state: "failed").canProtect)
    }

    func testDeleteTitleSaysCancelForUpcoming() {
        XCTAssertEqual(RecordingFixtures.recording(state: "scheduled").deleteActionTitle, "Cancel Recording")
        XCTAssertEqual(RecordingFixtures.recording(state: "waiting").deleteActionTitle, "Cancel Recording")
        XCTAssertEqual(RecordingFixtures.recording(state: "ready").deleteActionTitle, "Delete")
        XCTAssertEqual(RecordingFixtures.recording(state: "failed").deleteActionTitle, "Delete")
        XCTAssertEqual(RecordingFixtures.recording(state: "recording").deleteActionTitle, "Delete")
    }

    func testOnlyReadyIsPlayable() {
        XCTAssertTrue(RecordingFixtures.recording(state: "ready").isPlayable)
        XCTAssertFalse(RecordingFixtures.recording(state: "converting").isPlayable)
        XCTAssertFalse(RecordingFixtures.recording(state: "recording").isPlayable)
    }

    // MARK: - Labels

    func testStateLabels() {
        XCTAssertEqual(RecordingLogic.stateLabel(RecordingFixtures.recording(state: "scheduled")), "Scheduled")
        XCTAssertEqual(RecordingLogic.stateLabel(RecordingFixtures.recording(state: "waiting")), "Waiting for a tuner")
        XCTAssertEqual(
            RecordingLogic.stateLabel(RecordingFixtures.recording(state: "waiting", failure: "noSignal")),
            "Waiting for a signal"
        )
        XCTAssertEqual(RecordingLogic.stateLabel(RecordingFixtures.recording(state: "recording")), "Recording")
        XCTAssertEqual(RecordingLogic.stateLabel(RecordingFixtures.recording(state: "converting")), "Finishing up")
        XCTAssertEqual(RecordingLogic.stateLabel(RecordingFixtures.recording(state: "ready")), "Recorded")
        XCTAssertEqual(RecordingLogic.stateLabel(RecordingFixtures.recording(state: "ready", partial: true)), "Partly recorded")
        XCTAssertEqual(RecordingLogic.stateLabel(RecordingFixtures.recording(state: "failed")), "Missed")
    }

    func testFailureLabelsInPlainWords() {
        XCTAssertEqual(RecordingLogic.failureLabel("noTuner"), "No tuner was free")
        XCTAssertEqual(RecordingLogic.failureLabel("noSignal"), "The antenna got no signal")
        XCTAssertEqual(RecordingLogic.failureLabel("diskFull"), "The server ran out of space")
        XCTAssertEqual(RecordingLogic.failureLabel("error"), "Something went wrong on the server")
        XCTAssertNil(RecordingLogic.failureLabel(""))
        XCTAssertEqual(RecordingLogic.failureLabel("somethingNew"), "Something went wrong on the server")
    }

    func testDetailLine() {
        XCTAssertEqual(
            RecordingLogic.detailLine(RecordingFixtures.recording(state: "failed", failure: "noTuner")),
            "Missed: no tuner was free"
        )
        XCTAssertEqual(
            RecordingLogic.detailLine(RecordingFixtures.recording(state: "ready", partial: true)),
            "Part of this program is missing"
        )
        XCTAssertNil(RecordingLogic.detailLine(RecordingFixtures.recording(state: "ready")))
        XCTAssertNil(RecordingLogic.detailLine(RecordingFixtures.recording(state: "scheduled")))
    }

    func testBadgeTone() {
        XCTAssertEqual(RecordingLogic.badgeTone(RecordingFixtures.recording(state: "recording")), .live)
        XCTAssertEqual(RecordingLogic.badgeTone(RecordingFixtures.recording(state: "failed")), .alert)
        XCTAssertEqual(RecordingLogic.badgeTone(RecordingFixtures.recording(state: "waiting")), .warning)
        XCTAssertEqual(RecordingLogic.badgeTone(RecordingFixtures.recording(state: "ready", partial: true)), .warning)
        XCTAssertEqual(RecordingLogic.badgeTone(RecordingFixtures.recording(state: "ready")), .good)
        XCTAssertEqual(RecordingLogic.badgeTone(RecordingFixtures.recording(state: "scheduled")), .neutral)
        XCTAssertEqual(RecordingLogic.badgeTone(RecordingFixtures.recording(state: "converting")), .neutral)
    }

    // MARK: - Resume

    func testResumeOnlyBetweenTenSecondsAndThirtyBeforeEnd() {
        XCTAssertNil(RecordingLogic.resumePosition(positionSec: 0, durationSec: 1800))
        XCTAssertNil(RecordingLogic.resumePosition(positionSec: 10, durationSec: 1800))
        XCTAssertEqual(RecordingLogic.resumePosition(positionSec: 11, durationSec: 1800), 11)
        XCTAssertEqual(RecordingLogic.resumePosition(positionSec: 412, durationSec: 1800), 412)
        XCTAssertEqual(RecordingLogic.resumePosition(positionSec: 1769, durationSec: 1800), 1769)
        XCTAssertNil(RecordingLogic.resumePosition(positionSec: 1770, durationSec: 1800))
        XCTAssertNil(RecordingLogic.resumePosition(positionSec: 1800, durationSec: 1800))
    }

    func testResumeWithUnknownDurationUsesOnlyTheTenSecondFloor() {
        XCTAssertEqual(RecordingLogic.resumePosition(positionSec: 412, durationSec: 0), 412)
        XCTAssertNil(RecordingLogic.resumePosition(positionSec: 5, durationSec: 0))
    }

    func testPositionToSave() {
        XCTAssertEqual(RecordingLogic.positionToSave(seconds: 412.9), 412)
        XCTAssertEqual(RecordingLogic.positionToSave(seconds: -3), 0)
        XCTAssertNil(RecordingLogic.positionToSave(seconds: .nan))
        XCTAssertNil(RecordingLogic.positionToSave(seconds: .infinity))
    }

    func testWatchedFraction() {
        XCTAssertEqual(RecordingLogic.watchedFraction(RecordingFixtures.recording(durationSec: 2000, positionSec: 500)), 0.25)
        XCTAssertEqual(RecordingLogic.watchedFraction(RecordingFixtures.recording(durationSec: 0, positionSec: 500)), 0)
        XCTAssertEqual(RecordingLogic.watchedFraction(RecordingFixtures.recording(durationSec: 100, positionSec: 500)), 1)
    }

    // MARK: - Formatting

    func testFormatDuration() {
        XCTAssertEqual(RecordingLogic.formatDuration(45), "45 sec")
        XCTAssertEqual(RecordingLogic.formatDuration(60), "1 min")
        XCTAssertEqual(RecordingLogic.formatDuration(1980), "33 min")
        XCTAssertEqual(RecordingLogic.formatDuration(3600), "1 hr")
        XCTAssertEqual(RecordingLogic.formatDuration(3900), "1 hr 5 min")
        XCTAssertEqual(RecordingLogic.formatDuration(0), "0 min")
    }

    func testFormatSize() {
        XCTAssertEqual(RecordingLogic.formatSize(3_800_000_000), "3.8 GB")
        XCTAssertEqual(RecordingLogic.formatSize(12_000_000_000), "12.0 GB")
        XCTAssertEqual(RecordingLogic.formatSize(450_000_000), "450 MB")
        XCTAssertEqual(RecordingLogic.formatSize(2_600), "3 KB")
        XCTAssertEqual(RecordingLogic.formatSize(0), "0 KB")
    }

    func testFormatTimestamp() {
        XCTAssertEqual(RecordingLogic.formatTimestamp(412), "6:52")
        XCTAssertEqual(RecordingLogic.formatTimestamp(5), "0:05")
        XCTAssertEqual(RecordingLogic.formatTimestamp(3723), "1:02:03")
    }

    // MARK: - Tabs

    func testTabsMapToServerFilters() {
        XCTAssertEqual(RecordingsTab.allCases, [.upcoming, .recorded, .missed])
        XCTAssertEqual(RecordingsTab.upcoming.filter, .upcoming)
        XCTAssertEqual(RecordingsTab.recorded.filter, .recorded)
        XCTAssertEqual(RecordingsTab.missed.filter, .failed)
        XCTAssertEqual(RecordingsTab.missed.title, "Missed")
        XCTAssertFalse(RecordingsTab.upcoming.emptyMessage.isEmpty)
    }

    // MARK: - Conflict copy

    func testConflictMessageNamesTheOtherRecordings() {
        let msg = RecordingLogic.conflictMessage(
            tunerCount: 2,
            conflicts: [
                RecordingFixtures.recording(id: 1, title: "Jeopardy!"),
                RecordingFixtures.recording(id: 2, title: "Wheel of Fortune"),
            ]
        )
        XCTAssertTrue(msg.contains("2 tuners"), msg)
        XCTAssertTrue(msg.contains("Jeopardy! (5.1 KSTP)"), msg)
        XCTAssertTrue(msg.contains("Wheel of Fortune (5.1 KSTP)"), msg)
    }

    func testConflictMessageSingularTuner() {
        let msg = RecordingLogic.conflictMessage(tunerCount: 1, conflicts: [])
        XCTAssertTrue(msg.contains("1 tuner"), msg)
        XCTAssertFalse(msg.contains("1 tuners"), msg)
    }
}
