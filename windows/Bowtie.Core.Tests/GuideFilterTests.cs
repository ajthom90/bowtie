using Bowtie.Core.ViewModels;

namespace Bowtie.Core.Tests;

/// <summary>Same vectors as the web's guideFilterModel.test.ts and Android's GuideFilterTest.</summary>
public class GuideFilterTests
{
    private static readonly DateTimeOffset T18 = new(2026, 10, 4, 18, 0, 0, TimeSpan.Zero);

    private static GuideProgram Prog(
        string category = "",
        bool? isNew = null,
        string rating = "",
        string? programId = null,
        DateTimeOffset? start = null,
        DateTimeOffset? stop = null) => new()
        {
            Start = start ?? T18,
            Stop = stop ?? T18.AddHours(1),
            Title = "Show",
            Category = category,
            IsNew = isNew,
            Rating = rating,
            ProgramId = programId,
        };

    private static GuideBucket[] Buckets(string category, bool? isNew = null, string rating = "", string? programId = null) =>
        GuideFilters.Buckets(Prog(category, isNew, rating, programId)).OrderBy(b => b.ToString(), StringComparer.Ordinal).ToArray();

    [Theory]
    [InlineData("Sports")]
    [InlineData("Sports event")]
    [InlineData("Sports non-event")]
    [InlineData("Sports talk")]
    [InlineData("SPORTS EVENT")]
    [InlineData("Football")]
    [InlineData("College football")]
    [InlineData("Basketball")]
    [InlineData("Baseball")]
    [InlineData("Soccer")]
    [InlineData("Hockey")]
    [InlineData("Golf")]
    [InlineData("Tennis")]
    [InlineData("Boxing")]
    [InlineData("Pro wrestling")]
    [InlineData("Auto racing")]
    [InlineData("Motorsports")]
    [InlineData("Figure skating")]
    [InlineData("Track/field")]
    [InlineData("Olympics")]
    [InlineData("Mixed martial arts")]
    public void Sports(string category) => Assert.Equal(new[] { GuideBucket.Sports }, Buckets(category));

    [Theory]
    [InlineData("Movie")]
    [InlineData("Movies")]
    [InlineData("movie")]
    [InlineData("Feature Film")]
    [InlineData("Film")]
    [InlineData("TV Movie")]
    [InlineData("Made-for-TV movie")]
    public void Movies(string category) => Assert.Equal(new[] { GuideBucket.Movies }, Buckets(category));

    [Theory]
    [InlineData("News")]
    [InlineData("Newsmagazine")]
    [InlineData("News magazine")]
    [InlineData("Weather")]
    [InlineData("Local news")]
    [InlineData("Newscast")]
    public void News(string category) => Assert.Equal(new[] { GuideBucket.News }, Buckets(category));

    [Theory]
    [InlineData("Children")]
    [InlineData("Children's")]
    [InlineData("Children-music")]
    [InlineData("Children-special")]
    [InlineData("Kids")]
    [InlineData("Animated")]
    [InlineData("Animation")]
    [InlineData("Cartoon")]
    [InlineData("Educational")]
    public void Kids(string category) => Assert.Equal(new[] { GuideBucket.Kids }, Buckets(category));

    [Theory]
    [InlineData("")]
    [InlineData("Family")]
    [InlineData("Drama")]
    [InlineData("Sitcom")]
    [InlineData("Comedy")]
    [InlineData("Movie review")]
    [InlineData("Martial arts")]
    [InlineData("Transportation")]
    [InlineData("Talk")]
    [InlineData("Series")]
    [InlineData("Reality")]
    public void No_bucket(string category) => Assert.Empty(Buckets(category));

    [Fact]
    public void Trims_and_ignores_case() => Assert.Equal(new[] { GuideBucket.Sports }, Buckets("  sPoRtS eVeNt  "));

    [Fact]
    public void A_joined_category_string_can_land_in_several_buckets()
    {
        Assert.Equal(new[] { GuideBucket.News, GuideBucket.Sports }, Buckets("Sports talk; News"));
        Assert.Equal(new[] { GuideBucket.Kids }, Buckets("Children, Animated"));
        Assert.Equal(new[] { GuideBucket.Kids, GuideBucket.Movies }, Buckets("Movie | Animated"));
    }

    [Fact]
    public void Schedules_Direct_program_id_prefixes_mark_movies_and_sports_events()
    {
        Assert.Equal(new[] { GuideBucket.Movies }, Buckets("Action", programId: "MV000111220000"));
        Assert.Equal(new[] { GuideBucket.Sports }, Buckets("", programId: "SP012345670123"));
        Assert.Empty(Buckets("Drama", programId: "EP012345670012"));
        Assert.Empty(Buckets("", programId: "MVP"));
    }

    [Fact]
    public void A_kids_movie_is_in_both_buckets() =>
        Assert.Equal(new[] { GuideBucket.Kids, GuideBucket.Movies }, Buckets("Children", programId: "MV000111220000"));

    [Fact]
    public void IsNew_adds_the_new_bucket()
    {
        Assert.Equal(new[] { GuideBucket.New }, Buckets("Sitcom", isNew: true));
        Assert.Equal(new[] { GuideBucket.New, GuideBucket.Sports }, Buckets("Sports event", isNew: true));
        Assert.Empty(Buckets("Sitcom", isNew: false));
    }

    [Fact]
    public void Mature_ratings_keep_a_program_out_of_kids()
    {
        Assert.Empty(Buckets("Animated", rating: "TV-14"));
        Assert.Empty(Buckets("Animated", rating: "TV-MA"));
        Assert.Empty(Buckets("Animated", rating: "R"));
        Assert.Equal(new[] { GuideBucket.Kids }, Buckets("Animated", rating: "TV-PG"));
        Assert.Equal(new[] { GuideBucket.Kids }, Buckets("Children", rating: "TV-Y7"));
    }

    [Fact]
    public void Program_matches()
    {
        Assert.True(GuideFilter.All.Matches(Prog()));
        Assert.True(GuideFilter.Sports.Matches(Prog("Football")));
        Assert.False(GuideFilter.Movies.Matches(Prog("Football")));
        Assert.True(GuideFilter.New.Matches(Prog(isNew: true)));
    }

    // ── Channel window ──────────────────────────────────────────────────────

    private static readonly DateTimeOffset WinStart = T18;
    private static readonly DateTimeOffset WinStop = T18.AddHours(4);

    [Fact]
    public void All_keeps_every_channel_even_without_guide_data() =>
        Assert.True(GuideFilter.All.Matches(Array.Empty<GuideProgram>(), WinStart, WinStop));

    [Fact]
    public void A_filter_hides_channels_without_guide_data() =>
        Assert.False(GuideFilter.Sports.Matches(Array.Empty<GuideProgram>(), WinStart, WinStop));

    [Fact]
    public void Keeps_a_channel_with_a_matching_program_in_the_window()
    {
        var programs = new[]
        {
            Prog("News"),
            Prog("Football", start: T18.AddHours(2), stop: T18.AddHours(5)),
        };
        Assert.True(GuideFilter.Sports.Matches(programs, WinStart, WinStop));
        Assert.True(GuideFilter.News.Matches(programs, WinStart, WinStop));
        Assert.False(GuideFilter.Movies.Matches(programs, WinStart, WinStop));
    }

    [Fact]
    public void Ignores_matches_outside_the_window_stop_is_exclusive()
    {
        var before = Prog("Golf", start: T18.AddHours(-2), stop: T18);
        var after = Prog("Golf", start: T18.AddHours(4), stop: T18.AddHours(5));
        Assert.False(GuideFilter.Sports.Matches(new[] { before, after }, WinStart, WinStop));
    }

    [Fact]
    public void Counts_a_match_that_started_before_the_window_and_is_still_on()
    {
        var running = Prog("Golf", start: T18.AddHours(-1), stop: T18.AddMinutes(30));
        Assert.True(GuideFilter.Sports.Matches(new[] { running }, WinStart, WinStop));
    }

    // ── Row highlight ───────────────────────────────────────────────────────

    [Fact]
    public void Highlight_dims_non_matching_lines_and_finds_a_later_match()
    {
        var now = Prog("News", start: T18, stop: T18.AddMinutes(30));
        var next = Prog("Sitcom", start: T18.AddMinutes(30), stop: T18.AddHours(1));
        var later = Prog("Football", start: T18.AddHours(2), stop: T18.AddHours(3)) with { Title = "Game" };
        var programs = new[] { now, next, later };
        var nn = new GuideLogic.NowNext(now, next);

        Assert.Equal(new GuideFilters.RowHighlight(true, true, null), GuideFilter.All.Highlight(nn, programs, WinStart, WinStop));
        Assert.Equal(new GuideFilters.RowHighlight(true, false, null), GuideFilter.News.Highlight(nn, programs, WinStart, WinStop));
        var sports = GuideFilter.Sports.Highlight(nn, programs, WinStart, WinStop);
        Assert.False(sports.NowMatches);
        Assert.False(sports.NextMatches);
        Assert.Equal("Game", sports.Later?.Title);
        Assert.Equal("Later: Game · 8:00 PM", GuideFilters.LaterLine(sports.Later!, TimeZoneInfo.Utc));
    }

    // ── Labels and storage ──────────────────────────────────────────────────

    [Fact]
    public void Chips_are_All_Sports_Movies_News_Kids_New_in_order() =>
        Assert.Equal(new[] { "All", "Sports", "Movies", "News", "Kids", "New" }, GuideFilters.Chips.Select(f => f.Label()));

    [Fact]
    public void Empty_copy_names_the_bucket()
    {
        Assert.Equal("No sports on in this time window", GuideFilter.Sports.EmptyCopy());
        Assert.Equal("No movies on in this time window", GuideFilter.Movies.EmptyCopy());
        Assert.Equal("No news on in this time window", GuideFilter.News.EmptyCopy());
        Assert.Equal("No kids' shows on in this time window", GuideFilter.Kids.EmptyCopy());
        Assert.Equal("No new episodes on in this time window", GuideFilter.New.EmptyCopy());
    }

    [Fact]
    public void Parses_known_values_and_falls_back_to_all()
    {
        Assert.Equal(GuideFilter.Sports, GuideFilters.Parse("sports"));
        Assert.Equal("kids", GuideFilter.Kids.Stored());
        Assert.Equal(GuideFilter.All, GuideFilters.Parse("bogus"));
        Assert.Equal(GuideFilter.All, GuideFilters.Parse(null));
    }

    [Fact]
    public void Guide_program_decodes_filter_fields()
    {
        var p = BowtieJson.Deserialize<GuideProgram>(
            """{"start":"2026-10-04T18:00:00Z","stop":"2026-10-04T19:00:00Z","title":"x","subtitle":"","description":"","category":"Sports event; News","programId":"SP012345670123","isNew":true,"rating":"TV-PG","locked":true}""");
        Assert.Equal("SP012345670123", p.ProgramId);
        Assert.True(p.IsNew);
        Assert.Equal("TV-PG", p.Rating);
        Assert.True(p.Locked);

        var old = BowtieJson.Deserialize<GuideProgram>(
            """{"start":"2026-10-04T18:00:00Z","stop":"2026-10-04T19:00:00Z","title":"x","subtitle":"","description":"","category":""}""");
        Assert.Null(old.ProgramId);
        Assert.Null(old.IsNew);
        Assert.Equal("", old.Rating);
        Assert.False(old.Locked);
    }
}

public class PreferencesTests
{
    [Fact]
    public void App_preferences_default_and_round_trip()
    {
        var prefs = new AppPreferences(new InMemoryPreferences());
        Assert.Equal(GuideFilter.All, prefs.GuideFilter);
        Assert.False(prefs.AutoSkipAds);

        prefs.GuideFilter = GuideFilter.Kids;
        prefs.AutoSkipAds = true;
        Assert.Equal(GuideFilter.Kids, prefs.GuideFilter);
        Assert.True(prefs.AutoSkipAds);
    }

    [Fact]
    public void Json_file_survives_a_restart()
    {
        var dir = Path.Combine(Path.GetTempPath(), "bowtie-prefs-" + Guid.NewGuid().ToString("N"));
        var path = Path.Combine(dir, "nested", "settings.json");
        try
        {
            new AppPreferences(new JsonFilePreferences(path)) { GuideFilter = GuideFilter.News, AutoSkipAds = true };
            var reopened = new AppPreferences(new JsonFilePreferences(path));
            Assert.Equal(GuideFilter.News, reopened.GuideFilter);
            Assert.True(reopened.AutoSkipAds);
        }
        finally
        {
            if (Directory.Exists(dir)) Directory.Delete(dir, recursive: true);
        }
    }

    [Fact]
    public void A_corrupt_file_reads_as_empty_and_is_replaced()
    {
        var path = Path.Combine(Path.GetTempPath(), "bowtie-prefs-" + Guid.NewGuid().ToString("N") + ".json");
        try
        {
            File.WriteAllText(path, "{not json");
            var store = new JsonFilePreferences(path);
            Assert.Null(store.Get("guide.filter"));
            store.Set("guide.filter", "news");
            Assert.Equal("news", new JsonFilePreferences(path).Get("guide.filter"));
        }
        finally
        {
            File.Delete(path);
        }
    }

    [Fact]
    public void An_unwritable_path_never_throws()
    {
        // A file where the directory should be: creating the directory fails.
        var blocker = Path.Combine(Path.GetTempPath(), "bowtie-prefs-" + Guid.NewGuid().ToString("N"));
        File.WriteAllText(blocker, "");
        try
        {
            var store = new JsonFilePreferences(Path.Combine(blocker, "settings.json"));
            store.Set("k", "v");
            Assert.Equal("v", store.Get("k")); // remembered for this run
        }
        finally
        {
            File.Delete(blocker);
        }
    }
}

public class ChannelListFilterTests
{
    private static readonly DateTimeOffset Now = new(2026, 10, 4, 20, 10, 0, TimeSpan.Zero);

    private const string Channels =
        """[{"id":1,"guideNumber":"2.1","name":"A","logoUrl":""},{"id":2,"guideNumber":"4.1","name":"B","logoUrl":""},{"id":3,"guideNumber":"9.1","name":"C","logoUrl":""}]""";

    // A: news now, a game at 22:00. B: sitcom now. C: no guide data.
    private const string Guide =
        """
        [{"channelId":1,"guideNumber":"2.1","name":"A","logoUrl":"","programs":[
           {"start":"2026-10-04T20:00:00Z","stop":"2026-10-04T20:30:00Z","title":"Evening News","subtitle":"","description":"","category":"News"},
           {"start":"2026-10-04T20:30:00Z","stop":"2026-10-04T22:00:00Z","title":"Drama","subtitle":"","description":"","category":"Drama"},
           {"start":"2026-10-04T22:00:00Z","stop":"2026-10-04T23:30:00Z","title":"Game","subtitle":"","description":"","category":"Football"}]},
         {"channelId":2,"guideNumber":"4.1","name":"B","logoUrl":"","programs":[
           {"start":"2026-10-04T20:00:00Z","stop":"2026-10-04T20:30:00Z","title":"Sitcom","subtitle":"","description":"","category":"Sitcom","isNew":true}]}]
        """;

    private static async Task<(ChannelListViewModel Vm, AppPreferences Prefs)> Loaded(AppPreferences? prefs = null)
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        server.Json("GET", "/api/v1/channels", Channels).Json("GET", "/api/v1/guide", Guide);
        prefs ??= new AppPreferences(new InMemoryPreferences());
        var vm = new ChannelListViewModel(client, () => Now, prefs);
        await vm.RefreshAsync();
        return (vm, prefs);
    }

    [Fact]
    public async Task All_shows_every_channel()
    {
        var (vm, _) = await Loaded();
        Assert.Equal(GuideFilter.All, vm.Filter);
        Assert.Equal(new long[] { 1, 2, 3 }, vm.VisibleRows(vm.Rows, Now).Select(r => r.Id));
    }

    [Fact]
    public async Task A_filter_keeps_channels_with_a_match_in_the_window_and_is_remembered()
    {
        var (vm, prefs) = await Loaded();

        vm.SetFilter(GuideFilter.Sports);

        Assert.Equal(GuideFilter.Sports, vm.Filter);
        Assert.Equal(GuideFilter.Sports, prefs.GuideFilter);
        Assert.Equal(new long[] { 1 }, vm.VisibleRows(vm.Rows, Now).Select(r => r.Id));
        var highlight = vm.Highlight(vm.Rows.Single(r => r.Id == 1), Now);
        Assert.False(highlight.NowMatches);
        Assert.Equal("Game", highlight.Later?.Title);

        vm.SetFilter(GuideFilter.New);
        Assert.Equal(new long[] { 2 }, vm.VisibleRows(vm.Rows, Now).Select(r => r.Id));

        vm.SetFilter(GuideFilter.Movies);
        Assert.Empty(vm.VisibleRows(vm.Rows, Now));
    }

    [Fact]
    public async Task The_remembered_filter_is_restored()
    {
        var prefs = new AppPreferences(new InMemoryPreferences()) { GuideFilter = GuideFilter.News };
        var (vm, _) = await Loaded(prefs);
        Assert.Equal(GuideFilter.News, vm.Filter);
        Assert.Equal(new long[] { 1 }, vm.VisibleRows(vm.Rows, Now).Select(r => r.Id));
    }

    [Fact]
    public async Task Program_end_for_the_sleep_timer()
    {
        var (vm, _) = await Loaded();
        Assert.Equal(new DateTimeOffset(2026, 10, 4, 20, 30, 0, TimeSpan.Zero), vm.ProgramEndFor(1, Now));
        Assert.Equal(new DateTimeOffset(2026, 10, 4, 22, 0, 0, TimeSpan.Zero), vm.ProgramEndFor(1, Now.AddMinutes(30)));
        Assert.Null(vm.ProgramEndFor(3, Now));
        Assert.Null(vm.ProgramEndFor(99, Now));
    }
}
