package app.bowtie.tv

import android.app.Application
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.ViewModel
import androidx.lifecycle.ViewModelProvider
import androidx.lifecycle.ViewModelStoreOwner
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewmodel.compose.viewModel
import androidx.tv.material3.Text
import app.bowtie.core.BowtieClient
import app.bowtie.core.Caps
import app.bowtie.core.Channel
import app.bowtie.core.ClientCaps
import app.bowtie.core.EncryptedTokenStore
import app.bowtie.core.TokenStore
import app.bowtie.core.User
import app.bowtie.core.vm.AppViewModel
import app.bowtie.core.vm.ChannelListViewModel
import app.bowtie.core.vm.PlayerViewModel
import app.bowtie.tv.ui.ChannelRailScreen
import app.bowtie.tv.ui.ConnectScreen
import app.bowtie.tv.ui.LoginScreen
import app.bowtie.tv.ui.SettingsScreen
import app.bowtie.tv.ui.TvPlayerScreen
import okhttp3.HttpUrl

/** In-app routes once [AppViewModel.Phase.Ready]. */
private sealed class ReadyRoute {
    data object Channels : ReadyRoute()
    data object Settings : ReadyRoute()
    data class Player(val channel: Channel, val nowTitle: String?) : ReadyRoute()
}

/**
 * Root navigation driven by [AppViewModel.phase].
 * Mirrors phone [app.bowtie.BowtieRoot]; factories stay in this module.
 */
@Composable
fun TvRoot(
    appViewModel: AppViewModel,
    modifier: Modifier = Modifier,
) {
    val phase by appViewModel.phase.collectAsStateWithLifecycle()

    when (val p = phase) {
        is AppViewModel.Phase.Connect -> {
            ConnectScreen(appViewModel = appViewModel, modifier = modifier)
        }
        is AppViewModel.Phase.Checking -> {
            LaunchedEffect(Unit) { appViewModel.start() }
            Box(
                modifier = modifier
                    .fillMaxSize()
                    .background(BowtieColors.bg),
                contentAlignment = Alignment.Center,
            ) {
                Text(
                    text = "Loading…",
                    style = BowtieType.body,
                    color = BowtieColors.dim,
                )
            }
        }
        is AppViewModel.Phase.Login -> {
            LoginScreen(appViewModel = appViewModel, modifier = modifier)
        }
        is AppViewModel.Phase.Ready -> {
            ReadyShell(
                appViewModel = appViewModel,
                user = p.user,
                modifier = modifier,
            )
        }
    }
}

@Composable
private fun ReadyShell(
    appViewModel: AppViewModel,
    user: User,
    modifier: Modifier = Modifier,
) {
    val client = appViewModel.client
    if (client == null) {
        // Defensive — should not happen in Ready.
        Box(
            modifier = modifier
                .fillMaxSize()
                .background(BowtieColors.bg),
        )
        return
    }

    val context = LocalContext.current
    val caps = remember(client.server) { Caps.current(context) }
    val owner = context as ViewModelStoreOwner
    val factory = remember(client, caps) {
        ReadyViewModelFactory(client = client, caps = caps)
    }

    val channelListViewModel: ChannelListViewModel = viewModel(
        viewModelStoreOwner = owner,
        key = "channelList-${client.server}",
        factory = factory,
    )
    val playerViewModel: PlayerViewModel = viewModel(
        viewModelStoreOwner = owner,
        key = "player-${client.server}",
        factory = factory,
    )

    var route by remember(client.server) { mutableStateOf<ReadyRoute>(ReadyRoute.Channels) }
    val listState by channelListViewModel.state.collectAsStateWithLifecycle()
    val channelList = when (val s = listState) {
        is ChannelListViewModel.LoadState.Loaded -> s.rows.map { it.channel }
        else -> emptyList()
    }

    when (val r = route) {
        is ReadyRoute.Channels -> {
            ChannelRailScreen(
                user = user,
                channelListViewModel = channelListViewModel,
                playerViewModel = playerViewModel,
                onOpenChannel = { channel ->
                    val nowTitle = when (val s = channelListViewModel.state.value) {
                        is ChannelListViewModel.LoadState.Loaded ->
                            s.rows.find { it.channel.id == channel.id }
                                ?.nowNext?.now?.title
                        else -> null
                    }
                    playerViewModel.play(channel)
                    route = ReadyRoute.Player(channel = channel, nowTitle = nowTitle)
                },
                onOpenSettings = { route = ReadyRoute.Settings },
                modifier = modifier,
            )
        }
        is ReadyRoute.Settings -> {
            SettingsScreen(
                appViewModel = appViewModel,
                user = user,
                onBack = { route = ReadyRoute.Channels },
                modifier = modifier,
            )
        }
        is ReadyRoute.Player -> {
            val zapList = channelList.ifEmpty { listOf(r.channel) }
            TvPlayerScreen(
                channel = r.channel,
                channels = zapList,
                playerViewModel = playerViewModel,
                server = client.server,
                maxQuality = user.maxQuality,
                nowTitle = r.nowTitle,
                onChannelChanged = { channel, nowTitle ->
                    route = ReadyRoute.Player(channel = channel, nowTitle = nowTitle)
                },
                onBack = { route = ReadyRoute.Channels },
                modifier = modifier,
            )
        }
    }
}

/**
 * Builds [AppViewModel] with [EncryptedTokenStore] and a real [BowtieClient] factory.
 * TV-module own copy — mirrors phone Nav.kt factory pattern.
 */
class AppViewModelFactory(
    private val application: Application,
    private val store: TokenStore = EncryptedTokenStore(application),
    private val clientFactory: (HttpUrl) -> BowtieClient = { url ->
        BowtieClient(server = url, store = store)
    },
) : ViewModelProvider.Factory {
    @Suppress("UNCHECKED_CAST")
    override fun <T : ViewModel> create(modelClass: Class<T>): T {
        if (modelClass.isAssignableFrom(AppViewModel::class.java)) {
            return AppViewModel(
                store = store,
                clientFactory = clientFactory,
            ) as T
        }
        error("Unknown ViewModel class: ${modelClass.name}")
    }
}

private class ReadyViewModelFactory(
    private val client: BowtieClient,
    private val caps: ClientCaps,
) : ViewModelProvider.Factory {
    @Suppress("UNCHECKED_CAST")
    override fun <T : ViewModel> create(modelClass: Class<T>): T {
        return when {
            modelClass.isAssignableFrom(ChannelListViewModel::class.java) ->
                ChannelListViewModel(client = client) as T
            modelClass.isAssignableFrom(PlayerViewModel::class.java) ->
                PlayerViewModel(client = client, caps = caps) as T
            else -> error("Unknown ViewModel class: ${modelClass.name}")
        }
    }
}
