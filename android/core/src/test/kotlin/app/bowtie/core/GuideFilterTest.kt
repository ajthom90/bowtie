package app.bowtie.core

import app.bowtie.core.GuideBucket.KIDS
import app.bowtie.core.GuideBucket.MOVIES
import app.bowtie.core.GuideBucket.NEW
import app.bowtie.core.GuideBucket.NEWS
import app.bowtie.core.GuideBucket.SPORTS
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test
import java.time.Duration
import java.time.Instant

/** Same vectors as web guideFilterModel.test.ts and iOS GuideFilterTests. */
class GuideFilterTest {

    private val t0: Instant = Instant.parse("2026-10-04T18:00:00Z")

    private fun at(hours: Double): Instant = t0.plus(Duration.ofMinutes((hours * 60).toLong()))

    private fun prog(
        category: String = "",
        start: Double = 0.0,
        hours: Double = 1.0,
        rating: String = "",
        isNew: Boolean? = null,
        programId: String? = null,
    ) = GuideProgram(
        start = at(start),
        stop = at(start + hours),
        title = "Show",
        subtitle = "",
        description = "",
        category = category,
        rating = rating,
        isNew = isNew,
        programId = programId,
    )

    private fun buckets(
        category: String,
        rating: String = "",
        isNew: Boolean? = null,
        programId: String? = null,
    ): Set<GuideBucket> = GuideFilter.buckets(prog(category, rating = rating, isNew = isNew, programId = programId))

    // ── Mapping ─────────────────────────────────────────────────────────────

    @Test
    fun sportsCategories() {
        listOf(
            "Sports", "Sports event", "Sports non-event", "Sports talk", "SPORTS EVENT", "Football",
            "College football", "Basketball", "Baseball", "Soccer", "Hockey", "Golf", "Tennis",
            "Boxing", "Pro wrestling", "Auto racing", "Motorsports", "Figure skating", "Track/field",
            "Olympics", "Mixed martial arts",
        ).forEach { assertEquals(it, setOf(SPORTS), buckets(it)) }
    }

    @Test
    fun movieCategories() {
        listOf("Movie", "Movies", "movie", "Feature Film", "Film", "TV Movie", "Made-for-TV movie")
            .forEach { assertEquals(it, setOf(MOVIES), buckets(it)) }
    }

    @Test
    fun newsCategories() {
        listOf("News", "Newsmagazine", "News magazine", "Weather", "Local news", "Newscast")
            .forEach { assertEquals(it, setOf(NEWS), buckets(it)) }
    }

    @Test
    fun kidsCategories() {
        listOf(
            "Children", "Children's", "Children-music", "Children-special", "Kids", "Animated",
            "Animation", "Cartoon", "Educational",
        ).forEach { assertEquals(it, setOf(KIDS), buckets(it)) }
    }

    @Test
    fun unbucketedCategories() {
        listOf(
            "", "Family", "Drama", "Sitcom", "Comedy", "Movie review", "Martial arts",
            "Transportation", "Talk", "Series", "Reality",
        ).forEach { assertEquals(it, emptySet<GuideBucket>(), buckets(it)) }
    }

    @Test
    fun trimsAndIgnoresCase() {
        assertEquals(setOf(SPORTS), buckets("  sPoRtS eVeNt  "))
    }

    @Test
    fun joinedCategoryCanLandInSeveralBuckets() {
        assertEquals(setOf(SPORTS, NEWS), buckets("Sports talk; News"))
        assertEquals(setOf(KIDS), buckets("Children, Animated"))
        assertEquals(setOf(MOVIES, KIDS), buckets("Movie | Animated"))
    }

    @Test
    fun schedulesDirectProgramIdPrefixes() {
        assertEquals(setOf(MOVIES), buckets("Action", programId = "MV000111220000"))
        assertEquals(setOf(SPORTS), buckets("", programId = "SP012345670123"))
        assertEquals(emptySet<GuideBucket>(), buckets("Drama", programId = "EP012345670012"))
        assertEquals(emptySet<GuideBucket>(), buckets("", programId = "MVP"))
    }

    @Test
    fun kidsMovieIsInBothBuckets() {
        assertEquals(setOf(KIDS, MOVIES), buckets("Children", programId = "MV000111220000"))
    }

    @Test
    fun isNewAddsNewBucket() {
        assertEquals(setOf(NEW), buckets("Sitcom", isNew = true))
        assertEquals(setOf(NEW, SPORTS), buckets("Sports event", isNew = true))
        assertEquals(emptySet<GuideBucket>(), buckets("Sitcom", isNew = false))
    }

    @Test
    fun matureRatingsKeepProgramOutOfKids() {
        assertEquals(emptySet<GuideBucket>(), buckets("Animated", rating = "TV-14"))
        assertEquals(emptySet<GuideBucket>(), buckets("Animated", rating = "TV-MA"))
        assertEquals(emptySet<GuideBucket>(), buckets("Animated", rating = "R"))
        assertEquals(setOf(KIDS), buckets("Animated", rating = "TV-PG"))
        assertEquals(setOf(KIDS), buckets("Children", rating = "TV-Y7"))
    }

    // ── Matching ────────────────────────────────────────────────────────────

    @Test
    fun allMatchesEverything() {
        assertTrue(GuideFilter.ALL.matches(prog()))
    }

    @Test
    fun bucketMatchesOnlyItsPrograms() {
        assertTrue(GuideFilter.SPORTS.matches(prog("Football")))
        assertFalse(GuideFilter.MOVIES.matches(prog("Football")))
        assertTrue(GuideFilter.NEW.matches(prog(isNew = true)))
    }

    private fun classify(programs: List<GuideProgram>) = programs.map { GuideFilter.buckets(it) }

    @Test
    fun channelMatchesWithinWindow() {
        val from = t0
        val to = at(4.0)
        assertTrue(GuideFilter.ALL.matches(emptyList(), emptyList(), from, to))
        assertFalse(GuideFilter.SPORTS.matches(emptyList(), emptyList(), from, to))
        val programs = listOf(prog("News"), prog("Football", start = 2.0, hours = 3.0))
        val b = classify(programs)
        assertTrue(GuideFilter.SPORTS.matches(programs, b, from, to))
        assertTrue(GuideFilter.NEWS.matches(programs, b, from, to))
        assertFalse(GuideFilter.MOVIES.matches(programs, b, from, to))
    }

    @Test
    fun channelIgnoresMatchesOutsideWindow() {
        val from = t0
        val to = at(4.0)
        val before = prog("Golf", start = -2.0, hours = 2.0)
        val after = prog("Golf", start = 4.0)
        val outside = listOf(before, after)
        assertFalse(GuideFilter.SPORTS.matches(outside, classify(outside), from, to))
        val running = listOf(prog("Golf", start = -1.0, hours = 1.5))
        assertTrue(GuideFilter.SPORTS.matches(running, classify(running), from, to))
    }

    @Test
    fun firstMatchIsEarliestInWindow() {
        val late = prog("Golf", start = 3.0)
        val early = prog("Football", start = 1.0)
        val ended = prog("Golf", start = -2.0)
        val programs = listOf(late, ended, prog("News"), early)
        assertEquals(early, GuideFilter.SPORTS.firstMatch(programs, classify(programs), t0, at(4.0)))
        val two = listOf(late, early)
        assertNull(GuideFilter.MOVIES.firstMatch(two, classify(two), t0, at(4.0)))
    }

    @Test
    fun rowHighlight() {
        val news = prog("News")
        val drama = prog("Drama", start = 1.0)
        val golf = prog("Golf", start = 2.0)
        val nowNext = GuideLogic.NowNext(now = news, next = drama)
        val programs = listOf(news, drama, golf)
        val b = classify(programs)
        assertEquals(
            GuideFilter.RowHighlight(nowMatches = true, nextMatches = true, later = null),
            GuideFilter.ALL.highlight(nowNext, programs, b, t0, at(4.0)),
        )
        assertEquals(
            GuideFilter.RowHighlight(nowMatches = true, nextMatches = false, later = null),
            GuideFilter.NEWS.highlight(nowNext, programs, b, t0, at(4.0)),
        )
        assertEquals(
            GuideFilter.RowHighlight(nowMatches = false, nextMatches = false, later = golf),
            GuideFilter.SPORTS.highlight(nowNext, programs, b, t0, at(4.0)),
        )
    }

    // ── Classified once per load ────────────────────────────────────────────

    @Test
    fun filteringReadsThePrecomputedBuckets() {
        // Buckets that contradict the categories: filtering must use them,
        // not classify the programs again.
        val news = prog("News")
        val drama = prog("Drama", start = 1.0)
        val programs = listOf(news, drama)
        val b = listOf(setOf(GuideBucket.SPORTS), setOf(GuideBucket.MOVIES))
        val nowNext = GuideLogic.NowNext(now = news, next = drama)

        assertTrue(GuideFilter.SPORTS.matches(programs, b, t0, at(4.0)))
        assertFalse(GuideFilter.NEWS.matches(programs, b, t0, at(4.0)))
        assertEquals(drama, GuideFilter.MOVIES.firstMatch(programs, b, t0, at(4.0)))
        assertEquals(
            GuideFilter.RowHighlight(nowMatches = true, nextMatches = false, later = null),
            GuideFilter.SPORTS.highlight(nowNext, programs, b, t0, at(4.0)),
        )
        assertEquals(
            GuideFilter.RowHighlight(nowMatches = false, nextMatches = false, later = null),
            GuideFilter.NEWS.highlight(nowNext, programs, b, t0, at(4.0)),
        )
    }

    @Test
    fun highlightFindsNowByStartWhenItCarriesANewerRecordingMark() {
        val golf = prog("Golf")
        val marked = golf.copy(recording = GuideRecordingMark(id = 9, state = "scheduled"))
        val h = GuideFilter.SPORTS.highlight(
            GuideLogic.NowNext(now = marked, next = null),
            listOf(golf),
            listOf(setOf(GuideBucket.SPORTS)),
            t0,
            at(4.0),
        )
        assertTrue(h.nowMatches)
    }

    // ── Copy and persistence ────────────────────────────────────────────────

    @Test
    fun laterLine() {
        val p = prog("Golf", start = 2.0).copy(title = "The Masters")
        val line = GuideFilter.laterLine(p, java.time.ZoneOffset.UTC, java.util.Locale.US)
        // 18:00Z + 2h; whitespace before AM/PM varies by JDK (U+202F on newer ones).
        assertTrue(line, Regex("^Later: The Masters · 8:00[\\s\\u00A0\\u202F]PM$").matches(line))
    }

    @Test
    fun chipOrderAndLabels() {
        assertEquals(listOf("All", "Sports", "Movies", "News", "Kids", "New"), GuideFilter.entries.map { it.label })
    }

    @Test
    fun emptyCopy() {
        assertEquals("No sports on in this time window", GuideFilter.SPORTS.emptyCopy)
        assertEquals("No movies on in this time window", GuideFilter.MOVIES.emptyCopy)
        assertEquals("No news on in this time window", GuideFilter.NEWS.emptyCopy)
        assertEquals("No kids' shows on in this time window", GuideFilter.KIDS.emptyCopy)
        assertEquals("No new episodes on in this time window", GuideFilter.NEW.emptyCopy)
    }

    @Test
    fun storedValuesRoundTripAndFallBackToAll() {
        GuideFilter.entries.forEach { assertEquals(it, GuideFilter.fromStored(it.stored)) }
        assertEquals("sports", GuideFilter.SPORTS.stored)
        assertEquals(GuideFilter.ALL, GuideFilter.fromStored("bogus"))
        assertEquals(GuideFilter.ALL, GuideFilter.fromStored(null))
    }
}
