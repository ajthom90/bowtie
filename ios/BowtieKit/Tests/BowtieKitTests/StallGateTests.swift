import XCTest
@testable import BowtieKit

final class StallGateTests: XCTestCase {
    func testStartupBufferingIsNotAStall() {
        var gate = StallGate()
        gate.loaded(at: 0)
        gate.waiting(at: 0.1)
        XCTAssertNil(gate.check(at: 5), "AVPlayer starts in waiting-to-minimize-stalls; that is buffering, not a stall")
        XCTAssertEqual(gate.playing(at: 6), .recovered)
        XCTAssertNil(gate.check(at: 30))
    }

    func testStreamThatNeverStartsStallsAfterStartupTimeout() {
        var gate = StallGate(grace: 4, startupTimeout: 20)
        gate.loaded(at: 0)
        gate.waiting(at: 0.1)
        XCTAssertNil(gate.check(at: 19.9))
        XCTAssertEqual(gate.check(at: 20), .stalled)
        XCTAssertNil(gate.check(at: 21), "a stall is reported once")
    }

    func testWaitAfterPlayingStallsOnlyAfterGrace() {
        var gate = StallGate(grace: 4, startupTimeout: 20)
        gate.loaded(at: 0)
        _ = gate.playing(at: 2)
        gate.waiting(at: 10)
        XCTAssertNil(gate.check(at: 13.9))
        XCTAssertEqual(gate.check(at: 14), .stalled)
    }

    func testBriefRebufferIsNotAStall() {
        var gate = StallGate(grace: 4, startupTimeout: 20)
        gate.loaded(at: 0)
        _ = gate.playing(at: 2)
        gate.waiting(at: 10)
        XCTAssertEqual(gate.playing(at: 12), .recovered)
        XCTAssertNil(gate.check(at: 20))
    }

    func testReloadRestartsStartupWindow() {
        var gate = StallGate(grace: 4, startupTimeout: 20)
        gate.loaded(at: 0)
        _ = gate.playing(at: 2)
        gate.waiting(at: 10)
        XCTAssertEqual(gate.check(at: 14), .stalled)
        gate.loaded(at: 15)
        gate.waiting(at: 15.1)
        XCTAssertNil(gate.check(at: 25), "after a reload the new item gets its own startup window")
        XCTAssertEqual(gate.check(at: 35), .stalled)
    }

    func testPausedIsNeverAStall() {
        var gate = StallGate(grace: 4, startupTimeout: 20)
        gate.loaded(at: 0)
        _ = gate.playing(at: 2)
        gate.paused(at: 5)
        XCTAssertNil(gate.check(at: 300))
    }

    func testPauseDuringStartupKeepsStartupWindow() {
        var gate = StallGate(grace: 4, startupTimeout: 20)
        gate.loaded(at: 0)
        gate.paused(at: 0.05) // replaceCurrentItem can report paused before play()
        gate.waiting(at: 0.1)
        XCTAssertNil(gate.check(at: 10), "startup buffering after a transient pause is still startup")
        XCTAssertEqual(gate.check(at: 20), .stalled)
    }

    func testRecoveredOnlyReportedAfterAStallOrWait() {
        var gate = StallGate()
        gate.loaded(at: 0)
        XCTAssertEqual(gate.playing(at: 1), .recovered)
        XCTAssertNil(gate.playing(at: 2), "steady playing is not a new recovery")
    }
}
