using Bowtie.Core;
using Bowtie.Core.ViewModels;

namespace BowtieApp.Services;

/// <summary>What the player page should play.</summary>
public abstract record PlayerRequest
{
    private PlayerRequest() { }

    /// <summary>A live channel; <paramref name="Lineup"/> is the zap order (favorites first).</summary>
    public sealed record Live(Channel Channel, IReadOnlyList<Channel> Lineup) : PlayerRequest;

    /// <summary>A recording, from the start or from <paramref name="ResumeAtSec"/>.</summary>
    public sealed record Vod(PlayStart Start, int ResumeAtSec) : PlayerRequest;
}

/// <summary>
/// Composition root shared by the pages: the app view model (which owns the
/// API client) and the per-sign-in screen view models.
/// </summary>
public static class AppServices
{
    private static ChannelListViewModel? s_channels;
    private static RecordingsViewModel? s_recordings;
    private static ContinueWatchingViewModel? s_continue;
    private static BowtieClient? s_forClient;

    public static AppViewModel App { get; private set; } = null!;

    /// <summary>Per-device choices (guide filter, auto-skip ads), kept in %LOCALAPPDATA%\Bowtie\settings.json.</summary>
    public static AppPreferences Preferences { get; private set; } = new(new InMemoryPreferences());

    public static void Initialize()
    {
        var store = new PasswordVaultTokenStore();
        App = new AppViewModel(store, server => new BowtieClient(server, store));
        Preferences = new AppPreferences(new JsonFilePreferences(SettingsPath()));
    }

    private static string SettingsPath()
    {
        var root = Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData);
        if (string.IsNullOrEmpty(root)) root = Path.GetTempPath();
        return Path.Combine(root, "Bowtie", "settings.json");
    }

    public static BowtieClient Client => App.Client ?? throw new InvalidOperationException("Not connected");

    public static ChannelListViewModel Channels
    {
        get
        {
            EnsureForCurrentClient();
            return s_channels ??= new ChannelListViewModel(Client, prefs: Preferences);
        }
    }

    public static RecordingsViewModel Recordings
    {
        get
        {
            EnsureForCurrentClient();
            return s_recordings ??= new RecordingsViewModel(Client);
        }
    }

    /// <summary>The "Continue watching" strip shown on Live TV and Recordings.</summary>
    public static ContinueWatchingViewModel Continue
    {
        get
        {
            EnsureForCurrentClient();
            return s_continue ??= new ContinueWatchingViewModel(Client);
        }
    }

    /// <summary>
    /// Reload the strip once the player's last position save has landed (up
    /// to a few seconds), so the item just watched shows where it stopped.
    /// </summary>
    public static async Task ReloadContinueAfterPlayerAsync()
    {
        var recordings = Recordings;
        var strip = Continue;
        await recordings.SavesSettledAsync(TimeSpan.FromSeconds(3));
        await strip.LoadAsync();
    }

    /// <summary>Screen view models belong to one client (server + sign-in); drop them when it changes.</summary>
    private static void EnsureForCurrentClient()
    {
        if (ReferenceEquals(s_forClient, App.Client)) return;
        s_recordings?.Dispose();
        s_recordings = null;
        s_channels = null;
        s_continue = null;
        s_forClient = App.Client;
    }

    /// <summary>Forget the screen view models (sign-out, server change).</summary>
    public static void Reset()
    {
        s_recordings?.Dispose();
        s_recordings = null;
        s_channels = null;
        s_continue = null;
        s_forClient = null;
    }
}
