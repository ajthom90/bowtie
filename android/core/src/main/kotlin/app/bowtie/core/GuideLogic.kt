package app.bowtie.core

import java.time.Instant

/**
 * Pure guide helpers: now/next derivation and maxQuality → profile ladder filtering.
 * Mirrors iOS GuideLogic / web quality options.
 */
object GuideLogic {

    data class NowNext(
        val now: GuideProgram?,
        val next: GuideProgram?,
    )

    /** Quality ladder high → low. Empty maxQuality and unknown values unlock the full ladder. */
    val QUALITY_LADDER: List<String> = listOf("original", "high", "medium", "low")

    /**
     * Find the program airing at [at] and the soonest program after it.
     *
     * - **now**: `start <= at < stop` (stop is exclusive — a program ending at [at] is not current)
     * - **next**: earliest `start >= now.stop` when now is present; otherwise earliest `start > at`
     */
    fun nowNext(programs: List<GuideProgram>, at: Instant): NowNext {
        val now = programs.firstOrNull { p ->
            !at.isBefore(p.start) && at.isBefore(p.stop)
        }
        val next = if (now != null) {
            programs
                .filter { p -> !p.start.isBefore(now.stop) }
                .minByOrNull { it.start }
        } else {
            programs
                .filter { p -> p.start.isAfter(at) }
                .minByOrNull { it.start }
        }
        return NowNext(now = now, next = next)
    }

    /**
     * Profiles the user may pick given their `maxQuality` cap.
     *
     * - `""` → full ladder `[original, high, medium, low]`
     * - known rung → that rung and everything below (e.g. `"medium"` → `[medium, low]`)
     * - `"original"` → full ladder
     * - unknown → full ladder (defensive)
     */
    fun allowedProfiles(maxQuality: String): List<String> {
        if (maxQuality.isEmpty()) return QUALITY_LADDER
        val idx = QUALITY_LADDER.indexOf(maxQuality)
        if (idx < 0) return QUALITY_LADDER
        return QUALITY_LADDER.subList(idx, QUALITY_LADDER.size)
    }
}
