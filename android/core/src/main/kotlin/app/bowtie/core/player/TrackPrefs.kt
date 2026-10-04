package app.bowtie.core.player

/** Remembered audio language / captions choice; null = never chosen (player default). */
data class TrackPrefs(val audioLanguage: String? = null, val captionsOn: Boolean? = null) {
    fun encode(): String = "a=${audioLanguage.orEmpty()};c=${captionsOn?.toString().orEmpty()}"

    companion object {
        fun decode(s: String?): TrackPrefs {
            if (s == null) return TrackPrefs()
            val m = s.split(';').mapNotNull {
                val kv = it.split('=', limit = 2)
                if (kv.size == 2) kv[0] to kv[1] else null
            }.toMap()
            if (!m.containsKey("a") || !m.containsKey("c")) return TrackPrefs()
            return TrackPrefs(
                audioLanguage = m["a"]?.ifEmpty { null },
                captionsOn = m["c"]?.toBooleanStrictOrNull(),
            )
        }
    }
}

data class AudioOption(val language: String?, val label: String)

fun audioLabel(language: String?, label: String?, index: Int): String =
    label?.takeIf { it.isNotBlank() } ?: language?.takeIf { it.isNotBlank() } ?: "Audio ${index + 1}"

/** Per-device storage for [TrackPrefs] (plain SharedPreferences; not secret). */
class TrackPrefsStore(context: android.content.Context) {
    private val prefs = context.getSharedPreferences("bowtie.player", android.content.Context.MODE_PRIVATE)

    fun load(): TrackPrefs = TrackPrefs.decode(prefs.getString(KEY, null))

    fun save(p: TrackPrefs) {
        prefs.edit().putString(KEY, p.encode()).apply()
    }

    private companion object {
        const val KEY = "trackPrefs"
    }
}

/** The audio choice after [current] (wraps); null when there is nothing to switch to. */
fun nextAudio(options: List<AudioOption>, current: String?): AudioOption? {
    if (options.size < 2) return null
    val i = options.indexOfFirst { it.language == current }
    return options[(i + 1).mod(options.size)]
}
