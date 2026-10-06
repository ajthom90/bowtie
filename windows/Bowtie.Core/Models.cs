using System.Text.Json;
using System.Text.Json.Serialization;

namespace Bowtie.Core;

/// <summary>
/// Shared JSON settings for the viewer API (docs/api/openapi.yaml): camelCase
/// names, unknown fields ignored (the server sends more than these models
/// hold), and defaults always written (positionSec 0 and protected false
/// must reach the server).
///
/// Metadata comes only from the source-generated <see cref="BowtieJsonContext"/>,
/// never reflection: the UWP Xbox app runs under .NET Native, where reflection
/// over generic instantiations fails at run time. A type missing from the
/// context throws (tests catch that), so add new wire types there.
/// </summary>
public static class BowtieJson
{
    public static readonly JsonSerializerOptions Options = new(JsonSerializerDefaults.Web)
    {
        DefaultIgnoreCondition = JsonIgnoreCondition.Never,
        TypeInfoResolver = BowtieJsonContext.Default,
    };

    public static T Deserialize<T>(string json) =>
        JsonSerializer.Deserialize<T>(json, Options)
        ?? throw new JsonException($"Empty JSON for {typeof(T).Name}");

    public static string Serialize<T>(T value) => JsonSerializer.Serialize(value, Options);
}

public sealed record User
{
    public long Id { get; set; }
    public string Username { get; set; } = "";
    /// <summary>"admin" or "viewer".</summary>
    public string Role { get; set; } = "";
    /// <summary>Highest quality profile, or "" for no limit.</summary>
    public string MaxQuality { get; set; } = "";
    public int MaxStreams { get; set; }
    public int MaxTuners { get; set; }

    [JsonIgnore]
    public bool IsAdmin => Role == "admin";
}

public sealed record TokenPair
{
    public string AccessToken { get; set; } = "";
    public string RefreshToken { get; set; } = "";
    public User User { get; set; } = new();
}

/// <summary><c>POST /auth/device</c>: a quick sign-in code for a TV to show.</summary>
public sealed record DeviceSignIn
{
    /// <summary>Secret; only this device knows it. Polled with <c>/auth/device/token</c>.</summary>
    public string DeviceCode { get; set; } = "";
    /// <summary>What the person types on their phone ("BCDF-2345").</summary>
    public string UserCode { get; set; } = "";
    /// <summary>The web app's /link page with the code filled in.</summary>
    public string VerifyUrl { get; set; } = "";
    /// <summary>Server-relative PNG of <see cref="VerifyUrl"/> as a QR code; "" from older servers.</summary>
    public string QrUrl { get; set; } = "";
    /// <summary>Seconds the code lives.</summary>
    public int ExpiresIn { get; set; }
    /// <summary>Seconds between polls.</summary>
    public int Interval { get; set; }
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
    public long Id { get; set; }
    public string GuideNumber { get; set; } = "";
    public string Name { get; set; } = "";
    public string LogoUrl { get; set; } = "";
    /// <summary>"ok", "noSignal" or "unknown"; null from older servers.</summary>
    public string? Reception { get; set; }
    /// <summary>The caller starred this channel; null from servers without favorites (hide the star).</summary>
    public bool? Favorite { get; set; }
    /// <summary>
    /// False when every tuner on its HDHomeRun is busy with other channels
    /// and nobody in Bowtie is on this one (starting it would fail); null
    /// from older servers.
    /// </summary>
    public bool? Watchable { get; set; }

    [JsonIgnore]
    public bool HasNoSignal => Reception == "noSignal";

    [JsonIgnore]
    public bool IsFavorite => Favorite == true;

    /// <summary>A missing <see cref="Watchable"/> (older servers) counts as watchable.</summary>
    [JsonIgnore]
    public bool IsWatchable => Watchable != false;
}

/// <summary>
/// Antenna reception of the tuner feeding a live session (heartbeat
/// <c>?signal=1</c>). <see cref="Weak"/> needs two bad readings in a row on
/// the server, so it doesn't flicker.
/// </summary>
public sealed record ReceptionSignal
{
    public int Strength { get; set; }
    public int Quality { get; set; }
    public int SymbolQuality { get; set; }
    public bool Weak { get; set; }
}

/// <summary>A channel the caller watched recently, newest first.</summary>
public sealed record RecentChannel
{
    public long ChannelId { get; set; }
    public string GuideNumber { get; set; } = "";
    public string Name { get; set; } = "";
    public string LogoUrl { get; set; } = "";
    public DateTimeOffset WatchedAt { get; set; }
}

/// <summary>A guide program's DVR mark: which recording covers it, and its state.</summary>
public sealed record GuideRecordingMark
{
    public long Id { get; set; }
    public string State { get; set; } = "";
}

public sealed record GuideProgram
{
    public DateTimeOffset Start { get; set; }
    public DateTimeOffset Stop { get; set; }
    public string Title { get; set; } = "";
    public string Subtitle { get; set; } = "";
    public string Description { get; set; } = "";
    /// <summary>Raw guide category; newer servers join several with "; ".</summary>
    public string Category { get; set; } = "";
    /// <summary>Guide program ID of the episode (Schedules Direct: MV… movies, SP… sports).</summary>
    public string? ProgramId { get; set; }
    /// <summary>First airing; null when the guide doesn't say.</summary>
    public bool? IsNew { get; set; }
    /// <summary>Rating from the guide (e.g. "TV-14"); "" = not rated.</summary>
    public string Rating { get; set; } = "";
    /// <summary>Parental controls block this program for the caller.</summary>
    public bool Locked { get; set; }
    public GuideRecordingMark? Recording { get; set; }
}

public sealed record GuideChannel
{
    public long ChannelId { get; set; }
    public string GuideNumber { get; set; } = "";
    public string Name { get; set; } = "";
    public string LogoUrl { get; set; } = "";
    public IReadOnlyList<GuideProgram> Programs { get; set; } = Array.Empty<GuideProgram>();
    public bool? Favorite { get; set; }
}

/// <summary>What this device can decode; sent with every session create.</summary>
public sealed record ClientCaps
{
    public IReadOnlyList<string> VideoCodecs { get; set; } = new[] { "h264" };
    public IReadOnlyList<string> AudioCodecs { get; set; } = new[] { "aac" };
    /// <summary>0 = no limit.</summary>
    public int MaxHeight { get; set; }
    /// <summary>Quality ladder name; "" = Auto.</summary>
    public string Profile { get; set; } = "";
}

public sealed record SessionInfoMeta
{
    public string VideoCodec { get; set; } = "";
    public string Profile { get; set; } = "";
    public string Backend { get; set; } = "";
    public string ChannelName { get; set; } = "";
}

public sealed record CreatedSession
{
    public string ViewerId { get; set; } = "";
    /// <summary>Server-relative playlist path carrying the stream token query.</summary>
    public string PlaylistUrl { get; set; } = "";
    public SessionInfoMeta? Session { get; set; }
}

/// <summary>Trimmed view of an active session for the tuners-busy screen.</summary>
public sealed record ActiveSessionSummary
{
    public string ChannelName { get; set; } = "";
    public IReadOnlyList<ViewerSummary> Viewers { get; set; } = Array.Empty<ViewerSummary>();
}

public sealed record ViewerSummary
{
    public string Username { get; set; } = "";
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

    public long Id { get; set; }
    public string Title { get; set; } = "";
    public string Subtitle { get; set; } = "";
    public string Description { get; set; } = "";
    public string Category { get; set; } = "";
    public long ChannelId { get; set; }
    public string ChannelName { get; set; } = "";
    public DateTimeOffset Start { get; set; }
    public DateTimeOffset Stop { get; set; }
    /// <summary>scheduled, waiting, recording, converting, ready or failed.</summary>
    public string State { get; set; } = "";
    /// <summary>More than a minute is missing.</summary>
    public bool Partial { get; set; }
    /// <summary>"", noTuner, noSignal, diskFull or error.</summary>
    public string Failure { get; set; } = "";
    public string FailureDetail { get; set; } = "";
    public int DurationSec { get; set; }
    public long SizeBytes { get; set; }
    /// <summary>Kept: never deleted automatically when space runs low.</summary>
    [JsonPropertyName("protected")]
    public bool IsProtected { get; set; }
    /// <summary>The caller's resume position.</summary>
    public int PositionSec { get; set; }
    /// <summary>When the caller's position was last saved; null from older servers (or never saved).</summary>
    public DateTimeOffset? PositionUpdatedAt { get; set; }
    public string ScheduledBy { get; set; } = "";
    /// <summary>The caller may stop, delete or keep it.</summary>
    public bool CanManage { get; set; }
    /// <summary>The program's rating when scheduled; "" = not rated.</summary>
    public string Rating { get; set; } = "";
    /// <summary>Parental controls block it for the caller (play is refused).</summary>
    public bool Locked { get; set; }
    /// <summary>
    /// Detected commercial breaks on the playback timeline; null when none
    /// were found, detection hasn't run, or the server doesn't detect them.
    /// </summary>
    public IReadOnlyList<Commercial>? Commercials { get; set; }
}

/// <summary>
/// One detected commercial break: seconds on the recording's playback
/// timeline, <see cref="Start"/> inclusive, <see cref="End"/> exclusive.
/// </summary>
public sealed record Commercial(double Start, double End);

/// <summary>POST /recordings/{id}/play: server-relative, token-signed VOD playlist.</summary>
public sealed record RecordingPlayback
{
    public string PlaylistUrl { get; set; } = "";
    public int PositionSec { get; set; }
    public int DurationSec { get; set; }
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
    public string? Error { get; set; }
}

internal sealed record HeartbeatBody
{
    public ReceptionSignal? Signal { get; set; }
}

internal sealed record TunersBusyBody
{
    public string? Error { get; set; }
    public IReadOnlyList<ActiveSessionSummary>? Sessions { get; set; }
    public int OtherInUse { get; set; }
}

internal sealed record RecordingConflictBody
{
    public string? Error { get; set; }
    public int TunerCount { get; set; }
    public IReadOnlyList<Recording>? Conflicts { get; set; }
}

/// <summary>Source-generated JSON metadata for every type that crosses the wire (see <see cref="BowtieJson"/>).</summary>
[JsonSourceGenerationOptions(JsonSerializerDefaults.Web, DefaultIgnoreCondition = JsonIgnoreCondition.Never)]
[JsonSerializable(typeof(User))]
[JsonSerializable(typeof(TokenPair))]
[JsonSerializable(typeof(DeviceSignIn))]
[JsonSerializable(typeof(Channel))]
[JsonSerializable(typeof(List<Channel>))]
[JsonSerializable(typeof(RecentChannel))]
[JsonSerializable(typeof(List<RecentChannel>))]
[JsonSerializable(typeof(GuideProgram))]
[JsonSerializable(typeof(GuideChannel))]
[JsonSerializable(typeof(List<GuideChannel>))]
[JsonSerializable(typeof(ClientCaps))]
[JsonSerializable(typeof(CreatedSession))]
[JsonSerializable(typeof(Recording))]
[JsonSerializable(typeof(List<Recording>))]
[JsonSerializable(typeof(RecordingPlayback))]
[JsonSerializable(typeof(LoginRequest))]
[JsonSerializable(typeof(RefreshRequest))]
[JsonSerializable(typeof(DeviceStartRequest))]
[JsonSerializable(typeof(DeviceTokenRequest))]
[JsonSerializable(typeof(CreateSessionRequest))]
[JsonSerializable(typeof(PositionRequest))]
[JsonSerializable(typeof(ProtectRequest))]
[JsonSerializable(typeof(ErrorBody))]
[JsonSerializable(typeof(TunersBusyBody))]
[JsonSerializable(typeof(RecordingConflictBody))]
[JsonSerializable(typeof(Dictionary<string, string>))]
internal sealed partial class BowtieJsonContext : JsonSerializerContext
{
}
