using Bowtie.Core.ViewModels;

namespace Bowtie.Core.Tests;

public class SlowStartTests
{
    private static readonly Channel News = new() { Id = 7, Name = "News" };
    private static readonly ClientCaps Device = Caps.Detect(false, false, false, 1080);

    private static (PlayerViewModel Vm, ManualTimeProvider Clock, TaskCompletionSource<HttpResponseMessage> Answer) Starting()
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        var answer = new TaskCompletionSource<HttpResponseMessage>(TaskCreationOptions.RunContinuationsAsynchronously);
        server.OnAsync("POST", "/api/v1/sessions", _ => answer.Task);
        var clock = new ManualTimeProvider();
        var vm = new PlayerViewModel(client, Device, debounce: TimeSpan.Zero, timeProvider: clock);
        vm.Play(News);
        return (vm, clock, answer);
    }

    [Fact]
    public async Task Note_starts_at_five_seconds_and_clears_when_the_server_answers()
    {
        var (vm, clock, answer) = Starting();
        using (vm)
        {
            var changed = new List<string?>();
            vm.PropertyChanged += (_, e) => changed.Add(e.PropertyName);
            Assert.Equal("Starting…", vm.StartingText);
            clock.Advance(TimeSpan.FromMilliseconds(4999));
            Assert.False(vm.IsSlowStart);
            clock.Advance(TimeSpan.FromMilliseconds(1));
            await vm.SlowStartTask;
            Assert.IsType<PlayerState.Starting>(vm.State);
            Assert.Equal("Still starting — the stream is buffering. This can take a little while.", vm.StartingText);
            Assert.Contains(nameof(PlayerViewModel.StartingText), changed);
            answer.SetResult(FakeServer.Response(200, FakeServer.SessionJson("v1")));
            await vm.ReplaceTask;
            Assert.IsType<PlayerState.Playing>(vm.State);
            Assert.False(vm.IsSlowStart);
            vm.OnPlaybackStalled();
            clock.Advance(TimeSpan.FromSeconds(10));
            Assert.IsType<PlayerState.Stalled>(vm.State);
            Assert.False(vm.IsSlowStart);
        }
    }

    [Fact]
    public async Task Quick_start_never_shows_the_note()
    {
        var (vm, clock, answer) = Starting();
        using (vm)
        {
            answer.SetResult(FakeServer.Response(200, FakeServer.SessionJson("v1")));
            await vm.ReplaceTask;
            clock.Advance(TimeSpan.FromSeconds(6));
            await vm.SlowStartTask;
            Assert.False(vm.IsSlowStart);
        }
    }

    [Theory]
    [InlineData("zap")]
    [InlineData("quality")]
    [InlineData("retry")]
    public async Task Each_new_start_restarts_the_five_second_count(string action)
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        var gates = new List<TaskCompletionSource<HttpResponseMessage>>();
        server.OnAsync("POST", "/api/v1/sessions", _ =>
        {
            var gate = new TaskCompletionSource<HttpResponseMessage>(TaskCreationOptions.RunContinuationsAsynchronously);
            gates.Add(gate);
            return gate.Task;
        });
        var clock = new ManualTimeProvider();
        using var vm = new PlayerViewModel(client, Device, debounce: TimeSpan.Zero, timeProvider: clock);
        vm.Play(News);
        var oldStart = vm.ReplaceTask;
        clock.Advance(TimeSpan.FromSeconds(5));
        await vm.SlowStartTask;
        Assert.True(vm.IsSlowStart);
        switch (action)
        {
            case "zap": vm.Play(News with { Id = 8 }); break;
            case "quality": vm.SetProfile("low"); break;
            default: vm.Retry(); break;
        }
        Assert.False(vm.IsSlowStart);
        clock.Advance(TimeSpan.FromMilliseconds(4999));
        Assert.False(vm.IsSlowStart);
        clock.Advance(TimeSpan.FromMilliseconds(1));
        await vm.SlowStartTask;
        Assert.True(vm.IsSlowStart);
        vm.Stop();
        Assert.False(vm.IsSlowStart);
        for (var i = 0; i < gates.Count; i++)
            gates[i].SetResult(FakeServer.Response(200, FakeServer.SessionJson($"v{i}")));
        await Task.WhenAll(oldStart, vm.ReplaceTask);
        clock.Advance(TimeSpan.FromSeconds(10));
        Assert.IsType<PlayerState.Idle>(vm.State);
        Assert.False(vm.IsSlowStart);
    }

    [Theory]
    [InlineData(404)]
    [InlineData(500)]
    [InlineData(503)]
    public async Task Failure_or_busy_clears_the_note(int status)
    {
        var (vm, clock, answer) = Starting();
        using (vm)
        {
            clock.Advance(TimeSpan.FromSeconds(5));
            await vm.SlowStartTask;
            Assert.True(vm.IsSlowStart);
            answer.SetResult(FakeServer.Response(status, """{"error":"unavailable","sessions":[],"otherInUse":2}"""));
            await vm.ReplaceTask;
            Assert.False(vm.IsSlowStart);
            Assert.True(vm.State is PlayerState.Failed or PlayerState.TunersBusy);
        }
    }

    [Theory]
    [InlineData(false)]
    [InlineData(true)]
    public async Task Leaving_cancels_a_pending_note(bool dispose)
    {
        var (vm, clock, answer) = Starting();
        if (dispose) vm.Dispose(); else vm.Stop();
        clock.Advance(TimeSpan.FromSeconds(6));
        await vm.SlowStartTask;
        Assert.False(vm.IsSlowStart);
        answer.SetResult(FakeServer.Response(200, FakeServer.SessionJson("late")));
        await vm.ReplaceTask;
        vm.Dispose();
    }

    [Fact]
    public async Task Session_request_waits_sixty_seconds_while_other_requests_keep_their_deadline()
    {
        Assert.Equal(TimeSpan.FromSeconds(60), BowtieClient.SessionStartTimeout);
        Assert.True(BowtieClient.SessionStartTimeout.TotalSeconds > 5 + 12 + 3 + 15 + 15);
        using var defaults = BowtieClient.CreateDefaultHttpClient();
        Assert.Equal(Timeout.InfiniteTimeSpan, defaults.Timeout);
        var clock = new ManualTimeProvider();
        using var handler = new WaitingHandler();
        using var http = new HttpClient(handler) { Timeout = Timeout.InfiniteTimeSpan };
        var client = new BowtieClient(FakeServer.BaseUri, new InMemoryTokenStore(), http, clock);
        var session = client.CreateSessionAsync(7, Device);
        var channels = client.ChannelsAsync();
        clock.Advance(TimeSpan.FromSeconds(45));
        await Assert.ThrowsAsync<NetworkException>(() => channels);
        Assert.False(session.IsCompleted);
        clock.Advance(TimeSpan.FromSeconds(5));
        handler.Answer.SetResult(FakeServer.Response(200, FakeServer.SessionJson("slow")));
        Assert.Equal("slow", (await session).ViewerId);

        handler.Answer = new(TaskCreationOptions.RunContinuationsAsynchronously);
        session = client.CreateSessionAsync(7, Device);
        clock.Advance(TimeSpan.FromSeconds(59));
        Assert.False(session.IsCompleted);
        clock.Advance(TimeSpan.FromSeconds(1));
        await Assert.ThrowsAsync<NetworkException>(() => session);

        using var cancel = new CancellationTokenSource();
        session = client.CreateSessionAsync(7, Device, cancel.Token);
        cancel.Cancel();
        await Assert.ThrowsAnyAsync<OperationCanceledException>(() => session);
    }

    private sealed class WaitingHandler : HttpMessageHandler
    {
        public TaskCompletionSource<HttpResponseMessage> Answer = new(TaskCreationOptions.RunContinuationsAsynchronously);
        protected override Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken ct) =>
            Answer.Task.WaitAsync(ct);
    }
}
