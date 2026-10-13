using System.Text.Json;

namespace Bowtie.Core;

/// <summary>
/// Small per-device choices (not secrets): string values by key. Reads and
/// writes never throw — a store that can't be read or written just doesn't
/// remember.
/// </summary>
public interface IPreferences
{
    string? Get(string key);
    void Set(string key, string value);
}

/// <summary>Not persisted (tests, or when no settings file can be used).</summary>
public sealed class InMemoryPreferences : IPreferences
{
    private readonly Dictionary<string, string> _values = new();
    private readonly object _gate = new();

    public string? Get(string key)
    {
        lock (_gate) return _values.TryGetValue(key, out var v) ? v : null;
    }

    public void Set(string key, string value)
    {
        lock (_gate) _values[key] = value;
    }
}

/// <summary>
/// A JSON object in one file (the app uses <c>%LOCALAPPDATA%\Bowtie\settings.json</c>,
/// which works both installed and unpackaged). Loaded once; every
/// <see cref="Set"/> rewrites the file.
/// </summary>
public sealed class JsonFilePreferences : IPreferences
{
    private readonly string _path;
    private readonly object _gate = new();
    private Dictionary<string, string>? _values;

    public JsonFilePreferences(string path)
    {
        _path = path;
    }

    public string? Get(string key)
    {
        lock (_gate) return Values().TryGetValue(key, out var v) ? v : null;
    }

    public void Set(string key, string value)
    {
        lock (_gate)
        {
            Values()[key] = value;
            try
            {
                var dir = Path.GetDirectoryName(_path);
                if (!string.IsNullOrEmpty(dir)) Directory.CreateDirectory(dir);
                var tmp = _path + ".tmp";
                File.WriteAllText(tmp, JsonSerializer.Serialize(_values));
                File.Move(tmp, _path, overwrite: true);
            }
            catch (Exception)
            {
                // Read-only or full disk: the choice lasts until the app closes.
            }
        }
    }

    private Dictionary<string, string> Values()
    {
        if (_values != null) return _values;
        try
        {
            _values = File.Exists(_path)
                ? JsonSerializer.Deserialize<Dictionary<string, string>>(File.ReadAllText(_path)) ?? new()
                : new();
        }
        catch (Exception)
        {
            // Missing, unreadable or corrupt: start over.
            _values = new();
        }
        return _values;
    }
}

/// <summary>The app's per-device choices, typed.</summary>
public sealed class AppPreferences
{
    public const string GuideFilterKey = "guide.filter";
    public const string AutoSkipAdsKey = "playback.autoSkipAds";
    public const string WindowBoundsKey = "window.bounds";

    private readonly IPreferences _store;

    public AppPreferences(IPreferences store)
    {
        _store = store;
    }

    /// <summary>The guide category chip this device last picked (All when unknown).</summary>
    public GuideFilter GuideFilter
    {
        get => GuideFilters.Parse(_store.Get(GuideFilterKey));
        set => _store.Set(GuideFilterKey, value.Stored());
    }

    /// <summary>"Skip ads automatically" in recordings (default off).</summary>
    public bool AutoSkipAds
    {
        get => _store.Get(AutoSkipAdsKey) == "1";
        set => _store.Set(AutoSkipAdsKey, value ? "1" : "0");
    }

    /// <summary>The main window's last size and position (physical pixels), if any.</summary>
    public PixelRect? WindowBounds
    {
        get => PixelRect.Parse(_store.Get(WindowBoundsKey));
        set => _store.Set(WindowBoundsKey, value?.Stored() ?? "");
    }
}
