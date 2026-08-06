package app.bowtie.core.player

import android.content.Context
import androidx.media3.common.MediaItem
import androidx.media3.common.PlaybackException
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
            }
        }

        player.addListener(playerListener)
        player.addAnalyticsListener(analytics)
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
            listener.onStalled()
            try {
                player.seekToDefaultPosition()
                player.prepare()
            } catch (_: Exception) {
                // Fall through to network retry if seek fails — treat as network path.
            }
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

        val NETWORK_BACKOFF_MS = longArrayOf(1_000L, 2_000L, 4_000L)

        private val NETWORK_ERROR_CODES = intArrayOf(
            PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_FAILED,
            PlaybackException.ERROR_CODE_IO_NETWORK_CONNECTION_TIMEOUT,
            PlaybackException.ERROR_CODE_IO_BAD_HTTP_STATUS,
            PlaybackException.ERROR_CODE_TIMEOUT,
        )
    }
}
