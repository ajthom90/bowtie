import Foundation
import os

/// What a viewer reads when something fails: plain words, never exception,
/// network or HTTP text. The server's own messages are already written for
/// viewers and pass through. The technical cause goes to the log.
public enum ViewerErrorCopy {
    public static let cantReachServer = "Can't reach your Bowtie server. Check your connection and try again."
    public static let somethingWentWrong = "Something went wrong. Try again."
    public static let streamStopped = "The stream stopped. Try again."
    public static let signedOut = "You've been signed out. Sign in again."
    public static let tunersBusy = "All tuners are in use. Try again in a few minutes."
    public static let badServerAddress = "That server address doesn't look right. Check it and try again."

    private static let log = Logger(subsystem: "app.bowtie", category: "errors")

    public static func message(for error: Error) -> String {
        log.error("\(String(describing: error), privacy: .public)")
        guard let error = error as? BowtieError else {
            return error is URLError ? cantReachServer : somethingWentWrong
        }
        switch error {
        case .unauthorized:
            return signedOut
        case .tunersBusy:
            return tunersBusy
        case .negotiationFailed(let message),
             .recordingConflict(_, _, let message),
             .parental(let message):
            return serverText(message)
        case .server(let status, let message):
            // No message of its own: a proxy page or a bare status. 502–504
            // mean whatever sits in front of Bowtie couldn't reach it.
            if message.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty, (502...504).contains(status) {
                return cantReachServer
            }
            return serverText(message)
        case .notFound, .badResponse:
            return somethingWentWrong
        case .network:
            return cantReachServer
        case .invalidServerURL:
            return badServerAddress
        }
    }

    /// The server's message as a sentence (some are lowercase phrases).
    private static func serverText(_ message: String) -> String {
        let trimmed = message.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return somethingWentWrong }
        return trimmed.prefix(1).uppercased() + trimmed.dropFirst()
    }
}
