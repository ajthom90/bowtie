import XCTest
@testable import BowtieKit

final class CommercialSkipperTests: XCTestCase {
    private func c(_ start: Double, _ end: Double) -> Commercial {
        Commercial(start: start, end: end)
    }

    // MARK: - Decoding

    func testRecordingFromOlderServerHasNoCommercials() async throws {
        let recording = try await decodeRecording(RecordingFixtures.json())
        XCTAssertEqual(recording.commercials, [])
    }

    func testRecordingDecodesNullCommercialsAsNone() async throws {
        let recording = try await decodeRecording(RecordingFixtures.json(extra: #""commercials": null"#))
        XCTAssertEqual(recording.commercials, [])
    }

    func testRecordingDecodesCommercials() async throws {
        let recording = try await decodeRecording(
            RecordingFixtures.json(extra: #""commercials": [{"start": 312.5, "end": 401}, {"start": 900, "end": 1020.25}]"#)
        )
        XCTAssertEqual(recording.commercials, [c(312.5, 401), c(900, 1020.25)])
    }

    // MARK: - Normalizing

    func testSortsSegments() {
        XCTAssertEqual(CommercialSkipper.normalize([c(900, 1000), c(100, 200)]), [c(100, 200), c(900, 1000)])
    }

    func testMergesOverlappingAndTouchingSegments() {
        XCTAssertEqual(
            CommercialSkipper.normalize([c(100, 200), c(150, 250), c(250, 300), c(400, 500), c(420, 450)]),
            [c(100, 300), c(400, 500)]
        )
    }

    func testDropsEmptyBackwardsAndNonFiniteSegments() {
        XCTAssertEqual(
            CommercialSkipper.normalize([
                c(100, 100), c(300, 200), c(.nan, 50), c(10, .infinity), c(-.infinity, 5), c(600, 700),
            ]),
            [c(600, 700)]
        )
    }

    func testClampsNegativeStartToZero() {
        XCTAssertEqual(CommercialSkipper.normalize([c(-5, 30)]), [c(0, 30)])
        XCTAssertEqual(CommercialSkipper.normalize([c(-10, -2)]), [])
    }

    // MARK: - Active segment

    func testNoSegmentsNeverActive() {
        let skipper = CommercialSkipper([])
        XCTAssertNil(skipper.active(at: 0))
        XCTAssertNil(skipper.active(at: 500))
    }

    func testActiveIsStartInclusiveEndExclusive() {
        let skipper = CommercialSkipper([c(100, 200), c(500, 600)])
        XCTAssertNil(skipper.active(at: 99.9))
        XCTAssertEqual(skipper.active(at: 100), c(100, 200))
        XCTAssertEqual(skipper.active(at: 199.9), c(100, 200))
        XCTAssertNil(skipper.active(at: 200))
        XCTAssertNil(skipper.active(at: 350))
        XCTAssertEqual(skipper.active(at: 550), c(500, 600))
        XCTAssertNil(skipper.active(at: 600))
    }

    func testActiveUsesNormalizedSegments() {
        let skipper = CommercialSkipper([c(250, 300), c(100, 260)])
        XCTAssertEqual(skipper.active(at: 120), c(100, 300))
        XCTAssertEqual(skipper.segments, [c(100, 300)])
    }

    func testNonFiniteTimeIsNeverActive() {
        let skipper = CommercialSkipper([c(0, 100)])
        XCTAssertNil(skipper.active(at: .nan))
    }

    // MARK: - Skipping

    func testSkipTargetIsSegmentEnd() {
        var skipper = CommercialSkipper([c(100, 200)])
        XCTAssertEqual(skipper.skip(at: 150), 200)
        XCTAssertNil(skipper.skip(at: 250))
    }

    func testAutoSkipsEachSegmentOnce() {
        var skipper = CommercialSkipper([c(100, 200), c(500, 600)])
        XCTAssertNil(skipper.autoSkipTarget(at: 50))
        XCTAssertEqual(skipper.autoSkipTarget(at: 100.2), 200)
        XCTAssertEqual(skipper.autoSkipTarget(at: 500), 600)
    }

    func testSeekingBackIntoAnAutoSkippedSegmentDoesNotSkipAgain() {
        var skipper = CommercialSkipper([c(100, 200)])
        XCTAssertEqual(skipper.autoSkipTarget(at: 101), 200)
        XCTAssertNil(skipper.autoSkipTarget(at: 150))
        // Still inside it, so the Skip ad button still shows and still works.
        XCTAssertEqual(skipper.active(at: 150), c(100, 200))
        XCTAssertEqual(skipper.skip(at: 150), 200)
    }

    func testManualSkipCountsAsHandled() {
        var skipper = CommercialSkipper([c(100, 200)])
        XCTAssertEqual(skipper.skip(at: 120), 200)
        XCTAssertNil(skipper.autoSkipTarget(at: 120))
    }

    func testMergedSegmentIsSkippedInOneGo() {
        var skipper = CommercialSkipper([c(100, 200), c(200, 260)])
        XCTAssertEqual(skipper.autoSkipTarget(at: 100), 260)
        XCTAssertNil(skipper.autoSkipTarget(at: 210))
    }

    // MARK: - Helpers

    private func decodeRecording(_ json: String) async throws -> Recording {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .iso8601
        return try decoder.decode(Recording.self, from: Data(json.utf8))
    }
}
