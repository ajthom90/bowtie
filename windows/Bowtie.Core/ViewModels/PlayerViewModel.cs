namespace Bowtie.Core.ViewModels;

/// <summary>Live player state.</summary>
public abstract record PlayerState
{
    private PlayerState() { }

    public sealed record Idle : PlayerState;
    public sealed record Starting : PlayerState;
    public sealed record Playing(CreatedSession Session) : PlayerState;
    /// <summary>Buffering while the session stays alive.</summary>
    public sealed record Stalled : PlayerState;
    public sealed record Failed(string Message) : PlayerState;
    public sealed record TunersBusy(IReadOnlyList<ActiveSessionSummary> Sessions, int OtherInUse) : PlayerState;
}

/// <summary>
/// Live session state machine (mirrors the Android PlayerViewModel):
/// <list type="bullet">
/// <item>Every create sends the device caps with <c>profile = SelectedProfile</c> ("" = Auto).</item>
/// <item>422: reset to Auto and retry once; a second 422 fails with the device-can't-play copy.</item>
/// <item>404: Failed and <see cref="ChannelsStale"/> (the list should refresh).</item>
/// <item>Mid-play 403 (<see cref="OnPlaybackAuthError"/>): one silent replace, then fail.</item>
/// <item>Zap / quality change: cancel in-flight → DELETE old → debounce → POST new.
///   A create that lands after a newer request is deleted so it can't leak a tuner.</item>
/// <item>Heartbeat every 15 s while the session is open (Playing or Stalled), with the stream token;
///   each answer carries the tuner's reception (<see cref="Signal"/>, <see cref="ShowWeakSignalNote"/>).</item>
/// <item><see cref="Stop"/> is for really leaving the player only.</item>
/// </list>
/// </summary>
public sealed class PlayerViewModel : ObservableObject, IDisposable
{
    public const string DeviceCantPlayMessage = "This device can't play this channel at that quality";
    public const string PlaybackAuthFailedMessage = ErrorText.StreamStopped;
    public const string WeakSignalNote = "Weak signal — the picture may break up.";
    public const string ChannelNotFoundMessage = "Channel not found";

    public static readonly TimeSpan DefaultHeartbeatInterval = TimeSpan.FromSeconds(15);
    public static readonly TimeSpan DefaultDebounce = TimeSpan.FromMilliseconds(400);

    private readonly BowtieClient _client;
    private readonly ClientCaps _caps;
    private readonly TimeSpan _debounce;
    private readonly TimeSpan _heartbeatInterval;
    private readonly Func<TimeSpan, CancellationToken, Task> _delay;

    private PlayerState _state = new PlayerState.Idle();
    private Channel? _channel;
    private bool _channelsStale;
    private string _selectedProfile = "";
    private ReceptionSignal? _signal;
    private int _heartbeatCount;

    private CancellationTokenSource? _replaceCts;
    private CancellationTokenSource? _heartbeatCts;
    private string? _activeViewerId;
    private CreatedSession? _lastSession;
    private long _generation;
    private bool _authFailureRetried;

    public PlayerViewModel(
        BowtieClient client,
        ClientCaps caps,
        string maxQuality = "",
        TimeSpan? debounce = null,
        TimeSpan? heartbeatInterval = null,
        Func<TimeSpan, CancellationToken, Task>? delay = null)
    {
        _client = client;
        _caps = caps;
        _debounce = debounce ?? DefaultDebounce;
        _heartbeatInterval = heartbeatInterval ?? DefaultHeartbeatInterval;
        _delay = delay ?? Task.Delay;
        AllowedProfiles = GuideLogic.AllowedProfiles(maxQuality);
    }

    public PlayerState State
    {
        get => _state;
        private set
        {
            if (SetProperty(ref _state, value))
            {
                OnPropertyChanged(nameof(PlaylistUri));
                OnPropertyChanged(nameof(ShowWeakSignalNote));
            }
        }
    }

    /// <summary>Reception from the latest heartbeat; null when unknown or no session is open.</summary>
    public ReceptionSignal? Signal
    {
        get => _signal;
        private set
        {
            if (SetProperty(ref _signal, value)) OnPropertyChanged(nameof(ShowWeakSignalNote));
        }
    }

    /// <summary>
    /// Show <see cref="WeakSignalNote"/>: the latest heartbeat said weak and
    /// the session is open. The server already needs two bad readings, so
    /// this follows each answer without smoothing.
    /// </summary>
    public bool ShowWeakSignalNote =>
        Signal is { Weak: true } && State is PlayerState.Playing or PlayerState.Stalled;

    /// <summary>Heartbeats answered so far (tests wait on it).</summary>
    internal int HeartbeatCount => Volatile.Read(ref _heartbeatCount);

    public Channel? CurrentChannel
    {
        get => _channel;
        private set => SetProperty(ref _channel, value);
    }

    /// <summary>True after a 404: the channel list should refresh. Clear with <see cref="ClearChannelsStale"/>.</summary>
    public bool ChannelsStale
    {
        get => _channelsStale;
        private set => SetProperty(ref _channelsStale, value);
    }

    /// <summary>"" = Auto.</summary>
    public string SelectedProfile
    {
        get => _selectedProfile;
        private set => SetProperty(ref _selectedProfile, value);
    }

    /// <summary>Profiles the user may pick (their maxQuality cap); Auto ("") is always offered too.</summary>
    public IReadOnlyList<string> AllowedProfiles { get; }

    /// <summary>Caps sent on every create: the device caps plus the selected profile.</summary>
    public ClientCaps EffectiveCaps => _caps with { Profile = SelectedProfile };

    /// <summary>Absolute playlist URL (with its stream token) while Playing.</summary>
    public Uri? PlaylistUri =>
        State is PlayerState.Playing p ? ServerUrl.Resolve(p.Session.PlaylistUrl, _client.Server) : null;

    /// <summary>The replace in flight (tests await it).</summary>
    public Task ReplaceTask { get; private set; } = Task.CompletedTask;

    /// <summary>The heartbeat loop (tests await it).</summary>
    public Task HeartbeatTask { get; private set; } = Task.CompletedTask;

    /// <summary>The DELETE started by <see cref="Stop"/> (tests await it).</summary>
    public Task StopTask { get; private set; } = Task.CompletedTask;

    /// <summary>Start (or zap to) a channel.</summary>
    public void Play(Channel channel)
    {
        CurrentChannel = channel;
        _authFailureRetried = false;
        ScheduleReplace();
    }

    /// <summary>Change quality; restarts the session on the current channel.</summary>
    public void SetProfile(string profile)
    {
        SelectedProfile = profile;
        if (CurrentChannel == null) return;
        _authFailureRetried = false;
        ScheduleReplace();
    }

    /// <summary>Retry after a failure or tuners-busy.</summary>
    public void Retry()
    {
        if (CurrentChannel is { } c) Play(c);
    }

    /// <summary>Really leaving: DELETE the session and go idle.</summary>
    public void Stop()
    {
        _replaceCts?.Cancel();
        _replaceCts = null;
        StopHeartbeat();
        _generation++;

        var viewerId = _activeViewerId;
        _activeViewerId = null;
        _lastSession = null;
        CurrentChannel = null;
        _authFailureRetried = false;
        Signal = null;

        StopTask = viewerId != null ? _client.DeleteSessionAsync(viewerId) : Task.CompletedTask;
        State = new PlayerState.Idle();
    }

    /// <summary>Playlist or segment 403 mid-play: one silent replace, then fail.</summary>
    public void OnPlaybackAuthError()
    {
        if (State is not (PlayerState.Playing or PlayerState.Stalled)) return;
        if (_authFailureRetried)
        {
            State = new PlayerState.Failed(PlaybackAuthFailedMessage);
            return;
        }
        _authFailureRetried = true;
        ScheduleReplace();
    }

    public void OnPlaybackStalled()
    {
        if (State is PlayerState.Playing or PlayerState.Stalled) State = new PlayerState.Stalled();
    }

    public void OnPlaybackRecovered()
    {
        if (_lastSession is { } s && State is PlayerState.Stalled) State = new PlayerState.Playing(s);
    }

    /// <summary>The player gave up; keep the session so Retry can start over.</summary>
    public void OnPlaybackFailed(string message) => State = new PlayerState.Failed(message);

    public void ClearChannelsStale() => ChannelsStale = false;

    public void Dispose()
    {
        _replaceCts?.Cancel();
        StopHeartbeat();
    }

    // ── Replace machine ─────────────────────────────────────────────────────

    private void ScheduleReplace()
    {
        _replaceCts?.Cancel();
        _generation++;
        var gen = _generation;
        var cts = new CancellationTokenSource();
        _replaceCts = cts;
        ReplaceTask = PerformReplaceAsync(gen, cts.Token);
    }

    private bool IsCurrent(long gen) => gen == _generation;

    private async Task PerformReplaceAsync(long gen, CancellationToken ct)
    {
        try
        {
            var channel = CurrentChannel;
            if (channel == null) return;

            var oldViewerId = _activeViewerId;
            _activeViewerId = null;
            StopHeartbeat();
            Signal = null;
            State = new PlayerState.Starting();

            // Not cancellable: the old viewer must go even when the user zaps again.
            if (oldViewerId != null) await _client.DeleteSessionAsync(oldViewerId);
            if (!IsCurrent(gen)) return;

            await _delay(_debounce, ct);
            if (!IsCurrent(gen)) return;

            await CreateSessionAsync(channel, gen, isRetry: false, ct);
        }
        catch (OperationCanceledException)
        {
            // Superseded by a newer replace or Stop.
        }
    }

    private async Task CreateSessionAsync(Channel channel, long gen, bool isRetry, CancellationToken ct)
    {
        CreatedSession session;
        try
        {
            session = await _client.CreateSessionAsync(channel.Id, EffectiveCaps, ct);
        }
        catch (OperationCanceledException)
        {
            throw;
        }
        catch (Exception e)
        {
            if (!IsCurrent(gen)) return;
            await HandleCreateErrorAsync(e, channel, gen, isRetry, ct);
            return;
        }

        if (!IsCurrent(gen))
        {
            // Orphaned success: tear it down so it can't hold a tuner.
            _ = _client.DeleteSessionAsync(session.ViewerId);
            return;
        }
        _activeViewerId = session.ViewerId;
        _lastSession = session;
        ChannelsStale = false;
        State = new PlayerState.Playing(session);
        StartHeartbeat(session.ViewerId, session.PlaylistUrl);
    }

    private async Task HandleCreateErrorAsync(Exception error, Channel channel, long gen, bool isRetry, CancellationToken ct)
    {
        switch (error)
        {
            case NegotiationFailedException:
                SelectedProfile = "";
                if (!isRetry) await CreateSessionAsync(channel, gen, isRetry: true, ct);
                else State = new PlayerState.Failed(DeviceCantPlayMessage);
                break;
            case TunersBusyException busy:
                State = new PlayerState.TunersBusy(busy.Sessions, busy.OtherInUse);
                break;
            case NotFoundException:
                ChannelsStale = true;
                State = new PlayerState.Failed(ChannelNotFoundMessage);
                break;
            case UnauthorizedException:
                State = new PlayerState.Failed("Signed out");
                break;
            case NetworkException:
                State = new PlayerState.Failed(ErrorText.CantReachServer);
                break;
            default:
                // 429 account limits, 502 no signal, 500… carry the server's words.
                State = new PlayerState.Failed(ErrorText.For(error));
                break;
        }
    }

    // ── Heartbeats ──────────────────────────────────────────────────────────

    private void StartHeartbeat(string viewerId, string playlistUrl)
    {
        StopHeartbeat();
        var token = StreamToken.FromPlaylist(playlistUrl);
        if (token == null) return;
        var cts = new CancellationTokenSource();
        _heartbeatCts = cts;
        HeartbeatTask = LoopAsync(cts.Token);

        async Task LoopAsync(CancellationToken ct)
        {
            try
            {
                while (!ct.IsCancellationRequested)
                {
                    await _delay(_heartbeatInterval, ct);
                    // Keep beating while this viewer is open (Playing or Stalled).
                    if (_activeViewerId != viewerId) return;
                    var signal = await _client.HeartbeatAsync(viewerId, token, ct);
                    // A late answer for a viewer that's gone must not show its reception.
                    if (ct.IsCancellationRequested || _activeViewerId != viewerId) return;
                    Signal = signal;
                    Interlocked.Increment(ref _heartbeatCount);
                }
            }
            catch (OperationCanceledException)
            {
                // stopped
            }
        }
    }

    private void StopHeartbeat()
    {
        _heartbeatCts?.Cancel();
        _heartbeatCts = null;
    }
}
