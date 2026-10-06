namespace Bowtie.Core.Tests;

public class ServerUrlTests
{
    [Theory]
    [InlineData("192.168.1.20:8400", "http://192.168.1.20:8400/")]
    [InlineData("  https://tv.example.com/ ", "https://tv.example.com/")]
    [InlineData("http://host:8400/bowtie//", "http://host:8400/bowtie")]
    [InlineData("host?x=1#frag", "http://host/")]
    public void Normalize(string raw, string expected) =>
        Assert.Equal(expected, ServerUrl.Normalize(raw)!.AbsoluteUri);

    [Theory]
    [InlineData("")]
    [InlineData("   ")]
    [InlineData(null)]
    [InlineData("ftp://host")]
    [InlineData("http://")]
    public void Normalize_rejects(string? raw) => Assert.Null(ServerUrl.Normalize(raw));

    [Fact]
    public void Display_has_no_trailing_slash() =>
        Assert.Equal("http://host:8400", ServerUrl.Display(ServerUrl.Normalize("host:8400")!));

    [Fact]
    public void Resolve_keeps_the_token_query()
    {
        var url = ServerUrl.Resolve("/api/v1/stream/v1/index.m3u8?token=abc%2B1", new Uri("http://host:8400/"));
        Assert.Equal("http://host:8400/api/v1/stream/v1/index.m3u8?token=abc%2B1", url.AbsoluteUri);
    }

    [Fact]
    public void Resolve_passes_absolute_urls_through() =>
        Assert.Equal("https://cdn.example/logo.png",
            ServerUrl.Resolve("https://cdn.example/logo.png", new Uri("http://host/")).AbsoluteUri);

    [Fact]
    public async Task Validate_needs_a_200_healthz()
    {
        var ok = new FakeServer().Json("GET", "/healthz", "ok");
        Assert.True(await ServerUrl.ValidateAsync(new Uri("http://host/"), ok.Client()));
        Assert.False(await ServerUrl.ValidateAsync(new Uri("http://host/"), new FakeServer().Client()));
    }
}

public class GuideLogicTests
{
    private static readonly DateTimeOffset T0 = new(2026, 10, 4, 20, 0, 0, TimeSpan.Zero);

    private static GuideProgram P(int startMin, int stopMin, string title) =>
        new() { Start = T0.AddMinutes(startMin), Stop = T0.AddMinutes(stopMin), Title = title };

    [Fact]
    public void Now_is_start_inclusive_stop_exclusive_and_next_follows_it()
    {
        var programs = new[] { P(30, 60, "C"), P(0, 30, "B"), P(-30, 0, "A") };
        var nn = GuideLogic.ComputeNowNext(programs, T0);
        Assert.Equal("B", nn.Now?.Title);
        Assert.Equal("C", nn.Next?.Title);
    }

    [Fact]
    public void Without_a_current_program_next_is_the_soonest_upcoming()
    {
        var nn = GuideLogic.ComputeNowNext(new[] { P(60, 90, "Later"), P(10, 20, "Soon") }, T0);
        Assert.Null(nn.Now);
        Assert.Equal("Soon", nn.Next?.Title);
    }

    [Fact]
    public void Progress_is_clamped() =>
        Assert.Equal(0.5, GuideLogic.Progress(P(-15, 15, "x"), T0), 3);

    [Theory]
    [InlineData("", "original,high,medium,low")]
    [InlineData("medium", "medium,low")]
    [InlineData("original", "original,high,medium,low")]
    [InlineData("bogus", "original,high,medium,low")]
    public void Allowed_profiles(string max, string expected) =>
        Assert.Equal(expected, string.Join(",", GuideLogic.AllowedProfiles(max)));

    [Fact]
    public void Profile_labels()
    {
        Assert.Equal("Auto", GuideLogic.ProfileLabel(""));
        Assert.Equal("High", GuideLogic.ProfileLabel("high"));
    }

    [Fact]
    public void Guide_number_order_is_numeric_by_part()
    {
        var sorted = new[] { "11.1", "4.10", "9.1", "4.2", "7.1", "7" }.OrderBy(x => x, GuideLogic.GuideNumberOrder);
        Assert.Equal(new[] { "4.2", "4.10", "7", "7.1", "9.1", "11.1" }, sorted);
    }
}

public class RecordingLogicTests
{
    private static Recording R(string state, bool canManage = true, bool kept = false, bool partial = false,
        string failure = "", int pos = 0, int dur = 0, long size = 0, string by = "") => new()
        {
            Id = 1, Title = "Show", State = state, CanManage = canManage, IsProtected = kept, Partial = partial,
            Failure = failure, PositionSec = pos, DurationSec = dur, SizeBytes = size, ScheduledBy = by,
        };

    [Fact]
    public void Tabs_map_to_queries()
    {
        Assert.Equal("upcoming", RecordingTab.Upcoming.Query());
        Assert.Equal("recorded", RecordingTab.Recorded.Query());
        Assert.Equal("failed", RecordingTab.Missed.Query());
        Assert.Equal("Missed", RecordingTab.Missed.Title());
    }

    [Fact]
    public void Actions_depend_on_state_and_permission()
    {
        Assert.Equal(new[] { RecordingAction.Play, RecordingAction.Keep, RecordingAction.Delete }, RecordingLogic.Actions(R("ready")));
        Assert.Equal(new[] { RecordingAction.Play }, RecordingLogic.Actions(R("ready", canManage: false)));
        Assert.Equal(new[] { RecordingAction.Stop, RecordingAction.Delete }, RecordingLogic.Actions(R("recording")));
        Assert.Equal(new[] { RecordingAction.Delete }, RecordingLogic.Actions(R("scheduled")));
    }

    [Fact]
    public void Locked_recordings_cannot_play_and_show_a_lock_badge()
    {
        var locked = R("ready") with { Locked = true, Rating = "TV-MA" };
        Assert.Equal(new[] { RecordingAction.Keep, RecordingAction.Delete }, RecordingLogic.Actions(locked));
        Assert.Equal("🔒 TV-MA", RecordingLogic.LockLabel(locked));
        Assert.Contains(new Badge("🔒 TV-MA", BadgeTone.Alert), RecordingLogic.Badges(locked));
        Assert.Equal("🔒 Not rated", RecordingLogic.LockLabel(R("ready") with { Locked = true }));
        Assert.Null(RecordingLogic.LockLabel(R("ready")));
    }

    [Fact]
    public void Labels_and_confirmation()
    {
        Assert.Equal("Cancel", RecordingLogic.DeleteLabel(R("scheduled")));
        Assert.False(RecordingLogic.DeleteNeedsConfirm(R("waiting")));
        Assert.True(RecordingLogic.DeleteNeedsConfirm(R("ready")));
        Assert.Equal("Don't keep", RecordingLogic.ActionLabel(R("ready", kept: true), RecordingAction.Keep));
    }

    [Fact]
    public void Badges_and_status()
    {
        Assert.Equal(new[] { "Missed", "Partial", "Kept" },
            RecordingLogic.Badges(R("failed", kept: true, partial: true)).Select(b => b.Text));
        Assert.Empty(RecordingLogic.Badges(R("ready")));
        Assert.Equal("Missed: No tuner was free", RecordingLogic.StatusLine(R("failed", failure: "noTuner")));
        Assert.Equal("No signal yet. Still trying.", RecordingLogic.StatusLine(R("waiting", failure: "noSignal")));
        Assert.Equal("Part of this program is missing", RecordingLogic.StatusLine(R("ready", partial: true)));
    }

    [Theory]
    [InlineData(10, 3600, false)]
    [InlineData(11, 3600, true)]
    [InlineData(3569, 3600, true)]
    [InlineData(3570, 3600, false)]
    public void Resume_window(int pos, int dur, bool offer) =>
        Assert.Equal(offer, RecordingLogic.ShouldOfferResume(pos, dur));

    [Fact]
    public void Formatting()
    {
        Assert.Equal("0:05", RecordingLogic.FormatClock(5));
        Assert.Equal("1:02:03", RecordingLogic.FormatClock(3723));
        Assert.Equal("Under a minute", RecordingLogic.FormatDuration(59));
        Assert.Equal("1 hr 5 min", RecordingLogic.FormatDuration(3900));
        Assert.Equal("45 min", RecordingLogic.FormatDuration(2700));
        Assert.Equal("850 MB", RecordingLogic.FormatSize(850_000_000));
        Assert.Equal("1.9 GB", RecordingLogic.FormatSize(1_900_000_000));
        Assert.Equal("1 hr · stopped at 5:00 · 2.0 GB · by bob",
            RecordingLogic.DetailLine(R("ready", canManage: false, pos: 300, dur: 3600, size: 2_000_000_000, by: "bob")));
    }

    [Fact]
    public void Format_when()
    {
        var zone = TimeZoneInfo.Utc;
        var now = new DateTimeOffset(2026, 10, 4, 12, 0, 0, TimeSpan.Zero);
        Assert.Equal("Today, 8:00–8:30 PM",
            RecordingLogic.FormatWhen(now.AddHours(8), now.AddHours(8.5), now, zone));
        Assert.Equal("Tomorrow, 11:30 AM–12:30 PM",
            RecordingLogic.FormatWhen(now.AddHours(23.5), now.AddHours(24.5), now, zone));
        Assert.Equal("Thu Oct 8, 7:05–8:00 PM",
            RecordingLogic.FormatWhen(new DateTimeOffset(2026, 10, 8, 19, 5, 0, TimeSpan.Zero),
                new DateTimeOffset(2026, 10, 8, 20, 0, 0, TimeSpan.Zero), now, zone));
    }

    [Fact]
    public void Error_messages()
    {
        Assert.Equal("Only the person who scheduled it or an admin can change it.",
            RecordingLogic.ErrorMessage(new ServerException(403, "forbidden")));
        Assert.Equal("That recording is gone.", RecordingLogic.ErrorMessage(new NotFoundException()));
        Assert.Equal("Can't reach your Bowtie server. Check your connection and try again.",
            RecordingLogic.ErrorMessage(new NetworkException(new IOException("x"))));
        Assert.Equal("Something went wrong. Try again.", RecordingLogic.ErrorMessage(new IOException("decode failed: x")));
        Assert.Equal("Something went wrong. Try again.",
            RecordingLogic.ErrorMessage(BowtieClient.MapHttpError(500, "<html>500</html>", "/api/v1/recordings/3")));
    }
}

public class PlaybackLogicTests
{
    [Fact]
    public void Caps_always_have_h264_and_aac()
    {
        var caps = Caps.Detect(hevc: false, ac3: false, eac3: false, displayHeight: 0);
        Assert.Equal(new[] { "h264" }, caps.VideoCodecs);
        Assert.Equal(new[] { "aac" }, caps.AudioCodecs);
        Assert.Equal(1080, caps.MaxHeight);
        Assert.Equal("", caps.Profile);
    }

    [Fact]
    public void Caps_add_probed_codecs_and_4k()
    {
        var caps = Caps.Detect(hevc: true, ac3: true, eac3: true, displayHeight: 2160);
        Assert.Equal(new[] { "h264", "hevc" }, caps.VideoCodecs);
        Assert.Equal(new[] { "aac", "ac3", "eac3" }, caps.AudioCodecs);
        Assert.Equal(2160, caps.MaxHeight);
    }

    [Fact]
    public void Live_edge_math()
    {
        // Window 100..1000 s, player wants to sit 6 s back from the edge.
        Assert.Equal(994, LiveEdge.LiveTarget(100, 1000, 6));
        Assert.Equal(100, LiveEdge.LiveTarget(100, 103, 6));
        Assert.Equal(90, LiveEdge.SecondsBehind(100, 1000, 6, 904));
        Assert.Equal(0, LiveEdge.SecondsBehind(100, 1000, 6, 999));
        Assert.True(LiveEdge.IsLive(9.9));
        Assert.False(LiveEdge.IsLive(10));
        Assert.Equal("−1:30", LiveEdge.BehindLabel(90.7));
        Assert.Equal("−0:00", LiveEdge.BehindLabel(-3));
    }

    [Fact]
    public void Seek_clamps_and_skips()
    {
        Assert.Equal((994d, true), LiveEdge.ClampSeek(50, 100, 994));
        Assert.Equal((994d, true), LiveEdge.ClampSeek(2000, 100, 994));
        Assert.Equal((994d, true), LiveEdge.ClampSeek(double.NaN, 100, 994));
        Assert.Equal((500d, false), LiveEdge.ClampSeek(500, 100, 994));
        Assert.Equal(100, LiveEdge.SkipBack(110, 100));
        Assert.Equal(470, LiveEdge.SkipBack(500, 100));
        Assert.Equal(994, LiveEdge.SkipForward(980, 994));
    }

    [Fact]
    public void Audio_choices_dedupe_by_label()
    {
        var choices = TrackChoices.Audio(new (string?, string?)[]
        {
            ("en", null), ("eng", null), ("es", null), (null, "Descriptive"), (null, null),
        });
        Assert.Equal(new[] { "English", "Spanish", "Descriptive", "Audio 4" }, choices.Select(c => c.Label));
        Assert.Equal(new[] { 0, 2, 3, 4 }, choices.Select(c => c.Index));
    }

    [Fact]
    public void Language_names()
    {
        Assert.Equal("English", TrackChoices.LanguageName("en-US"));
        Assert.Equal("XYZ", TrackChoices.LanguageName("xyz"));
    }

    [Theory]
    [InlineData("/api/v1/stream/v1/index.m3u8?token=abc", "abc")]
    [InlineData("/api/v1/stream/v1/index.m3u8?x=1&token=a%2Bb", "a+b")]
    [InlineData("/api/v1/stream/v1/index.m3u8", null)]
    [InlineData("/api/v1/stream/v1/index.m3u8?token=", null)]
    public void Stream_token(string url, string? expected) =>
        Assert.Equal(expected, StreamToken.FromPlaylist(url));

    [Fact]
    public void Reception_copy_leads_with_quality()
    {
        var weak = new ReceptionSignal { Strength = 96, Quality = 46, SymbolQuality = 0, Weak = true };
        Assert.Equal("Weak signal (46%) — the picture may break up.", ReceptionText.WeakNote(weak));
        Assert.Equal("Signal quality 46% · strength 96% · error-free 0%", ReceptionText.Stats(weak));
        Assert.Equal("Signal quality 100% · strength 100% · error-free 100%",
            ReceptionText.Stats(new ReceptionSignal { Strength = 100, Quality = 100, SymbolQuality = 100 }));
    }

    [Fact]
    public void Tuners_busy_copy()
    {
        Assert.Null(TunersBusyCopy.OtherAppsLine(0));
        Assert.Equal("1 tuner is in use by another app (like Plex).", TunersBusyCopy.OtherAppsLine(1));
        Assert.Equal("2 tuners are in use by another app (like Plex).", TunersBusyCopy.OtherAppsLine(2));
        var sessions = new[]
        {
            new ActiveSessionSummary { ChannelName = "News", Viewers = new[] { new ViewerSummary { Username = "bob" } } },
        };
        Assert.StartsWith("Watching now: News (bob).", TunersBusyCopy.Body(sessions, 0));
    }

    [Fact]
    public void Error_text()
    {
        Assert.Equal("Your session ended. Sign in again.", ErrorText.For(new UnauthorizedException()));
        Assert.Equal("boom", ErrorText.For(new ServerException(500, "boom")));
        Assert.Equal("Can't reach your Bowtie server. Check your connection and try again.",
            ErrorText.For(new NetworkException(new HttpRequestException("No such host is known. (bowtie.test:8400)"))));
        Assert.Equal("Something went wrong. Try again.",
            ErrorText.For(new System.Text.Json.JsonException("decode failed: 'x' is an invalid start of a value")));
        Assert.Equal("Something went wrong. Try again.", ErrorText.For(new InvalidOperationException("")));
    }

    [Theory]
    [InlineData(500, """{"error":"db down"}""", "db down")] // the server's own words
    [InlineData(500, "<html>Internal Server Error</html>", "Something went wrong. Try again.")]
    [InlineData(502, "", "Something went wrong. Try again.")]
    [InlineData(409, "", "Something went wrong. Try again.")]
    [InlineData(503, "Service Unavailable", "Something went wrong. Try again.")]
    [InlineData(422, "", "This device can't play this channel.")]
    public void Error_text_hides_http_details(int status, string body, string expected) =>
        Assert.Equal(expected, ErrorText.For(BowtieClient.MapHttpError(status, body, "/api/v1/sessions")));

    [Fact]
    public void Http_details_stay_in_the_exception_for_logs() =>
        Assert.Equal("HTTP 500", BowtieClient.MapHttpError(500, "", "/api/v1/channels").Message);
}
