using System.Text.Json;
using System.Text.Json.Serialization;

namespace Bowtie.Core;

/// <summary>
/// Shared JSON settings for the viewer API (docs/api/openapi.yaml): camelCase
/// names, unknown fields ignored (the server sends more than these models
/// hold), and defaults always written (positionSec 0 and protected false
/// must reach the server).
/// </summary>
public static class BowtieJson
{
    public static readonly JsonSerializerOptions Options = new(JsonSerializerDefaults.Web)
    {
        DefaultIgnoreCondition = JsonIgnoreCondition.Never,
    };

    public static T Deserialize<T>(string json) =>
        JsonSerializer.Deserialize<T>(json, Options)
        ?? throw new JsonException($"Empty JSON for {typeof(T).Name}");

    public static string Serialize<T>(T value) => JsonSerializer.Serialize(value, Options);
}

public sealed record User
{
    public long Id { get; init; }
    public string Username { get; init; } = "";
    /// <summary>"admin" or "viewer".</summary>
    public string Role { get; init; } = "";
    /// <summary>Highest quality profile, or "" for no limit.</summary>
    public string MaxQuality { get; init; } = "";
    public int MaxStreams { get; init; }
    public int MaxTuners { get; init; }

    [JsonIgnore]
    public bool IsAdmin => Role == "admin";
}

public sealed record TokenPair
{
    public string AccessToken { get; init; } = "";
    public string RefreshToken { get; init; } = "";
    public User User { get; init; } = new();
}

/// <summary><c>POST /auth/device</c>: a quick sign-in code for a TV to show.</summary>
public sealed record DeviceSignIn
{
    /// <summary>Secret; only this device knows it. Polled with <c>/auth/device/token</c>.</summary>
    public string DeviceCode { get; init; } = "";
    /// <summary>What the person types on their phone ("BCDF-2345").</summary>
    public string UserCode { get; init; } = "";
    /// <summary>The web app's /link page with the code filled in.</summary>
    public string VerifyUrl { get; init; } = "";
    /// <summary>Server-relative PNG of <see cref="VerifyUrl"/> as a QR code; "" from older servers.</summary>
    public string QrUrl { get; init; } = "";
    /// <summary>Seconds the code lives.</summary>
    public int ExpiresIn { get; init; }
    /// <summary>Seconds between polls.</summary>
    public int Interval { get; init; }
}

/// <summary>One <c>/auth/device/token</c> poll.</summary>
public abstract record DevicePoll
{
    private DevicePoll() { }

    /// <summary>428: not approved yet.</summary>
    public sealed record Pending : DevicePoll;

    /// <summary>410: expired, used or unknown; start over with a new code.</summary>
    public sealed record Expired : DevicePoll;

    /// <summary>200: approved; the client now holds the session, as after a password sign-in.</summary>
    public sealed record SignedIn(User User) : DevicePoll;
}

/// <summary>A channel the viewer may watch (OpenAPI ViewerChannel).</summary>
public sealed record Channel
{
    public long Id { get; init; }
    public string GuideNumber { get; init; } = "";
    public string Name { get; init; } = "";
    public string LogoUrl { get; init; } = "";
    /// <summary>"ok", "noSignal" or "unknown"; null from older servers.</summary>
    public string? Reception { get; init; }
    /// <summary>The caller starred this channel; null from servers without favorites (hide the star).</summary>
    public bool? Favorite { get; init; }

    [JsonIgnore]
    public bool HasNoSignal => Reception == "noSignal";

    [JsonIgnore]
    public bool IsFavorite => Favorite == true;
}

/// <summary>A channel the caller watched recently, newest first.</summary>
public sealed record RecentChannel
{
    public long ChannelId { get; init; }
    public string GuideNumber { get; init; } = "";
    public string Name { get; init; } = "";
    public string LogoUrl { get; init; } = "";
    public DateTimeOffset WatchedAt { get; init; }
}

/// <summary>A guide program's DVR mark: which recording covers it, and its state.</summary>
public sealed record GuideRecordingMark
{
    public long Id { get; init; }
    public string State { get; init; } = "";
}

public sealed record GuideProgram
{
    public DateTimeOffset Start { get; init; }
    public DateTimeOffset Stop { get; init; }
    public string Title { get; init; } = "";
    public string Subtitle { get; init; } = "";
    public string Description { get; init; } = "";
    /// <summary>Raw guide category; newer servers join several with "; ".</summary>
    public string Category { get; init; } = "";
    /// <summary>Guide program ID of the episode (Schedules Direct: MV… movies, SP… sports).</summary>
    public string? ProgramId { get; init; }
    /// <summary>First airing; null when the guide doesn't say.</summary>
    public bool? IsNew { get; init; }
    /// <summary>Rating from the guide (e.g. "TV-14"); "" = not rated.</summary>
    public string Rating { get; init; } = "";
    /// <summary>Parental controls block this program for the caller.</summary>
    public bool Locked { get; init; }
    public GuideRecordingMark? Recording { get; init; }
}

public sealed record GuideChannel
{
    public long ChannelId { get; init; }
    public string GuideNumber { get; init; } = "";
    public string Name { get; init; } = "";
    public string LogoUrl { get; init; } = "";
    public IReadOnlyList<GuideProgram> Programs { get; init; } = Array.Empty<GuideProgram>();
    public bool? Favorite { get; init; }
}

/// <summary>What this device can decode; sent with every session create.</summary>
public sealed record ClientCaps
{
    public IReadOnlyList<string> VideoCodecs { get; init; } = new[] { "h264" };
    public IReadOnlyList<string> AudioCodecs { get; init; } = new[] { "aac" };
    /// <summary>0 = no limit.</summary>
    public int MaxHeight { get; init; }
    /// <summary>Quality ladder name; "" = Auto.</summary>
    public string Profile { get; init; } = "";
}

public sealed record SessionInfoMeta
{
    public string VideoCodec { get; init; } = "";
    public string Profile { get; init; } = "";
    public string Backend { get; init; } = "";
    public string ChannelName { get; init; } = "";
}

public sealed record CreatedSession
{
    public string ViewerId { get; init; } = "";
    /// <summary>Server-relative playlist path carrying the stream token query.</summary>
    public string PlaylistUrl { get; init; } = "";
    public SessionInfoMeta? Session { get; init; }
}

/// <summary>Trimmed view of an active session for the tuners-busy screen.</summary>
public sealed record ActiveSessionSummary
{
    public string ChannelName { get; init; } = "";
    public IReadOnlyList<ViewerSummary> Viewers { get; init; } = Array.Empty<ViewerSummary>();
}

public sealed record ViewerSummary
{
    public string Username { get; init; } = "";
}

/// <summary>A DVR recording: scheduled, in progress, recorded or missed.</summary>
public sealed record Recording
{
    public const string Scheduled = "scheduled";
    public const string Waiting = "waiting";
    public const string InProgress = "recording";
    public const string Converting = "converting";
    public const string Ready = "ready";
    public const string Failed = "failed";

    public long Id { get; init; }
    public string Title { get; init; } = "";
    public string Subtitle { get; init; } = "";
    public string Description { get; init; } = "";
    public string Category { get; init; } = "";
    public long ChannelId { get; init; }
    public string ChannelName { get; init; } = "";
    public DateTimeOffset Start { get; init; }
    public DateTimeOffset Stop { get; init; }
    /// <summary>scheduled, waiting, recording, converting, ready or failed.</summary>
    public string State { get; init; } = "";
    /// <summary>More than a minute is missing.</summary>
    public bool Partial { get; init; }
    /// <summary>"", noTuner, noSignal, diskFull or error.</summary>
    public string Failure { get; init; } = "";
    public string FailureDetail { get; init; } = "";
    public int DurationSec { get; init; }
    public long SizeBytes { get; init; }
    /// <summary>Kept: never deleted automatically when space runs low.</summary>
    [JsonPropertyName("protected")]
    public bool IsProtected { get; init; }
    /// <summary>The caller's resume position.</summary>
    public int PositionSec { get; init; }
    /// <summary>When the caller's position was last saved; null from older servers (or never saved).</summary>
    public DateTimeOffset? PositionUpdatedAt { get; init; }
    public string ScheduledBy { get; init; } = "";
    /// <summary>The caller may stop, delete or keep it.</summary>
    public bool CanManage { get; init; }
    /// <summary>The program's rating when scheduled; "" = not rated.</summary>
    public string Rating { get; init; } = "";
    /// <summary>Parental controls block it for the caller (play is refused).</summary>
    public bool Locked { get; init; }
    /// <summary>
    /// Detected commercial breaks on the playback timeline; null when none
    /// were found, detection hasn't run, or the server doesn't detect them.
    /// </summary>
    public IReadOnlyList<Commercial>? Commercials { get; init; }
}

/// <summary>
/// One detected commercial break: seconds on the recording's playback
/// timeline, <see cref="Start"/> inclusive, <see cref="End"/> exclusive.
/// </summary>
public sealed record Commercial(double Start, double End);

/// <summary>POST /recordings/{id}/play: server-relative, token-signed VOD playlist.</summary>
public sealed record RecordingPlayback
{
    public string PlaylistUrl { get; init; } = "";
    public int PositionSec { get; init; }
    public int DurationSec { get; init; }
}

// ── Wire envelopes ───────────────────────────────────────────────────────────

internal sealed record LoginRequest(string Username, string Password);

internal sealed record RefreshRequest(string RefreshToken);

internal sealed record DeviceStartRequest(string DeviceName);

internal sealed record DeviceTokenRequest(string DeviceCode);

internal sealed record CreateSessionRequest(long ChannelId, ClientCaps Caps);

internal sealed record PositionRequest(int PositionSec);

internal sealed record ProtectRequest([property: JsonPropertyName("protected")] bool IsProtected);

internal sealed record ErrorBody
{
    public string? Error { get; init; }
}

internal sealed record TunersBusyBody
{
    public string? Error { get; init; }
    public IReadOnlyList<ActiveSessionSummary>? Sessions { get; init; }
    public int OtherInUse { get; init; }
}

internal sealed record RecordingConflictBody
{
    public string? Error { get; init; }
    public int TunerCount { get; init; }
    public IReadOnlyList<Recording>? Conflicts { get; init; }
}
