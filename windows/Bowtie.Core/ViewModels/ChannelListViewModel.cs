namespace Bowtie.Core.ViewModels;

/// <summary>A channel row joined with the guide's now/next (and its programs in the loaded window, for filters).</summary>
public sealed record ChannelRow(Channel Channel, GuideLogic.NowNext NowNext, IReadOnlyList<GuideProgram>? Programs = null)
{
    public long Id => Channel.Id;
    public bool IsFavorite => Channel.IsFavorite;

    /// <summary>The channel's programs in the loaded guide window (empty without guide data).</summary>
    public IReadOnlyList<GuideProgram> GuidePrograms => Programs ?? Array.Empty<GuideProgram>();
}

public enum ChannelListStatus { Loading, Loaded, Empty, Failed }

/// <summary>
/// Channel list: channels joined with a 4-hour guide window, favorites
/// first (guide-number order) then the rest in server order, the Recent row,
/// optimistic star toggles that revert with a message when refused, and the
/// guide category chip (remembered per device).
/// A server without favorites omits <c>favorite</c>: stars and Recent hide.
/// Channels the server marks <c>watchable:false</c> (every tuner busy with
/// other channels) are left out of the visible lists with
/// <see cref="TunersNote"/>; <see cref="PollAsync"/> re-checks them.
/// Mirrors the Android ChannelListViewModel.
/// </summary>
public sealed class ChannelListViewModel : ObservableObject
{
    /// <summary>Guide request window: now … now+4h.</summary>
    public static readonly TimeSpan GuideWindow = TimeSpan.FromHours(4);

    /// <summary>Reload after this long (also the auto-refresh timer).</summary>
    public static readonly TimeSpan StaleInterval = TimeSpan.FromMinutes(5);

    /// <summary>How often the visible list re-checks which channels can start.</summary>
    public static readonly TimeSpan RecheckInterval = TimeSpan.FromSeconds(30);

    public const int RecentsLimit = 8;

    public const string SomeTunersBusyNote = "All tuners are in use — showing channels you can join.";
    public const string AllTunersBusyNote = "All tuners are in use. Try again in a few minutes.";

    private readonly BowtieClient _client;
    private readonly Func<DateTimeOffset> _now;
    private readonly AppPreferences _prefs;

    private ChannelListStatus _status = ChannelListStatus.Loading;
    private IReadOnlyList<ChannelRow> _rows = Array.Empty<ChannelRow>();
    private IReadOnlyList<RecentChannel> _recents = Array.Empty<RecentChannel>();
    private bool _favoritesSupported;
    private string? _error;
    private string? _message;
    private Dictionary<long, int> _serverOrder = new();
    private Dictionary<long, IReadOnlyList<GuideProgram>> _programs = new();
    private DateTimeOffset? _lastLoadedAt;
    private DateTimeOffset? _windowEnd;
    private GuideFilter _filter;

    public ChannelListViewModel(BowtieClient client, Func<DateTimeOffset>? now = null, AppPreferences? prefs = null)
    {
        _client = client;
        _now = now ?? (() => DateTimeOffset.UtcNow);
        _prefs = prefs ?? new AppPreferences(new InMemoryPreferences());
        _filter = _prefs.GuideFilter;
    }

    /// <summary>The guide category chip (All · Sports · Movies · News · Kids · New).</summary>
    public GuideFilter Filter
    {
        get => _filter;
        private set => SetProperty(ref _filter, value);
    }

    /// <summary>Pick a chip and remember it on this device.</summary>
    public void SetFilter(GuideFilter filter)
    {
        Filter = filter;
        _prefs.GuideFilter = filter;
    }

    /// <summary>End of the loaded guide window (its start is "now").</summary>
    private DateTimeOffset WindowEnd(DateTimeOffset at) => _windowEnd ?? at + GuideWindow;

    /// <summary>
    /// The watchable <paramref name="rows"/> with something matching <see cref="Filter"/>
    /// between <paramref name="at"/> and the end of the loaded window, order
    /// kept. All returns every row.
    /// </summary>
    public IReadOnlyList<ChannelRow> VisibleRows(IReadOnlyList<ChannelRow> rows, DateTimeOffset at)
    {
        var filter = Filter;
        var watchable = rows.Where(r => r.Channel.IsWatchable);
        if (filter == GuideFilter.All) return watchable.ToList();
        var to = WindowEnd(at);
        return watchable.Where(r => filter.Matches(r.GuidePrograms, at, to)).ToList();
    }

    /// <summary>How <paramref name="row"/> reads under <see cref="Filter"/> (dimmed lines, a later match).</summary>
    public GuideFilters.RowHighlight Highlight(ChannelRow row, DateTimeOffset at) =>
        Filter.Highlight(row.NowNext, row.GuidePrograms, at, WindowEnd(at));

    /// <summary>When the program on <paramref name="channelId"/> at <paramref name="at"/> ends; null when unknown.</summary>
    public DateTimeOffset? ProgramEndFor(long channelId, DateTimeOffset at)
    {
        var row = Rows.FirstOrDefault(r => r.Id == channelId);
        return row == null ? null : GuideLogic.ComputeNowNext(row.GuidePrograms, at).Now?.Stop;
    }

    public ChannelListStatus Status
    {
        get => _status;
        private set => SetProperty(ref _status, value);
    }

    /// <summary>Favorites first (guide-number order), then the rest in server order.</summary>
    public IReadOnlyList<ChannelRow> Rows
    {
        get => _rows;
        private set
        {
            if (SetProperty(ref _rows, value))
            {
                OnPropertyChanged(nameof(Favorites));
                OnPropertyChanged(nameof(Others));
                OnPropertyChanged(nameof(TunersNote));
                OnPropertyChanged(nameof(NoneWatchable));
                OnPropertyChanged(nameof(VisibleRecents));
            }
        }
    }

    /// <summary>Channels are listed but none can start now (every one is <c>watchable:false</c>).</summary>
    public bool NoneWatchable => Rows.Count > 0 && Rows.All(r => !r.Channel.IsWatchable);

    /// <summary>
    /// Shown above the lists when some channels are hidden because every
    /// tuner is busy; null when all can start.
    /// </summary>
    public string? TunersNote =>
        Rows.All(r => r.Channel.IsWatchable) ? null : NoneWatchable ? AllTunersBusyNote : SomeTunersBusyNote;

    public IReadOnlyList<ChannelRow> Favorites => Rows.Where(r => r.IsFavorite).ToList();

    public IReadOnlyList<ChannelRow> Others => Rows.Where(r => !r.IsFavorite).ToList();

    /// <summary>Recently watched channels, newest first; empty when none or unsupported.</summary>
    public IReadOnlyList<RecentChannel> Recents
    {
        get => _recents;
        private set
        {
            if (SetProperty(ref _recents, value)) OnPropertyChanged(nameof(VisibleRecents));
        }
    }

    /// <summary><see cref="Recents"/> without the listed channels that can't start now.</summary>
    public IReadOnlyList<RecentChannel> VisibleRecents =>
        Recents.Where(r => Rows.FirstOrDefault(row => row.Id == r.ChannelId)?.Channel.IsWatchable ?? true).ToList();

    /// <summary>The server reports favorites; false on older servers (hide stars and Recent).</summary>
    public bool FavoritesSupported
    {
        get => _favoritesSupported;
        private set => SetProperty(ref _favoritesSupported, value);
    }

    /// <summary>Why the list failed to load (Failed only).</summary>
    public string? Error
    {
        get => _error;
        private set => SetProperty(ref _error, value);
    }

    /// <summary>One-shot notice (e.g. a refused star toggle); clear with <see cref="ConsumeMessage"/>.</summary>
    public string? Message
    {
        get => _message;
        private set => SetProperty(ref _message, value);
    }

    public void ConsumeMessage() => Message = null;

    /// <summary>Fetch channels + guide(now..now+4h), join now/next, then the Recent row.</summary>
    public async Task RefreshAsync(CancellationToken ct = default)
    {
        if (Rows.Count == 0) Status = ChannelListStatus.Loading;
        var at = _now();
        bool supported;
        try
        {
            var channels = await _client.ChannelsAsync(ct);
            IReadOnlyList<GuideChannel> guide;
            try
            {
                guide = await _client.GuideAsync(at, at + GuideWindow, ct);
            }
            catch (Exception e) when (e is not UnauthorizedException && !ct.IsCancellationRequested)
            {
                // No guide is not a reason to hide the channels.
                guide = Array.Empty<GuideChannel>();
            }

            if (channels.Count == 0)
            {
                Rows = Array.Empty<ChannelRow>();
                Recents = Array.Empty<RecentChannel>();
                Error = null;
                Status = ChannelListStatus.Empty;
                _lastLoadedAt = at;
                return;
            }

            _programs = new Dictionary<long, IReadOnlyList<GuideProgram>>();
            foreach (var g in guide) _programs[g.ChannelId] = g.Programs;
            _windowEnd = at + GuideWindow;
            supported = channels.Any(c => c.Favorite != null);
            ApplyChannels(channels, at);
            Error = null;
            Status = ChannelListStatus.Loaded;
            _lastLoadedAt = at;
        }
        catch (OperationCanceledException) when (ct.IsCancellationRequested)
        {
            throw;
        }
        catch (Exception e)
        {
            Error = ErrorText.For(e);
            Status = ChannelListStatus.Failed;
            return;
        }
        await LoadRecentsAsync(supported, ct);
    }

    /// <summary>Reload when never loaded or the last load is at least 5 minutes old.</summary>
    public async Task RefreshIfStaleAsync(CancellationToken ct = default)
    {
        if (_lastLoadedAt is not { } last || _now() - last >= StaleInterval)
        {
            await RefreshAsync(ct);
        }
    }

    /// <summary>
    /// Called every <see cref="RecheckInterval"/> while the list is visible
    /// and when the app comes back to the foreground: a full reload when
    /// stale (see <see cref="RefreshIfStaleAsync"/>), otherwise a channels-only
    /// re-check so busy channels come back once a tuner frees up.
    /// </summary>
    public async Task PollAsync(CancellationToken ct = default)
    {
        if (_lastLoadedAt is not { } last || _now() - last >= StaleInterval)
        {
            await RefreshAsync(ct);
        }
        else if (Status == ChannelListStatus.Loaded)
        {
            await RecheckAsync(ct);
        }
    }

    /// <summary>Re-fetch the channel list (watchable, reception, stars) and keep the loaded guide.</summary>
    private async Task RecheckAsync(CancellationToken ct)
    {
        IReadOnlyList<Channel> channels;
        try
        {
            channels = await _client.ChannelsAsync(ct);
        }
        catch (Exception) when (!ct.IsCancellationRequested)
        {
            // Best-effort: keep showing the list; the next check tries again.
            return;
        }
        if (channels.Count == 0 || Status != ChannelListStatus.Loaded)
        {
            await RefreshAsync(ct);
            return;
        }
        ApplyChannels(channels, _now());
    }

    /// <summary>Rows from <paramref name="channels"/> (server order) joined with the loaded guide.</summary>
    private void ApplyChannels(IReadOnlyList<Channel> channels, DateTimeOffset at)
    {
        var rows = channels
            .Select(c =>
            {
                var programs = _programs.TryGetValue(c.Id, out var p) ? p : Array.Empty<GuideProgram>();
                return new ChannelRow(c, GuideLogic.ComputeNowNext(programs, at), programs);
            })
            .ToList();
        _serverOrder = channels.Select((c, i) => (c.Id, i)).ToDictionary(t => t.Id, t => t.i);
        FavoritesSupported = channels.Any(c => c.Favorite != null);
        Rows = FavoritesFirst(rows);
    }

    /// <summary>Re-fetch just the Recent row (e.g. back from the player).</summary>
    public Task RefreshRecentsAsync(CancellationToken ct = default) =>
        Status == ChannelListStatus.Loaded ? LoadRecentsAsync(FavoritesSupported, ct) : Task.CompletedTask;

    private async Task LoadRecentsAsync(bool supported, CancellationToken ct)
    {
        if (!supported)
        {
            Recents = Array.Empty<RecentChannel>();
            return;
        }
        try
        {
            Recents = await _client.RecentsAsync(RecentsLimit, ct);
        }
        catch (NotFoundException)
        {
            Recents = Array.Empty<RecentChannel>();
        }
        catch (Exception) when (!ct.IsCancellationRequested)
        {
            // Best-effort: a failed Recent row never fails the list.
        }
    }

    /// <summary>Star or unstar; optimistic, reverts with <see cref="Message"/> on failure.</summary>
    public async Task SetFavoriteAsync(long channelId, bool on)
    {
        if (!ApplyFavorite(channelId, on)) return;
        try
        {
            await _client.SetFavoriteAsync(channelId, on);
        }
        catch (Exception e)
        {
            ApplyFavorite(channelId, !on);
            Message = "Couldn't update favorites: " + ErrorText.For(e);
        }
    }

    /// <summary>Flip the star from its current state.</summary>
    public Task ToggleFavoriteAsync(long channelId)
    {
        var row = Rows.FirstOrDefault(r => r.Id == channelId);
        return row == null ? Task.CompletedTask : SetFavoriteAsync(channelId, !row.IsFavorite);
    }

    /// <summary>The listed channel for a Recent item, or one built from its fields.</summary>
    public Channel ChannelFor(RecentChannel recent) =>
        Rows.FirstOrDefault(r => r.Id == recent.ChannelId)?.Channel ?? new Channel
        {
            Id = recent.ChannelId,
            GuideNumber = recent.GuideNumber,
            Name = recent.Name,
            LogoUrl = recent.LogoUrl,
        };

    /// <summary>Absolute logo URL for a channel (logos are server-relative); null when none.</summary>
    public Uri? LogoUri(string logoUrl) =>
        string.IsNullOrEmpty(logoUrl) ? null : ServerUrl.Resolve(logoUrl, _client.Server);

    private bool ApplyFavorite(long channelId, bool on)
    {
        if (Status != ChannelListStatus.Loaded || !FavoritesSupported || Rows.All(r => r.Id != channelId))
        {
            return false;
        }
        var updated = Rows
            .Select(r => r.Id == channelId ? r with { Channel = r.Channel with { Favorite = on } } : r)
            .OrderBy(r => _serverOrder.TryGetValue(r.Id, out var i) ? i : int.MaxValue)
            .ToList();
        Rows = FavoritesFirst(updated);
        return true;
    }

    /// <summary>Favorites in guide-number order, then the rest in the order given (server order).</summary>
    public static IReadOnlyList<ChannelRow> FavoritesFirst(IReadOnlyList<ChannelRow> rows)
    {
        var favorites = rows.Where(r => r.IsFavorite)
            .OrderBy(r => r.Channel.GuideNumber, GuideLogic.GuideNumberOrder);
        return favorites.Concat(rows.Where(r => !r.IsFavorite)).ToList();
    }
}
