using System.Globalization;

namespace Bowtie.Core;

/// <summary>Now/next derivation, guide-number order and the quality ladder.</summary>
public static class GuideLogic
{
    public sealed record NowNext(GuideProgram? Now, GuideProgram? Next);

    /// <summary>Quality ladder, high to low.</summary>
    public static readonly IReadOnlyList<string> QualityLadder = new[] { "original", "high", "medium", "low" };

    /// <summary>
    /// The program airing at <paramref name="at"/> (start &lt;= at &lt; stop) and
    /// the soonest one after it.
    /// </summary>
    public static NowNext ComputeNowNext(IEnumerable<GuideProgram> programs, DateTimeOffset at)
    {
        var list = programs as IReadOnlyList<GuideProgram> ?? programs.ToList();
        var now = list.FirstOrDefault(p => p.Start <= at && at < p.Stop);
        var next = now != null
            ? list.Where(p => p.Start >= now.Stop).OrderBy(p => p.Start).FirstOrDefault()
            : list.Where(p => p.Start > at).OrderBy(p => p.Start).FirstOrDefault();
        return new NowNext(now, next);
    }

    /// <summary>Progress through <paramref name="program"/> in [0, 1]; 0 when unknown.</summary>
    public static double Progress(GuideProgram? program, DateTimeOffset at)
    {
        if (program == null) return 0;
        var total = (program.Stop - program.Start).TotalMilliseconds;
        if (total <= 0) return 0;
        return Shim.Clamp((at - program.Start).TotalMilliseconds / total, 0, 1);
    }

    /// <summary>
    /// Profiles the user may pick under their <c>maxQuality</c> cap: "" or an
    /// unknown value unlocks the whole ladder; a rung allows itself and below.
    /// </summary>
    public static IReadOnlyList<string> AllowedProfiles(string? maxQuality)
    {
        if (string.IsNullOrEmpty(maxQuality)) return QualityLadder;
        var idx = IndexOf(QualityLadder, maxQuality!);
        return idx < 0 ? QualityLadder : QualityLadder.Skip(idx).ToList();
    }

    /// <summary>"Auto" for "", otherwise the capitalized rung ("High").</summary>
    public static string ProfileLabel(string profile) =>
        profile.Length == 0 ? "Auto" : char.ToUpperInvariant(profile[0]) + profile[1..];

    /// <summary>
    /// Orders guide numbers numerically part by part ("9.1" &lt; "11.1",
    /// "4.2" &lt; "4.10", "7" &lt; "7.1"); non-numeric parts compare as text.
    /// </summary>
    public static readonly IComparer<string> GuideNumberOrder = Comparer<string>.Create((a, b) =>
    {
        var pa = a.Split('.', '-');
        var pb = b.Split('.', '-');
        for (var i = 0; i < Math.Max(pa.Length, pb.Length); i++)
        {
            if (i >= pa.Length) return -1;
            if (i >= pb.Length) return 1;
            int c;
            if (long.TryParse(pa[i], NumberStyles.None, CultureInfo.InvariantCulture, out var x) &&
                long.TryParse(pb[i], NumberStyles.None, CultureInfo.InvariantCulture, out var y))
            {
                c = x.CompareTo(y);
            }
            else
            {
                c = string.CompareOrdinal(pa[i], pb[i]);
            }
            if (c != 0) return c;
        }
        return string.CompareOrdinal(a, b);
    });

    private static int IndexOf(IReadOnlyList<string> list, string value)
    {
        for (var i = 0; i < list.Count; i++)
        {
            if (list[i] == value) return i;
        }
        return -1;
    }
}
