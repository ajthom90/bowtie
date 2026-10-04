package app.bowtie.core.player

import android.content.Context

/** Per-device "Skip ads automatically" (off unless turned on); same file as [TrackPrefsStore]. */
class AutoSkipAdsStore(context: Context) {
    private val prefs = context.getSharedPreferences("bowtie.player", Context.MODE_PRIVATE)

    var enabled: Boolean
        get() = prefs.getBoolean(KEY, false)
        set(value) {
            prefs.edit().putBoolean(KEY, value).apply()
        }

    private companion object {
        const val KEY = "skipAdsAutomatically"
    }
}
