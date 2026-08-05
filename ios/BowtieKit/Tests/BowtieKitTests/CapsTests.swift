import XCTest
@testable import BowtieKit

final class CapsTests: XCTestCase {
    func testMakeReturnsFixedCodecsAndEmptyProfile() {
        let caps = Caps.make(maxHeight: 1080)
        XCTAssertEqual(caps.videoCodecs, ["h264", "hevc"])
        XCTAssertEqual(caps.audioCodecs, ["aac", "ac3", "eac3"])
        XCTAssertEqual(caps.maxHeight, 1080)
        XCTAssertEqual(caps.profile, "")
    }

    func testMakePropagatesMaxHeight() {
        XCTAssertEqual(Caps.make(maxHeight: 720).maxHeight, 720)
        XCTAssertEqual(Caps.make(maxHeight: 2160).maxHeight, 2160)
        XCTAssertEqual(Caps.make(maxHeight: 0).maxHeight, 0)
    }
}
