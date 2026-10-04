namespace Bowtie.Core.ViewModels;

/// <summary>A channel row joined with the guide's now/next.</summary>
public sealed record ChannelRow(Channel Channel, GuideLogic.NowNext NowNext)
{
    public long Id => Channel.Id;
    public bool IsFavorite => Channel.IsFavorite;
}

public enum ChannelListStatus { Loading, Loaded, Empty, Failed }

/// <summary>
/// Channel list: channels joined with a 4-hour guide window, favorites
/// first (guide-number order) then the rest in server order, the Recent row,
/// and optimistic star toggles that revert with a message when refused.
/// A server without favorites omits <c>favorite</c>: stars and Recent hide.
/// Mirrors the Android ChannelListViewModel.
/// </summary>
public sealed class ChannelListViewModel : ObservableObject
{
    /// <summary>Guide request window: now … now+4h.</summary>
    public static readonly TimeSpan GuideWindow = TimeSpan.FromHours(4);

    /// <summary>Reload after this long (also the auto-refresh timer).</summary>
    public static readonly TimeSpan StaleInterval = TimeSpan.FromMinutes(5);

    public const int RecentsLimit = 8;

    private readonly BowtieClient _client;
    private readonly Func<DateTimeOffset> _now;

    private ChannelListStatus _status = ChannelListStatus.Loading;
    private IReadOnlyList<ChannelRow> _rows = Array.Empty<ChannelRow>();
    private IReadOnlyList<RecentChannel> _recents = Array.Empty<RecentChannel>();
    private bool _favoritesSupported;
    private string? _error;
    private string? _message;
    private Dictionary<long, int> _serverOrder = new();
    private DateTimeOffset? _lastLoadedAt;

    public ChannelListViewModel(BowtieClient client, Func<DateTimeOffset>? now = null)
    {
        _client = client;
        _now = now ?? (() => DateTimeOffset.UtcNow);
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
            }
        }
    }

    public IReadOnlyList<ChannelRow> Favorites => Rows.Where(r => r.IsFavorite).ToList();

    public IReadOnlyList<ChannelRow> Others => Rows.Where(r => !r.IsFavorite).ToList();

    /// <summary>Recently watched channels, newest first; empty when none or unsupported.</summary>
    public IReadOnlyList<RecentChannel> Recents
    {
        get => _recents;
        private set => SetProperty(ref _recents, value);
    }

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

            var byId = new Dictionary<long, GuideChannel>();
            foreach (var g in guide) byId[g.ChannelId] = g;
            var rows = channels
                .Select(c => new ChannelRow(c, GuideLogic.ComputeNowNext(
                    byId.TryGetValue(c.Id, out var g) ? g.Programs : Array.Empty<GuideProgram>(), at)))
                .ToList();
            supported = channels.Any(c => c.Favorite != null);
            _serverOrder = channels.Select((c, i) => (c.Id, i)).ToDictionary(t => t.Id, t => t.i);
            FavoritesSupported = supported;
            Rows = FavoritesFirst(rows);
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
