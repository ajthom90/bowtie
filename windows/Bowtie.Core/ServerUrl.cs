namespace Bowtie.Core;

/// <summary>
/// Server base-URL helpers: normalize what the user typed, probe /healthz, and
/// resolve server-relative paths (keeping the query: HLS playlists carry
/// <c>?token=</c>).
/// </summary>
public static class ServerUrl
{
    /// <summary>
    /// Adds <c>http://</c> when there is no scheme, strips trailing slashes,
    /// rejects empty input and non-http(s) schemes. Null when unusable.
    /// </summary>
    public static Uri? Normalize(string? raw)
    {
        var trimmed = raw?.Trim() ?? "";
        if (trimmed.Length == 0) return null;
        if (!trimmed.Contains("://", StringComparison.Ordinal)) trimmed = "http://" + trimmed;

        if (!Uri.TryCreate(trimmed, UriKind.Absolute, out var parsed)) return null;
        if (parsed.Scheme != Uri.UriSchemeHttp && parsed.Scheme != Uri.UriSchemeHttps) return null;
        if (string.IsNullOrEmpty(parsed.Host)) return null;

        var path = parsed.AbsolutePath.TrimEnd('/');
        var builder = new UriBuilder(parsed)
        {
            Path = path.Length == 0 ? "/" : path,
            Query = "",
            Fragment = "",
        };
        return builder.Uri;
    }

    /// <summary>The form saved to the token store and shown to the user ("http://host:8400").</summary>
    public static string Display(Uri server)
    {
        var s = server.GetLeftPart(UriPartial.Path);
        return s.EndsWith('/') ? s[..^1] : s;
    }

    /// <summary>GET <c>{url}/healthz</c>; true only for HTTP 200 within <paramref name="timeout"/>.</summary>
    public static async Task<bool> ValidateAsync(
        Uri url,
        HttpClient http,
        TimeSpan? timeout = null,
        CancellationToken ct = default)
    {
        using var cts = CancellationTokenSource.CreateLinkedTokenSource(ct);
        cts.CancelAfter(timeout ?? TimeSpan.FromSeconds(4));
        try
        {
            using var response = await http
                .GetAsync(Resolve("/healthz", url), HttpCompletionOption.ResponseHeadersRead, cts.Token)
                .ConfigureAwait(false);
            return (int)response.StatusCode == 200;
        }
        catch (Exception) when (!ct.IsCancellationRequested)
        {
            return false;
        }
    }

    /// <summary>
    /// Resolve <paramref name="path"/> (e.g. <c>/api/v1/stream/v1/index.m3u8?token=x</c>)
    /// against <paramref name="baseUri"/>, keeping the query. An absolute URL is returned as is.
    /// </summary>
    public static Uri Resolve(string path, Uri baseUri)
    {
        if (Uri.TryCreate(path, UriKind.Absolute, out var absolute) &&
            (absolute.Scheme == Uri.UriSchemeHttp || absolute.Scheme == Uri.UriSchemeHttps))
        {
            return absolute;
        }
        return new Uri(baseUri, path);
    }
}
