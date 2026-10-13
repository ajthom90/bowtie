using System.Globalization;
using System.Text.RegularExpressions;

namespace Bowtie.Core;

/// <summary>A coarse program category a guide filter chip selects.</summary>
public enum GuideBucket { Sports, Movies, News, Kids, New }

/// <summary>Guide category chips, in order: All · Sports · Movies · News · Kids · New.</summary>
public enum GuideFilter { All, Sports, Movies, News, Kids, New }

/// <summary>
/// Guide category filters.
///
/// The guide carries a raw category string per program (XMLTV
/// <c>&lt;category&gt;</c>, Schedules Direct genre, SiliconDust free guide;
/// newer servers join several with "; "). These rules map it to coarse
/// buckets; the same rules and test vectors live in the web
/// (<c>guideFilterModel.ts</c>), Android (<c>GuideFilter.kt</c>) and BowtieKit
/// (<c>GuideFilter.swift</c>) — keep them in step.
/// </summary>
public static class GuideFilters
{
    /// <summary>Chip order.</summary>
    public static readonly IReadOnlyList<GuideFilter> Chips = Shim.EnumValues<GuideFilter>();

    /// <summary>Sport words / phrases matched as whole words ("Sports talk", "College football").</summary>
    private static readonly string[] SportsPhrases =
    {
        "sport", "sports", "motorsport", "motorsports", "esports",
        "football", "basketball", "baseball", "soccer", "hockey", "golf", "tennis",
        "boxing", "wrestling", "racing", "volleyball", "softball", "lacrosse", "rugby",
        "cricket", "bowling", "skiing", "snowboarding", "skating", "gymnastics",
        "swimming", "cycling", "track field", "athletics", "olympics",
        "mixed martial arts", "mma", "rodeo", "curling", "billiards", "darts",
        "surfing", "triathlon", "polo", "handball", "badminton", "equestrian",
    };

    /// <summary>The whole piece must be one of these ("Movie review" is not a movie).</summary>
    private static readonly HashSet<string> MoviePieces = new(StringComparer.Ordinal)
    {
        "movie", "movies", "film", "films", "feature film", "tv movie", "made for tv movie",
        "motion picture",
    };

    private static readonly string[] NewsPhrases = { "news", "newsmagazine", "newscast", "weather" };

    /// <summary>
    /// "Family" is deliberately absent: family sitcoms and dramas are
    /// general-audience prime time, not children's programming.
    /// </summary>
    private static readonly string[] KidsPhrases =
    {
        "children", "childrens", "kids", "animated", "animation", "cartoon", "cartoons",
        "educational", "preschool",
    };

    /// <summary>Ratings that keep a program out of Kids (adult animation); letters/digits only.</summary>
    private static readonly HashSet<string> MatureRatings = new(StringComparer.Ordinal) { "tv14", "tvma", "r", "nc17", "x" };

    /// <summary>Schedules Direct program IDs: MV… movies, SP… sports events.</summary>
    private static readonly Regex SdMovie = new(@"^MV[0-9]{8}", RegexOptions.CultureInvariant);
    private static readonly Regex SdSports = new(@"^SP[0-9]{8}", RegexOptions.CultureInvariant);

    private static readonly Regex NonWord = new("[^a-z0-9]+", RegexOptions.CultureInvariant);

    /// <summary>The bucket a chip selects; null for All.</summary>
    public static GuideBucket? Bucket(this GuideFilter filter) => filter switch
    {
        GuideFilter.Sports => GuideBucket.Sports,
        GuideFilter.Movies => GuideBucket.Movies,
        GuideFilter.News => GuideBucket.News,
        GuideFilter.Kids => GuideBucket.Kids,
        GuideFilter.New => GuideBucket.New,
        _ => null,
    };

    public static string Label(this GuideFilter filter) => filter switch
    {
        GuideFilter.All => "All",
        GuideFilter.Sports => "Sports",
        GuideFilter.Movies => "Movies",
        GuideFilter.News => "News",
        GuideFilter.Kids => "Kids",
        GuideFilter.New => "New",
        _ => filter.ToString(),
    };

    /// <summary>"No sports on in this time window" ("" for All).</summary>
    public static string EmptyCopy(this GuideFilter filter) => filter switch
    {
        GuideFilter.Sports => "No sports on in this time window",
        GuideFilter.Movies => "No movies on in this time window",
        GuideFilter.News => "No news on in this time window",
        GuideFilter.Kids => "No kids' shows on in this time window",
        GuideFilter.New => "No new episodes on in this time window",
        _ => "",
    };

    /// <summary>Value kept in preferences ("sports"), same as the other apps.</summary>
    public static string Stored(this GuideFilter filter) => filter.ToString().ToLowerInvariant();

    /// <summary>Parse a stored value; unknown or missing → All.</summary>
    public static GuideFilter Parse(string? raw)
    {
        foreach (var f in Chips)
        {
            if (f.Stored() == raw) return f;
        }
        return GuideFilter.All;
    }

    /// <summary>Every bucket <paramref name="program"/> belongs to (may be several, or none).</summary>
    public static IReadOnlyCollection<GuideBucket> Buckets(GuideProgram program)
    {
        var output = new HashSet<GuideBucket>();
        foreach (var piece in (program.Category ?? "").Split(',', ';', '|'))
        {
            var w = Words(piece);
            if (w.Length == 0) continue;
            if (HasAny(w, SportsPhrases)) output.Add(GuideBucket.Sports);
            if (MoviePieces.Contains(w.Trim())) output.Add(GuideBucket.Movies);
            if (HasAny(w, NewsPhrases)) output.Add(GuideBucket.News);
            if (HasAny(w, KidsPhrases)) output.Add(GuideBucket.Kids);
        }
        var pid = program.ProgramId ?? "";
        if (SdMovie.IsMatch(pid)) output.Add(GuideBucket.Movies);
        if (SdSports.IsMatch(pid)) output.Add(GuideBucket.Sports);
        if (MatureRatings.Contains(NonWord.Replace((program.Rating ?? "").ToLowerInvariant(), ""))) output.Remove(GuideBucket.Kids);
        if (program.IsNew == true) output.Add(GuideBucket.New);
        return output;
    }

    /// <summary>All matches everything; otherwise the program is in the chip's bucket.</summary>
    public static bool Matches(this GuideFilter filter, GuideProgram program) =>
        filter.Bucket() is not { } bucket || Buckets(program).Contains(bucket);

    /// <summary>
    /// Keep the channel: some program overlapping [from, to) matches. All
    /// keeps every channel, even one without guide data.
    /// </summary>
    public static bool Matches(this GuideFilter filter, IEnumerable<GuideProgram> programs, DateTimeOffset from, DateTimeOffset to) =>
        filter == GuideFilter.All || programs.Any(p => Overlaps(p, from, to) && filter.Matches(p));

    /// <summary>The earliest matching program overlapping [from, to).</summary>
    public static GuideProgram? FirstMatch(this GuideFilter filter, IEnumerable<GuideProgram> programs, DateTimeOffset from, DateTimeOffset to) =>
        programs.Where(p => Overlaps(p, from, to) && filter.Matches(p)).OrderBy(p => p.Start).FirstOrDefault();

    /// <summary>How a now/next row reads: which lines stay bright, and a later match when neither does.</summary>
    public sealed record RowHighlight(bool NowMatches, bool NextMatches, GuideProgram? Later);

    public static RowHighlight Highlight(
        this GuideFilter filter,
        GuideLogic.NowNext nowNext,
        IEnumerable<GuideProgram> programs,
        DateTimeOffset from,
        DateTimeOffset to)
    {
        if (filter == GuideFilter.All) return new RowHighlight(true, true, null);
        var nowMatches = nowNext.Now is { } now && filter.Matches(now);
        var nextMatches = nowNext.Next is { } next && filter.Matches(next);
        var later = nowMatches || nextMatches ? null : filter.FirstMatch(programs, from, to);
        return new RowHighlight(nowMatches, nextMatches, later);
    }

    /// <summary>"Later: Title · 8:00 PM" for a match that isn't on now or next (invariant 12-hour clock).</summary>
    public static string LaterLine(GuideProgram program, TimeZoneInfo zone)
    {
        var t = TimeZoneInfo.ConvertTime(program.Start, zone);
        var h = t.Hour % 12 == 0 ? 12 : t.Hour % 12;
        var clock = string.Format(CultureInfo.InvariantCulture, "{0}:{1:00} {2}", h, t.Minute, t.Hour < 12 ? "AM" : "PM");
        return $"Later: {program.Title} · {clock}";
    }

    /// <summary>" word word " for whole-word phrase checks ("" when no words).</summary>
    private static string Words(string piece)
    {
        var ws = NonWord.Split(piece.ToLowerInvariant()).Where(w => w.Length > 0).ToList();
        return ws.Count == 0 ? "" : " " + string.Join(" ", ws) + " ";
    }

    private static bool HasAny(string padded, IEnumerable<string> phrases) =>
        phrases.Any(p => padded.Contains(" " + p + " ", StringComparison.Ordinal));

    private static bool Overlaps(GuideProgram p, DateTimeOffset from, DateTimeOffset to) =>
        p.Start < to && p.Stop > from;
}
