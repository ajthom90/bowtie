import XCTest
@testable import BowtieKit

final class SessionStoreTests: XCTestCase {
    func testInMemoryRoundTrip() {
        let store = InMemorySessionStore()
        XCTAssertNil(store.loadServer())
        XCTAssertNil(store.loadRefreshToken())

        let url = URL(string: "http://192.168.1.50:8400")!
        store.save(server: url, refreshToken: "refresh-1")
        XCTAssertEqual(store.loadServer(), url)
        XCTAssertEqual(store.loadRefreshToken(), "refresh-1")

        store.save(server: url, refreshToken: "refresh-2")
        XCTAssertEqual(store.loadRefreshToken(), "refresh-2")
        XCTAssertEqual(store.loadServer(), url)
    }

    func testInMemoryNilClears() {
        let store = InMemorySessionStore()
        let url = URL(string: "https://tv.example.com")!
        store.save(server: url, refreshToken: "tok")

        store.save(server: nil, refreshToken: nil)
        XCTAssertNil(store.loadServer())
        XCTAssertNil(store.loadRefreshToken())
    }

    func testInMemoryPartialClear() {
        let store = InMemorySessionStore()
        let url = URL(string: "http://localhost:8400")!
        store.save(server: url, refreshToken: "tok")

        // Clear token only, keep server (refresh-failure / logout pattern).
        store.save(server: url, refreshToken: nil)
        XCTAssertEqual(store.loadServer(), url)
        XCTAssertNil(store.loadRefreshToken())
    }
}
