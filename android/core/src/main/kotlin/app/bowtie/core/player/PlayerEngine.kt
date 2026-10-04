package app.bowtie.core.player

import android.content.Context
import androidx.media3.common.C
import androidx.media3.common.MediaItem
import androidx.media3.common.PlaybackException
import androidx.media3.common.TrackSelectionOverride
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.datasource.HttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.analytics.AnalyticsListener
import androidx.media3.exoplayer.hls.HlsMediaSource
import androidx.media3.exoplayer.source.BehindLiveWindowException
import app.bowtie.core.ServerUrl
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import okhttp3.HttpUrl

/**
 * Shared Media3 HLS playback engine for phone and TV.
 *
 * Owns the unauthenticated data source, playlist resolve, 403 → auth callback,
 * behind-live-window recovery, and bounded network backoff.
 *
 * Global constraint: NEVER attach a Bearer OkHttp client / auth interceptor to
 * the media [DefaultHttpDataSource]. Stream auth is the playlist `?token=` query.
 *
 * [PlayerView] and Compose UI stay per-app; this class is the non-UI engine.
 */
@UnstableApi
class PlayerEngine(
    context: Context,
    private val scope: CoroutineScope,
    private val listener: Listener,
    userAgent: String = USER_AGENT,
    /** Remembered audio language / captions choice (per device). */
    private val loadPrefs: () -> TrackPrefs = { TrackPrefs() },
    private val savePrefs: (TrackPrefs) -> Unit = {},
) {
    /**
     * Host callbacks — map onto [app.bowtie.core.vm.PlayerViewModel] (or UI) as needed.
     */
    interface Listener {
        fun onAuthError()
        fun onStalled()
        fun onRecovered()
        fun onFailed(message: String)
        /** Fired when ExoPlayer reaches [Player.STATE_READY] (focus re-request hook). */
        fun onReady()
        fun onBitrate(bps: Int?)
        fun onDroppedFrames(total: Long)
        /**
         * Behind-live-window: player already sought to the live edge.
         * Host should show the out-of-window notice (spec B).
         */
        fun onJumpedToLive() {}

        /** Audio or caption tracks changed; refresh [audioOptions]/[hasCaptions]. */
        fun onTracksAvailable() {}
    }

    val player: ExoPlayer

    private var networkRetryJob: Job? = null
    private var networkAttempt: Int = 0
    private var droppedFramesTotal: Long = 0L

    init {
        val httpFactory = DefaultHttpDataSource.Factory()
            .setUserAgent(userAgent)
            .setAllowCrossProtocolRedirects(true)
        // No setDefaultRequestProperties for Authorization — stream auth is ?token=.
        val hlsFactory = HlsMediaSource.Factory(httpFactory)
        player = ExoPlayer.Builder(context)
            .setMediaSourceFactory(hlsFactory)
            .setSeekBackIncrementMs(SEEK_INCREMENT_MS)
            .setSeekForwardIncrementMs(SEEK_INCREMENT_MS)
            .build()
            .apply {
                playWhenReady = true
                setHandleAudioBecomingNoisy(true)
            }

        val analytics = object : AnalyticsListener {
            override fun onDroppedVideoFrames(
                eventTime: AnalyticsListener.EventTime,
                droppedFramesCount: Int,
                elapsedMs: Long,
            ) {
                droppedFramesTotal += droppedFramesCount.toLong()
                listener.onDroppedFrames(droppedFramesTotal)
            }

            override fun onVideoSizeChanged(
                eventTime: AnalyticsListener.EventTime,
                videoSize: androidx.media3.common.VideoSize,
            ) {
                emitBitrate()
            }
        }

        val playerListener = object : Player.Listener {
            override fun onPlayerError(error: PlaybackException) {
                handlePlayerError(error)
            }

            override fun onPlaybackStateChanged(playbackState: Int) {
                if (playbackState == Player.STATE_READY) {
                    networkAttempt = 0
                    networkRetryJob?.cancel()
                    networkRetryJob = null
                    emitBitrate()
                    listener.onRecovered()
                    listener.onReady()
                }
            }

            override fun onTracksChanged(tracks: androidx.media3.common.Tracks) {
                emitBitrate()
                listener.onTracksAvailable()
            }
        }

        player.addListener(playerListener)
        player.addAnalyticsListener(analytics)
        applyPrefs(loadPrefs())
    }

    private fun applyPrefs(p: TrackPrefs) {
        val b = player.trackSelectionParameters.buildUpon()
        p.audioLanguage?.let { b.setPreferredAudioLanguage(it) }
        when (p.captionsOn) {
            true -> b.setTrackTypeDisabled(C.TRACK_TYPE_TEXT, false).setPreferredTextLanguage("en")
            false -> b.setTrackTypeDisabled(C.TRACK_TYPE_TEXT, true)
            null -> {}
        }
        player.trackSelectionParameters = b.build()
    }

    /**
     * Playable audio choices, one per language/name (the AAC and 5.1 copies of
     * a language are one choice; Media3 picks the copy the device can play).
     */
    fun audioOptions(): List<AudioOption> {
        val seen = mutableSetOf<String>()
        val out = mutableListOf<AudioOption>()
        player.currentTracks.groups.forEachIndexed { gi, group ->
            if (group.type != C.TRACK_TYPE_AUDIO) return@forEachIndexed
            for (i in 0 until group.length) {
                if (!group.isTrackSupported(i)) continue
                val f = group.getTrackFormat(i)
                val label = audioLabel(f.language, f.label, out.size)
                // The AAC and 5.1 copies of one track share a label: one choice.
                if (seen.add(label)) {
                    out += AudioOption("$gi:$i", f.language, label)
                }
            }
        }
        return out
    }

    /** Id ("group:track") of the playing audio track, if any. */
    fun selectedAudioId(): String? {
        player.currentTracks.groups.forEachIndexed { gi, g ->
            if (g.type == C.TRACK_TYPE_AUDIO && g.isSelected) {
                for (i in 0 until g.length) {
                    if (g.isTrackSelected(i)) return "$gi:$i"
                }
            }
        }
        return null
    }

    /**
     * Play [option]'s track exactly (two tracks can share a language, e.g.
     * main and described audio) and remember its language for later items.
     */
    fun selectAudio(option: AudioOption) {
        val p = loadPrefs().copy(audioLanguage = option.language)
        savePrefs(p)
        val (gi, ti) = option.id.split(':').map { it.toInt() }
        val group = player.currentTracks.groups.getOrNull(gi) ?: return applyPrefs(p)
        player.trackSelectionParameters = player.trackSelectionParameters.buildUpon()
            .setPreferredAudioLanguage(option.language)
            .clearOverridesOfType(C.TRACK_TYPE_AUDIO)
            .addOverride(TrackSelectionOverride(group.mediaTrackGroup, ti))
            .build()
    }

    fun hasCaptions(): Boolean = player.currentTracks.groups.any { it.type == C.TRACK_TYPE_TEXT }

    fun captionsOn(): Boolean =
        player.currentTracks.groups.any { it.type == C.TRACK_TYPE_TEXT && it.isSelected }

    fun setCaptions(on: Boolean) {
        val p = loadPrefs().copy(captionsOn = on)
        savePrefs(p)
        applyPrefs(p)
    }

    /**
     * Resolve [playlistUrl] against [server] (preserving `?token=`) and start HLS.
     * Resets dropped-frame counters for a fresh session bind.
     */
    fun loadPlaylist(playlistUrl: String, server: HttpUrl) {
        networkAttempt = 0
        networkRetryJob?.cancel()
        networkRetryJob = null
        droppedFramesTotal = 0L
        listener.onDroppedFrames(0L)

        val resolved = ServerUrl.resolve(playlistUrl, server)
        val item = MediaItem.fromUri(resolved.toString())
        player.setMediaItem(item)
        player.prepare()
        player.playWhenReady = true
    }

    fun stopAndClear() {
        networkRetryJob?.cancel()
        networkRetryJob = null
        networkAttempt = 0
        player.stop()
        player.clearMediaItems()
    }

    fun stopDecoder() {
        player.stop()
    }

    fun togglePlayPause() {
        player.playWhenReady = !player.playWhenReady
    }

    /** Seek relative to current position by [deltaMs] (clamped by ExoPlayer live window). */
    fun seekBy(deltaMs: Long) {
        val target = (player.currentPosition + deltaMs).coerceAtLeast(0L)
        player.seekTo(target)
    }

    fun seekBack30() {
        player.seekBack()
    }

    fun seekForward30() {
        player.seekForward()
    }

    fun release() {
        networkRetryJob?.cancel()
        networkRetryJob = null
        player.release()
    }

    private fun emitBitrate() {
        listener.onBitrate(player.videoFormat?.bitrate?.takeIf { it > 0 })
    }

    private fun handlePlayerError(error: PlaybackException) {
        val cause = error.cause
        val responseCode =
            (cause as? HttpDataSource.InvalidResponseCodeException)?.responseCode

        if (responseCode == 403) {
            listener.onAuthError()
            return
        }

        val behindLive = cause is BehindLiveWindowException ||
            error.errorCode == PlaybackException.ERROR_CODE_BEHIND_LIVE_WINDOW

        if (behindLive) {
            // Spec B: clamp to live edge + notice — not a stall reconnect loop.
            try {
                player.seekToDefaultPosition()
                player.prepare()
                player.playWhenReady = true
            } catch (_: Exception) {
                // Best-effort; still surface the notice.
            }
            listener.onJumpedToLive()
            return
        }

        val isNetwork = error.errorCode in NETWORK_ERROR_CODES ||
            cause is HttpDataSource.HttpDataSourceException ||
            cause is java.io.IOException

        if (isNetwork) {
            listener.onStalled()
            networkRetryJob?.cancel()
            val attempt = networkAttempt
            if (attempt >= NETWORK_BACKOFF_MS.size) {
                networkAttempt = 0
                listener.onFailed(PLAYBACK_FAILED_COPY)
                return
            }
            val delayMs = NETWORK_BACKOFF_MS[attempt]
            networkAttempt = attempt + 1
            networkRetryJob = scope.launch {
                delay(delayMs)
                try {
                    player.seekToDefaultPosition()
                    player.prepare()
                    player.playWhenReady = true
                } catch (_: Exception) {
                    listener.onFailed(PLAYBACK_FAILED_COPY)
                }
            }
            return
        }

        listener.onFailed(error.message ?: PLAYBACK_FAILED_COPY)
    }

    companion object {
        const val USER_AGENT = "BowtieAndroid/0.1"
        const val PLAYBACK_FAILED_COPY =
            "Playback failed. Try again or pick a lower quality."
        /** Fire TV DPAD / controller seek step (spec D). */
        const val SEEK_INCREMENT_MS = 30_000L

        val NETWORK_BACKOFF_MS = longArrayOf(1_000L, 2_000L, 4_000L)

        private val NETWORK_ERROR_CODES = intArrayOf(
            PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_FAILED,
            PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_TIMEOUT,
            PlaybackException.ERROR_CODE_IO_BAD_HTTP_STATUS,
            PlaybackException.ERROR_CODE_TIMEOUT,
        )
    }
}
