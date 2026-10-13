import XCTest

/// Apple TV App Store screenshots: signs in with a password (Siri Remote
/// presses), then captures the channel rail and Recordings. Skipped unless a
/// server is supplied:
///
///     TEST_RUNNER_BOWTIE_UITEST_URL=https://tv.example.com \
///     TEST_RUNNER_BOWTIE_UITEST_USER=… TEST_RUNNER_BOWTIE_UITEST_PASS=… \
///     xcodebuild test -scheme BowtieTV -only-testing:BowtieTVUITests …
final class TVStoreScreenshotUITests: XCTestCase {
    private var app: XCUIApplication!
    private let remote = XCUIRemote.shared

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
        let recordings = app.buttons["Recordings"]
        let urlField = app.textFields.firstMatch
        let usePassword = app.buttons["Use a Password Instead"]
        let username = app.textFields["Username"]
        var first = waitForAny([recordings, urlField, usePassword, username], timeout: 20)

        if first == urlField && !recordings.exists && !usePassword.exists && !username.exists {
            type(env["BOWTIE_UITEST_URL"]!, into: urlField)
            press(app.buttons["Validate"])
            first = waitForAny([usePassword, username], timeout: 20)
        }
        if first == usePassword || usePassword.exists {
            press(usePassword)
            XCTAssertTrue(username.waitForExistence(timeout: 10), "no password sign-in")
        }
        if username.exists {
            type(env["BOWTIE_UITEST_USER"] ?? "admin", into: username)
            type(env["BOWTIE_UITEST_PASS"] ?? "", into: app.secureTextFields["Password"])
            press(app.buttons["Sign in"])
        }
        let signedIn = recordings.waitForExistence(timeout: 30)
        if !signedIn { attach("sign-in-stuck") }
        XCTAssertTrue(signedIn, "channel rail did not appear:\n\(app.debugDescription)")
        // Let logos and now/next load, and focus settle on the first channel.
        RunLoop.current.run(until: Date().addingTimeInterval(8))
        attach("01-channels")

        press(recordings)
        RunLoop.current.run(until: Date().addingTimeInterval(6))
        attach("02-recordings")
    }

    /// Moves focus to the element with the remote, then presses Select.
    private func press(_ element: XCUIElement) {
        XCTAssertTrue(element.waitForExistence(timeout: 10), "missing \(element)")
        focus(element)
        remote.press(.select)
    }

    private func type(_ text: String, into field: XCUIElement) {
        press(field)
        RunLoop.current.run(until: Date().addingTimeInterval(1))
        // Return submits the full-screen keyboard and goes back to the form.
        app.typeText(text + "\n")
        RunLoop.current.run(until: Date().addingTimeInterval(1.5))
    }

    /// Walks focus down, then up, until the element has it.
    private func focus(_ element: XCUIElement) {
        for direction in [XCUIRemote.Button.down, .up, .right, .left] {
            for _ in 0..<12 {
                if element.hasFocus { return }
                remote.press(direction)
                RunLoop.current.run(until: Date().addingTimeInterval(0.3))
            }
        }
        XCTAssertTrue(element.hasFocus, "could not focus \(element):\n\(app.debugDescription)")
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
