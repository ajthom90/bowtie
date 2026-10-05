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
    private readonly Channel<(long Id, int Sec, TaskCompletionSource Done)> _saves =
        System.Threading.Channels.Channel.CreateUnbounded<(long, int, TaskCompletionSource)>(
            new UnboundedChannelOptions { SingleReader = true });

    /// <summary>Completes once the most recently queued save has been sent (or failed).</summary>
    private Task _lastSave = Task.CompletedTask;

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
            return await StartAsync(_client, r, ct);
        }
        catch (Exception e) when (!ct.IsCancellationRequested)
        {
            Message = PlayErrorMessage(e);
            return null;
        }
    }

    /// <summary>POST /recordings/{id}/play and resolve the playlist (shared with Continue watching).</summary>
    internal static async Task<PlayStart> StartAsync(BowtieClient client, Recording r, CancellationToken ct)
    {
        var p = await client.PlayRecordingAsync(r.Id, ct);
        return new PlayStart(
            r,
            ServerUrl.Resolve(p.PlaylistUrl, client.Server),
            p.PositionSec,
            p.DurationSec,
            RecordingLogic.ShouldOfferResume(p.PositionSec, p.DurationSec));
    }

    internal static string PlayErrorMessage(Exception e) =>
        e is ServerException { Status: 409 }
            ? "This recording isn't ready to play yet."
            : RecordingLogic.ErrorMessage(e);

    /// <summary>Queue a resume-position save (best-effort, in order).</summary>
    public void SavePosition(long recordingId, TimeSpan position)
    {
        var done = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        if (_saves.Writer.TryWrite((recordingId, Math.Max(0, (int)position.TotalSeconds), done)))
        {
            _lastSave = done.Task;
        }
    }

    /// <summary>
    /// Waits (up to <paramref name="timeout"/>) for the saves queued so far to
    /// reach the server, so a reload after the player closes sees the new
    /// position. Never throws.
    /// </summary>
    public async Task SavesSettledAsync(TimeSpan timeout)
    {
        var last = _lastSave;
        if (last.IsCompleted) return;
        await Task.WhenAny(last, Task.Delay(timeout)).ConfigureAwait(false);
    }

    public void Dispose() => _saves.Writer.TryComplete();

    private async Task DrainSavesAsync()
    {
        // WaitToRead/TryRead rather than ReadAllAsync, which .NET Standard 2.0 lacks.
        var reader = _saves.Reader;
        while (await reader.WaitToReadAsync().ConfigureAwait(false))
        {
            if (!reader.TryRead(out var item)) continue;
            var (id, sec, done) = item;
            try
            {
                await _client.SaveRecordingPositionAsync(id, sec).ConfigureAwait(false);
            }
            catch (Exception)
            {
                // Best-effort: the next save (every 15 s) catches up.
            }
            finally
            {
                done.TrySetResult();
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
