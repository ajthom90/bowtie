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
        // Right after sign-in the list is still transitioning in and not hittable.
        let hittableBy = Date().addingTimeInterval(5)
        while !row.isHittable && Date() < hittableBy {
            RunLoop.current.run(until: Date().addingTimeInterval(0.25))
        }
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

    /// Live rewind: once the buffer has some history, the system scrubber must
    /// be reachable — a tap on the video shows it, not only Bowtie's chrome.
    func testLiveRewindScrubberReachable() throws {
        try testPlayChannel()
        // Let the DVR window grow past AVKit's minimum for showing a scrubber.
        RunLoop.current.run(until: Date().addingTimeInterval(40))
        attach("before-tap")
        app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
        let tree = app.debugDescription
        attach("after-tap")
        let treeAttachment = XCTAttachment(string: tree)
        treeAttachment.name = "after-tap-tree"
        treeAttachment.lifetime = .keepAlways
        add(treeAttachment)
        let scrubber = app.sliders.matching(NSPredicate(format: "identifier == %@", "Current position")).firstMatch
        XCTAssertTrue(scrubber.exists && scrubber.isHittable, "no reachable scrubber; tree:\n\(tree)")

        // Live pill: a just-started channel can leave playback behind the live
        // point; Live catches up. After skipping back it reports the delay and
        // jumps back to live when tapped.
        let live = app.buttons["Live"]
        XCTAssertTrue(live.exists, "no Live pill")
        if live.value as? String != "Watching live" {
            live.tap()
            expectation(for: NSPredicate(format: "value == %@", "Watching live"), evaluatedWith: live)
            waitForExpectations(timeout: 8)
        }
        if !app.buttons["Skip Backward"].isHittable {
            app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
        }
        app.buttons["Skip Backward"].tap()
        app.buttons["Skip Backward"].tap()
        let behind = NSPredicate(format: "value ENDSWITH %@", "seconds behind")
        expectation(for: behind, evaluatedWith: live)
        waitForExpectations(timeout: 5)
        attach("behind-live")
        if !live.isHittable {
            // Chrome auto-hid; a tap on the video brings it back.
            app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.3)).tap()
        }
        live.tap()
        expectation(for: NSPredicate(format: "value == %@", "Watching live"), evaluatedWith: live)
        waitForExpectations(timeout: 8)
    }

    /// The sleep timer is reachable from the live player chrome and shows the
    /// time left once set.
    func testSleepTimerFromPlayer() throws {
        try testPlayChannel()
        let sleep = app.buttons["bowtie.sleep"]
        if !sleep.isHittable {
            app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
        }
        XCTAssertTrue(sleep.waitForExistence(timeout: 3) && sleep.isHittable, "no reachable sleep timer button")
        attach("sleep-chrome")
        sleep.tap()
        let fifteen = app.buttons["15 minutes"]
        XCTAssertTrue(fifteen.waitForExistence(timeout: 3), "no 15 minutes option")
        attach("sleep-choices")
        fifteen.tap()
        if !sleep.isHittable {
            app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
        }
        expectation(for: NSPredicate(format: "value BEGINSWITH %@", "Sleeping in 1"), evaluatedWith: sleep)
        waitForExpectations(timeout: 5)
        attach("sleep-set")
    }

    /// The audio language chosen in Bowtie's Audio dialog is remembered across
    /// launches (MediaSelectionMemory; captions use the same path but AVKit's
    /// Subtitles menu is not reliably reachable from UI tests).
    func testAudioChoicePersists() throws {
        try testPlayChannel()
        RunLoop.current.run(until: Date().addingTimeInterval(6))
        chooseAudio(["Spanish", "Español"])
        attach("chosen")
        RunLoop.current.run(until: Date().addingTimeInterval(2))

        app.terminate()
        app.launch()
        try testPlayChannel()
        RunLoop.current.run(until: Date().addingTimeInterval(8))
        openMenu("bowtie.audio")
        let spanish = menuItem(["Spanish", "Español"])
        XCTAssertTrue(spanish.waitForExistence(timeout: 5) && spanish.label.hasSuffix("✓"),
                      "Spanish not restored:\n\(app.debugDescription)")
        attach("restored-audio")
        spanish.tap() // closes the dialog, keeps Spanish
    }

    /// Opens one of AVKit's menus (bringing the controls up first).
    private func openMenu(_ identifier: String) {
        let button = app.buttons[identifier]
        // A tap on the video toggles the controls, so one tap may hide them.
        for _ in 0..<3 where !(button.exists && button.isHittable) {
            app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
            _ = button.waitForExistence(timeout: 2)
        }
        XCTAssertTrue(button.exists && button.isHittable, "no \(identifier):\n\(app.debugDescription)")
        button.tap()
    }

    private func menuItem(_ labels: [String]) -> XCUIElement {
        let preds = labels.map { NSPredicate(format: "label CONTAINS[c] %@", $0) }
        return app.buttons.matching(NSCompoundPredicate(orPredicateWithSubpredicates: preds)).firstMatch
    }

    private func chooseAudio(_ labels: [String]) {
        let before = app.debugDescription
        openMenu("bowtie.audio")
        RunLoop.current.run(until: Date().addingTimeInterval(1))
        attach("audio-menu")
        let tree = XCTAttachment(string: "BEFORE\n" + before + "\nAFTER\n" + app.debugDescription)
        tree.name = "audio-menu-tree"
        tree.lifetime = .keepAlways
        add(tree)
        let item = menuItem(labels)
        XCTAssertTrue(item.waitForExistence(timeout: 5), "no audio item \(labels):\n\(app.debugDescription)")
        item.tap()
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
