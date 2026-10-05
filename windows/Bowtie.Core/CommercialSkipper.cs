namespace Bowtie.Core;

/// <summary>
/// "Skip ad" for one playback of a recording (same rules as the web, Android
/// and Apple apps).
///
/// Holds the cleaned-up breaks and remembers which ones were already
/// skipped, so auto-skip happens at most once per break: seeking back into a
/// skipped one plays it (Skip ad still offers to skip it). Time comes from
/// the player; nothing here runs on its own. Not thread-safe (UI thread).
/// </summary>
public sealed class CommercialSkipper
{
    private readonly HashSet<int> _handled = new();

    public CommercialSkipper(IEnumerable<Commercial>? commercials)
    {
        Segments = Normalize(commercials);
    }

    /// <summary>Sorted, non-overlapping, non-empty breaks.</summary>
    public IReadOnlyList<Commercial> Segments { get; }

    /// <summary>The break playing at <paramref name="timeSec"/>, if any.</summary>
    public Commercial? Active(double timeSec) => IndexAt(timeSec) is { } i ? Segments[i] : null;

    /// <summary>Skip ad pressed: where to seek (the break's end); also counts as skipped for auto-skip.</summary>
    public double? Skip(double timeSec)
    {
        if (IndexAt(timeSec) is not { } i) return null;
        _handled.Add(i);
        return Segments[i].End;
    }

    /// <summary>Auto-skip: where to seek the first time playback is inside a break; null once it was skipped.</summary>
    public double? AutoSkipTarget(double timeSec)
    {
        if (IndexAt(timeSec) is not { } i) return null;
        if (!_handled.Add(i)) return null;
        return Segments[i].End;
    }

    private int? IndexAt(double timeSec)
    {
        if (!double.IsFinite(timeSec)) return null;
        for (var i = 0; i < Segments.Count; i++)
        {
            if (Segments[i].Start <= timeSec && timeSec < Segments[i].End) return i;
        }
        return null;
    }

    /// <summary>
    /// Drops empty, backwards and non-finite breaks, clamps a negative start
    /// to 0, sorts by start and merges breaks that overlap or touch.
    /// </summary>
    public static IReadOnlyList<Commercial> Normalize(IEnumerable<Commercial>? commercials)
    {
        var clean = (commercials ?? Array.Empty<Commercial>())
            .Where(c => c != null && double.IsFinite(c.Start) && double.IsFinite(c.End))
            .Select(c => new Commercial(Math.Max(0, c.Start), c.End))
            .Where(c => c.End > c.Start)
            .OrderBy(c => c.Start)
            .ThenBy(c => c.End);
        var merged = new List<Commercial>();
        foreach (var seg in clean)
        {
            if (merged.Count > 0 && seg.Start <= merged[^1].End)
            {
                var last = merged[^1];
                merged[^1] = new Commercial(last.Start, Math.Max(last.End, seg.End));
            }
            else
            {
                merged.Add(seg);
            }
        }
        return merged;
    }
}
