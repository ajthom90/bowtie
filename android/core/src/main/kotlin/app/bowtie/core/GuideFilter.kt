package app.bowtie.core

import android.content.Context
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle
import java.util.Locale

/** A coarse program category a guide filter chip selects. */
enum class GuideBucket { SPORTS, MOVIES, NEWS, KIDS, NEW }

/**
 * Guide category chips: All · Sports · Movies · News · Kids · New.
 *
 * The guide carries one raw category string per program (XMLTV `<category>`,
 * Schedules Direct genre, SiliconDust free guide). These rules map it to
 * buckets; the same rules and test vectors live in the web
 * (`guideFilterModel.ts`) and BowtieKit (`GuideFilter.swift`).
 */
enum class GuideFilter(val label: String, val bucket: GuideBucket?) {
    ALL("All", null),
    SPORTS("Sports", GuideBucket.SPORTS),
    MOVIES("Movies", GuideBucket.MOVIES),
    NEWS("News", GuideBucket.NEWS),
    KIDS("Kids", GuideBucket.KIDS),
    NEW("New", GuideBucket.NEW),
    ;

    /** Value kept in preferences ("sports"). */
    val stored: String get() = name.lowercase()

    /** "No sports on in this time window" (empty for [ALL]). */
    val emptyCopy: String
        get() = when (this) {
            ALL -> ""
            SPORTS -> "No sports on in this time window"
            MOVIES -> "No movies on in this time window"
            NEWS -> "No news on in this time window"
            KIDS -> "No kids' shows on in this time window"
            NEW -> "No new episodes on in this time window"
        }

    /**
     * [ALL] matches everything; otherwise the program is in this bucket.
     * Classifies [program]; rows carry their programs' buckets already.
     */
    fun matches(program: GuideProgram): Boolean = matches(buckets(program))

    /** [ALL] matches everything; otherwise [buckets] holds this one. */
    fun matches(buckets: Set<GuideBucket>): Boolean = bucket == null || bucket in buckets

    /**
     * Some program overlapping `[from, to)` matches. [buckets] are
     * [programs]' buckets, same order, worked out once when the guide loaded.
     * [ALL] keeps every channel, even one without guide data.
     */
    fun matches(
        programs: List<GuideProgram>,
        buckets: List<Set<GuideBucket>>,
        from: Instant,
        to: Instant,
    ): Boolean =
        this == ALL || programs.indices.any { programs[it].overlaps(from, to) && matches(buckets[it]) }

    /** The earliest matching program overlapping `[from, to)` ([buckets] as for [matches]). */
    fun firstMatch(
        programs: List<GuideProgram>,
        buckets: List<Set<GuideBucket>>,
        from: Instant,
        to: Instant,
    ): GuideProgram? =
        programs.indices
            .filter { programs[it].overlaps(from, to) && matches(buckets[it]) }
            .map { programs[it] }
            .minByOrNull { it.start }

    /** How a now/next row reads: which lines stay bright, and a later match when neither does. */
    data class RowHighlight(
        val nowMatches: Boolean,
        val nextMatches: Boolean,
        val later: GuideProgram?,
    )

    /** Reads the precomputed [buckets] ([programs]' buckets, same order). */
    fun highlight(
        nowNext: GuideLogic.NowNext,
        programs: List<GuideProgram>,
        buckets: List<Set<GuideBucket>>,
        from: Instant,
        to: Instant,
    ): RowHighlight {
        if (this == ALL) return RowHighlight(nowMatches = true, nextMatches = true, later = null)
        val nowMatches = nowNext.now?.let { matches(bucketsOf(it, programs, buckets)) } ?: false
        val nextMatches = nowNext.next?.let { matches(bucketsOf(it, programs, buckets)) } ?: false
        val later = if (nowMatches || nextMatches) null else firstMatch(programs, buckets, from, to)
        return RowHighlight(nowMatches, nextMatches, later)
    }

    companion object {
        /** Sport words / phrases matched as whole words ("Sports talk", "College football"). */
        private val SPORTS_PHRASES = listOf(
            "sport", "sports", "motorsport", "motorsports", "esports",
            "football", "basketball", "baseball", "soccer", "hockey", "golf", "tennis",
            "boxing", "wrestling", "racing", "volleyball", "softball", "lacrosse", "rugby",
            "cricket", "bowling", "skiing", "snowboarding", "skating", "gymnastics",
            "swimming", "cycling", "track field", "athletics", "olympics",
            "mixed martial arts", "mma", "rodeo", "curling", "billiards", "darts",
            "surfing", "triathlon", "polo", "handball", "badminton", "equestrian",
        )

        /** The whole piece must be one of these ("Movie review" is not a movie). */
        private val MOVIE_PIECES = setOf(
            "movie", "movies", "film", "films", "feature film", "tv movie", "made for tv movie",
            "motion picture",
        )

        private val NEWS_PHRASES = listOf("news", "newsmagazine", "newscast", "weather")

        /**
         * "Family" is deliberately absent: family sitcoms and dramas are
         * general-audience prime time, not children's programming.
         */
        private val KIDS_PHRASES = listOf(
            "children", "childrens", "kids", "animated", "animation", "cartoon", "cartoons",
            "educational", "preschool",
        )

        /** Ratings that keep a program out of Kids (adult animation); letters/digits only. */
        private val MATURE_RATINGS = setOf("tv14", "tvma", "r", "nc17", "x")

        /** Schedules Direct program IDs: MV… movies, SP… sports events. */
        private val SD_MOVIE = Regex("^MV\\d{8}")
        private val SD_SPORTS = Regex("^SP\\d{8}")

        private val NON_WORD = Regex("[^a-z0-9]+")

        /** " word word " for whole-word phrase checks. */
        private fun words(piece: String): String {
            val ws = piece.lowercase().split(NON_WORD).filter { it.isNotEmpty() }
            return if (ws.isEmpty()) "" else " " + ws.joinToString(" ") + " "
        }

        private fun String.hasAny(phrases: List<String>): Boolean =
            phrases.any { contains(" $it ") }

        /** Every bucket [program] belongs to (may be several, or none). */
        fun buckets(program: GuideProgram): Set<GuideBucket> {
            val out = mutableSetOf<GuideBucket>()
            for (piece in program.category.split(',', ';', '|')) {
                val w = words(piece)
                if (w.isEmpty()) continue
                if (w.hasAny(SPORTS_PHRASES)) out += GuideBucket.SPORTS
                if (w.trim() in MOVIE_PIECES) out += GuideBucket.MOVIES
                if (w.hasAny(NEWS_PHRASES)) out += GuideBucket.NEWS
                if (w.hasAny(KIDS_PHRASES)) out += GuideBucket.KIDS
            }
            val pid = program.programId.orEmpty()
            if (SD_MOVIE.containsMatchIn(pid)) out += GuideBucket.MOVIES
            if (SD_SPORTS.containsMatchIn(pid)) out += GuideBucket.SPORTS
            if (program.rating.lowercase().replace(NON_WORD, "") in MATURE_RATINGS) out -= GuideBucket.KIDS
            if (program.isNew == true) out += GuideBucket.NEW
            return out
        }

        /**
         * [program]'s buckets from the precomputed list, found by start time
         * (now / next may carry a newer recording mark than [programs]).
         */
        private fun bucketsOf(
            program: GuideProgram,
            programs: List<GuideProgram>,
            buckets: List<Set<GuideBucket>>,
        ): Set<GuideBucket> {
            val i = programs.indexOfFirst { it.start == program.start }
            return if (i >= 0) buckets[i] else buckets(program)
        }

        /** "Later: Title · 8:00 PM" for a match that isn't on now or next. */
        fun laterLine(
            program: GuideProgram,
            zone: ZoneId = ZoneId.systemDefault(),
            locale: Locale = Locale.getDefault(),
        ): String {
            val time = DateTimeFormatter.ofLocalizedTime(FormatStyle.SHORT).withLocale(locale)
                .format(program.start.atZone(zone))
            return "Later: ${program.title} · $time"
        }

        /** Parse a stored value; unknown or missing → [ALL]. */
        fun fromStored(raw: String?): GuideFilter =
            entries.firstOrNull { it.stored == raw } ?: ALL

        private fun GuideProgram.overlaps(from: Instant, to: Instant): Boolean =
            start.isBefore(to) && stop.isAfter(from)
    }
}

/** Where the chosen guide chip is remembered. */
interface GuideFilterPrefs {
    var filter: GuideFilter

    /** Not persisted (tests, previews). */
    class InMemory(override var filter: GuideFilter = GuideFilter.ALL) : GuideFilterPrefs
}

/** Per-device guide chip (plain SharedPreferences; not secret). */
class GuideFilterStore(context: Context) : GuideFilterPrefs {
    private val prefs = context.getSharedPreferences("bowtie.guide", Context.MODE_PRIVATE)

    override var filter: GuideFilter
        get() = GuideFilter.fromStored(prefs.getString(KEY, null))
        set(value) {
            prefs.edit().putString(KEY, value.stored).apply()
        }

    private companion object {
        const val KEY = "filter"
    }
}
