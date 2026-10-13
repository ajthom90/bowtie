using System.Globalization;
using Bowtie.Core.ViewModels;

namespace Bowtie.Core;

/// <summary>A sleep timer choice.</summary>
public enum SleepOption
{
    Off,
    Min15,
    Min30,
    Min45,
    Min60,
    Min90,
    Hours2,
    /// <summary>Live TV only, when the guide knows when the program ends.</summary>
    EndOfProgram,
}

/// <summary>Where the sleep timer stands.</summary>
public sealed record SleepStatus(SleepOption Option = SleepOption.Off, TimeSpan? Remaining = null)
{
    public bool IsActive => Remaining != null;

    /// <summary>In the last minute: show "Still watching?".</summary>
    public bool Warning => Remaining is { } left && left <= SleepTimer.WarningLead;
}

/// <summary>
/// Player sleep timer: stops playback after a chosen time, or when the live
/// program ends. One per player page — it survives channel changes there and
/// goes away with the player. Never persisted. Same rules as the Android and
/// Apple apps.
///
/// Time comes from the injected clock, and nothing happens on its own: the
/// player calls <see cref="Tick"/> (about once a second), which updates
/// <see cref="Status"/>, raises the warning in the last minute, and calls
/// <c>onFire</c> once at the end.
/// </summary>
public sealed class SleepTimer : ObservableObject
{
    /// <summary>The "Still watching?" prompt shows for this long before the timer fires.</summary>
    public static readonly TimeSpan WarningLead = TimeSpan.FromSeconds(60);

    /// <summary>Keep watching after "End of this program" adds this much.</summary>
    public static readonly TimeSpan EndOfProgramExtension = TimeSpan.FromMinutes(30);

    private readonly Func<DateTimeOffset> _now;
    private readonly Action _onFire;
    private DateTimeOffset? _deadline;
    private SleepStatus _status = new();

    /// <param name="onFire">Called once when the timer runs out: stop playback and leave the player.</param>
    /// <param name="now">Clock (tests inject a fake one).</param>
    public SleepTimer(Action onFire, Func<DateTimeOffset>? now = null)
    {
        _onFire = onFire;
        _now = now ?? (() => DateTimeOffset.UtcNow);
    }

    public SleepStatus Status
    {
        get => _status;
        private set => SetProperty(ref _status, value);
    }

    /// <summary>
    /// Start (or restart) with <paramref name="option"/>; Off cancels. Returns
    /// false — and leaves the timer off — for End of this program without a
    /// future end.
    /// </summary>
    public bool Start(SleepOption option, DateTimeOffset? programEnd = null)
    {
        var now = _now();
        switch (option)
        {
            case SleepOption.Off:
                Cancel();
                return true;
            case SleepOption.EndOfProgram:
                if (programEnd is not { } end || end <= now)
                {
                    Cancel();
                    return false;
                }
                _deadline = end;
                break;
            default:
                _deadline = now + Duration(option)!.Value;
                break;
        }
        Status = new SleepStatus(option);
        Refresh(now);
        return true;
    }

    /// <summary>Keep watching: push the end out by the chosen duration (30 minutes for End of this program).</summary>
    public void Extend()
    {
        if (_deadline is not { } deadline) return;
        var extra = Status.Option switch
        {
            SleepOption.Off => (TimeSpan?)null,
            SleepOption.EndOfProgram => EndOfProgramExtension,
            var o => Duration(o),
        };
        if (extra is not { } add) return;
        _deadline = deadline + add;
        Refresh(_now());
    }

    public void Cancel()
    {
        _deadline = null;
        Status = new SleepStatus();
    }

    /// <summary>Advance to the current time; fires (once) when the time is up.</summary>
    public void Tick()
    {
        if (_deadline == null) return;
        Refresh(_now());
    }

    private void Refresh(DateTimeOffset now)
    {
        if (_deadline is not { } deadline) return;
        var left = deadline - now;
        if (left <= TimeSpan.Zero)
        {
            Cancel();
            _onFire();
            return;
        }
        Status = Status with { Remaining = left };
    }

    /// <summary>Fixed length of a timed option; null for Off and End of this program.</summary>
    public static TimeSpan? Duration(SleepOption option) => option switch
    {
        SleepOption.Min15 => TimeSpan.FromMinutes(15),
        SleepOption.Min30 => TimeSpan.FromMinutes(30),
        SleepOption.Min45 => TimeSpan.FromMinutes(45),
        SleepOption.Min60 => TimeSpan.FromMinutes(60),
        SleepOption.Min90 => TimeSpan.FromMinutes(90),
        SleepOption.Hours2 => TimeSpan.FromHours(2),
        _ => null,
    };

    public static string Label(SleepOption option) => option switch
    {
        SleepOption.Off => "Off",
        SleepOption.Min15 => "15 minutes",
        SleepOption.Min30 => "30 minutes",
        SleepOption.Min45 => "45 minutes",
        SleepOption.Min60 => "60 minutes",
        SleepOption.Min90 => "90 minutes",
        SleepOption.Hours2 => "2 hours",
        SleepOption.EndOfProgram => "End of this program",
        _ => option.ToString(),
    };

    /// <summary>Choices to offer: End of this program only when its end is known and still ahead.</summary>
    public static IReadOnlyList<SleepOption> Options(DateTimeOffset? programEnd, DateTimeOffset now) =>
        Shim.EnumValues<SleepOption>()
            .Where(o => o != SleepOption.EndOfProgram || (programEnd is { } end && end > now))
            .ToList();

    /// <summary>"1:00", "29:41", "1:05:00". Rounds up, so the last second reads 0:01.</summary>
    public static string Format(TimeSpan remaining)
    {
        var ms = Math.Max(0L, (long)Math.Ceiling(remaining.TotalMilliseconds));
        var total = (ms + 999) / 1000;
        var hours = total / 3600;
        var minutes = total % 3600 / 60;
        var secs = total % 60;
        return hours > 0
            ? string.Format(CultureInfo.InvariantCulture, "{0}:{1:00}:{2:00}", hours, minutes, secs)
            : string.Format(CultureInfo.InvariantCulture, "{0}:{1:00}", minutes, secs);
    }

    /// <summary>"Still watching? Sleeping in 0:59".</summary>
    public static string PromptText(TimeSpan remaining) => $"Still watching? Sleeping in {Format(remaining)}";

    /// <summary>The player's button label: "Sleep", or the time left.</summary>
    public static string ButtonLabel(SleepStatus status) =>
        status.Remaining is { } left ? Format(left) : "Sleep";
}
