package app.bowtie.core.vm

import app.bowtie.core.BowtieClient
import app.bowtie.core.RecordingLogic
import app.bowtie.core.RecordingRule
import kotlinx.coroutines.CancellationException
import java.time.Instant

/** Outcome of "Record series" (channel list or search). */
sealed class SeriesResult {
    /** The rule exists; [scheduled] upcoming airings were set to record now. */
    data class Scheduled(val rule: RecordingRule, val scheduled: Int) : SeriesResult() {
        /** "Scheduled N episodes". */
        val message: String get() = RecordingLogic.seriesScheduledMessage(scheduled)
    }

    data class Failed(val message: String) : SeriesResult()
}

/** Creates a series rule with the default options (this channel, new episodes, keep all). */
internal suspend fun createSeriesRule(
    client: BowtieClient,
    channelId: Long,
    programStart: Instant,
): SeriesResult = try {
    val created = client.createRecordingRule(channelId = channelId, programStart = programStart)
    SeriesResult.Scheduled(created.rule, created.scheduled)
} catch (e: CancellationException) {
    throw e
} catch (e: Exception) {
    SeriesResult.Failed(RecordingLogic.scheduleErrorMessage(e))
}
