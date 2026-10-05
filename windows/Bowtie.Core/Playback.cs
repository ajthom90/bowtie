using System.Globalization;

namespace Bowtie.Core;

/// <summary>
/// Capability set for session negotiation. The app probes the codecs
/// (Windows.Media.Core.CodecQuery); this pure part builds the caps so it can
/// be tested anywhere.
/// </summary>
public static class Caps
{
    /// <summary>
    /// h264 and aac always; hevc only with an HEVC decoder (the HEVC Video
    /// Extension is often missing on Windows); ac3/eac3 when decoders exist.
    /// maxHeight 2160 on displays taller than 1080, else 1080.
    /// </summary>
    public static ClientCaps Detect(bool hevc, bool ac3, bool eac3, int displayHeight)
    {
        var video = new List<string> { "h264" };
        if (hevc) video.Add("hevc");
        var audio = new List<string> { "aac" };
        if (ac3) audio.Add("ac3");
        if (eac3) audio.Add("eac3");
        return new ClientCaps
        {
            VideoCodecs = video,
            AudioCodecs = audio,
            MaxHeight = displayHeight > 1080 ? 2160 : 1080,
            Profile = "",
        };
    }
}

/// <summary>
/// Where live playback sits relative to the live edge. The server's live
/// playlist is a sliding window (the pause/rewind buffer), so the seekable
/// range is [start, end] in media seconds and "live" is
/// <c>end - liveOffset</c> (the player's recommended distance from the edge).
/// </summary>
public static class LiveEdge
{
    /// <summary>Drift (seconds) still shown as live; more than this is "behind".</summary>
    public const double Tolerance = 10;

    /// <summary>Default skip for the back / forward buttons.</summary>
    public const double SkipSeconds = 30;

    /// <summary>Exact out-of-window notice copy (spec B).</summary>
    public const string OutOfWindowNotice = "Jumped to live — paused longer than the buffer";

    /// <summary>Where "Go Live" seeks: the live point, never before the window start.</summary>
    public static double LiveTarget(double seekableStart, double seekableEnd, double liveOffset) =>
        Math.Max(seekableStart, Math.Max(0, seekableEnd - Math.Max(0, liveOffset)));

    public static double SecondsBehind(double seekableStart, double seekableEnd, double liveOffset, double current) =>
        Math.Max(0, LiveTarget(seekableStart, seekableEnd, liveOffset) - current);

    public static bool IsLive(double secondsBehind) => secondsBehind < Tolerance;

    /// <summary>"−1:30" for 90 seconds behind live.</summary>
    public static string BehindLabel(double secondsBehind)
    {
        var total = (int)Math.Floor(Math.Max(0, secondsBehind));
        return string.Format(CultureInfo.InvariantCulture, "−{0}:{1:00}", total / 60, total % 60);
    }

    /// <summary>
    /// Clamp a seek target into the window. Before the start (the buffer
    /// moved past a long pause) jumps to live and reports <c>Clamped</c> so
    /// the UI can show <see cref="OutOfWindowNotice"/>; past live clamps to live.
    /// </summary>
    public static (double Position, bool Clamped) ClampSeek(double target, double seekableStart, double liveTarget)
    {
        if (double.IsNaN(target) || double.IsInfinity(target)) return (liveTarget, true);
        if (target < seekableStart) return (liveTarget, true);
        if (target > liveTarget) return (liveTarget, true);
        return (target, false);
    }

    /// <summary>Skip back within the window; lands on the window start rather than leaving it.</summary>
    public static double SkipBack(double current, double seekableStart, double seconds = SkipSeconds) =>
        Math.Max(seekableStart, current - seconds);

    /// <summary>Skip forward, never past live.</summary>
    public static double SkipForward(double current, double liveTarget, double seconds = SkipSeconds) =>
        Math.Min(liveTarget, current + seconds);
}

/// <summary>One audio choice: <see cref="Index"/> is the player's track index.</summary>
public sealed record AudioChoice(int Index, string Label);

/// <summary>Audio and caption track labels for the player menus.</summary>
public static class TrackChoices
{
    /// <summary>
    /// One choice per label: the AAC and 5.1 copies of a language are one
    /// choice (the first index wins; the player picks a playable copy).
    /// </summary>
    public static IReadOnlyList<AudioChoice> Audio(IReadOnlyList<(string? Language, string? Label)> tracks)
    {
        var seen = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        var output = new List<AudioChoice>();
        for (var i = 0; i < tracks.Count; i++)
        {
            var label = TrackLabel(tracks[i].Language, tracks[i].Label, output.Count, "Audio");
            if (seen.Add(label)) output.Add(new AudioChoice(i, label));
        }
        return output;
    }

    /// <summary>Label first, then the language's English name, then "Audio N".</summary>
    public static string TrackLabel(string? language, string? label, int ordinal, string fallback)
    {
        if (!string.IsNullOrWhiteSpace(label)) return label!.Trim();
        if (!string.IsNullOrWhiteSpace(language)) return LanguageName(language!);
        return $"{fallback} {ordinal + 1}";
    }

    /// <summary>"en"/"eng"/"en-US" → "English"; unknown codes are shown upper-cased.</summary>
    public static string LanguageName(string code)
    {
        var primary = code.Split('-', '_')[0].ToLowerInvariant();
        return primary switch
        {
            "en" or "eng" => "English",
            "es" or "spa" => "Spanish",
            "fr" or "fra" or "fre" => "French",
            "de" or "deu" or "ger" => "German",
            "pt" or "por" => "Portuguese",
            "it" or "ita" => "Italian",
            "zh" or "zho" or "chi" => "Chinese",
            "ko" or "kor" => "Korean",
            "vi" or "vie" => "Vietnamese",
            "ja" or "jpn" => "Japanese",
            _ => code.ToUpperInvariant(),
        };
    }
}

/// <summary>Stream-token helpers for session playlists.</summary>
public static class StreamToken
{
    /// <summary>The decoded <c>token</c> query value of a playlist path or URL; null when absent.</summary>
    public static string? FromPlaylist(string playlistUrl)
    {
        var q = playlistUrl.IndexOf('?');
        if (q < 0) return null;
        foreach (var part in playlistUrl[(q + 1)..].Split('&'))
        {
            var eq = part.IndexOf('=');
            var key = eq < 0 ? part : part[..eq];
            if (key != "token") continue;
            var raw = eq < 0 ? "" : part[(eq + 1)..];
            string value;
            try
            {
                value = Uri.UnescapeDataString(raw.Replace('+', ' '));
            }
            catch (UriFormatException)
            {
                value = raw;
            }
            return value.Length == 0 ? null : value;
        }
        return null;
    }
}

/// <summary>Copy for the tuners-busy screen.</summary>
public static class TunersBusyCopy
{
    public const string Title = "All tuners are in use";

    /// <summary>Line naming tuners held by other apps (e.g. Plex); null when none.</summary>
    public static string? OtherAppsLine(int otherInUse)
    {
        if (otherInUse <= 0) return null;
        var tuners = otherInUse == 1 ? "1 tuner is" : $"{otherInUse} tuners are";
        return $"{tuners} in use by another app (like Plex).";
    }

    /// <summary>"News on 4.1 (alice, bob)" per active session.</summary>
    public static IReadOnlyList<string> SessionLines(IReadOnlyList<ActiveSessionSummary> sessions) =>
        sessions.Select(s =>
        {
            var names = s.Viewers.Select(v => v.Username).Where(n => n.Length > 0).Distinct().ToList();
            return names.Count == 0 ? s.ChannelName : $"{s.ChannelName} ({string.Join(", ", names)})";
        }).ToList();

    public static string Body(IReadOnlyList<ActiveSessionSummary> sessions, int otherInUse)
    {
        var lines = new List<string>();
        var watching = SessionLines(sessions);
        if (watching.Count > 0) lines.Add("Watching now: " + string.Join("; ", watching) + ".");
        if (OtherAppsLine(otherInUse) is { } other) lines.Add(other);
        lines.Add("Try again in a few minutes, or watch a channel someone is already watching.");
        return string.Join("\n", lines);
    }
}
