using Bowtie.Core.ViewModels;

namespace Bowtie.Core.Tests;

/// <summary>Same cases as the iOS ContinueWatchingTests and the web's continueModel tests.</summary>
public class ContinueWatchingTests
{
    private static Recording Rec(
        long id,
        string state = "ready",
        int durationSec = 3600,
        int positionSec = 600,
        bool locked = false,
        string start = "2026-10-05T00:00:00Z",
        string? updated = null) => new()
        {
            Id = id,
            Title = "Show",
            State = state,
            DurationSec = durationSec,
            PositionSec = positionSec,
            Locked = locked,
            Start = DateTimeOffset.Parse(start),
            Stop = DateTimeOffset.Parse(start).AddHours(1),
            PositionUpdatedAt = updated == null ? null : DateTimeOffset.Parse(updated),
        };

    private static string RecordingJson(string extra = "") =>
        """{"id":7,"title":"Jeopardy!","channelId":3,"start":"2026-10-05T00:00:00Z","stop":"2026-10-05T00:30:00Z","state":"ready","durationSec":1800,"positionSec":412"""
        + (extra.Length == 0 ? "" : "," + extra) + "}";

    // ── Decoding ────────────────────────────────────────────────────────────

    [Fact]
    public void Decodes_position_updated_at_when_present()
    {
        var r = BowtieJson.Deserialize<Recording>(RecordingJson("\"positionUpdatedAt\":\"2026-10-06T01:02:03Z\""));
        Assert.Equal(412, r.PositionSec);
        Assert.Equal(new DateTimeOffset(2026, 10, 6, 1, 2, 3, TimeSpan.Zero), r.PositionUpdatedAt);
    }

    [Fact]
    public void Decodes_fractional_seconds_and_offsets()
    {
        var r = BowtieJson.Deserialize<Recording>(RecordingJson("\"positionUpdatedAt\":\"2026-10-02T09:30:00.5+02:00\""));
        Assert.Equal(new DateTimeOffset(2026, 10, 2, 7, 30, 0, 500, TimeSpan.Zero), r.PositionUpdatedAt);
    }

    [Fact]
    public void Position_updated_at_is_null_when_omitted()
    {
        Assert.Null(BowtieJson.Deserialize<Recording>(RecordingJson()).PositionUpdatedAt);
    }

    [Fact]
    public void Position_updated_at_is_null_when_null()
    {
        Assert.Null(BowtieJson.Deserialize<Recording>(RecordingJson("\"positionUpdatedAt\":null")).PositionUpdatedAt);
    }

    // ── Eligibility ─────────────────────────────────────────────────────────

    [Fact]
    public void Needs_at_least_a_minute_watched()
    {
        Assert.False(ContinueWatching.IsEligible(Rec(1, positionSec: 0)));
        Assert.False(ContinueWatching.IsEligible(Rec(1, positionSec: 59)));
        Assert.True(ContinueWatching.IsEligible(Rec(1, positionSec: 60)));
    }

    [Fact]
    public void Excludes_the_last_two_minutes()
    {
        Assert.True(ContinueWatching.IsEligible(Rec(1, durationSec: 3600, positionSec: 3479)));
        Assert.False(ContinueWatching.IsEligible(Rec(1, durationSec: 3600, positionSec: 3480)));
        Assert.False(ContinueWatching.IsEligible(Rec(1, durationSec: 3600, positionSec: 3600)));
    }

    [Fact]
    public void Excludes_unknown_or_too_short_duration()
    {
        Assert.False(ContinueWatching.IsEligible(Rec(1, durationSec: 0, positionSec: 600)));
        Assert.False(ContinueWatching.IsEligible(Rec(1, durationSec: 150, positionSec: 60)));
    }

    [Fact]
    public void Only_ready_and_unlocked()
    {
        foreach (var state in new[] { "scheduled", "waiting", "recording", "converting", "failed" })
        {
            Assert.False(ContinueWatching.IsEligible(Rec(1, state: state)), state);
        }
        Assert.False(ContinueWatching.IsEligible(Rec(1, locked: true)));
        Assert.True(ContinueWatching.IsEligible(Rec(1)));
    }

    // ── Ordering ────────────────────────────────────────────────────────────

    [Fact]
    public void Sorts_by_last_saved_newest_first_then_unsaved_by_start()
    {
        var rows = new[]
        {
            Rec(1, start: "2026-10-01T00:00:00Z"),
            Rec(2, start: "2026-10-03T00:00:00Z", updated: "2026-10-04T10:00:00Z"),
            Rec(3, start: "2026-10-02T00:00:00Z"),
            Rec(4, start: "2026-10-01T00:00:00Z", updated: "2026-10-04T12:00:00Z"),
            Rec(5, positionSec: 0, updated: "2026-10-04T13:00:00Z"),
            Rec(6, locked: true, updated: "2026-10-04T14:00:00Z"),
        };
        Assert.Equal(new long[] { 4, 2, 3, 1 }, ContinueWatching.Items(rows).Select(r => r.Id));
    }

    [Fact]
    public void Compares_save_times_across_offsets()
    {
        var rows = new[]
        {
            Rec(1, updated: "2026-10-01T00:00:00Z"),
            Rec(2, updated: "2026-10-03T20:00:00Z"),
            Rec(3, updated: "2026-10-02T09:30:00+02:00"),
        };
        Assert.Equal(new long[] { 2, 3, 1 }, ContinueWatching.Items(rows).Select(r => r.Id));
    }

    [Fact]
    public void Same_save_time_falls_back_to_start()
    {
        var rows = new[]
        {
            Rec(1, start: "2026-10-01T00:00:00Z", updated: "2026-10-04T10:00:00Z"),
            Rec(2, start: "2026-10-02T00:00:00Z", updated: "2026-10-04T10:00:00Z"),
        };
        Assert.Equal(new long[] { 2, 1 }, ContinueWatching.Items(rows).Select(r => r.Id));
    }

    [Fact]
    public void Caps_at_ten()
    {
        var rows = Enumerable.Range(1, 15)
            .Select(i => Rec(i, updated: $"2026-10-04T10:{i:00}:00Z"))
            .ToList();
        var items = ContinueWatching.Items(rows);
        Assert.Equal(10, ContinueWatching.MaxItems);
        Assert.Equal(10, items.Count);
        Assert.Equal(15, items[0].Id);
        Assert.Equal(6, items[^1].Id);
    }

    [Fact]
    public void Nothing_eligible_is_empty()
    {
        Assert.Empty(ContinueWatching.Items(new[] { Rec(1, positionSec: 0) }));
        Assert.Empty(ContinueWatching.Items(Array.Empty<Recording>()));
    }

    // ── Copy ────────────────────────────────────────────────────────────────

    [Fact]
    public void Remaining_text()
    {
        Assert.Equal("less than a minute left", ContinueWatching.RemainingText(Rec(1, durationSec: 3600, positionSec: 3541)));
        Assert.Equal("1 min left", ContinueWatching.RemainingText(Rec(1, durationSec: 3600, positionSec: 3540)));
        Assert.Equal("23 min left", ContinueWatching.RemainingText(Rec(1, durationSec: 1980, positionSec: 600)));
        Assert.Equal("37 min left", ContinueWatching.RemainingText(Rec(1, durationSec: 3600, positionSec: 1380)));
        Assert.Equal("59 min left", ContinueWatching.RemainingText(Rec(1, durationSec: 3600, positionSec: 1)));
        Assert.Equal("1 hr left", ContinueWatching.RemainingText(Rec(1, durationSec: 3660, positionSec: 60)));
        Assert.Equal("1 hr 5 min left", ContinueWatching.RemainingText(Rec(1, durationSec: 7200, positionSec: 3300)));
        Assert.Equal("2 hr 59 min left", ContinueWatching.RemainingText(Rec(1, durationSec: 3 * 3600, positionSec: 60)));
        Assert.Equal("less than a minute left", ContinueWatching.RemainingText(Rec(1, durationSec: 600, positionSec: 900)));
    }

    [Fact]
    public void Progress()
    {
        Assert.Equal(0.25, ContinueWatching.Progress(Rec(1, durationSec: 3600, positionSec: 900)), 4);
        Assert.Equal(0, ContinueWatching.Progress(Rec(1, durationSec: 0, positionSec: 900)));
        Assert.Equal(1, ContinueWatching.Progress(Rec(1, durationSec: 600, positionSec: 900)));
    }

    [Fact]
    public void Accessible_labels()
    {
        var plain = Rec(1, durationSec: 3600, positionSec: 1380) with { Title = "Jeopardy!" };
        Assert.Equal("Resume Jeopardy!, 37 min left", ContinueWatching.ResumeLabel(plain));
        var episode = plain with { Subtitle = "Tournament of Champions" };
        Assert.Equal("Resume Jeopardy!: Tournament of Champions, 37 min left", ContinueWatching.ResumeLabel(episode));
        Assert.Equal("Remove Jeopardy!: Tournament of Champions from Continue watching", ContinueWatching.RemoveLabel(episode));
    }
}

public class ContinueWatchingViewModelTests
{
    private static string Row(long id, int positionSec, string state = "ready", string? updated = null) =>
        $$"""{"id":{{id}},"title":"Show {{id}}","channelId":7,"start":"2026-10-04T20:00:00Z","stop":"2026-10-04T21:00:00Z","state":"{{state}}","durationSec":3600,"positionSec":{{positionSec}}{{(updated == null ? "" : $",\"positionUpdatedAt\":\"{updated}\"")}}}""";

    private static string List(params string[] rows) => "[" + string.Join(",", rows) + "]";

    private static (ContinueWatchingViewModel Vm, FakeServer Server) Make(string json)
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        server.Json("GET", "/api/v1/recordings", json);
        return (new ContinueWatchingViewModel(client), server);
    }

    [Fact]
    public async Task Load_asks_for_recorded_and_keeps_only_in_progress()
    {
        var (vm, server) = Make(List(
            Row(1, 0),
            Row(2, 600, updated: "2026-10-06T01:00:00Z"),
            Row(3, 300, updated: "2026-10-06T02:00:00.5Z"),
            Row(4, 300, state: "recording")));
        Assert.Empty(vm.Items);

        await vm.LoadAsync();

        Assert.Equal(new long[] { 3, 2 }, vm.Items.Select(r => r.Id));
        Assert.True(vm.HasItems);
        Assert.Equal("/api/v1/recordings?state=recorded", server.For("GET", "/api/v1/recordings").Single().PathAndQuery);
    }

    [Fact]
    public async Task Load_failure_keeps_what_was_shown()
    {
        var (vm, server) = Make(List(Row(2, 600)));
        await vm.LoadAsync();
        server.Json("GET", "/api/v1/recordings", """{"error":"recording is not available"}""", 503);

        await vm.LoadAsync();

        Assert.Equal(new long[] { 2 }, vm.Items.Select(r => r.Id));
    }

    [Fact]
    public async Task Remove_resets_position_to_zero_and_drops_it()
    {
        var (vm, server) = Make(List(Row(2, 600), Row(5, 700)));
        server.Json("PUT", "/api/v1/recordings/2/position", "", 204);
        await vm.LoadAsync();
        var target = vm.Items.Single(r => r.Id == 2);

        Assert.True(await vm.RemoveAsync(target));

        Assert.Equal(new long[] { 5 }, vm.Items.Select(r => r.Id));
        Assert.Null(vm.Message);
        Assert.Equal(new[] { """{"positionSec":0}""" }, server.For("PUT", "/api/v1/recordings/2/position").Select(r => r.Body));
    }

    [Fact]
    public async Task Remove_failure_keeps_it_and_explains()
    {
        var (vm, server) = Make(List(Row(2, 600)));
        server.Json("PUT", "/api/v1/recordings/2/position", """{"error":"not found"}""", 404);
        await vm.LoadAsync();

        Assert.False(await vm.RemoveAsync(vm.Items.Single()));

        Assert.Equal(new long[] { 2 }, vm.Items.Select(r => r.Id));
        Assert.Equal("That recording is gone.", vm.Message);
        vm.ClearMessage();
        Assert.Null(vm.Message);
    }

    [Fact]
    public async Task Play_resumes_at_the_server_position()
    {
        var (vm, server) = Make(List(Row(2, 600)));
        server.Json("POST", "/api/v1/recordings/2/play", """{"playlistUrl":"/api/v1/recordings/2/hls/index.m3u8?token=t","positionSec":640,"durationSec":3600}""");
        await vm.LoadAsync();

        var start = await vm.PlayAsync(vm.Items.Single());

        Assert.NotNull(start);
        Assert.Equal(640, start!.ResumeAtSec);
        Assert.Equal("http://bowtie.test:8400/api/v1/recordings/2/hls/index.m3u8?token=t", start.PlaylistUri.AbsoluteUri);
    }

    [Fact]
    public async Task Play_failure_is_a_message()
    {
        var (vm, server) = Make(List(Row(2, 600)));
        server.Json("POST", "/api/v1/recordings/2/play", """{"error":"not ready"}""", 409);
        await vm.LoadAsync();

        Assert.Null(await vm.PlayAsync(vm.Items.Single()));
        Assert.Equal("This recording isn't ready to play yet.", vm.Message);
    }
}

public class RecordingsSaveSettleTests
{
    [Fact]
    public async Task Saves_settled_waits_for_queued_saves()
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        var release = new TaskCompletionSource();
        server.OnAsync("PUT", "/api/v1/recordings/3/position", async _ =>
        {
            await release.Task;
            return FakeServer.Response(204);
        });
        using var vm = new RecordingsViewModel(client);

        Assert.True(vm.SavesSettledAsync(TimeSpan.FromSeconds(1)).IsCompleted); // nothing queued
        vm.SavePosition(3, TimeSpan.FromSeconds(42));
        var settled = vm.SavesSettledAsync(TimeSpan.FromSeconds(10));
        await Task.Delay(50);
        Assert.False(settled.IsCompleted);

        release.SetResult();
        await settled;
        Assert.Single(server.For("PUT", "/api/v1/recordings/3/position"));
    }

    [Fact]
    public async Task Saves_settled_gives_up_after_the_timeout()
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        var never = new TaskCompletionSource();
        server.OnAsync("PUT", "/api/v1/recordings/3/position", async _ =>
        {
            await never.Task;
            return FakeServer.Response(204);
        });
        using var vm = new RecordingsViewModel(client);
        vm.SavePosition(3, TimeSpan.FromSeconds(42));

        await vm.SavesSettledAsync(TimeSpan.FromMilliseconds(50)); // returns, doesn't throw
        never.SetResult();
    }
}
