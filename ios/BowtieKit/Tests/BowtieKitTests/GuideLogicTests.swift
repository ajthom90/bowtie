import XCTest
@testable import BowtieKit

final class GuideLogicTests: XCTestCase {
    // Fixed UTC timeline: prog A 20:00–21:00, B 21:00–22:00, C 22:30–23:00 (gap after B)
    private let a = TestFixtures.program(
        start: "2024-06-15T20:00:00Z",
        stop: "2024-06-15T21:00:00Z",
        title: "A"
    )
    private let b = TestFixtures.program(
        start: "2024-06-15T21:00:00Z",
        stop: "2024-06-15T22:00:00Z",
        title: "B"
    )
    private let c = TestFixtures.program(
        start: "2024-06-15T22:30:00Z",
        stop: "2024-06-15T23:00:00Z",
        title: "C"
    )

    private var programs: [GuideProgram] { [a, b, c] }

    // MARK: - nowNext

    func testNowNextMidProgram() {
        let at = TestFixtures.iso("2024-06-15T20:30:00Z")
        let nn = GuideLogic.nowNext(programs: programs, at: at)
        XCTAssertEqual(nn.now?.title, "A")
        XCTAssertEqual(nn.next?.title, "B")
    }

    func testNowNextExactStartIsInclusive() {
        let at = TestFixtures.iso("2024-06-15T21:00:00Z")
        let nn = GuideLogic.nowNext(programs: programs, at: at)
        XCTAssertEqual(nn.now?.title, "B")
        XCTAssertEqual(nn.next?.title, "C")
    }

    func testNowNextExactStopIsExclusive() {
        // At A's stop, A is no longer "now"; B starts at the same instant.
        let at = TestFixtures.iso("2024-06-15T21:00:00Z")
        let nn = GuideLogic.nowNext(programs: [a, b], at: at)
        XCTAssertEqual(nn.now?.title, "B")
        XCTAssertNil(nn.next)
        // Boundary-only on A alone: stop exclusive → no current, no next after.
        let onlyA = GuideLogic.nowNext(programs: [a], at: at)
        XCTAssertNil(onlyA.now)
        XCTAssertNil(onlyA.next)
    }

    func testNowNextGapNoCurrentNextOnly() {
        // In the 22:00–22:30 gap: no now, next is C.
        let at = TestFixtures.iso("2024-06-15T22:15:00Z")
        let nn = GuideLogic.nowNext(programs: programs, at: at)
        XCTAssertNil(nn.now)
        XCTAssertEqual(nn.next?.title, "C")
    }

    func testNowNextEmpty() {
        let at = TestFixtures.iso("2024-06-15T20:30:00Z")
        let nn = GuideLogic.nowNext(programs: [], at: at)
        XCTAssertNil(nn.now)
        XCTAssertNil(nn.next)
    }

    func testNowNextBeforeAllPrograms() {
        let at = TestFixtures.iso("2024-06-15T19:00:00Z")
        let nn = GuideLogic.nowNext(programs: programs, at: at)
        XCTAssertNil(nn.now)
        XCTAssertEqual(nn.next?.title, "A")
    }

    func testNowNextAfterAllPrograms() {
        let at = TestFixtures.iso("2024-06-15T23:30:00Z")
        let nn = GuideLogic.nowNext(programs: programs, at: at)
        XCTAssertNil(nn.now)
        XCTAssertNil(nn.next)
    }

    // MARK: - allowedProfiles

    func testAllowedProfilesEmptyIsAll() {
        XCTAssertEqual(
            GuideLogic.allowedProfiles(maxQuality: ""),
            ["original", "high", "medium", "low"]
        )
    }

    func testAllowedProfilesMediumIsSuffix() {
        XCTAssertEqual(
            GuideLogic.allowedProfiles(maxQuality: "medium"),
            ["medium", "low"]
        )
    }

    func testAllowedProfilesOriginalIsAll() {
        XCTAssertEqual(
            GuideLogic.allowedProfiles(maxQuality: "original"),
            ["original", "high", "medium", "low"]
        )
    }

    func testAllowedProfilesHigh() {
        XCTAssertEqual(
            GuideLogic.allowedProfiles(maxQuality: "high"),
            ["high", "medium", "low"]
        )
    }

    func testAllowedProfilesLow() {
        XCTAssertEqual(
            GuideLogic.allowedProfiles(maxQuality: "low"),
            ["low"]
        )
    }

    func testAllowedProfilesUnknownIsAllDefensive() {
        XCTAssertEqual(
            GuideLogic.allowedProfiles(maxQuality: "ultra"),
            ["original", "high", "medium", "low"]
        )
    }
}
