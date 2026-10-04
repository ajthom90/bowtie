import XCTest
@testable import BowtieKit

final class MediaPrefsTests: XCTestCase {
    func testPickIndexMatchesPrimarySubtag() {
        XCTAssertEqual(MediaPrefs.pickIndex(languages: ["en", "es"], preferred: "es"), 1)
        XCTAssertEqual(MediaPrefs.pickIndex(languages: ["en-US", "es-MX"], preferred: "es"), 1)
        XCTAssertNil(MediaPrefs.pickIndex(languages: ["en", nil], preferred: "fr"))
        XCTAssertNil(MediaPrefs.pickIndex(languages: ["en"], preferred: nil))
    }

    func testRoundTripAndDefaults() {
        let d = UserDefaults(suiteName: "MediaPrefsTests")!
        d.removePersistentDomain(forName: "MediaPrefsTests")
        XCTAssertEqual(MediaPrefs.load(from: d), MediaPrefs())
        MediaPrefs(audioLanguage: "es", captionsOn: true).save(to: d)
        XCTAssertEqual(MediaPrefs.load(from: d), MediaPrefs(audioLanguage: "es", captionsOn: true))
        d.set(Data("junk".utf8), forKey: MediaPrefs.defaultsKey)
        XCTAssertEqual(MediaPrefs.load(from: d), MediaPrefs())
    }
}
