import XCTest

/// App Store screenshots: signs in, then captures the channel list, a guide
/// search and Recordings. Skipped unless a server is
/// supplied, like PlaybackUITests:
///
///     TEST_RUNNER_BOWTIE_UITEST_URL=https://tv.example.com \
///     TEST_RUNNER_BOWTIE_UITEST_USER=… TEST_RUNNER_BOWTIE_UITEST_PASS=… \
///     TEST_RUNNER_BOWTIE_UITEST_SEARCH=news \
///     xcodebuild test -scheme Bowtie -only-testing:BowtieUITests/StoreScreenshotUITests …
///
/// Export with `xcrun xcresulttool export attachments`. Set the status bar
/// first (`xcrun simctl status_bar <device> override --time 9:41 …`).
final class StoreScreenshotUITests: XCTestCase {
    private var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard ProcessInfo.processInfo.environment["BOWTIE_UITEST_URL"] != nil else {
            throw XCTSkip("BOWTIE_UITEST_URL not set; no server to capture")
        }
        app = XCUIApplication()
        app.launch()
    }

    func testCaptureStoreScreenshots() throws {
        let env = ProcessInfo.processInfo.environment
        let channels = app.navigationBars["Channels"]
        signIn(env, channels: channels)
        XCTAssertTrue(channels.waitForExistence(timeout: 20), "channel list did not appear")
        // The system's "Save Password?" prompt after a fresh sign-in.
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for notNow in [app.buttons["Not Now"], springboard.buttons["Not Now"]] where notNow.waitForExistence(timeout: 3) {
            notNow.tap()
        }
        // Let logos and now/next load.
        RunLoop.current.run(until: Date().addingTimeInterval(6))
        attach("01-channels")

        // Guide search. No playing video: App Review can object to recognizable
        // broadcast shows in screenshots.
        let search = app.searchFields.firstMatch
        XCTAssertTrue(search.waitForExistence(timeout: 5), "no guide search field")
        search.tap()
        // On a fresh simulator the first tap can land before the field takes focus.
        for _ in 0..<3 where !app.keyboards.firstMatch.waitForExistence(timeout: 3) {
            search.tap()
        }
        // Return submits the search and puts the keyboard away.
        search.typeText((env["BOWTIE_UITEST_SEARCH"] ?? "news") + "\n")
        RunLoop.current.run(until: Date().addingTimeInterval(5))
        attach("02-search")
        // Back to the channel list without hunting for the search's close button.
        app.terminate()
        app.launch()
        XCTAssertTrue(channels.waitForExistence(timeout: 20), "channel list did not come back")
        app.buttons["Recordings"].tap()
        XCTAssertTrue(app.navigationBars["Recordings"].waitForExistence(timeout: 10), "no Recordings screen")
        RunLoop.current.run(until: Date().addingTimeInterval(5))
        attach("03-recordings")
    }

    private func signIn(_ env: [String: String], channels: XCUIElement) {
        let urlField = app.textFields.firstMatch
        let username = app.textFields["Username"]
        let first = waitForAny([channels, username, urlField], timeout: 15)
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
        let shot = XCTAttachment(screenshot: XCUIScreen.main.screenshot())
        shot.name = name
        shot.lifetime = .keepAlways
        add(shot)
    }
}
