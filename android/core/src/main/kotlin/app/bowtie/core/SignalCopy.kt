package app.bowtie.core

/**
 * Player copy for antenna reception, shared by the phone and TV apps.
 * Quality leads: strength can read 96% while the picture breaks up.
 */
object SignalCopy {
    /** Stats overlay line; null (hide it) when the reading is unknown. */
    fun statsLine(signal: SignalReading?): String? {
        if (signal == null) return null
        return "Signal quality ${pct(signal.quality)}% · strength ${pct(signal.strength)}% · " +
            "error-free ${pct(signal.symbolQuality)}%"
    }

    /** The note near the channel chrome; null (hide it) unless the server says weak. */
    fun weakNote(signal: SignalReading?): String? {
        if (signal?.weak != true) return null
        return "Weak signal (${pct(signal.quality)}%) — the picture may break up."
    }

    private fun pct(v: Int) = v.coerceIn(0, 100)
}
