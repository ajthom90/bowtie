namespace Bowtie.Core;

/// <summary>
/// "Continue watching": recordings the caller started and hasn't finished.
/// Same rules as the web, iOS and Android apps:
/// ready, not parental-locked, with a known length; at least a minute
/// watched; more than two minutes still to go; most recently saved first
/// (recordings without a save time last, newest recording first); at most 10.
/// </summary>
public static class ContinueWatching
{
    /// <summary>At least a minute watched…</summary>
    public const int MinPositionSec = 60;

    /// <summary>…and more than two minutes still to go.</summary>
    public const int EndMarginSec = 120;

    public const int MaxItems = 10;

    public static bool IsEligible(Recording r) =>
        r.State == Recording.Ready
        && !r.Locked
        && r.DurationSec > 0
        && r.PositionSec >= MinPositionSec
        && r.PositionSec < r.DurationSec - EndMarginSec;

    /// <summary>The strip's items, in display order.</summary>
    public static IReadOnlyList<Recording> Items(IEnumerable<Recording> recordings) =>
        recordings
            .Where(IsEligible)
            // Nullable comparison puts null lowest, so descending sends unsaved ones last.
            .OrderByDescending(r => r.PositionUpdatedAt)
            .ThenByDescending(r => r.Start)
            .ThenByDescending(r => r.Id)
            .Take(MaxItems)
            .ToList();

    /// <summary>"less than a minute left", "23 min left", "1 hr left", "1 hr 5 min left".</summary>
    public static string RemainingText(Recording r)
    {
        var remaining = r.DurationSec - r.PositionSec;
        if (remaining < 60) return "less than a minute left";
        var hours = remaining / 3600;
        var minutes = remaining % 3600 / 60;
        if (hours == 0) return $"{minutes} min left";
        return minutes == 0 ? $"{hours} hr left" : $"{hours} hr {minutes} min left";
    }

    /// <summary>0…1 watched, for the card's progress bar.</summary>
    public static double Progress(Recording r) =>
        r.DurationSec <= 0 ? 0 : Shim.Clamp((double)r.PositionSec / r.DurationSec, 0, 1);

    /// <summary>Accessible name for a card: "Resume Title: Subtitle, 23 min left".</summary>
    public static string ResumeLabel(Recording r) => $"Resume {Name(r)}, {RemainingText(r)}";

    /// <summary>"Remove Title: Subtitle from Continue watching".</summary>
    public static string RemoveLabel(Recording r) => $"Remove {Name(r)} from Continue watching";

    private static string Name(Recording r) => r.Subtitle.Length > 0 ? $"{r.Title}: {r.Subtitle}" : r.Title;
}
