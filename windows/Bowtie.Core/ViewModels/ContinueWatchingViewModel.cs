namespace Bowtie.Core.ViewModels;

/// <summary>
/// The "Continue watching" strip: loads the caller's recorded list and keeps
/// the part-watched ones. Empty hides the strip. Playback goes through the
/// usual recording player, at the server's saved position.
/// </summary>
public sealed class ContinueWatchingViewModel : ObservableObject
{
    private readonly BowtieClient _client;
    private IReadOnlyList<Recording> _items = Array.Empty<Recording>();
    private string? _message;
    private int _generation;

    public ContinueWatchingViewModel(BowtieClient client)
    {
        _client = client;
    }

    /// <summary>The strip's items in display order; empty hides it.</summary>
    public IReadOnlyList<Recording> Items
    {
        get => _items;
        private set
        {
            if (SetProperty(ref _items, value)) OnPropertyChanged(nameof(HasItems));
        }
    }

    public bool HasItems => _items.Count > 0;

    /// <summary>Last failed remove or play, for a banner.</summary>
    public string? Message
    {
        get => _message;
        private set => SetProperty(ref _message, value);
    }

    public void ClearMessage() => Message = null;

    /// <summary>Refresh. A failure keeps what's shown (it's a convenience row).</summary>
    public async Task LoadAsync(CancellationToken ct = default)
    {
        var gen = Interlocked.Increment(ref _generation);
        IReadOnlyList<Recording> rows;
        try
        {
            rows = await _client.RecordingsAsync(RecordingTab.Recorded, ct);
        }
        catch (Exception) when (!ct.IsCancellationRequested)
        {
            return;
        }
        if (gen != Volatile.Read(ref _generation)) return; // a newer load (or a remove) superseded this one
        Items = ContinueWatching.Items(rows);
    }

    /// <summary>"Remove from Continue watching": resets the saved position to 0.</summary>
    public async Task<bool> RemoveAsync(Recording r)
    {
        try
        {
            await _client.SaveRecordingPositionAsync(r.Id, 0);
            Interlocked.Increment(ref _generation); // a load already in flight mustn't bring it back
            Items = Items.Where(x => x.Id != r.Id).ToList();
            Message = null;
            return true;
        }
        catch (Exception e)
        {
            Message = RecordingLogic.ErrorMessage(e);
            return false;
        }
    }

    /// <summary>Fetch the playlist to resume at the saved position; null (with <see cref="Message"/>) on error.</summary>
    public async Task<PlayStart?> PlayAsync(Recording r, CancellationToken ct = default)
    {
        try
        {
            var start = await RecordingsViewModel.StartAsync(_client, r, ct);
            Message = null;
            return start;
        }
        catch (Exception e) when (!ct.IsCancellationRequested)
        {
            Message = RecordingsViewModel.PlayErrorMessage(e);
            return null;
        }
    }
}
