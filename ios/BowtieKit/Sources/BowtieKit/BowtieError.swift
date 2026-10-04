import Foundation

public enum BowtieError: Error, Equatable {
    /// Post-refresh 401 → sign out.
    case unauthorized
    /// 503 all tuners in use. `otherInUse`: tuners held by other apps (e.g.
    /// Plex); 0 when only Bowtie viewers hold them or the server is older.
    case tunersBusy([ActiveSessionSummary], otherInUse: Int)
    /// 422 negotiation / validation message.
    case negotiationFailed(String)
    /// 409 on `POST /recordings`: more distinct channels than tuners at that
    /// time. `message` is the server's copy; retry with `force` to record anyway.
    case recordingConflict(tunerCount: Int, conflicts: [Recording], message: String)
    case notFound
    case server(status: Int, message: String)
    case network(String)
    case invalidServerURL
}
