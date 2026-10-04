using System.Threading.Channels;

namespace Bowtie.Core.ViewModels;

public enum RecordingsStatus { Loading, Loaded, Failed }

/// <summary>What the player needs to start a recording.</summary>
public sealed record PlayStart(
    Recording Recording,
    Uri PlaylistUri, // absolute, token-signed VOD playlist (no Bearer)
    int ResumeAtSec,
    int DurationSec,
    bool OfferResume); // ask "Resume / Start over"; otherwise start from the beginning

/// <summary>
/// Recordings screen: Upcoming / Recorded / Missed tabs, cancel or delete,
/// stop, keep, and play with a resume decision. Position saves are queued
/// and sent one at a time in order (the server keeps the last), never
/// blocking the caller.
/// </summary>
public sealed class RecordingsViewModel : ObservableObject, IDisposable
{
    private readonly BowtieClient _client;
    private readonly Channel<(long Id, int Sec)> _saves = System.Threading.Channels.Channel.CreateUnbounded<(long, int)>(
        new UnboundedChannelOptions { SingleReader = true });

    private RecordingTab _tab = RecordingTab.Recorded;
    private RecordingsStatus _status = RecordingsStatus.Loading;
    private IReadOnlyList<Recording> _items = Array.Empty<Recording>();
    private string? _error;
    private string? _message;

    public RecordingsViewModel(BowtieClient client)
    {
        _client = client;
        SaveLoop = Task.Run(DrainSavesAsync);
    }

    public RecordingTab Tab
    {
        get => _tab;
        private set => SetProperty(ref _tab, value);
    }

    public RecordingsStatus Status
    {
        get => _status;
        private set => SetProperty(ref _status, value);
    }

    public IReadOnlyList<Recording> Items
    {
        get => _items;
        private set => SetProperty(ref _items, value);
    }

    /// <summary>Why the tab failed to load (Failed only).</summary>
    public string? Error
    {
        get => _error;
        private set => SetProperty(ref _error, value);
    }

    /// <summary>Transient error from an action (shown in a banner).</summary>
    public string? Message
    {
        get => _message;
        private set => SetProperty(ref _message, value);
    }

    /// <summary>The background position-save loop (tests await it after <see cref="Dispose"/>).</summary>
    public Task SaveLoop { get; }

    public void ClearMessage() => Message = null;

    public async Task SelectTabAsync(RecordingTab tab, CancellationToken ct = default)
    {
        Tab = tab;
        Items = Array.Empty<Recording>();
        Status = RecordingsStatus.Loading;
        await RefreshAsync(ct);
    }

    /// <summary>Reload the current tab; keeps the list shown until the new one arrives.</summary>
    public async Task RefreshAsync(CancellationToken ct = default)
    {
        var tab = Tab;
        try
        {
            var items = await _client.RecordingsAsync(tab, ct);
            if (Tab != tab) return; // the user already left this tab
            Items = items;
            Error = null;
            Status = RecordingsStatus.Loaded;
        }
        catch (Exception e) when (!ct.IsCancellationRequested)
        {
            if (Tab != tab) return;
            Error = RecordingLogic.ErrorMessage(e);
            Status = RecordingsStatus.Failed;
        }
    }

    /// <summary>Cancel (upcoming) or delete (recorded / missed).</summary>
    public Task<bool> DeleteAsync(Recording r) => ActAsync(() => _client.DeleteRecordingAsync(r.Id));

    public Task<bool> StopAsync(Recording r) => ActAsync(() => _client.StopRecordingAsync(r.Id));

    public Task<bool> SetKeptAsync(Recording r, bool keep) =>
        ActAsync(() => _client.SetRecordingProtectedAsync(r.Id, keep));

    /// <summary>Fetch the playlist and decide whether to offer resuming; null (with <see cref="Message"/>) on error.</summary>
    public async Task<PlayStart?> PlayAsync(Recording r, CancellationToken ct = default)
    {
        try
        {
            var p = await _client.PlayRecordingAsync(r.Id, ct);
            return new PlayStart(
                r,
                ServerUrl.Resolve(p.PlaylistUrl, _client.Server),
                p.PositionSec,
                p.DurationSec,
                RecordingLogic.ShouldOfferResume(p.PositionSec, p.DurationSec));
        }
        catch (Exception e) when (!ct.IsCancellationRequested)
        {
            Message = e is ServerException { Status: 409 }
                ? "This recording isn't ready to play yet."
                : RecordingLogic.ErrorMessage(e);
            return null;
        }
    }

    /// <summary>Queue a resume-position save (best-effort, in order).</summary>
    public void SavePosition(long recordingId, TimeSpan position) =>
        _saves.Writer.TryWrite((recordingId, Math.Max(0, (int)position.TotalSeconds)));

    public void Dispose() => _saves.Writer.TryComplete();

    private async Task DrainSavesAsync()
    {
        await foreach (var (id, sec) in _saves.Reader.ReadAllAsync().ConfigureAwait(false))
        {
            try
            {
                await _client.SaveRecordingPositionAsync(id, sec).ConfigureAwait(false);
            }
            catch (Exception)
            {
                // Best-effort: the next save (every 15 s) catches up.
            }
        }
    }

    private async Task<bool> ActAsync(Func<Task> action)
    {
        try
        {
            await action();
            Message = null;
            await RefreshAsync();
            return true;
        }
        catch (Exception e)
        {
            Message = RecordingLogic.ErrorMessage(e);
            return false;
        }
    }
}
