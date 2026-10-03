import XCTest
@testable import BowtieKit

final class LiveEdgeTests: XCTestCase {
    func testAtThePlayersLiveOffsetIsLive() {
        // AVPlayer's live point is its recommended offset back from the seekable end.
        let behind = LiveEdge.secondsBehind(seekableEnd: 900, current: 888, liveOffset: 12)
        XCTAssertEqual(behind, 0)
        XCTAssertTrue(LiveEdge.isLive(secondsBehind: behind))
    }

    func testStartingBehindAFreshPlaylistIsBehind() {
        // A just-started channel: playback began at the start of a short playlist
        // and runs ~33 s behind the end — 21 s behind where live playback sits.
        let behind = LiveEdge.secondsBehind(seekableEnd: 900, current: 867, liveOffset: 12)
        XCTAssertEqual(behind, 21)
        XCTAssertFalse(LiveEdge.isLive(secondsBehind: behind))
    }

    func testAheadOfTheOffsetClampsToZero() {
        XCTAssertEqual(LiveEdge.secondsBehind(seekableEnd: 900, current: 899, liveOffset: 12), 0)
    }

    func testLiveTarget() {
        XCTAssertEqual(LiveEdge.liveTarget(seekableEnd: 900, liveOffset: 12), 888)
        XCTAssertEqual(LiveEdge.liveTarget(seekableEnd: 5, liveOffset: 12), 0)
    }

    func testSmallDriftStillCountsAsLive() {
        XCTAssertTrue(LiveEdge.isLive(secondsBehind: 9))
        XCTAssertFalse(LiveEdge.isLive(secondsBehind: 10))
    }

    func testBehindLabel() {
        XCTAssertEqual(LiveEdge.behindLabel(secondsBehind: 90), "−1:30")
        XCTAssertEqual(LiveEdge.behindLabel(secondsBehind: 605), "−10:05")
        XCTAssertEqual(LiveEdge.behindLabel(secondsBehind: 9.6), "−0:09")
    }
}
