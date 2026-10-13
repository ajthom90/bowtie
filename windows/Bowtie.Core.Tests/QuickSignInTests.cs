using Bowtie.Core.ViewModels;

namespace Bowtie.Core.Tests;

public class QuickSignInClientTests
{
    private const string StartJson =
        """{"deviceCode":"dev-1","userCode":"BCDF-2345","verifyUrl":"http://bowtie.test:8400/link?code=BCDF2345","qrUrl":"/api/v1/auth/device/qr/BCDF2345.png","expiresIn":600,"interval":5}""";

    private static (BowtieClient Client, InMemoryTokenStore Store) NewClient(FakeServer server)
    {
        var store = new InMemoryTokenStore("http://bowtie.test:8400", null);
        return (new BowtieClient(FakeServer.BaseUri, store, server.Client()), store);
    }

    [Fact]
    public async Task Start_sends_the_device_name_and_reads_the_codes()
    {
        var server = new FakeServer().Json("POST", "/api/v1/auth/device", StartJson);
        var (client, _) = NewClient(server);

        var start = await client.StartDeviceSignInAsync("Living room Xbox");

        Assert.Equal("""{"deviceName":"Living room Xbox"}""", server.For("POST", "/api/v1/auth/device").Single().Body);
        Assert.Null(server.For("POST", "/api/v1/auth/device").Single().Authorization);
        Assert.Equal("dev-1", start.DeviceCode);
        Assert.Equal("BCDF-2345", start.UserCode);
        Assert.Equal("http://bowtie.test:8400/link?code=BCDF2345", start.VerifyUrl);
        Assert.Equal("/api/v1/auth/device/qr/BCDF2345.png", start.QrUrl);
        Assert.Equal(600, start.ExpiresIn);
        Assert.Equal(5, start.Interval);
    }

    [Fact]
    public async Task Start_refused_carries_the_server_words()
    {
        var server = new FakeServer().Json("POST", "/api/v1/auth/device", """{"error":"too many sign-ins in progress; try again shortly"}""", 429);
        var (client, _) = NewClient(server);

        var e = await Assert.ThrowsAsync<ServerException>(() => client.StartDeviceSignInAsync("Xbox"));
        Assert.Equal(429, e.Status);
        Assert.Equal("too many sign-ins in progress; try again shortly", e.Message);
    }

    [Fact]
    public async Task Poll_pending_and_expired()
    {
        var server = new FakeServer();
        var (client, store) = NewClient(server);

        server.Json("POST", "/api/v1/auth/device/token", """{"error":"authorization_pending"}""", 428);
        Assert.IsType<DevicePoll.Pending>(await client.PollDeviceSignInAsync("dev-1"));
        Assert.Equal("""{"deviceCode":"dev-1"}""", server.For("POST", "/api/v1/auth/device/token").First().Body);

        server.Json("POST", "/api/v1/auth/device/token", """{"error":"expired_token"}""", 410);
        Assert.IsType<DevicePoll.Expired>(await client.PollDeviceSignInAsync("dev-1"));

        Assert.Null(store.LoadRefreshToken());
        Assert.Null(client.CurrentUser);
    }

    [Fact]
    public async Task Poll_approved_signs_in_like_a_password_login()
    {
        var server = new FakeServer()
            .Json("POST", "/api/v1/auth/device/token", FakeServer.TokenJson("a1", "r1", user: "bob"))
            .Json("GET", "/api/v1/channels", "[]");
        var (client, store) = NewClient(server);

        var poll = await client.PollDeviceSignInAsync("dev-1");

        var signedIn = Assert.IsType<DevicePoll.SignedIn>(poll);
        Assert.Equal("bob", signedIn.User.Username);
        Assert.Equal("bob", client.CurrentUser?.Username);
        Assert.Equal("r1", store.LoadRefreshToken());
        Assert.Equal("http://bowtie.test:8400", store.LoadServer());
        await client.ChannelsAsync();
        Assert.Equal("Bearer a1", server.For("GET", "/api/v1/channels").Single().Authorization);
    }

    [Fact]
    public async Task Poll_other_errors_throw()
    {
        var server = new FakeServer().Json("POST", "/api/v1/auth/device/token", """{"error":"boom"}""", 500);
        var (client, _) = NewClient(server);

        var e = await Assert.ThrowsAsync<ServerException>(() => client.PollDeviceSignInAsync("dev-1"));
        Assert.Equal("boom", e.Message);
    }

    [Fact]
    public void Qr_url_resolves_against_the_server()
    {
        var (client, _) = NewClient(new FakeServer());
        var start = new DeviceSignIn { QrUrl = "/api/v1/auth/device/qr/BCDF2345.png" };
        Assert.Equal("http://bowtie.test:8400/api/v1/auth/device/qr/BCDF2345.png", client.DeviceQrUri(start)?.ToString());
        Assert.Null(client.DeviceQrUri(new DeviceSignIn()));
    }
}

public class QuickSignInViewModelTests
{
    private static string Start(string device, string user) =>
        $$$"""{"deviceCode":"{{{device}}}","userCode":"{{{user}}}","verifyUrl":"http://bowtie.test:8400/link?code=X","qrUrl":"/api/v1/auth/device/qr/{{{user}}}.png","expiresIn":600,"interval":3}""";

    private sealed class Delays
    {
        public List<TimeSpan> Seen { get; } = new();

        public Task Delay(TimeSpan t, CancellationToken ct)
        {
            ct.ThrowIfCancellationRequested();
            Seen.Add(t);
            return Task.CompletedTask;
        }
    }

    private static BowtieClient NewClient(FakeServer server) =>
        new(FakeServer.BaseUri, new InMemoryTokenStore("http://bowtie.test:8400", null), server.Client());

    [Fact]
    public async Task Shows_the_code_then_signs_in_once_approved()
    {
        var polls = 0;
        var server = new FakeServer()
            .Json("POST", "/api/v1/auth/device", Start("dev-1", "BCDF-2345"))
            .On("POST", "/api/v1/auth/device/token", _ => ++polls < 3
                ? FakeServer.Response(428, """{"error":"authorization_pending"}""")
                : FakeServer.Response(200, FakeServer.TokenJson("a1", "r1", user: "bob")));
        var delays = new Delays();
        var vm = new QuickSignInViewModel(NewClient(server), "Xbox", delays.Delay);
        var seenCodes = new List<string?>();
        vm.PropertyChanged += (_, e) =>
        {
            if (e.PropertyName == nameof(QuickSignInViewModel.UserCode)) seenCodes.Add(vm.UserCode);
        };

        var user = await vm.RunAsync();

        Assert.Equal("bob", user?.Username);
        Assert.Equal(QuickSignInStatus.SignedIn, vm.Status);
        Assert.Contains("BCDF-2345", seenCodes);
        Assert.Equal("http://bowtie.test:8400/api/v1/auth/device/qr/BCDF-2345.png", vm.QrUri?.ToString());
        Assert.Equal(3, polls);
        Assert.All(delays.Seen, d => Assert.Equal(TimeSpan.FromSeconds(3), d));
        Assert.Equal(3, delays.Seen.Count);
    }

    [Fact]
    public async Task An_expired_code_is_replaced_with_a_new_one()
    {
        var starts = 0;
        var server = new FakeServer()
            .On("POST", "/api/v1/auth/device", _ => FakeServer.Response(200, ++starts == 1
                ? Start("dev-1", "AAAA-1111")
                : Start("dev-2", "BBBB-2222")))
            .On("POST", "/api/v1/auth/device/token", r => r.Body.Contains("dev-1")
                ? FakeServer.Response(410, """{"error":"expired_token"}""")
                : FakeServer.Response(200, FakeServer.TokenJson("a1", "r1")));
        var vm = new QuickSignInViewModel(NewClient(server), "Xbox", new Delays().Delay);

        var user = await vm.RunAsync();

        Assert.NotNull(user);
        Assert.Equal(2, starts);
        Assert.Equal("BBBB-2222", vm.UserCode);
    }

    [Fact]
    public async Task A_failed_start_reports_the_error_and_returns_null()
    {
        var server = new FakeServer().Json("POST", "/api/v1/auth/device", """{"error":"too many sign-ins in progress; try again shortly"}""", 429);
        var vm = new QuickSignInViewModel(NewClient(server), "Xbox", new Delays().Delay);

        var user = await vm.RunAsync();

        Assert.Null(user);
        Assert.Equal(QuickSignInStatus.Failed, vm.Status);
        Assert.Equal("too many sign-ins in progress; try again shortly", vm.Error);
        Assert.Null(vm.UserCode);
    }

    [Fact]
    public async Task A_poll_that_cannot_reach_the_server_keeps_polling()
    {
        var polls = 0;
        var server = new FakeServer()
            .Json("POST", "/api/v1/auth/device", Start("dev-1", "BCDF-2345"))
            .On("POST", "/api/v1/auth/device/token", _ => ++polls == 1
                ? throw new HttpRequestException("offline")
                : FakeServer.Response(200, FakeServer.TokenJson("a1", "r1")));
        var vm = new QuickSignInViewModel(NewClient(server), "Xbox", new Delays().Delay);

        var user = await vm.RunAsync();

        Assert.NotNull(user);
        Assert.Equal(2, polls);
    }

    [Fact]
    public async Task Cancelling_stops_polling_quietly()
    {
        using var cts = new CancellationTokenSource();
        var polls = 0;
        var server = new FakeServer()
            .Json("POST", "/api/v1/auth/device", Start("dev-1", "BCDF-2345"))
            .On("POST", "/api/v1/auth/device/token", _ =>
            {
                if (++polls == 2) cts.Cancel();
                return FakeServer.Response(428, """{"error":"authorization_pending"}""");
            });
        var vm = new QuickSignInViewModel(NewClient(server), "Xbox", new Delays().Delay);

        var user = await vm.RunAsync(cts.Token);

        Assert.Null(user);
        Assert.Equal(2, polls);
        Assert.NotEqual(QuickSignInStatus.Failed, vm.Status);
    }

    [Fact]
    public void Status_starts_idle()
    {
        var vm = new QuickSignInViewModel(NewClient(new FakeServer()), "Xbox");
        Assert.Equal(QuickSignInStatus.Idle, vm.Status);
    }
}

public class AppViewModelQuickSignInTests
{
    [Fact]
    public async Task Completing_a_quick_sign_in_moves_to_ready()
    {
        var server = new FakeServer().Json("POST", "/api/v1/auth/device/token", FakeServer.TokenJson("a1", "r1", user: "bob"));
        var store = new InMemoryTokenStore("http://bowtie.test:8400", null);
        var vm = new AppViewModel(store, uri => new BowtieClient(uri, store, server.Client()), server.Client());
        Assert.Equal(AppPhase.Login, vm.Phase);

        var poll = await vm.Client!.PollDeviceSignInAsync("dev-1");
        vm.CompleteQuickSignIn(((DevicePoll.SignedIn)poll).User);

        Assert.Equal(AppPhase.Ready, vm.Phase);
        Assert.Equal("bob", vm.User?.Username);
        Assert.Equal("r1", store.LoadRefreshToken());
    }
}
