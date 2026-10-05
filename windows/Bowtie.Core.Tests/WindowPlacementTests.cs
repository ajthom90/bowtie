namespace Bowtie.Core.Tests;

public class WindowPlacementTests
{
    // A 1920×1080 display with a 48 px taskbar along the bottom.
    private static readonly PixelRect Work = new(0, 0, 1920, 1032);

    [Fact]
    public void Default_is_centered_at_100_percent() =>
        Assert.Equal(new PixelRect(320, 116, 1280, 800), WindowPlacement.Default(Work, 1.0));

    [Fact]
    public void Default_is_scaled_to_physical_pixels()
    {
        // 2560×1600 at 150%: 1280×800 view pixels is 1920×1200 physical.
        var work = new PixelRect(0, 0, 2560, 1552);
        Assert.Equal(new PixelRect(320, 176, 1920, 1200), WindowPlacement.Default(work, 1.5));
    }

    [Fact]
    public void Default_is_clamped_to_the_work_area()
    {
        // 1920×1080 at 150% can't fit 1920×1200: fill the work area.
        Assert.Equal(new PixelRect(0, 0, 1920, 1032), WindowPlacement.Default(Work, 1.5));
    }

    [Fact]
    public void Default_respects_a_work_area_offset()
    {
        // A second display to the left, taskbar on top.
        var work = new PixelRect(-1920, 40, 1920, 1040);
        Assert.Equal(new PixelRect(-1600, 160, 1280, 800), WindowPlacement.Default(work, 1.0));
    }

    [Fact]
    public void Default_treats_a_bad_scale_as_100_percent() =>
        Assert.Equal(WindowPlacement.Default(Work, 1.0), WindowPlacement.Default(Work, 0));

    [Fact]
    public void Restore_keeps_a_window_that_fits()
    {
        var saved = new PixelRect(100, 50, 1000, 700);
        Assert.Equal(saved, WindowPlacement.Restore(saved, Work, 1.0));
    }

    [Fact]
    public void Restore_pulls_an_offscreen_window_back()
    {
        // Last on a display that's gone: moved fully inside this one.
        var saved = new PixelRect(2500, 900, 1000, 700);
        Assert.Equal(new PixelRect(920, 332, 1000, 700), WindowPlacement.Restore(saved, Work, 1.0));
    }

    [Fact]
    public void Restore_shrinks_a_window_bigger_than_the_display()
    {
        var saved = new PixelRect(-10, -10, 2560, 1600);
        Assert.Equal(Work, WindowPlacement.Restore(saved, Work, 1.0));
    }

    [Fact]
    public void Restore_falls_back_to_the_default()
    {
        Assert.Equal(WindowPlacement.Default(Work, 1.25), WindowPlacement.Restore(null, Work, 1.25));
        // Too small to be useful (e.g. saved while minimized).
        Assert.Equal(WindowPlacement.Default(Work, 1.0),
            WindowPlacement.Restore(new PixelRect(0, 0, 160, 28), Work, 1.0));
    }

    [Theory]
    [InlineData(null)]
    [InlineData("")]
    [InlineData("1,2,3")]
    [InlineData("a,b,c,d")]
    [InlineData("0,0,0,600")]
    [InlineData("0,0,800,-1")]
    public void Parse_rejects(string? stored) => Assert.Null(PixelRect.Parse(stored));

    [Fact]
    public void Stored_round_trips()
    {
        var rect = new PixelRect(-1600, 40, 1280, 800);
        Assert.Equal("-1600,40,1280,800", rect.Stored());
        Assert.Equal(rect, PixelRect.Parse(rect.Stored()));
    }

    [Fact]
    public void Preferences_remember_the_window()
    {
        var prefs = new AppPreferences(new InMemoryPreferences());
        Assert.Null(prefs.WindowBounds);
        prefs.WindowBounds = new PixelRect(10, 20, 1280, 800);
        Assert.Equal(new PixelRect(10, 20, 1280, 800), prefs.WindowBounds);
        prefs.WindowBounds = null;
        Assert.Null(prefs.WindowBounds);
    }
}
