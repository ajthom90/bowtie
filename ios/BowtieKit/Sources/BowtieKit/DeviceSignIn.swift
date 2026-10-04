import Foundation
import Observation

// MARK: - Wire types

/// `POST /api/v1/auth/device` 200 body.
public struct DeviceSignInCode: Codable, Equatable, Sendable {
    /// Secret; only this device knows it.
    public let deviceCode: String
    /// What the person types on the link page, e.g. "BCDF-2345".
    public let userCode: String
    /// The web app's link page as the server saw the request (its host may
    /// not be the one this device reached).
    public let verifyUrl: String
    /// Server-relative PNG of the link as a QR code. Absent from older servers.
    public let qrUrl: String?
    public let expiresIn: Int
    /// Seconds between polls.
    public let interval: Int

    public init(deviceCode: String, userCode: String, verifyUrl: String, qrUrl: String?, expiresIn: Int, interval: Int) {
        self.deviceCode = deviceCode
        self.userCode = userCode
        self.verifyUrl = verifyUrl
        self.qrUrl = qrUrl
        self.expiresIn = expiresIn
        self.interval = interval
    }
}

/// One `POST /api/v1/auth/device/token` answer.
public enum DeviceSignInPoll: Equatable, Sendable {
    /// 428: not approved yet.
    case pending
    /// 200: signed in as `User`; the client holds the new tokens.
    case approved(User)
    /// 410: expired, used or unknown.
    case expired
}

// MARK: - Model

/// "Sign in with your phone" on Apple TV: gets a code, shows it with its QR
/// image, and polls every `interval` seconds until a signed-in phone or
/// browser approves it, the code expires, or `cancel()`.
@Observable
@MainActor
public final class DeviceSignInModel {
    /// What the TV shows while it waits.
    public struct Code: Equatable, Sendable {
        public let userCode: String
        /// Absolute QR image URL (unauthenticated PNG).
        public let qrURL: URL
        /// The link page on the server this device is connected to.
        public let linkURL: URL
        public let expiresAt: Date

        public init(userCode: String, qrURL: URL, linkURL: URL, expiresAt: Date) {
            self.userCode = userCode
            self.qrURL = qrURL
            self.linkURL = linkURL
            self.expiresAt = expiresAt
        }

        /// The link without its scheme, for "Or go to …" ("192.168.1.50:8400/link").
        public var linkText: String {
            var text = linkURL.absoluteString
            for prefix in ["https://", "http://"] where text.hasPrefix(prefix) {
                text.removeFirst(prefix.count)
            }
            return text
        }
    }

    public enum State: Equatable {
        case idle
        case starting
        case waiting(Code)
        /// The code ran out; offer a new one.
        case expired
        case failed(String)
        /// Approved: the client is signed in as this user.
        case signedIn(User)
    }

    /// Used when the server sends no usable interval.
    public static let defaultInterval = 5

    public private(set) var state: State = .idle
    /// Called each time a new code is on screen.
    public var onWaiting: ((Code) -> Void)?

    private let client: BowtieClient
    private let deviceName: String
    private let sleep: @MainActor (Duration) async throws -> Void
    private let now: @MainActor () -> Date

    private var runTask: Task<Void, Never>?
    /// Bumped by every start / cancel so an old loop stops touching state.
    private var generation: UInt64 = 0

    /// - Parameters:
    ///   - deviceName: shown to the person approving ("Living Room Apple TV").
    ///   - sleep: waits between polls; injectable for tests.
    ///   - now: clock for the code's local expiry; injectable for tests.
    public init(
        client: BowtieClient,
        deviceName: String,
        sleep: @escaping @MainActor (Duration) async throws -> Void = { try await Task.sleep(for: $0) },
        now: @escaping @MainActor () -> Date = Date.init
    ) {
        self.client = client
        self.deviceName = deviceName
        self.sleep = sleep
        self.now = now
    }

    /// Gets a fresh code and polls until done. Returns when the run ends
    /// (signed in, expired, failed or cancelled). A new start replaces a
    /// running one.
    public func start() async {
        runTask?.cancel()
        generation &+= 1
        let gen = generation
        let task = Task { @MainActor in
            await self.run(generation: gen)
        }
        runTask = task
        await task.value
    }

    /// Stops polling (e.g. the person chose to type a password instead).
    public func cancel() {
        generation &+= 1
        runTask?.cancel()
        runTask = nil
        state = .idle
    }

    // MARK: - Loop

    private func isCurrent(_ gen: UInt64) -> Bool {
        gen == generation && !Task.isCancelled
    }

    private func run(generation gen: UInt64) async {
        state = .starting
        let issued: DeviceSignInCode
        do {
            issued = try await client.startDeviceSignIn(deviceName: deviceName)
        } catch {
            guard isCurrent(gen) else { return }
            state = .failed(RecordingErrorCopy.message(for: error))
            return
        }
        guard isCurrent(gen) else { return }

        let code = makeCode(issued)
        state = .waiting(code)
        onWaiting?(code)

        let interval = Duration.seconds(issued.interval > 0 ? issued.interval : Self.defaultInterval)
        while true {
            do {
                try await sleep(interval)
            } catch {
                return
            }
            guard isCurrent(gen) else { return }
            if now() >= code.expiresAt {
                state = .expired
                return
            }

            let poll: DeviceSignInPoll
            do {
                poll = try await client.pollDeviceSignIn(deviceCode: issued.deviceCode)
            } catch {
                // A dropped request mid-wait: keep polling until the code expires.
                guard isCurrent(gen) else { return }
                continue
            }
            guard isCurrent(gen) else { return }

            switch poll {
            case .pending:
                continue
            case .expired:
                state = .expired
                return
            case .approved(let user):
                state = .signedIn(user)
                return
            }
        }
    }

    private func makeCode(_ issued: DeviceSignInCode) -> Code {
        let base = client.serverURL
        let raw = issued.userCode.replacingOccurrences(of: "-", with: "")
        let qrPath = issued.qrUrl ?? "/api/v1/auth/device/qr/\(raw).png"
        return Code(
            userCode: issued.userCode,
            qrURL: ServerURL.resolve(path: qrPath, against: base),
            linkURL: ServerURL.resolve(path: "/link", against: base),
            expiresAt: now().addingTimeInterval(TimeInterval(max(0, issued.expiresIn)))
        )
    }
}
