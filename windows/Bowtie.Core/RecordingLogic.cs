using System.Globalization;

namespace Bowtie.Core;

/// <summary>Recordings screen tabs and their <c>GET /recordings?state=</c> filter.</summary>
public enum RecordingTab
{
    Upcoming,
    Recorded,
    Missed,
}

public static class RecordingTabExtensions
{
    public static string Query(this RecordingTab tab) => tab switch
    {
        RecordingTab.Upcoming => "upcoming",
        RecordingTab.Recorded => "recorded",
        RecordingTab.Missed => "failed",
        _ => throw new ArgumentOutOfRangeException(nameof(tab)),
    };

    public static string Title(this RecordingTab tab) => tab switch
    {
        RecordingTab.Upcoming => "Upcoming",
        RecordingTab.Recorded => "Recorded",
        RecordingTab.Missed => "Missed",
        _ => throw new ArgumentOutOfRangeException(nameof(tab)),
    };
}

public enum BadgeTone { Neutral, Live, Warn, Alert, Good }

public sealed record Badge(string Text, BadgeTone Tone);

public enum RecordingAction { Play, Stop, Keep, Delete }

/// <summary>
/// Pure DVR helpers (same copy as the Android, iOS and web apps): plain-words
/// labels, which actions a row offers, the resume decision, and formatting.
/// Times are formatted by hand with the invariant culture so output doesn't
/// depend on the machine's locale data.
/// </summary>
public static class RecordingLogic
{
    /// <summary>Seconds into a recording before resuming is worth offering.</summary>
    public const int ResumeMinSec = 10;

    /// <summary>Seconds before the end inside which a recording counts as watched.</summary>
    public const int ResumeEndMarginSec = 30;

    /// <summary>How often the player saves the resume position.</summary>
    public static readonly TimeSpan PositionSaveInterval = TimeSpan.FromSeconds(15);

    public static string? FailureLabel(string failure) => failure switch
    {
        "" => null,
        "noTuner" => "No tuner was free",
        "noSignal" => "The channel had no signal",
        "diskFull" => "The recordings disk was full",
        _ => "Something went wrong",
    };

    /// <summary>State badges for a row; a ready recording shows none unless partial or kept.</summary>
    public static IReadOnlyList<Badge> Badges(Recording r)
    {
        var output = new List<Badge>();
        switch (r.State)
        {
            case Recording.Scheduled: output.Add(new Badge("Scheduled", BadgeTone.Neutral)); break;
            case Recording.Waiting: output.Add(new Badge("Waiting for a tuner", BadgeTone.Warn)); break;
            case Recording.InProgress: output.Add(new Badge("Recording", BadgeTone.Live)); break;
            case Recording.Converting: output.Add(new Badge("Processing", BadgeTone.Neutral)); break;
            case Recording.Failed: output.Add(new Badge("Missed", BadgeTone.Alert)); break;
        }
        if (r.Partial) output.Add(new Badge("Partial", BadgeTone.Warn));
        if (r.IsProtected) output.Add(new Badge("Kept", BadgeTone.Good));
        if (LockLabel(r) is { } lockLabel) output.Add(new Badge(lockLabel, BadgeTone.Alert));
        return output;
    }

    /// <summary>A sentence explaining a missed, waiting or partial recording; null otherwise.</summary>
    public static string? StatusLine(Recording r) => r.State switch
    {
        Recording.Failed => "Missed: " + (FailureLabel(r.Failure) ?? "Something went wrong"),
        Recording.Waiting => r.Failure switch
        {
            "noTuner" => "No tuner is free yet. Still trying.",
            "noSignal" => "No signal yet. Still trying.",
            _ => "Still trying.",
        },
        _ => r.Partial ? "Part of this program is missing" : null,
    };

    /// <summary>"🔒 TV-MA" when parental controls block it for the caller; null otherwise.</summary>
    public static string? LockLabel(Recording r) =>
        r.Locked ? "🔒 " + (r.Rating.Length > 0 ? r.Rating : "Not rated") : null;

    /// <summary>Play when ready (and not locked by parental controls); stop/keep/delete only when the caller can manage it.</summary>
    public static IReadOnlyList<RecordingAction> Actions(Recording r)
    {
        var output = new List<RecordingAction>();
        if (r.State == Recording.Ready && !r.Locked) output.Add(RecordingAction.Play);
        if (!r.CanManage) return output;
        if (r.State == Recording.InProgress) output.Add(RecordingAction.Stop);
        if (r.State is Recording.Ready or Recording.Converting) output.Add(RecordingAction.Keep);
        output.Add(RecordingAction.Delete);
        return output;
    }

    /// <summary>"Cancel" before it starts; "Delete" once there's something to delete.</summary>
    public static string DeleteLabel(Recording r) =>
        r.State is Recording.Scheduled or Recording.Waiting ? "Cancel" : "Delete";

    public static string ActionLabel(Recording r, RecordingAction action) => action switch
    {
        RecordingAction.Play => "Play",
        RecordingAction.Stop => "Stop",
        RecordingAction.Keep => r.IsProtected ? "Don't keep" : "Keep",
        RecordingAction.Delete => DeleteLabel(r),
        _ => action.ToString(),
    };

    /// <summary>Deleting files is permanent, so confirm; cancelling a schedule isn't.</summary>
    public static bool DeleteNeedsConfirm(Recording r) => DeleteLabel(r) == "Delete";

    /// <summary>Length, saved position and size; who scheduled it when it isn't yours.</summary>
    public static string? DetailLine(Recording r)
    {
        var parts = new List<string>();
        if (r.State == Recording.Ready && r.DurationSec > 0)
        {
            parts.Add(FormatDuration(r.DurationSec));
            if (ShouldOfferResume(r.PositionSec, r.DurationSec))
            {
                parts.Add("stopped at " + FormatClock(r.PositionSec));
            }
        }
        if (r.SizeBytes > 0) parts.Add(FormatSize(r.SizeBytes));
        if (!r.CanManage && r.ScheduledBy.Length > 0) parts.Add("by " + r.ScheduledBy);
        return parts.Count == 0 ? null : string.Join(" · ", parts);
    }

    /// <summary>Offer "Resume" when past the first 10 s and not within the last 30 s.</summary>
    public static bool ShouldOfferResume(int positionSec, int durationSec) =>
        positionSec > ResumeMinSec && positionSec < durationSec - ResumeEndMarginSec;

    /// <summary>"0:05", "12:34", "1:02:03".</summary>
    public static string FormatClock(int sec)
    {
        var s = Math.Max(0, sec);
        var h = s / 3600;
        var m = s % 3600 / 60;
        var ss = s % 60;
        return h > 0
            ? string.Format(CultureInfo.InvariantCulture, "{0}:{1:00}:{2:00}", h, m, ss)
            : string.Format(CultureInfo.InvariantCulture, "{0}:{1:00}", m, ss);
    }

    /// <summary>"Under a minute", "45 min", "1 hr", "1 hr 5 min".</summary>
    public static string FormatDuration(int sec)
    {
        if (sec < 60) return "Under a minute";
        var h = sec / 3600;
        var m = sec % 3600 / 60;
        if (h == 0) return $"{m} min";
        return m == 0 ? $"{h} hr" : $"{h} hr {m} min";
    }

    /// <summary>Decimal units, as disks are sold: "850 MB", "1.9 GB".</summary>
    public static string FormatSize(long bytes)
    {
        var mb = bytes / 1_000_000.0;
        if (mb < 1) return "Under 1 MB";
        if (mb < 1000) return mb.ToString("0", CultureInfo.InvariantCulture) + " MB";
        return (mb / 1000).ToString("0.0", CultureInfo.InvariantCulture) + " GB";
    }

    /// <summary>"Today, 8:00–8:30 PM", "Tomorrow, 11:30 AM–12:30 PM", "Thu Oct 8, 7:05–8:00 PM".</summary>
    public static string FormatWhen(DateTimeOffset start, DateTimeOffset stop, DateTimeOffset now, TimeZoneInfo zone)
    {
        var s = TimeZoneInfo.ConvertTime(start, zone);
        var e = TimeZoneInfo.ConvertTime(stop, zone);
        var today = TimeZoneInfo.ConvertTime(now, zone).Date;
        var days = (s.Date - today).Days;
        var day = days switch
        {
            0 => "Today",
            1 => "Tomorrow",
            -1 => "Yesterday",
            _ => DateLabel(s),
        };
        var sameHalf = Meridiem(s) == Meridiem(e);
        var from = sameHalf ? Clock12(s) : $"{Clock12(s)} {Meridiem(s)}";
        return $"{day}, {from}–{Clock12(e)} {Meridiem(e)}";
    }

    /// <summary>Generic error copy for DVR actions.</summary>
    public static string ErrorMessage(Exception e) => e switch
    {
        UnauthorizedException => "Your session ended. Sign in again.",
        NotFoundException => "That recording is gone.",
        ServerException { Status: 403 } => "Only the person who scheduled it or an admin can change it.",
        ServerException { Status: 503 } => "Recording isn't available on this server.",
        ServerException s => s.Message,
        NetworkException => "Couldn't reach the server.",
        _ => string.IsNullOrEmpty(e.Message) ? "Something went wrong" : e.Message,
    };

    private static readonly string[] DayNames = { "Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat" };

    private static readonly string[] MonthNames =
        { "Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec" };

    private static string DateLabel(DateTimeOffset d) =>
        $"{DayNames[(int)d.DayOfWeek]} {MonthNames[d.Month - 1]} {d.Day}";

    private static string Meridiem(DateTimeOffset t) => t.Hour < 12 ? "AM" : "PM";

    private static string Clock12(DateTimeOffset t)
    {
        var h = t.Hour % 12 == 0 ? 12 : t.Hour % 12;
        return string.Format(CultureInfo.InvariantCulture, "{0}:{1:00}", h, t.Minute);
    }
}
