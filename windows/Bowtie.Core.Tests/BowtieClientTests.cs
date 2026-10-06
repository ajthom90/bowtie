namespace Bowtie.Core.Tests;

public class BowtieClientTests
{
    [Fact]
    public async Task Login_saves_server_and_refresh_token_and_uses_bearer()
    {
        var server = new FakeServer()
            .Json("POST", "/api/v1/auth/login", FakeServer.TokenJson("a1", "r1"))
            .Json("GET", "/api/v1/channels", """[{"id":7,"guideNumber":"4.1","name":"News","logoUrl":"","favorite":true,"extra":"ignored"}]""");
        var store = new InMemoryTokenStore();
        var client = new BowtieClient(FakeServer.BaseUri, store, server.Client());

        var user = await client.LoginAsync("alice", "pw");
        var channels = await client.ChannelsAsync();

        Assert.Equal("alice", user.Username);
        Assert.Equal("http://bowtie.test:8400", store.LoadServer());
        Assert.Equal("r1", store.LoadRefreshToken());
        Assert.Equal("""{"username":"alice","password":"pw"}""", server.For("POST", "/api/v1/auth/login").Single().Body);
        Assert.Equal("Bearer a1", server.For("GET", "/api/v1/channels").Single().Authorization);
        var ch = Assert.Single(channels);
        Assert.Equal(7, ch.Id);
        Assert.True(ch.IsFavorite);
    }

    [Fact]
    public async Task Bad_login_is_unauthorized()
    {
        var server = new FakeServer().Json("POST", "/api/v1/auth/login", """{"error":"invalid credentials"}""", 401);
        var client = new BowtieClient(FakeServer.BaseUri, new InMemoryTokenStore(), server.Client());
        await Assert.ThrowsAsync<UnauthorizedException>(() => client.LoginAsync("alice", "nope"));
    }

    [Fact]
    public async Task A_401_refreshes_once_persists_the_new_token_then_retries()
    {
        var server = new FakeServer();
        var (client, store) = TestClients.SignedIn(server, "a1", "r1");
        server.On("GET", "/api/v1/channels", r => r.Authorization == "Bearer a2"
            ? FakeServer.Response(200, "[]")
            : FakeServer.Response(401, """{"error":"expired"}"""));
        server.Json("POST", "/api/v1/auth/refresh", FakeServer.TokenJson("a2", "r2"));

        var channels = await client.ChannelsAsync();

        Assert.Empty(channels);
        Assert.Equal("r2", store.LoadRefreshToken());
        Assert.Equal("""{"refreshToken":"r1"}""", server.For("POST", "/api/v1/auth/refresh").Single().Body);
        Assert.Equal(new[] { "Bearer a1", "Bearer a2" }, server.For("GET", "/api/v1/channels").Select(r => r.Authorization));
    }

    [Fact]
    public async Task Concurrent_401s_share_a_single_refresh()
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server, "a1", "r1");
        var gate = new TaskCompletionSource();
        server.On("GET", "/api/v1/channels", r => r.Authorization == "Bearer a2"
            ? FakeServer.Response(200, "[]")
            : FakeServer.Response(401));
        server.OnAsync("POST", "/api/v1/auth/refresh", async _ =>
        {
            await gate.Task;
            return FakeServer.Response(200, FakeServer.TokenJson("a2", "r2"));
        });

        var calls = Enumerable.Range(0, 5).Select(_ => client.ChannelsAsync()).ToList();
        await Task.Delay(50);
        gate.SetResult();
        await Task.WhenAll(calls);

        Assert.Single(server.For("POST", "/api/v1/auth/refresh"));
    }

    [Fact]
    public async Task A_refused_refresh_clears_the_token_keeps_the_server_and_raises_SessionEnded()
    {
        var server = new FakeServer();
        var (client, store) = TestClients.SignedIn(server);
        server.Json("GET", "/api/v1/channels", "", 401);
        server.Json("POST", "/api/v1/auth/refresh", """{"error":"revoked"}""", 401);
        var ended = 0;
        client.SessionEnded += (_, _) => ended++;

        await Assert.ThrowsAsync<UnauthorizedException>(() => client.ChannelsAsync());

        Assert.Null(store.LoadRefreshToken());
        Assert.Equal("http://bowtie.test:8400", store.LoadServer());
        Assert.Null(client.CurrentUser);
        Assert.Equal(1, ended);
    }

    [Fact]
    public async Task Bootstrap_rotates_the_stored_token()
    {
        var server = new FakeServer().Json("POST", "/api/v1/auth/refresh", FakeServer.TokenJson("a9", "r9", "bob"));
        var store = new InMemoryTokenStore("http://bowtie.test:8400", "r8");
        var client = new BowtieClient(FakeServer.BaseUri, store, server.Client());

        var user = await client.BootstrapFromStoredTokenAsync();

        Assert.Equal("bob", user.Username);
        Assert.Equal("r9", store.LoadRefreshToken());
    }

    [Fact]
    public async Task Bootstrap_without_a_token_is_unauthorized()
    {
        var client = new BowtieClient(FakeServer.BaseUri, new InMemoryTokenStore("http://x", null), new FakeServer().Client());
        await Assert.ThrowsAsync<UnauthorizedException>(() => client.BootstrapFromStoredTokenAsync());
    }

    [Fact]
    public async Task Bootstrap_keeps_the_token_when_the_server_is_unreachable()
    {
        var store = new InMemoryTokenStore("http://bowtie.test:8400", "r1");
        var client = new BowtieClient(FakeServer.BaseUri, store, new HttpClient(new ThrowingHandler()));

        await Assert.ThrowsAsync<NetworkException>(() => client.BootstrapFromStoredTokenAsync());
        Assert.Equal("r1", store.LoadRefreshToken());
    }

    [Fact]
    public async Task Logout_revokes_and_clears_even_when_the_call_fails()
    {
        var server = new FakeServer();
        var (client, store) = TestClients.SignedIn(server);
        server.Json("POST", "/api/v1/auth/logout", "", 500);

        await client.LogoutAsync();

        Assert.Equal("""{"refreshToken":"r1"}""", server.For("POST", "/api/v1/auth/logout").Single().Body);
        Assert.Null(store.LoadRefreshToken());
        Assert.NotNull(store.LoadServer());
    }

    [Fact]
    public async Task Guide_sends_utc_rfc3339_window()
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        server.Json("GET", "/api/v1/guide", """[{"channelId":7,"guideNumber":"4.1","name":"News","logoUrl":"","programs":[{"start":"2026-10-04T20:00:00Z","stop":"2026-10-04T20:30:00Z","title":"Evening News","subtitle":"","description":"","category":"News"}]}]""");

        var start = new DateTimeOffset(2026, 10, 4, 15, 0, 0, TimeSpan.FromHours(-5));
        var guide = await client.GuideAsync(start, start.AddHours(4));

        Assert.Equal("/api/v1/guide?start=2026-10-04T20%3A00%3A00Z&stop=2026-10-05T00%3A00%3A00Z",
            server.For("GET", "/api/v1/guide").Single().PathAndQuery);
        var p = Assert.Single(Assert.Single(guide).Programs);
        Assert.Equal(new DateTimeOffset(2026, 10, 4, 20, 0, 0, TimeSpan.Zero), p.Start);
    }

    [Fact]
    public async Task Favorites_use_put_and_delete()
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        server.Json("PUT", "/api/v1/me/favorites/7", "", 204).Json("DELETE", "/api/v1/me/favorites/7", "", 204);

        await client.SetFavoriteAsync(7, true);
        await client.SetFavoriteAsync(7, false);

        Assert.Single(server.For("PUT", "/api/v1/me/favorites/7"));
        Assert.Single(server.For("DELETE", "/api/v1/me/favorites/7"));
    }

    [Fact]
    public async Task Create_session_sends_caps_and_maps_errors()
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        server.Json("POST", "/api/v1/sessions", FakeServer.SessionJson("v1"));
        var caps = Caps.Detect(hevc: false, ac3: true, eac3: false, displayHeight: 1440) with { Profile = "high" };

        var created = await client.CreateSessionAsync(7, caps);

        Assert.Equal("v1", created.ViewerId);
        Assert.Equal(
            """{"channelId":7,"caps":{"videoCodecs":["h264"],"audioCodecs":["aac","ac3"],"maxHeight":2160,"profile":"high"}}""",
            server.For("POST", "/api/v1/sessions").Single().Body);
    }

    [Theory]
    [InlineData(503, """{"error":"all tuners in use","sessions":[{"channelName":"News","viewers":[{"username":"bob","extra":1}]}],"otherInUse":1}""", typeof(TunersBusyException))]
    [InlineData(422, """{"error":"no codec"}""", typeof(NegotiationFailedException))]
    [InlineData(404, """{"error":"not found"}""", typeof(NotFoundException))]
    [InlineData(429, """{"error":"Your account can use 1 tuner at a time.","code":"user_limit","kind":"tuners","limit":1}""", typeof(ServerException))]
    [InlineData(502, """{"error":"no signal on this channel"}""", typeof(ServerException))]
    public async Task Session_errors_map_to_types(int status, string body, Type expected)
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        server.Json("POST", "/api/v1/sessions", body, status);

        var e = await Assert.ThrowsAnyAsync<BowtieException>(() => client.CreateSessionAsync(7, new ClientCaps()));

        Assert.IsType(expected, e);
        if (e is TunersBusyException busy)
        {
            Assert.Equal("News", busy.Sessions.Single().ChannelName);
            Assert.Equal("bob", busy.Sessions.Single().Viewers.Single().Username);
            Assert.Equal(1, busy.OtherInUse);
        }
        if (status == 429) Assert.Equal("Your account can use 1 tuner at a time.", e.Message);
    }

    [Fact]
    public async Task Heartbeat_uses_the_stream_token_and_no_bearer()
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        server.Json("POST", "/api/v1/sessions/v1/heartbeat", "", 204);

        await client.HeartbeatAsync("v1", "a b+c");

        var hb = server.For("POST", "/api/v1/sessions/v1/heartbeat").Single();
        Assert.Null(hb.Authorization);
        Assert.Equal("/api/v1/sessions/v1/heartbeat?signal=1&token=a%20b%2Bc", hb.PathAndQuery);
    }

    [Fact]
    public async Task Heartbeat_returns_the_reception_signal()
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        server.Json("POST", "/api/v1/sessions/v1/heartbeat",
            """{"signal":{"strength":96,"quality":46,"symbolQuality":0,"weak":true}}""");

        var signal = await client.HeartbeatAsync("v1", "t");

        Assert.Equal(new ReceptionSignal { Strength = 96, Quality = 46, SymbolQuality = 0, Weak = true }, signal);
    }

    [Theory]
    [InlineData(204, "")] // older servers
    [InlineData(200, "")]
    [InlineData(200, """{"signal":null}""")] // unknown
    [InlineData(200, "not json")]
    [InlineData(500, """{"error":"boom"}""")]
    public async Task Heartbeat_without_a_reading_is_unknown(int status, string body)
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        server.Json("POST", "/api/v1/sessions/v1/heartbeat", body, status);

        Assert.Null(await client.HeartbeatAsync("v1", "t"));
    }

    [Fact]
    public async Task Channels_report_watchable_and_default_to_true()
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        server.Json("GET", "/api/v1/channels",
            """[{"id":1,"guideNumber":"2.1","name":"A","logoUrl":"","watchable":false},{"id":2,"guideNumber":"4.1","name":"B","logoUrl":""}]""");

        var channels = await client.ChannelsAsync();

        Assert.False(channels[0].IsWatchable);
        Assert.True(channels[1].IsWatchable);
    }

    [Fact]
    public async Task Delete_session_never_throws()
    {
        var client = new BowtieClient(FakeServer.BaseUri, new InMemoryTokenStore(), new HttpClient(new ThrowingHandler()));
        await client.DeleteSessionAsync("v1");
    }

    [Fact]
    public async Task Recordings_list_play_position_and_keep()
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        server.Json("GET", "/api/v1/recordings", """[{"id":3,"title":"Show","channelId":7,"start":"2026-10-04T20:00:00Z","stop":"2026-10-04T21:00:00Z","state":"ready","protected":true,"positionSec":120,"durationSec":3600,"canManage":true}]""");
        server.Json("POST", "/api/v1/recordings/3/play", """{"playlistUrl":"/api/v1/recordings/3/hls/index.m3u8?token=t","positionSec":120,"durationSec":3600}""");
        server.Json("PUT", "/api/v1/recordings/3/position", "", 204);
        server.Json("PATCH", "/api/v1/recordings/3", """{"id":3,"title":"Show","channelId":7,"start":"2026-10-04T20:00:00Z","stop":"2026-10-04T21:00:00Z","state":"ready","protected":false}""");

        var list = await client.RecordingsAsync(RecordingTab.Missed);
        var play = await client.PlayRecordingAsync(3);
        await client.SaveRecordingPositionAsync(3, 0);
        var kept = await client.SetRecordingProtectedAsync(3, false);

        Assert.Equal("/api/v1/recordings?state=failed", server.For("GET", "/api/v1/recordings").Single().PathAndQuery);
        Assert.True(Assert.Single(list).IsProtected);
        Assert.Equal(120, play.PositionSec);
        Assert.Equal("""{"positionSec":0}""", server.For("PUT", "/api/v1/recordings/3/position").Single().Body);
        Assert.Equal("""{"protected":false}""", server.For("PATCH", "/api/v1/recordings/3").Single().Body);
        Assert.False(kept.IsProtected);
    }

    [Fact]
    public async Task Recordings_503_means_dvr_off_not_tuners_busy()
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        server.Json("GET", "/api/v1/recordings", """{"error":"recording is off"}""", 503);

        var e = await Assert.ThrowsAsync<ServerException>(() => client.RecordingsAsync());
        Assert.Equal(503, e.Status);
    }

    [Fact]
    public async Task Play_409_is_a_plain_server_error()
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        server.Json("POST", "/api/v1/recordings/3/play", """{"error":"not ready"}""", 409);

        var e = await Assert.ThrowsAsync<ServerException>(() => client.PlayRecordingAsync(3));
        Assert.Equal(409, e.Status);
        Assert.Equal("not ready", e.Message);
    }

    [Fact]
    public void Stream_paths_never_get_bearer()
    {
        Assert.True(BowtieClient.IsStreamPath("/api/v1/stream/v1/index.m3u8?token=x"));
        Assert.False(BowtieClient.IsStreamPath("/api/v1/sessions"));
    }

    private sealed class ThrowingHandler : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken ct) =>
            throw new HttpRequestException("connection refused");
    }
}
