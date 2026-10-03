import XCTest
@testable import BowtieKit

final class TunersBusyCopyTests: XCTestCase {
    func testNamesOtherApps() {
        XCTAssertEqual(TunersBusyCopy.otherAppsLine(otherInUse: 1), "1 tuner is in use by another app (like Plex).")
        XCTAssertEqual(TunersBusyCopy.otherAppsLine(otherInUse: 2), "2 tuners are in use by another app (like Plex).")
    }

    func testNoLineWhenOnlyBowtieViewers() {
        XCTAssertNil(TunersBusyCopy.otherAppsLine(otherInUse: 0))
    }
}
