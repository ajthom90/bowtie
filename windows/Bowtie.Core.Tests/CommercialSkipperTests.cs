namespace Bowtie.Core.Tests;

/// <summary>Same cases as Android's CommercialSkipperTest and the web's commercialsModel tests.</summary>
public class CommercialSkipperTests
{
    private static Commercial C(double start, double end) => new(start, end);

    private static string RecordingJson(string extra = "") =>
        """{"id":7,"title":"Jeopardy!","channelId":3,"start":"2026-10-05T00:00:00Z","stop":"2026-10-05T00:30:00Z","state":"ready" """
        + (extra.Length == 0 ? "" : "," + extra) + "}";

    // ── Decoding ────────────────────────────────────────────────────────────

    [Fact]
    public void Recording_from_an_older_server_has_no_commercials()
    {
        var r = BowtieJson.Deserialize<Recording>(RecordingJson());
        Assert.Null(r.Commercials);
        Assert.Empty(new CommercialSkipper(r.Commercials).Segments);
    }

    [Fact]
    public void Null_commercials_decode_as_none()
    {
        var r = BowtieJson.Deserialize<Recording>(RecordingJson("\"commercials\":null"));
        Assert.Empty(new CommercialSkipper(r.Commercials).Segments);
    }

    [Fact]
    public void Recording_decodes_commercials_rating_and_lock()
    {
        var r = BowtieJson.Deserialize<Recording>(RecordingJson(
            "\"commercials\":[{\"start\":312.5,\"end\":401},{\"start\":900,\"end\":1020.25}],\"rating\":\"TV-14\",\"locked\":true"));
        Assert.Equal(new[] { C(312.5, 401), C(900, 1020.25) }, r.Commercials!);
        Assert.Equal("TV-14", r.Rating);
        Assert.True(r.Locked);
    }

    // ── Normalizing ─────────────────────────────────────────────────────────

    [Fact]
    public void Sorts_segments() =>
        Assert.Equal(new[] { C(100, 200), C(900, 1000) },
            CommercialSkipper.Normalize(new[] { C(900, 1000), C(100, 200) }));

    [Fact]
    public void Merges_overlapping_and_touching_segments() =>
        Assert.Equal(new[] { C(100, 300), C(400, 500) },
            CommercialSkipper.Normalize(new[] { C(100, 200), C(150, 250), C(250, 300), C(400, 500), C(420, 450) }));

    [Fact]
    public void Drops_empty_backwards_and_non_finite_segments() =>
        Assert.Equal(new[] { C(600, 700) },
            CommercialSkipper.Normalize(new[]
            {
                C(100, 100),
                C(300, 200),
                C(double.NaN, 50),
                C(10, double.PositiveInfinity),
                C(double.NegativeInfinity, 5),
                C(600, 700),
            }));

    [Fact]
    public void Clamps_a_negative_start_to_zero()
    {
        Assert.Equal(new[] { C(0, 30) }, CommercialSkipper.Normalize(new[] { C(-5, 30) }));
        Assert.Empty(CommercialSkipper.Normalize(new[] { C(-10, -2) }));
    }

    [Fact]
    public void Null_input_normalizes_to_nothing() => Assert.Empty(CommercialSkipper.Normalize(null));

    // ── Active segment ──────────────────────────────────────────────────────

    [Fact]
    public void No_segments_are_never_active()
    {
        var s = new CommercialSkipper(Array.Empty<Commercial>());
        Assert.Null(s.Active(0));
        Assert.Null(s.Active(500));
    }

    [Fact]
    public void Active_is_start_inclusive_and_end_exclusive()
    {
        var s = new CommercialSkipper(new[] { C(100, 200), C(500, 600) });
        Assert.Null(s.Active(99.9));
        Assert.Equal(C(100, 200), s.Active(100));
        Assert.Equal(C(100, 200), s.Active(199.9));
        Assert.Null(s.Active(200));
        Assert.Null(s.Active(350));
        Assert.Equal(C(500, 600), s.Active(550));
        Assert.Null(s.Active(600));
    }

    [Fact]
    public void Active_uses_normalized_segments()
    {
        var s = new CommercialSkipper(new[] { C(250, 300), C(100, 260) });
        Assert.Equal(C(100, 300), s.Active(120));
        Assert.Equal(new[] { C(100, 300) }, s.Segments);
    }

    [Fact]
    public void Non_finite_time_is_never_active() =>
        Assert.Null(new CommercialSkipper(new[] { C(0, 100) }).Active(double.NaN));

    // ── Skipping ────────────────────────────────────────────────────────────

    [Fact]
    public void Skip_target_is_the_segment_end()
    {
        var s = new CommercialSkipper(new[] { C(100, 200) });
        Assert.Equal(200, s.Skip(150));
        Assert.Null(s.Skip(250));
    }

    [Fact]
    public void Auto_skips_each_segment_once()
    {
        var s = new CommercialSkipper(new[] { C(100, 200), C(500, 600) });
        Assert.Null(s.AutoSkipTarget(50));
        Assert.Equal(200, s.AutoSkipTarget(100.2));
        Assert.Equal(600, s.AutoSkipTarget(500));
    }

    [Fact]
    public void Seeking_back_into_an_auto_skipped_segment_does_not_skip_again()
    {
        var s = new CommercialSkipper(new[] { C(100, 200) });
        Assert.Equal(200, s.AutoSkipTarget(101));
        Assert.Null(s.AutoSkipTarget(150));
        // Still inside it, so Skip ad still shows and still works.
        Assert.Equal(C(100, 200), s.Active(150));
        Assert.Equal(200, s.Skip(150));
    }

    [Fact]
    public void Manual_skip_counts_as_handled()
    {
        var s = new CommercialSkipper(new[] { C(100, 200) });
        Assert.Equal(200, s.Skip(120));
        Assert.Null(s.AutoSkipTarget(120));
    }

    [Fact]
    public void Merged_segment_is_skipped_in_one_go()
    {
        var s = new CommercialSkipper(new[] { C(100, 200), C(200, 260) });
        Assert.Equal(260, s.AutoSkipTarget(100));
        Assert.Null(s.AutoSkipTarget(210));
    }
}
