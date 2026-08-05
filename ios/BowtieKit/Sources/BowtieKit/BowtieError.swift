import Foundation

public enum BowtieError: Error, Equatable {
    /// Post-refresh 401 → sign out.
    case unauthorized
    /// 503 all tuners in use.
    case tunersBusy([ActiveSessionSummary])
    /// 422 negotiation / validation message.
    case negotiationFailed(String)
    case notFound
    case server(status: Int, message: String)
    case network(String)
    case invalidServerURL
}
