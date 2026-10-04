package app.bowtie.core.player

import android.content.Context
import androidx.annotation.OptIn
import androidx.media3.common.MediaItem
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.DefaultHttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.hls.HlsMediaSource
import app.bowtie.core.ServerUrl
import okhttp3.HttpUrl

/**
 * ExoPlayer for a recording's HLS VOD (seekable, resumable), shared by phone and TV.
 *
 * Not [PlayerEngine]: its retry seeks to the default position, which for VOD
 * is the start. Same global constraint: the media data source never carries a
 * Bearer; the playlist's `?token=` authorizes every file.
 */
object VodPlayer {
    const val SEEK_BACK_MS = 10_000L
    const val SEEK_FORWARD_MS = 30_000L

    @OptIn(UnstableApi::class)
    fun create(context: Context): ExoPlayer {
        val http = DefaultHttpDataSource.Factory()
            .setUserAgent(PlayerEngine.USER_AGENT)
            .setAllowCrossProtocolRedirects(true)
        return ExoPlayer.Builder(context)
            .setMediaSourceFactory(HlsMediaSource.Factory(http))
            .setSeekBackIncrementMs(SEEK_BACK_MS)
            .setSeekForwardIncrementMs(SEEK_FORWARD_MS)
            .build()
            .apply { setHandleAudioBecomingNoisy(true) }
    }

    /** Resolve the server-relative [playlistUrl] (keeping `?token=`) and play from [startAtSec]. */
    fun load(player: ExoPlayer, playlistUrl: String, server: HttpUrl, startAtSec: Int) {
        val uri = ServerUrl.resolve(playlistUrl, server).toString()
        player.setMediaItem(MediaItem.fromUri(uri), startAtSec.coerceAtLeast(0) * 1000L)
        player.prepare()
        player.playWhenReady = true
    }
}
