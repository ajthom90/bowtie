namespace Bowtie.Core.Tests;

/// <summary>Sleep timer on a fake clock: no real waiting. Same cases as Android's SleepTimerTest.</summary>
public class SleepTimerTests
{
    private static readonly TimeSpan Min = TimeSpan.FromMinutes(1);

    private DateTimeOffset _now = new(2026, 10, 4, 20, 0, 0, TimeSpan.Zero);
    private int _fired;
    private readonly SleepTimer _timer;

    public SleepTimerTests()
    {
        _timer = new SleepTimer(() => _fired++, () => _now);
    }

    private void Advance(TimeSpan by) => _now += by;

    private static TimeSpan Sec(double s) => TimeSpan.FromSeconds(s);

    [Fact]
    public void Starts_off()
    {
        var s = _timer.Status;
        Assert.Equal(SleepOption.Off, s.Option);
        Assert.Null(s.Remaining);
        Assert.False(s.IsActive);
        Assert.False(s.Warning);
    }

    [Fact]
    public void Remaining_time_counts_down()
    {
        Assert.True(_timer.Start(SleepOption.Min30));
        Assert.Equal(SleepOption.Min30, _timer.Status.Option);
        Assert.Equal(30 * Min, _timer.Status.Remaining);
        Advance(Sec(61));
        _timer.Tick();
        Assert.Equal(30 * Min - Sec(61), _timer.Status.Remaining);
        Assert.True(_timer.Status.IsActive);
    }

    [Fact]
    public void Status_changes_are_observable()
    {
        var changes = 0;
        _timer.PropertyChanged += (_, e) => { if (e.PropertyName == nameof(SleepTimer.Status)) changes++; };
        _timer.Start(SleepOption.Min15);
        Advance(Sec(1));
        _timer.Tick();
        Assert.True(changes >= 2);
    }

    [Fact]
    public void Warning_starts_sixty_seconds_before_firing()
    {
        _timer.Start(SleepOption.Min15);
        Advance(15 * Min - Sec(61));
        _timer.Tick();
        Assert.False(_timer.Status.Warning);
        Advance(Sec(1));
        _timer.Tick();
        Assert.True(_timer.Status.Warning);
        Assert.Equal(Sec(60), _timer.Status.Remaining);
        Assert.Equal(0, _fired);
    }

    [Fact]
    public void Extend_adds_the_chosen_duration()
    {
        _timer.Start(SleepOption.Min15);
        Advance(15 * Min - Sec(30));
        _timer.Tick();
        Assert.True(_timer.Status.Warning);
        _timer.Extend();
        Assert.False(_timer.Status.Warning);
        Assert.Equal(15 * Min + Sec(30), _timer.Status.Remaining);
        Assert.Equal(SleepOption.Min15, _timer.Status.Option);
        Advance(Sec(31));
        _timer.Tick();
        Assert.Equal(0, _fired); // the original deadline no longer fires
    }

    [Fact]
    public void Extend_end_of_program_adds_thirty_minutes()
    {
        _timer.Start(SleepOption.EndOfProgram, _now + Sec(45));
        Assert.True(_timer.Status.Warning);
        _timer.Extend();
        Assert.Equal(Sec(45) + 30 * Min, _timer.Status.Remaining);
    }

    [Fact]
    public void Extend_when_off_does_nothing()
    {
        _timer.Extend();
        Assert.Null(_timer.Status.Remaining);
    }

    [Fact]
    public void Cancel_stops_the_timer()
    {
        _timer.Start(SleepOption.Min45);
        _timer.Cancel();
        Assert.Equal(SleepOption.Off, _timer.Status.Option);
        Assert.Null(_timer.Status.Remaining);
        Advance(46 * Min);
        _timer.Tick();
        Assert.Equal(0, _fired);
    }

    [Fact]
    public void Starting_off_cancels()
    {
        _timer.Start(SleepOption.Min45);
        Assert.True(_timer.Start(SleepOption.Off));
        Assert.False(_timer.Status.IsActive);
    }

    [Fact]
    public void End_of_program_uses_the_program_end()
    {
        Assert.True(_timer.Start(SleepOption.EndOfProgram, _now + 22 * Min));
        Assert.Equal(SleepOption.EndOfProgram, _timer.Status.Option);
        Assert.Equal(22 * Min, _timer.Status.Remaining);
        Advance(22 * Min);
        _timer.Tick();
        Assert.Equal(1, _fired);
    }

    [Fact]
    public void End_of_program_is_unavailable_when_the_end_is_unknown_or_past()
    {
        Assert.DoesNotContain(SleepOption.EndOfProgram, SleepTimer.Options(null, _now));
        Assert.DoesNotContain(SleepOption.EndOfProgram, SleepTimer.Options(_now, _now));
        Assert.DoesNotContain(SleepOption.EndOfProgram, SleepTimer.Options(_now - Sec(1), _now));
        Assert.Equal(
            new[]
            {
                SleepOption.Off, SleepOption.Min15, SleepOption.Min30, SleepOption.Min45,
                SleepOption.Min60, SleepOption.Min90, SleepOption.Hours2, SleepOption.EndOfProgram,
            },
            SleepTimer.Options(_now + Min, _now));

        Assert.False(_timer.Start(SleepOption.EndOfProgram, null));
        Assert.False(_timer.Status.IsActive);
        Assert.False(_timer.Start(SleepOption.EndOfProgram, _now - Sec(1)));
        Assert.False(_timer.Status.IsActive);
    }

    [Fact]
    public void Fire_calls_stop_exactly_once()
    {
        _timer.Start(SleepOption.Min15);
        Advance(15 * Min);
        _timer.Tick();
        Assert.Equal(1, _fired);
        Assert.False(_timer.Status.IsActive);
        Assert.Equal(SleepOption.Off, _timer.Status.Option);
        Advance(Sec(5));
        _timer.Tick();
        _timer.Tick();
        Assert.Equal(1, _fired);
    }

    [Fact]
    public void Fires_when_the_tick_is_late()
    {
        _timer.Start(SleepOption.Min15);
        Advance(20 * Min); // e.g. the PC slept
        _timer.Tick();
        Assert.Equal(1, _fired);
    }

    [Fact]
    public void Labels()
    {
        Assert.Equal("Off", SleepTimer.Label(SleepOption.Off));
        Assert.Equal("15 minutes", SleepTimer.Label(SleepOption.Min15));
        Assert.Equal("60 minutes", SleepTimer.Label(SleepOption.Min60));
        Assert.Equal("90 minutes", SleepTimer.Label(SleepOption.Min90));
        Assert.Equal("2 hours", SleepTimer.Label(SleepOption.Hours2));
        Assert.Equal("End of this program", SleepTimer.Label(SleepOption.EndOfProgram));
    }

    [Fact]
    public void Format()
    {
        Assert.Equal("1:00", SleepTimer.Format(Sec(60)));
        Assert.Equal("1:00", SleepTimer.Format(TimeSpan.FromMilliseconds(59_400))); // rounds up
        Assert.Equal("0:05", SleepTimer.Format(Sec(5)));
        Assert.Equal("0:00", SleepTimer.Format(TimeSpan.Zero));
        Assert.Equal("29:41", SleepTimer.Format(Sec(29 * 60 + 41)));
        Assert.Equal("2:00:00", SleepTimer.Format(TimeSpan.FromHours(2)));
        Assert.Equal("1:01:05", SleepTimer.Format(Sec(3600 + 65)));
    }

    [Fact]
    public void Prompt_and_button_copy()
    {
        Assert.Equal("Still watching? Sleeping in 1:00", SleepTimer.PromptText(Sec(60)));
        Assert.Equal("Sleep", SleepTimer.ButtonLabel(new SleepStatus()));
        Assert.Equal("29:41", SleepTimer.ButtonLabel(new SleepStatus(SleepOption.Min30, Sec(29 * 60 + 41))));
    }
}
