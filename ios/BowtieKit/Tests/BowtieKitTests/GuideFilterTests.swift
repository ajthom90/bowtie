import XCTest
@testable import BowtieKit

/// Same vectors as web/src/guide/guideFilterModel.test.ts and Android GuideFilterTest.
final class GuideFilterTests: XCTestCase {
    private let t0 = Date(timeIntervalSince1970: 1_790_000_000)

    private func program(
        _ category: String = "",
        start: TimeInterval = 0,
        hours: Double = 1,
        rating: String? = nil,
        isNew: Bool? = nil,
        programId: String? = nil
    ) -> GuideProgram {
        GuideProgram(
            start: t0.addingTimeInterval(start * 3600),
            stop: t0.addingTimeInterval((start + hours) * 3600),
            title: "Show",
            subtitle: "",
            description: "",
            category: category,
            rating: rating,
            isNew: isNew,
            programId: programId
        )
    }

    private func buckets(_ category: String, rating: String? = nil, isNew: Bool? = nil, programId: String? = nil) -> Set<GuideBucket> {
        GuideFilter.buckets(for: program(category, rating: rating, isNew: isNew, programId: programId))
    }

    // MARK: - Mapping

    func testSportsCategories() {
        for c in [
            "Sports", "Sports event", "Sports non-event", "Sports talk", "SPORTS EVENT", "Football",
            "College football", "Basketball", "Baseball", "Soccer", "Hockey", "Golf", "Tennis",
            "Boxing", "Pro wrestling", "Auto racing", "Motorsports", "Figure skating", "Track/field",
            "Olympics", "Mixed martial arts",
        ] {
            XCTAssertEqual(buckets(c), [.sports], c)
        }
    }

    func testMovieCategories() {
        for c in ["Movie", "Movies", "movie", "Feature Film", "Film", "TV Movie", "Made-for-TV movie"] {
            XCTAssertEqual(buckets(c), [.movies], c)
        }
    }

    func testNewsCategories() {
        for c in ["News", "Newsmagazine", "News magazine", "Weather", "Local news", "Newscast"] {
            XCTAssertEqual(buckets(c), [.news], c)
        }
    }

    func testKidsCategories() {
        for c in [
            "Children", "Children's", "Children-music", "Children-special", "Kids", "Animated",
            "Animation", "Cartoon", "Educational",
        ] {
            XCTAssertEqual(buckets(c), [.kids], c)
        }
    }

    func testUnbucketedCategories() {
        for c in [
            "", "Family", "Drama", "Sitcom", "Comedy", "Movie review", "Martial arts",
            "Transportation", "Talk", "Series", "Reality",
        ] {
            XCTAssertEqual(buckets(c), [], c)
        }
    }

    func testTrimsAndIgnoresCase() {
        XCTAssertEqual(buckets("  sPoRtS eVeNt  "), [.sports])
    }

    func testJoinedCategoryCanLandInSeveralBuckets() {
        XCTAssertEqual(buckets("Sports talk; News"), [.sports, .news])
        XCTAssertEqual(buckets("Children, Animated"), [.kids])
        XCTAssertEqual(buckets("Movie | Animated"), [.movies, .kids])
    }

    func testSchedulesDirectProgramIDPrefixes() {
        XCTAssertEqual(buckets("Action", programId: "MV000111220000"), [.movies])
        XCTAssertEqual(buckets("", programId: "SP012345670123"), [.sports])
        XCTAssertEqual(buckets("Drama", programId: "EP012345670012"), [])
        XCTAssertEqual(buckets("", programId: "MVP"), [])
    }

    func testKidsMovieIsInBothBuckets() {
        XCTAssertEqual(buckets("Children", programId: "MV000111220000"), [.kids, .movies])
    }

    func testIsNewAddsNewBucket() {
        XCTAssertEqual(buckets("Sitcom", isNew: true), [.new])
        XCTAssertEqual(buckets("Sports event", isNew: true), [.new, .sports])
        XCTAssertEqual(buckets("Sitcom", isNew: false), [])
    }

    func testMatureRatingsKeepProgramOutOfKids() {
        XCTAssertEqual(buckets("Animated", rating: "TV-14"), [])
        XCTAssertEqual(buckets("Animated", rating: "TV-MA"), [])
        XCTAssertEqual(buckets("Animated", rating: "R"), [])
        XCTAssertEqual(buckets("Animated", rating: "TV-PG"), [.kids])
        XCTAssertEqual(buckets("Children", rating: "TV-Y7"), [.kids])
    }

    // MARK: - Matching

    func testAllMatchesEverything() {
        XCTAssertTrue(GuideFilter.all.matches(program()))
    }

    func testBucketMatchesOnlyItsPrograms() {
        XCTAssertTrue(GuideFilter.sports.matches(program("Football")))
        XCTAssertFalse(GuideFilter.movies.matches(program("Football")))
        XCTAssertTrue(GuideFilter.new.matches(program(isNew: true)))
    }

    func testChannelMatchesWithinWindow() {
        let from = t0
        let to = t0.addingTimeInterval(4 * 3600)
        XCTAssertTrue(GuideFilter.all.matches(programs: [], from: from, to: to))
        XCTAssertFalse(GuideFilter.sports.matches(programs: [], from: from, to: to))

        let programs = [program("News"), program("Football", start: 2, hours: 3)]
        XCTAssertTrue(GuideFilter.sports.matches(programs: programs, from: from, to: to))
        XCTAssertTrue(GuideFilter.news.matches(programs: programs, from: from, to: to))
        XCTAssertFalse(GuideFilter.movies.matches(programs: programs, from: from, to: to))
    }

    func testChannelIgnoresMatchesOutsideWindow() {
        let from = t0
        let to = t0.addingTimeInterval(4 * 3600)
        let before = program("Golf", start: -2, hours: 2)
        let after = program("Golf", start: 4, hours: 1)
        XCTAssertFalse(GuideFilter.sports.matches(programs: [before, after], from: from, to: to))
        let running = program("Golf", start: -1, hours: 1.5)
        XCTAssertTrue(GuideFilter.sports.matches(programs: [running], from: from, to: to))
    }

    func testFirstMatchIsEarliestInWindow() {
        let from = t0
        let to = t0.addingTimeInterval(4 * 3600)
        let late = program("Golf", start: 3)
        let early = program("Football", start: 1)
        let ended = program("Golf", start: -2, hours: 1)
        XCTAssertEqual(GuideFilter.sports.firstMatch(in: [late, ended, program("News"), early], from: from, to: to), early)
        XCTAssertNil(GuideFilter.movies.firstMatch(in: [late, early], from: from, to: to))
    }

    func testVisibleRowsHidesChannelsWithoutMatches() {
        let from = t0
        let to = t0.addingTimeInterval(4 * 3600)
        func row(_ id: Int64, _ programs: [GuideProgram]) -> ChannelListModel.Row {
            ChannelListModel.Row(
                channel: Channel(id: id, guideNumber: "\(id).1", name: "C\(id)", logoUrl: ""),
                nowNext: GuideLogic.nowNext(programs: programs, at: from),
                programs: programs
            )
        }
        let rows = [row(1, [program("Football")]), row(2, [program("News")]), row(3, [])]
        XCTAssertEqual(GuideFilter.all.visibleRows(rows, from: from, to: to).map(\.id), [1, 2, 3])
        XCTAssertEqual(GuideFilter.sports.visibleRows(rows, from: from, to: to).map(\.id), [1])
        XCTAssertEqual(GuideFilter.kids.visibleRows(rows, from: from, to: to).map(\.id), [])
    }

    func testRowHighlight() {
        let from = t0
        let to = t0.addingTimeInterval(4 * 3600)
        let news = program("News")
        let drama = program("Drama", start: 1)
        let golf = program("Golf", start: 2)
        let nowNext = GuideLogic.NowNext(now: news, next: drama)

        let all = GuideFilter.all.highlight(nowNext: nowNext, programs: [news, drama, golf], from: from, to: to)
        XCTAssertEqual(all, GuideFilter.RowHighlight(nowMatches: true, nextMatches: true, later: nil))

        let newsOnly = GuideFilter.news.highlight(nowNext: nowNext, programs: [news, drama, golf], from: from, to: to)
        XCTAssertEqual(newsOnly, GuideFilter.RowHighlight(nowMatches: true, nextMatches: false, later: nil))

        // Neither now nor next is sports: surface the later match.
        let sports = GuideFilter.sports.highlight(nowNext: nowNext, programs: [news, drama, golf], from: from, to: to)
        XCTAssertEqual(sports, GuideFilter.RowHighlight(nowMatches: false, nextMatches: false, later: golf))
    }

    // MARK: - Copy and persistence

    func testChipOrderAndLabels() {
        XCTAssertEqual(GuideFilter.allCases.map(\.label), ["All", "Sports", "Movies", "News", "Kids", "New"])
    }

    func testEmptyCopy() {
        XCTAssertEqual(GuideFilter.sports.emptyCopy, "No sports on in this time window")
        XCTAssertEqual(GuideFilter.movies.emptyCopy, "No movies on in this time window")
        XCTAssertEqual(GuideFilter.news.emptyCopy, "No news on in this time window")
        XCTAssertEqual(GuideFilter.kids.emptyCopy, "No kids' shows on in this time window")
        XCTAssertEqual(GuideFilter.new.emptyCopy, "No new episodes on in this time window")
    }

    func testPersistenceRoundTripAndFallback() {
        let d = UserDefaults(suiteName: "GuideFilterTests")!
        d.removePersistentDomain(forName: "GuideFilterTests")
        XCTAssertEqual(GuideFilter.load(from: d), .all)
        GuideFilter.kids.save(to: d)
        XCTAssertEqual(GuideFilter.load(from: d), .kids)
        d.set("bogus", forKey: GuideFilter.defaultsKey)
        XCTAssertEqual(GuideFilter.load(from: d), .all)
    }
}
