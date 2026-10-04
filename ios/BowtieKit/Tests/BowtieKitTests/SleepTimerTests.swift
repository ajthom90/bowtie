import XCTest
@testable import BowtieKit

@MainActor
final class SleepTimerTests: XCTestCase {
    private final class FakeClock {
        var now = Date(timeIntervalSince1970: 1_700_000_000)
        func advance(_ seconds: TimeInterval) { now += seconds }
    }

    private var clock: FakeClock!
    private var fired = 0

    override func setUp() {
        super.setUp()
        clock = FakeClock()
        fired = 0
    }

    private func makeTimer() -> SleepTimer {
        let clock = clock!
        let timer = SleepTimer(now: { clock.now })
        timer.onFire = { [unowned self] in self.fired += 1 }
        return timer
    }

    func testStartsOff() {
        let timer = makeTimer()
        XCTAssertEqual(timer.option, .off)
        XCTAssertNil(timer.remaining)
        XCTAssertFalse(timer.isActive)
        XCTAssertFalse(timer.isWarning)
    }

    func testRemainingTimeCountsDown() {
        let timer = makeTimer()
        XCTAssertTrue(timer.start(.minutes(30)))
        XCTAssertEqual(timer.option, .minutes(30))
        XCTAssertEqual(timer.remaining, 30 * 60)
        clock.advance(61)
        timer.tick()
        XCTAssertEqual(timer.remaining, 30 * 60 - 61)
        XCTAssertTrue(timer.isActive)
    }

    func testWarningStartsSixtySecondsBeforeFiring() {
        let timer = makeTimer()
        timer.start(.minutes(15))
        clock.advance(15 * 60 - 61)
        timer.tick()
        XCTAssertFalse(timer.isWarning)
        clock.advance(1)
        timer.tick()
        XCTAssertTrue(timer.isWarning, "warning at T-60s")
        XCTAssertEqual(timer.remaining, 60)
        XCTAssertEqual(fired, 0)
    }

    func testExtendAddsTheChosenDuration() {
        let timer = makeTimer()
        timer.start(.minutes(15))
        clock.advance(15 * 60 - 30)
        timer.tick()
        XCTAssertTrue(timer.isWarning)
        timer.extend()
        XCTAssertFalse(timer.isWarning)
        XCTAssertEqual(timer.remaining, 15 * 60 + 30)
        XCTAssertEqual(timer.option, .minutes(15))
        clock.advance(31)
        timer.tick()
        XCTAssertEqual(fired, 0, "the original deadline no longer fires")
    }

    func testExtendEndOfProgramAddsThirtyMinutes() {
        let timer = makeTimer()
        timer.start(.endOfProgram, programEnd: clock.now + 45)
        XCTAssertTrue(timer.isWarning)
        timer.extend()
        XCTAssertEqual(timer.remaining, 45 + 30 * 60)
    }

    func testExtendDoesNothingWhenOff() {
        let timer = makeTimer()
        timer.extend()
        XCTAssertNil(timer.remaining)
    }

    func testCancelStopsTheTimer() {
        let timer = makeTimer()
        timer.start(.minutes(45))
        timer.cancel()
        XCTAssertEqual(timer.option, .off)
        XCTAssertNil(timer.remaining)
        clock.advance(46 * 60)
        timer.tick()
        XCTAssertEqual(fired, 0)
    }

    func testStartingOffCancels() {
        let timer = makeTimer()
        timer.start(.minutes(45))
        timer.start(.off)
        XCTAssertFalse(timer.isActive)
    }

    func testEndOfProgramUsesTheProgramEnd() {
        let timer = makeTimer()
        let end = clock.now + 22 * 60
        XCTAssertTrue(timer.start(.endOfProgram, programEnd: end))
        XCTAssertEqual(timer.option, .endOfProgram)
        XCTAssertEqual(timer.remaining, 22 * 60)
        clock.advance(22 * 60)
        timer.tick()
        XCTAssertEqual(fired, 1)
    }

    func testEndOfProgramUnavailableWhenEndUnknownOrPast() {
        let now = clock.now
        XCTAssertFalse(SleepTimer.options(programEnd: nil, now: now).contains(.endOfProgram))
        XCTAssertFalse(SleepTimer.options(programEnd: now, now: now).contains(.endOfProgram))
        XCTAssertFalse(SleepTimer.options(programEnd: now - 10, now: now).contains(.endOfProgram))
        XCTAssertEqual(
            SleepTimer.options(programEnd: now + 60, now: now),
            [.off, .minutes(15), .minutes(30), .minutes(45), .minutes(60), .minutes(90), .minutes(120), .endOfProgram]
        )

        let timer = makeTimer()
        XCTAssertFalse(timer.start(.endOfProgram, programEnd: nil))
        XCTAssertFalse(timer.isActive)
        XCTAssertFalse(timer.start(.endOfProgram, programEnd: now - 1))
        XCTAssertFalse(timer.isActive)
    }

    func testFireCallsStopExactlyOnce() {
        let timer = makeTimer()
        timer.start(.minutes(15))
        clock.advance(15 * 60)
        timer.tick()
        XCTAssertEqual(fired, 1)
        XCTAssertFalse(timer.isActive)
        XCTAssertEqual(timer.option, .off)
        clock.advance(5)
        timer.tick()
        timer.tick()
        XCTAssertEqual(fired, 1)
    }

    func testFiresWhenTickIsLate() {
        let timer = makeTimer()
        timer.start(.minutes(15))
        clock.advance(20 * 60) // e.g. the app was suspended
        timer.tick()
        XCTAssertEqual(fired, 1)
    }

    func testLabels() {
        XCTAssertEqual(SleepTimer.Option.off.label, "Off")
        XCTAssertEqual(SleepTimer.Option.minutes(15).label, "15 minutes")
        XCTAssertEqual(SleepTimer.Option.minutes(60).label, "60 minutes")
        XCTAssertEqual(SleepTimer.Option.minutes(90).label, "90 minutes")
        XCTAssertEqual(SleepTimer.Option.minutes(120).label, "2 hours")
        XCTAssertEqual(SleepTimer.Option.endOfProgram.label, "End of this program")
    }

    func testFormat() {
        XCTAssertEqual(SleepTimer.format(60), "1:00")
        XCTAssertEqual(SleepTimer.format(59.4), "1:00", "rounds up so the prompt never shows 0:00 early")
        XCTAssertEqual(SleepTimer.format(5), "0:05")
        XCTAssertEqual(SleepTimer.format(0), "0:00")
        XCTAssertEqual(SleepTimer.format(29 * 60 + 41), "29:41")
        XCTAssertEqual(SleepTimer.format(2 * 3600), "2:00:00")
        XCTAssertEqual(SleepTimer.format(3600 + 65), "1:01:05")
    }

    func testPromptCopy() {
        XCTAssertEqual(SleepTimer.promptText(remaining: 60), "Still watching? Sleeping in 1:00")
    }
}
