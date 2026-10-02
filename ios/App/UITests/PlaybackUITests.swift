import XCTest

/// End-to-end playback against a real Bowtie server. Skipped unless the server
/// is supplied, so CI (no server) never runs it. Pass settings through
/// xcodebuild with the TEST_RUNNER_ prefix, e.g.
///
///     TEST_RUNNER_BOWTIE_UITEST_URL=http://127.0.0.1:8400 \
///     TEST_RUNNER_BOWTIE_UITEST_USER=admin TEST_RUNNER_BOWTIE_UITEST_PASS=… \
///     TEST_RUNNER_BOWTIE_UITEST_CHANNEL="FOX 9" \
///     xcodebuild test -scheme Bowtie -only-testing:BowtieUITests …
final class PlaybackUITests: XCTestCase {
    private var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
        let env = ProcessInfo.processInfo.environment
        guard env["BOWTIE_UITEST_URL"] != nil else {
            throw XCTSkip("BOWTIE_UITEST_URL not set; no server to play against")
        }
        app = XCUIApplication()
        app.launch()
    }

    func testPlayChannel() throws {
        let env = ProcessInfo.processInfo.environment
        let channel = env["BOWTIE_UITEST_CHANNEL"] ?? ""

        let urlField = app.textFields.firstMatch
        let username = app.textFields["Username"]
        let channels = app.navigationBars["Channels"]
        let first = waitForAny([urlField, username, channels], timeout: 15)

        if first == urlField && !username.exists && !channels.exists {
            urlField.tap()
            urlField.typeText(env["BOWTIE_UITEST_URL"]!)
            app.buttons["Validate"].tap()
            XCTAssertTrue(username.waitForExistence(timeout: 15), "login screen did not appear")
        }
        if username.exists {
            username.tap()
            username.typeText(env["BOWTIE_UITEST_USER"] ?? "admin")
            let password = app.secureTextFields["Password"]
            password.tap()
            password.typeText(env["BOWTIE_UITEST_PASS"] ?? "")
            app.buttons["Sign in"].tap()
        }
        XCTAssertTrue(channels.waitForExistence(timeout: 20), "channel list did not appear")
        attach("channels")

        let row = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", channel)).firstMatch
        XCTAssertTrue(row.waitForExistence(timeout: 15), "no channel row matching \"\(channel)\"")
        row.tap()

        // Give the session time to start and the first segments to play.
        let failed = [app.buttons["Try again"], app.staticTexts["Not connected to a server"]]
        let deadline = Date().addingTimeInterval(25)
        while Date() < deadline && !failed.contains(where: \.exists) {
            RunLoop.current.run(until: Date().addingTimeInterval(1))
        }
        let stats = app.buttons["Show stats"]
        if stats.exists && stats.isHittable {
            stats.tap()
        }
        attach("player")
        let shown = app.staticTexts.allElementsBoundByIndex.map(\.label).joined(separator: " | ")
        XCTAssertFalse(failed.contains(where: \.exists), "playback failed; screen shows: \(shown)")
        // Playing hides the chrome; a tap brings it back.
        if !app.buttons["Done"].exists {
            app.tap()
        }
        XCTAssertTrue(app.buttons["Done"].waitForExistence(timeout: 3), "not on the player screen; screen shows: \(shown)")
    }

    func testSwitchToSavedServerKeepsLogin() throws {
        try testPlayChannel() // ensures we are signed in and the server is saved
        app.buttons["Done"].tap()

        app.buttons["Settings"].tap()
        app.buttons["Change server"].tap()
        let saved = app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "Saved server")).firstMatch
        XCTAssertTrue(saved.waitForExistence(timeout: 10), "saved server not listed")
        attach("saved-servers")
        saved.tap()

        XCTAssertTrue(app.navigationBars["Channels"].waitForExistence(timeout: 20),
                      "switching to a saved server should not ask to sign in again")
        XCTAssertFalse(app.textFields["Username"].exists)
    }

    private func waitForAny(_ elements: [XCUIElement], timeout: TimeInterval) -> XCUIElement? {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if let hit = elements.first(where: \.exists) {
                return hit
            }
            RunLoop.current.run(until: Date().addingTimeInterval(0.25))
        }
        return nil
    }

    private func attach(_ name: String) {
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }
}
