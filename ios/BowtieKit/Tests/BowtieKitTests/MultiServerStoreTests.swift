import XCTest
@testable import BowtieKit

final class MultiServerStoreTests: XCTestCase {
    let prod = URL(string: "https://bowtie.example.net")!
    let local = URL(string: "http://192.168.0.25:8400")!

    func testServersAreSavedWithTheirOwnTokens() {
        let store = InMemorySessionStore()
        store.save(server: prod, refreshToken: "prod-tok")
        store.save(server: local, refreshToken: "local-tok")

        XCTAssertEqual(store.savedServers().map(\.url), [prod, local])
        XCTAssertEqual(store.loadServer(), local)
        XCTAssertEqual(store.loadRefreshToken(), "local-tok")

        store.selectServer(prod)
        XCTAssertEqual(store.loadServer(), prod)
        XCTAssertEqual(store.loadRefreshToken(), "prod-tok", "switching back keeps that server's login")
    }

    func testHasLoginForAnySavedServer() {
        let store = InMemorySessionStore()
        store.save(server: prod, refreshToken: "prod-tok")
        store.save(server: local, refreshToken: nil)

        XCTAssertTrue(store.hasLogin(for: prod), "inactive server keeps its login")
        XCTAssertFalse(store.hasLogin(for: local))
        XCTAssertFalse(store.hasLogin(for: URL(string: "http://unknown:8400")!))
    }

    func testClearingServerKeepsSavedServersAndLogins() {
        let store = InMemorySessionStore()
        store.save(server: prod, refreshToken: "prod-tok")
        store.save(server: nil, refreshToken: nil) // "Change server"

        XCTAssertNil(store.loadServer())
        XCTAssertNil(store.loadRefreshToken())
        XCTAssertEqual(store.savedServers().map(\.url), [prod])
        store.selectServer(prod)
        XCTAssertEqual(store.loadRefreshToken(), "prod-tok")
    }

    func testSignOutClearsOnlyThatServersToken() {
        let store = InMemorySessionStore()
        store.save(server: prod, refreshToken: "prod-tok")
        store.save(server: local, refreshToken: "local-tok")
        store.save(server: local, refreshToken: nil)

        XCTAssertNil(store.loadRefreshToken())
        store.selectServer(prod)
        XCTAssertEqual(store.loadRefreshToken(), "prod-tok")
    }

    func testRemoveForgetsServerAndToken() {
        let store = InMemorySessionStore()
        store.save(server: prod, refreshToken: "prod-tok")
        store.save(server: local, refreshToken: "local-tok")
        store.removeServer(local)

        XCTAssertEqual(store.savedServers().map(\.url), [prod])
        XCTAssertNil(store.loadServer(), "removing the active server leaves none active")
        store.save(server: local, refreshToken: nil)
        XCTAssertNil(store.loadRefreshToken(), "a removed server's token is gone")
    }

    func testLabelIsHostAndPort() {
        let store = InMemorySessionStore()
        store.save(server: local, refreshToken: nil)
        store.save(server: prod, refreshToken: nil)
        XCTAssertEqual(store.savedServers().map(\.label), ["192.168.0.25:8400", "bowtie.example.net"])
    }

    func testMigratesLegacySingleServer() {
        let backing = MemoryBacking()
        backing.write(account: "server", value: prod.absoluteString)
        backing.write(account: "refreshToken", value: "legacy-tok")
        let store = MultiServerStore(backing: backing)

        XCTAssertEqual(store.loadServer(), prod)
        XCTAssertEqual(store.loadRefreshToken(), "legacy-tok")
        XCTAssertEqual(store.savedServers().map(\.url), [prod])
        XCTAssertNil(backing.read(account: "server"))
        XCTAssertNil(backing.read(account: "refreshToken"))
    }

    func testPersistsAcrossInstances() {
        let backing = MemoryBacking()
        MultiServerStore(backing: backing).save(server: prod, refreshToken: "prod-tok")
        let reopened = MultiServerStore(backing: backing)
        XCTAssertEqual(reopened.loadServer(), prod)
        XCTAssertEqual(reopened.loadRefreshToken(), "prod-tok")
    }
}
