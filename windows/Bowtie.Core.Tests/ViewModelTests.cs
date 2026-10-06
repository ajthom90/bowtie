using Bowtie.Core.ViewModels;

namespace Bowtie.Core.Tests;

public class AppViewModelTests
{
    private static Func<Uri, BowtieClient> Factory(FakeServer server, ITokenStore store) =>
        uri => new BowtieClient(uri, store, server.Client());

    [Fact]
    public void Initial_phase_follows_the_store()
    {
        var server = new FakeServer();
        Assert.Equal(AppPhase.Connect, new AppViewModel(new InMemoryTokenStore(), Factory(server, new InMemoryTokenStore())).Phase);
        var loginStore = new InMemoryTokenStore("http://host:8400", null);
        Assert.Equal(AppPhase.Login, new AppViewModel(loginStore, Factory(server, loginStore)).Phase);
        var checkStore = new InMemoryTokenStore("http://host:8400", "r1");
        Assert.Equal(AppPhase.Checking, new AppViewModel(checkStore, Factory(server, checkStore)).Phase);
    }

    [Fact]
    public async Task Connect_validates_and_saves_the_server()
    {
        var server = new FakeServer().Json("GET", "/healthz", "ok");
        var store = new InMemoryTokenStore();
        var vm = new AppViewModel(store, Factory(server, store), server.Client());

        Assert.NotNull(await vm.ConnectAsync(""));
        var error = await vm.ConnectAsync("bowtie.test:8400");

        Assert.Null(error);
        Assert.Equal(AppPhase.Login, vm.Phase);
        Assert.Equal("http://bowtie.test:8400", store.LoadServer());
        Assert.Equal("http://bowtie.test:8400", vm.ServerDisplay);
    }

    [Fact]
    public async Task Connect_reports_an_unreachable_server()
    {
        var server = new FakeServer();
        var store = new InMemoryTokenStore();
        var vm = new AppViewModel(store, Factory(server, store), server.Client());

        Assert.NotNull(await vm.ConnectAsync("nothing.here"));
        Assert.Equal(AppPhase.Connect, vm.Phase);
        Assert.Null(store.LoadServer());
    }

    [Fact]
    public async Task Sign_in_then_out_then_change_server()
    {
        var server = new FakeServer()
            .Json("POST", "/api/v1/auth/login", FakeServer.TokenJson("a1", "r1"))
            .Json("POST", "/api/v1/auth/logout", "", 204);
        var store = new InMemoryTokenStore("http://bowtie.test:8400", null);
        var vm = new AppViewModel(store, Factory(server, store), server.Client());

        Assert.Null(await vm.SignInAsync("alice", "pw"));
        Assert.Equal(AppPhase.Ready, vm.Phase);
        Assert.Equal("alice", vm.User?.Username);

        await vm.SignOutAsync();
        Assert.Equal(AppPhase.Login, vm.Phase);
        Assert.Null(store.LoadRefreshToken());

        vm.ChangeServer();
        Assert.Equal(AppPhase.Connect, vm.Phase);
        Assert.Null(store.LoadServer());
    }

    [Fact]
    public async Task Wrong_password_message()
    {
        var server = new FakeServer().Json("POST", "/api/v1/auth/login", "{}", 401);
        var store = new InMemoryTokenStore("http://bowtie.test:8400", null);
        var vm = new AppViewModel(store, Factory(server, store), server.Client());

        Assert.Equal("Wrong username or password.", await vm.SignInAsync("alice", "x"));
        Assert.Equal(AppPhase.Login, vm.Phase);
    }

    [Fact]
    public async Task Start_signs_in_with_the_saved_token_or_falls_back_to_login()
    {
        var ok = new FakeServer().Json("POST", "/api/v1/auth/refresh", FakeServer.TokenJson("a2", "r2"));
        var store = new InMemoryTokenStore("http://bowtie.test:8400", "r1");
        var vm = new AppViewModel(store, Factory(ok, store), ok.Client());
        await vm.StartAsync();
        Assert.Equal(AppPhase.Ready, vm.Phase);

        var refused = new FakeServer().Json("POST", "/api/v1/auth/refresh", "{}", 401);
        var store2 = new InMemoryTokenStore("http://bowtie.test:8400", "r1");
        var vm2 = new AppViewModel(store2, Factory(refused, store2), refused.Client());
        await vm2.StartAsync();
        Assert.Equal(AppPhase.Login, vm2.Phase);
    }

    [Fact]
    public async Task Session_end_from_the_client_is_forwarded()
    {
        var server = new FakeServer()
            .Json("POST", "/api/v1/auth/login", FakeServer.TokenJson("a1", "r1"))
            .Json("GET", "/api/v1/channels", "", 401)
            .Json("POST", "/api/v1/auth/refresh", "", 401);
        var store = new InMemoryTokenStore("http://bowtie.test:8400", null);
        var vm = new AppViewModel(store, Factory(server, store), server.Client());
        await vm.SignInAsync("alice", "pw");
        var forwarded = 0;
        vm.SessionEnded += (_, _) => forwarded++;

        await Assert.ThrowsAsync<UnauthorizedException>(() => vm.Client!.ChannelsAsync());
        vm.OnSessionEnded();

        Assert.Equal(1, forwarded);
        Assert.Equal(AppPhase.Login, vm.Phase);
    }
}

public class ChannelListViewModelTests
{
    private static readonly DateTimeOffset Now = new(2026, 10, 4, 20, 10, 0, TimeSpan.Zero);

    private static (ChannelListViewModel Vm, FakeServer Server) Make(
        string channels, bool recents = true, Func<DateTimeOffset>? now = null)
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        server.Json("GET", "/api/v1/channels", channels);
        server.Json("GET", "/api/v1/guide", """[{"channelId":2,"guideNumber":"4.1","name":"B","logoUrl":"","programs":[{"start":"2026-10-04T20:00:00Z","stop":"2026-10-04T20:30:00Z","title":"Now","subtitle":"","description":"","category":""},{"start":"2026-10-04T20:30:00Z","stop":"2026-10-04T21:00:00Z","title":"Next","subtitle":"","description":"","category":""}]}]""");
        if (recents)
        {
            server.Json("GET", "/api/v1/me/recents", """[{"channelId":2,"guideNumber":"4.1","name":"B","logoUrl":"","watchedAt":"2026-10-04T19:00:00Z"}]""");
        }
        return (new ChannelListViewModel(client, now ?? (() => Now)), server);
    }

    private const string ThreeChannels =
        """[{"id":1,"guideNumber":"11.1","name":"A","logoUrl":"/logo/1.png","favorite":true},{"id":2,"guideNumber":"4.1","name":"B","logoUrl":"","favorite":false},{"id":3,"guideNumber":"9.1","name":"C","logoUrl":"","favorite":true}]""";

    [Fact]
    public async Task Refresh_orders_favorites_first_and_joins_now_next()
    {
        var (vm, server) = Make(ThreeChannels);

        await vm.RefreshAsync();

        Assert.Equal(ChannelListStatus.Loaded, vm.Status);
        Assert.True(vm.FavoritesSupported);
        Assert.Equal(new long[] { 3, 1, 2 }, vm.Rows.Select(r => r.Id));
        Assert.Equal(new long[] { 3, 1 }, vm.Favorites.Select(r => r.Id));
        var b = vm.Rows.Single(r => r.Id == 2);
        Assert.Equal("Now", b.NowNext.Now?.Title);
        Assert.Equal("Next", b.NowNext.Next?.Title);
        Assert.Equal(2, Assert.Single(vm.Recents).ChannelId);
        Assert.Equal("/api/v1/me/recents?limit=8", server.For("GET", "/api/v1/me/recents").Single().PathAndQuery);
        Assert.Equal("http://bowtie.test:8400/logo/1.png", vm.LogoUri("/logo/1.png")!.AbsoluteUri);
    }

    [Fact]
    public async Task Older_servers_hide_favorites_and_recents()
    {
        var (vm, server) = Make("""[{"id":1,"guideNumber":"2.1","name":"A","logoUrl":""}]""");

        await vm.RefreshAsync();

        Assert.False(vm.FavoritesSupported);
        Assert.Empty(vm.Recents);
        Assert.Empty(server.For("GET", "/api/v1/me/recents"));
    }

    [Fact]
    public async Task Empty_and_failed_states()
    {
        var (empty, _) = Make("[]");
        await empty.RefreshAsync();
        Assert.Equal(ChannelListStatus.Empty, empty.Status);

        var (failed, server) = Make("[]");
        server.Json("GET", "/api/v1/channels", """{"error":"db down"}""", 500);
        await failed.RefreshAsync();
        Assert.Equal(ChannelListStatus.Failed, failed.Status);
        Assert.Equal("db down", failed.Error);
    }

    [Fact]
    public async Task A_missing_guide_still_shows_channels()
    {
        var (vm, server) = Make(ThreeChannels);
        server.Json("GET", "/api/v1/guide", """{"error":"boom"}""", 500);

        await vm.RefreshAsync();

        Assert.Equal(ChannelListStatus.Loaded, vm.Status);
        Assert.All(vm.Rows, r => Assert.Null(r.NowNext.Now));
    }

    [Fact]
    public async Task Toggle_favorite_is_optimistic()
    {
        var (vm, server) = Make(ThreeChannels);
        server.Json("PUT", "/api/v1/me/favorites/2", "", 204);
        await vm.RefreshAsync();

        await vm.ToggleFavoriteAsync(2);

        Assert.Equal(new long[] { 2, 3, 1 }, vm.Rows.Select(r => r.Id));
        Assert.Single(server.For("PUT", "/api/v1/me/favorites/2"));
        Assert.Null(vm.Message);
    }

    [Fact]
    public async Task Refused_toggle_reverts_with_a_message()
    {
        var (vm, server) = Make(ThreeChannels);
        server.Json("DELETE", "/api/v1/me/favorites/1", """{"error":"nope"}""", 500);
        await vm.RefreshAsync();

        await vm.ToggleFavoriteAsync(1);

        Assert.Equal(new long[] { 3, 1, 2 }, vm.Rows.Select(r => r.Id));
        Assert.Equal("Couldn't update favorites: nope", vm.Message);
        vm.ConsumeMessage();
        Assert.Null(vm.Message);
    }

    [Fact]
    public async Task Refresh_if_stale_only_reloads_after_five_minutes()
    {
        var now = Now;
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        server.Json("GET", "/api/v1/channels", "[]").Json("GET", "/api/v1/guide", "[]");
        var vm = new ChannelListViewModel(client, () => now);

        await vm.RefreshIfStaleAsync();
        now = now.AddMinutes(4);
        await vm.RefreshIfStaleAsync();
        Assert.Single(server.For("GET", "/api/v1/channels"));
        now = now.AddMinutes(1);
        await vm.RefreshIfStaleAsync();
        Assert.Equal(2, server.For("GET", "/api/v1/channels").Count());
    }

    private const string OneBusy =
        """[{"id":1,"guideNumber":"11.1","name":"A","logoUrl":"","favorite":true,"watchable":false},{"id":2,"guideNumber":"4.1","name":"B","logoUrl":"","favorite":false,"watchable":true},{"id":3,"guideNumber":"9.1","name":"C","logoUrl":"","favorite":true}]""";

    private const string AllBusy =
        """[{"id":1,"guideNumber":"11.1","name":"A","logoUrl":"","favorite":true,"watchable":false},{"id":2,"guideNumber":"4.1","name":"B","logoUrl":"","favorite":false,"watchable":false}]""";

    [Fact]
    public async Task Every_channel_watchable_shows_everything_and_no_note()
    {
        var (vm, _) = Make(ThreeChannels);
        await vm.RefreshAsync();

        Assert.Equal(new long[] { 3, 1, 2 }, vm.VisibleRows(vm.Rows, Now).Select(r => r.Id));
        Assert.Null(vm.TunersNote);
        Assert.False(vm.NoneWatchable);
        Assert.Single(vm.VisibleRecents);
    }

    [Fact]
    public async Task Busy_channels_are_hidden_with_a_note()
    {
        var (vm, _) = Make(OneBusy);
        await vm.RefreshAsync();

        Assert.Equal(new long[] { 3, 2 }, vm.VisibleRows(vm.Rows, Now).Select(r => r.Id));
        Assert.Equal("All tuners are in use — showing channels you can join.", vm.TunersNote);
        Assert.False(vm.NoneWatchable);
        // All rows stay in Rows (for zapping and lookups); only the visible list shrinks.
        Assert.Equal(3, vm.Rows.Count);
    }

    [Fact]
    public async Task No_watchable_channel_says_try_again_later()
    {
        var (vm, _) = Make(AllBusy);
        await vm.RefreshAsync();

        Assert.Empty(vm.VisibleRows(vm.Rows, Now));
        Assert.Equal("All tuners are in use. Try again in a few minutes.", vm.TunersNote);
        Assert.True(vm.NoneWatchable);
        Assert.Empty(vm.VisibleRecents);
    }

    [Fact]
    public async Task Recent_hides_busy_channels()
    {
        var (vm, server) = Make(OneBusy);
        server.Json("GET", "/api/v1/me/recents",
            """[{"channelId":1,"guideNumber":"11.1","name":"A","logoUrl":"","watchedAt":"2026-10-04T19:00:00Z"},{"channelId":2,"guideNumber":"4.1","name":"B","logoUrl":"","watchedAt":"2026-10-04T18:00:00Z"},{"channelId":99,"guideNumber":"1.1","name":"Gone","logoUrl":"","watchedAt":"2026-10-04T17:00:00Z"}]""");
        await vm.RefreshAsync();

        Assert.Equal(new long[] { 2, 99 }, vm.VisibleRecents.Select(r => r.ChannelId));
    }

    [Fact]
    public async Task Busy_filter_applies_on_top_of_the_category_filter()
    {
        // A and B both have sports on now, but A's tuners are busy; C has no guide data.
        var (vm, server) = Make(OneBusy);
        server.Json("GET", "/api/v1/guide", """[{"channelId":1,"guideNumber":"11.1","name":"A","logoUrl":"","programs":[{"start":"2026-10-04T20:00:00Z","stop":"2026-10-04T21:00:00Z","title":"Game","category":"Sports"}]},{"channelId":2,"guideNumber":"4.1","name":"B","logoUrl":"","programs":[{"start":"2026-10-04T20:00:00Z","stop":"2026-10-04T21:00:00Z","title":"Match","category":"Sports"}]}]""");
        await vm.RefreshAsync();

        vm.SetFilter(GuideFilter.Sports);

        Assert.Equal(new long[] { 2 }, vm.VisibleRows(vm.Rows, Now).Select(r => r.Id));
    }

    [Fact]
    public async Task Poll_rechecks_channels_without_reloading_the_guide()
    {
        var now = Now;
        var (vm, server) = Make(AllBusy, now: () => now);
        await vm.PollAsync();
        Assert.True(vm.NoneWatchable);
        Assert.Single(server.For("GET", "/api/v1/guide"));

        // A tuner frees up: the next 30-second check brings the channel back.
        server.Json("GET", "/api/v1/channels", AllBusy.Replace("\"favorite\":false,\"watchable\":false", "\"favorite\":false,\"watchable\":true"));
        now = now.AddSeconds(30);
        await vm.PollAsync();

        Assert.Equal(new long[] { 2 }, vm.VisibleRows(vm.Rows, now).Select(r => r.Id));
        Assert.Equal("Now", vm.Rows.Single(r => r.Id == 2).NowNext.Now?.Title);
        Assert.Equal("All tuners are in use — showing channels you can join.", vm.TunersNote);
        Assert.Equal(2, server.For("GET", "/api/v1/channels").Count());
        Assert.Single(server.For("GET", "/api/v1/guide"));

        // Five minutes after the full load, a poll is a full reload again.
        now = Now.AddMinutes(5);
        await vm.PollAsync();
        Assert.Equal(2, server.For("GET", "/api/v1/guide").Count());
    }

    [Fact]
    public async Task A_failed_recheck_keeps_the_list()
    {
        var now = Now;
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        server.Json("GET", "/api/v1/channels", OneBusy).Json("GET", "/api/v1/guide", "[]");
        var vm = new ChannelListViewModel(client, () => now);
        await vm.PollAsync();

        server.Json("GET", "/api/v1/channels", """{"error":"db down"}""", 500);
        now = now.AddSeconds(30);
        await vm.PollAsync();

        Assert.Equal(ChannelListStatus.Loaded, vm.Status);
        Assert.Equal(3, vm.Rows.Count);
        Assert.Equal("All tuners are in use — showing channels you can join.", vm.TunersNote);
    }

    [Fact]
    public async Task Channel_for_recent_prefers_the_listed_channel()
    {
        var (vm, _) = Make(ThreeChannels);
        await vm.RefreshAsync();

        Assert.Equal("/logo/1.png", vm.ChannelFor(new RecentChannel { ChannelId = 1, Name = "x" }).LogoUrl);
        Assert.Equal("Gone", vm.ChannelFor(new RecentChannel { ChannelId = 99, Name = "Gone" }).Name);
    }
}

public class PlayerViewModelTests
{
    private static readonly Channel News = new() { Id = 7, GuideNumber = "4.1", Name = "News" };
    private static readonly TimeSpan Beat = TimeSpan.FromSeconds(15);

    /// <summary>Debounce completes at once; heartbeat delays wait for <see cref="Tick"/>.</summary>
    private sealed class Clock
    {
        private readonly SemaphoreSlim _ticks = new(0);

        public Task Delay(TimeSpan ts, CancellationToken ct) =>
            ts == Beat ? _ticks.WaitAsync(ct) : Task.CompletedTask;

        public void Tick() => _ticks.Release();
    }

    private static (PlayerViewModel Vm, FakeServer Server, Clock Clock) Make(string maxQuality = "")
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        var clock = new Clock();
        var caps = Caps.Detect(false, false, false, 1080);
        var vm = new PlayerViewModel(client, caps, maxQuality, TimeSpan.Zero, Beat, clock.Delay);
        return (vm, server, clock);
    }

    private static async Task WaitFor(Func<bool> condition)
    {
        for (var i = 0; i < 200 && !condition(); i++) await Task.Delay(10);
        Assert.True(condition());
    }

    [Fact]
    public async Task Play_creates_a_session_and_exposes_the_playlist()
    {
        var (vm, server, _) = Make();
        server.Json("POST", "/api/v1/sessions", FakeServer.SessionJson("v1", "tk"));

        vm.Play(News);
        await vm.ReplaceTask;

        var playing = Assert.IsType<PlayerState.Playing>(vm.State);
        Assert.Equal("v1", playing.Session.ViewerId);
        Assert.Equal("http://bowtie.test:8400/api/v1/stream/v1/index.m3u8?token=tk", vm.PlaylistUri!.AbsoluteUri);
        Assert.Contains("\"profile\":\"\"", server.For("POST", "/api/v1/sessions").Single().Body);
    }

    [Fact]
    public async Task Heartbeats_use_the_stream_token_and_stop_with_the_session()
    {
        var (vm, server, clock) = Make();
        server.Json("POST", "/api/v1/sessions", FakeServer.SessionJson("v1", "tk"));
        server.Json("POST", "/api/v1/sessions/v1/heartbeat", "", 204);
        server.Json("DELETE", "/api/v1/sessions/v1", "", 204);
        vm.Play(News);
        await vm.ReplaceTask;

        clock.Tick();
        await WaitFor(() => server.For("POST", "/api/v1/sessions/v1/heartbeat").Count() == 1);
        clock.Tick();
        await WaitFor(() => server.For("POST", "/api/v1/sessions/v1/heartbeat").Count() == 2);

        var hb = server.For("POST", "/api/v1/sessions/v1/heartbeat").First();
        Assert.Null(hb.Authorization);
        Assert.EndsWith("?token=tk", hb.PathAndQuery);

        vm.Stop();
        await vm.StopTask;
        await vm.HeartbeatTask;
        Assert.IsType<PlayerState.Idle>(vm.State);
        Assert.Single(server.For("DELETE", "/api/v1/sessions/v1"));
    }

    [Fact]
    public async Task Stalled_sessions_keep_beating_and_recover()
    {
        var (vm, server, clock) = Make();
        server.Json("POST", "/api/v1/sessions", FakeServer.SessionJson("v1"));
        server.Json("POST", "/api/v1/sessions/v1/heartbeat", "", 204);
        vm.Play(News);
        await vm.ReplaceTask;

        vm.OnPlaybackStalled();
        Assert.IsType<PlayerState.Stalled>(vm.State);
        clock.Tick();
        await WaitFor(() => server.For("POST", "/api/v1/sessions/v1/heartbeat").Any());
        vm.OnPlaybackRecovered();
        Assert.IsType<PlayerState.Playing>(vm.State);
        vm.Dispose();
    }

    private const string Weak = """{"signal":{"strength":96,"quality":46,"symbolQuality":0,"weak":true}}""";
    private const string Fine = """{"signal":{"strength":100,"quality":100,"symbolQuality":100,"weak":false}}""";

    [Fact]
    public async Task Weak_signal_note_follows_the_latest_heartbeat()
    {
        var (vm, server, clock) = Make();
        server.Json("POST", "/api/v1/sessions", FakeServer.SessionJson("v1"));
        var answers = new Queue<HttpResponseMessage>(new[]
        {
            FakeServer.Response(200, Weak),
            FakeServer.Response(200, Fine),
            FakeServer.Response(200, Weak),
            FakeServer.Response(200, """{"signal":null}"""),
            FakeServer.Response(200, Weak),
            FakeServer.Response(204),
        });
        server.On("POST", "/api/v1/sessions/v1/heartbeat", _ => answers.Dequeue());
        vm.Play(News);
        await vm.ReplaceTask;
        Assert.False(vm.ShowWeakSignalNote);

        var expected = new[] { true, false, true, false, true, false };
        for (var i = 0; i < expected.Length; i++)
        {
            clock.Tick();
            await WaitFor(() => vm.HeartbeatCount == i + 1);
            Assert.Equal(expected[i], vm.ShowWeakSignalNote);
        }
        vm.Dispose();
    }

    [Fact]
    public async Task Weak_signal_note_raises_property_changed()
    {
        var (vm, server, clock) = Make();
        server.Json("POST", "/api/v1/sessions", FakeServer.SessionJson("v1"));
        server.Json("POST", "/api/v1/sessions/v1/heartbeat", Weak);
        var changed = new List<string?>();
        vm.PropertyChanged += (_, e) => changed.Add(e.PropertyName);
        vm.Play(News);
        await vm.ReplaceTask;

        clock.Tick();
        await WaitFor(() => vm.ShowWeakSignalNote);

        Assert.Contains(nameof(PlayerViewModel.ShowWeakSignalNote), changed);
        Assert.Equal("Weak signal — the picture may break up.", PlayerViewModel.WeakSignalNote);
        vm.Dispose();
    }

    [Fact]
    public async Task Weak_signal_note_clears_on_zap_failure_and_stop()
    {
        var (vm, server, clock) = Make();
        var n = 0;
        server.On("POST", "/api/v1/sessions", _ => FakeServer.Response(200, FakeServer.SessionJson($"v{++n}")));
        server.Json("POST", "/api/v1/sessions/v1/heartbeat", Weak);
        server.Json("POST", "/api/v1/sessions/v2/heartbeat", Weak);
        vm.Play(News);
        await vm.ReplaceTask;
        clock.Tick();
        await WaitFor(() => vm.ShowWeakSignalNote);

        // A new channel starts without a reading.
        vm.Play(News with { Id = 8, Name = "Sports" });
        await vm.ReplaceTask;
        Assert.False(vm.ShowWeakSignalNote);

        clock.Tick();
        await WaitFor(() => vm.ShowWeakSignalNote);
        vm.OnPlaybackFailed(ErrorText.StreamStopped);
        Assert.False(vm.ShowWeakSignalNote);

        vm.Stop();
        Assert.False(vm.ShowWeakSignalNote);
        Assert.Null(vm.Signal);
    }

    [Fact]
    public async Task Unreachable_server_is_plain_words()
    {
        var (vm, server, _) = Make();
        server.On("POST", "/api/v1/sessions", _ => throw new HttpRequestException("No such host is known."));
        vm.Play(News);
        await vm.ReplaceTask;

        Assert.Equal("Can't reach your Bowtie server. Check your connection and try again.",
            Assert.IsType<PlayerState.Failed>(vm.State).Message);
    }

    [Fact]
    public async Task Negotiation_failure_retries_once_on_auto()
    {
        var (vm, server, _) = Make();
        var calls = 0;
        server.On("POST", "/api/v1/sessions", r =>
            ++calls == 1
                ? FakeServer.Response(422, """{"error":"no codec"}""")
                : FakeServer.Response(200, FakeServer.SessionJson("v2")));
        vm.SetProfile("high");
        vm.Play(News);
        await vm.ReplaceTask;

        Assert.IsType<PlayerState.Playing>(vm.State);
        Assert.Equal("", vm.SelectedProfile);
        var bodies = server.For("POST", "/api/v1/sessions").Select(r => r.Body).ToList();
        Assert.Contains("\"profile\":\"high\"", bodies[0]);
        Assert.Contains("\"profile\":\"\"", bodies[1]);
        vm.Dispose();
    }

    [Fact]
    public async Task Second_negotiation_failure_is_device_cant_play()
    {
        var (vm, server, _) = Make();
        server.Json("POST", "/api/v1/sessions", """{"error":"no codec"}""", 422);
        vm.Play(News);
        await vm.ReplaceTask;

        Assert.Equal(PlayerViewModel.DeviceCantPlayMessage, Assert.IsType<PlayerState.Failed>(vm.State).Message);
        Assert.Equal(2, server.For("POST", "/api/v1/sessions").Count());
    }

    [Fact]
    public async Task Not_found_marks_channels_stale()
    {
        var (vm, server, _) = Make();
        server.Json("POST", "/api/v1/sessions", """{"error":"not found"}""", 404);
        vm.Play(News);
        await vm.ReplaceTask;

        Assert.True(vm.ChannelsStale);
        Assert.Equal(PlayerViewModel.ChannelNotFoundMessage, Assert.IsType<PlayerState.Failed>(vm.State).Message);
        vm.ClearChannelsStale();
        Assert.False(vm.ChannelsStale);
    }

    [Fact]
    public async Task Tuners_busy_and_account_limits()
    {
        var (vm, server, _) = Make();
        server.Json("POST", "/api/v1/sessions", """{"error":"all tuners in use","sessions":[],"otherInUse":2}""", 503);
        vm.Play(News);
        await vm.ReplaceTask;
        Assert.Equal(2, Assert.IsType<PlayerState.TunersBusy>(vm.State).OtherInUse);

        server.Json("POST", "/api/v1/sessions", """{"error":"Your account can use 1 stream at a time.","code":"user_limit","kind":"streams","limit":1}""", 429);
        vm.Retry();
        await vm.ReplaceTask;
        Assert.Equal("Your account can use 1 stream at a time.", Assert.IsType<PlayerState.Failed>(vm.State).Message);
    }

    [Fact]
    public async Task Zapping_deletes_the_old_viewer_before_creating_the_new_one()
    {
        var (vm, server, _) = Make();
        var n = 0;
        server.On("POST", "/api/v1/sessions", _ => FakeServer.Response(200, FakeServer.SessionJson($"v{++n}")));
        server.Json("DELETE", "/api/v1/sessions/v1", "", 204);
        vm.Play(News);
        await vm.ReplaceTask;

        vm.Play(News with { Id = 8, Name = "Sports" });
        await vm.ReplaceTask;

        Assert.Equal("v2", Assert.IsType<PlayerState.Playing>(vm.State).Session.ViewerId);
        var order = server.Requests.Select(r => $"{r.Method} {r.PathAndQuery}").ToList();
        Assert.True(order.IndexOf("DELETE /api/v1/sessions/v1") < order.LastIndexOf("POST /api/v1/sessions"));
        Assert.Equal("Sports", vm.CurrentChannel?.Name);
        vm.Dispose();
    }

    [Fact]
    public async Task Stop_cancels_an_in_flight_create_and_it_never_plays()
    {
        // Like Android: leaving cancels the request (closing the connection lets
        // the server drop a start nobody wants) and a late answer is ignored.
        var (vm, server, _) = Make();
        var release = new TaskCompletionSource();
        server.OnAsync("POST", "/api/v1/sessions", async _ =>
        {
            await release.Task;
            return FakeServer.Response(200, FakeServer.SessionJson("late"));
        });

        vm.Play(News);
        await WaitFor(() => server.For("POST", "/api/v1/sessions").Any());
        var replace = vm.ReplaceTask;
        vm.Stop();
        release.SetResult();
        await replace;

        Assert.IsType<PlayerState.Idle>(vm.State);
        Assert.Null(vm.PlaylistUri);
        Assert.Empty(server.For("POST", "/api/v1/sessions/late/heartbeat"));
    }

    [Fact]
    public async Task Auth_errors_replace_once_then_fail()
    {
        var (vm, server, _) = Make();
        var n = 0;
        server.On("POST", "/api/v1/sessions", _ => FakeServer.Response(200, FakeServer.SessionJson($"v{++n}")));
        vm.Play(News);
        await vm.ReplaceTask;

        vm.OnPlaybackAuthError();
        await vm.ReplaceTask;
        Assert.Equal("v2", Assert.IsType<PlayerState.Playing>(vm.State).Session.ViewerId);

        vm.OnPlaybackAuthError();
        Assert.Equal(PlayerViewModel.PlaybackAuthFailedMessage, Assert.IsType<PlayerState.Failed>(vm.State).Message);
        vm.Dispose();
    }

    [Fact]
    public void Allowed_profiles_follow_max_quality()
    {
        var (vm, _, _) = Make("medium");
        Assert.Equal(new[] { "medium", "low" }, vm.AllowedProfiles);
    }
}

public class RecordingsViewModelTests
{
    private const string Ready =
        """[{"id":3,"title":"Show","channelId":7,"start":"2026-10-04T20:00:00Z","stop":"2026-10-04T21:00:00Z","state":"ready","durationSec":3600,"canManage":true}]""";

    private static (RecordingsViewModel Vm, FakeServer Server) Make()
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        server.Json("GET", "/api/v1/recordings", Ready);
        return (new RecordingsViewModel(client), server);
    }

    [Fact]
    public async Task Tabs_load_their_state()
    {
        var (vm, server) = Make();

        await vm.SelectTabAsync(RecordingTab.Upcoming);

        Assert.Equal(RecordingTab.Upcoming, vm.Tab);
        Assert.Equal(RecordingsStatus.Loaded, vm.Status);
        Assert.Single(vm.Items);
        Assert.Equal("/api/v1/recordings?state=upcoming", server.For("GET", "/api/v1/recordings").Single().PathAndQuery);
    }

    [Fact]
    public async Task Load_failure_shows_plain_words()
    {
        var (vm, server) = Make();
        server.Json("GET", "/api/v1/recordings", """{"error":"off"}""", 503);

        await vm.RefreshAsync();

        Assert.Equal(RecordingsStatus.Failed, vm.Status);
        Assert.Equal("Recording isn't available on this server.", vm.Error);
    }

    [Fact]
    public async Task Delete_refreshes_and_errors_surface_as_messages()
    {
        var (vm, server) = Make();
        server.Json("DELETE", "/api/v1/recordings/3", "", 204);
        server.Json("POST", "/api/v1/recordings/3/stop", """{"error":"forbidden"}""", 403);
        await vm.RefreshAsync();
        var r = vm.Items.Single();

        Assert.True(await vm.DeleteAsync(r));
        Assert.Equal(2, server.For("GET", "/api/v1/recordings").Count());

        Assert.False(await vm.StopAsync(r));
        Assert.Equal("Only the person who scheduled it or an admin can change it.", vm.Message);
        vm.ClearMessage();
        Assert.Null(vm.Message);
    }

    [Fact]
    public async Task Play_offers_resume_inside_the_window()
    {
        var (vm, server) = Make();
        server.Json("POST", "/api/v1/recordings/3/play", """{"playlistUrl":"/api/v1/recordings/3/hls/index.m3u8?token=t","positionSec":600,"durationSec":3600}""");
        await vm.RefreshAsync();

        var start = await vm.PlayAsync(vm.Items.Single());

        Assert.NotNull(start);
        Assert.True(start!.OfferResume);
        Assert.Equal(600, start.ResumeAtSec);
        Assert.Equal("http://bowtie.test:8400/api/v1/recordings/3/hls/index.m3u8?token=t", start.PlaylistUri.AbsoluteUri);
    }

    [Fact]
    public async Task Play_not_ready_is_a_message()
    {
        var (vm, server) = Make();
        server.Json("POST", "/api/v1/recordings/3/play", """{"error":"not ready"}""", 409);
        await vm.RefreshAsync();

        Assert.Null(await vm.PlayAsync(vm.Items.Single()));
        Assert.Equal("This recording isn't ready to play yet.", vm.Message);
    }

    [Fact]
    public async Task Position_saves_are_sent_in_order()
    {
        var (vm, server) = Make();
        server.Json("PUT", "/api/v1/recordings/3/position", "", 204);

        vm.SavePosition(3, TimeSpan.FromSeconds(15.9));
        vm.SavePosition(3, TimeSpan.FromSeconds(30));
        vm.SavePosition(3, TimeSpan.FromSeconds(-1));
        vm.Dispose();
        await vm.SaveLoop;

        Assert.Equal(
            new[] { """{"positionSec":15}""", """{"positionSec":30}""", """{"positionSec":0}""" },
            server.For("PUT", "/api/v1/recordings/3/position").Select(r => r.Body));
    }
}
