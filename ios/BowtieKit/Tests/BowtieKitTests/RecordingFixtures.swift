import Foundation
@testable import BowtieKit

/// JSON + value fixtures for DVR recordings (shape of `Recording` in openapi.yaml).
enum RecordingFixtures {
    static func json(
        id: Int64 = 7,
        title: String = "Jeopardy!",
        state: String = "ready",
        partial: Bool = false,
        failure: String = "",
        durationSec: Int = 1980,
        sizeBytes: Int64 = 3_800_000_000,
        protected: Bool = false,
        positionSec: Int = 0,
        canManage: Bool = true,
        extra: String = ""
    ) -> String {
        """
        {
          "id": \(id),
          "title": "\(title)",
          "subtitle": "Teachers Tournament",
          "description": "Quiz show.",
          "category": "Game show",
          "channelId": 3,
          "channelName": "5.1 KSTP",
          "start": "2026-10-05T00:00:00Z",
          "stop": "2026-10-05T00:30:00Z",
          "state": "\(state)",
          "partial": \(partial),
          "failure": "\(failure)",
          "failureDetail": "",
          "durationSec": \(durationSec),
          "sizeBytes": \(sizeBytes),
          "protected": \(protected),
          "positionSec": \(positionSec),
          "scheduledBy": "andrew",
          "canManage": \(canManage)\(extra.isEmpty ? "" : ", " + extra)
        }
        """
    }

    static func listJSON(_ items: [String]) -> Data {
        "[\(items.joined(separator: ","))]".data(using: .utf8)!
    }

    static func recording(
        id: Int64 = 7,
        title: String = "Jeopardy!",
        state: String = "ready",
        partial: Bool = false,
        failure: String = "",
        durationSec: Int = 1980,
        sizeBytes: Int64 = 3_800_000_000,
        protected: Bool = false,
        positionSec: Int = 0,
        canManage: Bool = true,
        rating: String = "",
        ruleId: Int64 = 0,
        locked: Bool = false
    ) -> Recording {
        Recording(
            id: id,
            title: title,
            subtitle: "Teachers Tournament",
            description: "Quiz show.",
            category: "Game show",
            channelId: 3,
            channelName: "5.1 KSTP",
            start: TestFixtures.iso("2026-10-05T00:00:00Z"),
            stop: TestFixtures.iso("2026-10-05T00:30:00Z"),
            state: state,
            partial: partial,
            failure: failure,
            failureDetail: "",
            durationSec: durationSec,
            sizeBytes: sizeBytes,
            protected: protected,
            positionSec: positionSec,
            scheduledBy: "andrew",
            canManage: canManage,
            rating: rating,
            ruleId: ruleId,
            locked: locked
        )
    }
}
