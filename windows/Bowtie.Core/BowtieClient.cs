using System.Globalization;
using System.Net;
using System.Text;

namespace Bowtie.Core;

/// <summary>
/// Viewer API client (docs/api/openapi.yaml): auth, channels, guide,
/// favorites, recents, stream sessions and DVR recordings.
///
/// The access token is memory-only. Refresh is single-flight: concurrent 401s
/// share one <c>/auth/refresh</c> (refresh tokens are single-use, so a second
/// rotation would sign the user out), and the new refresh token is saved
/// before anything retries.
///
/// Stream URLs never get a Bearer header: their auth is the playlist's
/// <c>?token=</c> query. Heartbeats use that token too, so they never race a
/// refresh mid-session.
/// </summary>
public sealed class BowtieClient
{
    private const string JsonMedia = "application/json";

    private readonly ITokenStore _store;
    private readonly HttpClient _http;
    private readonly SemaphoreSlim _refreshLock = new(1, 1);
    private volatile string? _accessToken;
    private volatile User? _currentUser;

    public BowtieClient(Uri server, ITokenStore store, HttpClient? http = null)
    {
        Server = server;
        _store = store;
        _http = http ?? CreateDefaultHttpClient();
    }

    /// <summary>Server base URL (normalized).</summary>
    public Uri Server { get; }

    /// <summary>The signed-in user, or null.</summary>
    public User? CurrentUser => _currentUser;

    /// <summary>Raised (on any thread) when a failed refresh ends the session.</summary>
    public event EventHandler? SessionEnded;

    public static HttpClient CreateDefaultHttpClient() =>
        new(new SocketsHttpHandler
        {
            ConnectTimeout = TimeSpan.FromSeconds(10),
            PooledConnectionLifetime = TimeSpan.FromMinutes(5),
        })
        {
            // Tuning a channel can take several seconds; leave room.
            Timeout = TimeSpan.FromSeconds(45),
        };

    // ── Auth ────────────────────────────────────────────────────────────────

    public async Task<User> LoginAsync(string username, string password, CancellationToken ct = default)
    {
        var body = await SendUnauthedAsync(
            HttpMethod.Post, "/api/v1/auth/login",
            BowtieJson.Serialize(new LoginRequest(username, password)), ct).ConfigureAwait(false);
        var pair = BowtieJson.Deserialize<TokenPair>(body);
        ApplyTokens(pair);
        return pair.User;
    }

    /// <summary>
    /// Rotate the stored refresh token into a live session. Throws
    /// <see cref="UnauthorizedException"/> when there is none or it was refused.
    /// </summary>
    public async Task<User> BootstrapFromStoredTokenAsync(CancellationToken ct = default)
    {
        var rt = _store.LoadRefreshToken() ?? throw new UnauthorizedException();
        await _refreshLock.WaitAsync(ct).ConfigureAwait(false);
        try
        {
            await PerformRefreshAsync(rt, ct).ConfigureAwait(false);
        }
        catch (NetworkException)
        {
            // Keep the token: the server may just be unreachable right now.
            throw;
        }
        finally
        {
            _refreshLock.Release();
        }
        return _currentUser ?? throw new UnauthorizedException();
    }

    /// <summary>Best-effort logout; always clears the session and the stored refresh token.</summary>
    public async Task LogoutAsync(CancellationToken ct = default)
    {
        var rt = _store.LoadRefreshToken();
        if (rt != null)
        {
            try
            {
                await SendUnauthedAsync(
                    HttpMethod.Post, "/api/v1/auth/logout",
                    BowtieJson.Serialize(new RefreshRequest(rt)), ct).ConfigureAwait(false);
            }
            catch (Exception) when (!ct.IsCancellationRequested)
            {
                // best-effort
            }
        }
        ClearSessionKeepServer();
    }

    public async Task<User> MeAsync(CancellationToken ct = default)
    {
        var user = BowtieJson.Deserialize<User>(await AuthedAsync(HttpMethod.Get, "/api/v1/me", null, ct).ConfigureAwait(false));
        _currentUser = user;
        return user;
    }

    // ── Channels, guide, favorites, recents ─────────────────────────────────

    public async Task<IReadOnlyList<Channel>> ChannelsAsync(CancellationToken ct = default) =>
        BowtieJson.Deserialize<List<Channel>>(
            await AuthedAsync(HttpMethod.Get, "/api/v1/channels", null, ct).ConfigureAwait(false));

    public async Task<IReadOnlyList<GuideChannel>> GuideAsync(
        DateTimeOffset start, DateTimeOffset stop, CancellationToken ct = default)
    {
        var path = $"/api/v1/guide?start={Uri.EscapeDataString(Rfc3339(start))}&stop={Uri.EscapeDataString(Rfc3339(stop))}";
        return BowtieJson.Deserialize<List<GuideChannel>>(
            await AuthedAsync(HttpMethod.Get, path, null, ct).ConfigureAwait(false));
    }

    /// <summary>Star (<paramref name="on"/>) or unstar a channel (PUT / DELETE, idempotent).</summary>
    public Task SetFavoriteAsync(long channelId, bool on, CancellationToken ct = default) =>
        AuthedAsync(on ? HttpMethod.Put : HttpMethod.Delete,
            $"/api/v1/me/favorites/{channelId.ToString(CultureInfo.InvariantCulture)}", null, ct);

    /// <summary>Recently watched channels, newest first (server caps <paramref name="limit"/> at 20).</summary>
    public async Task<IReadOnlyList<RecentChannel>> RecentsAsync(int limit = 8, CancellationToken ct = default) =>
        BowtieJson.Deserialize<List<RecentChannel>>(
            await AuthedAsync(HttpMethod.Get,
                $"/api/v1/me/recents?limit={limit.ToString(CultureInfo.InvariantCulture)}", null, ct).ConfigureAwait(false));

    // ── Stream sessions ─────────────────────────────────────────────────────

    public async Task<CreatedSession> CreateSessionAsync(long channelId, ClientCaps caps, CancellationToken ct = default) =>
        BowtieJson.Deserialize<CreatedSession>(
            await AuthedAsync(HttpMethod.Post, "/api/v1/sessions",
                BowtieJson.Serialize(new CreateSessionRequest(channelId, caps)), ct).ConfigureAwait(false));

    /// <summary>Best-effort DELETE; never throws.</summary>
    public async Task DeleteSessionAsync(string viewerId, CancellationToken ct = default)
    {
        try
        {
            await AuthedAsync(HttpMethod.Delete, $"/api/v1/sessions/{Uri.EscapeDataString(viewerId)}", null, ct)
                .ConfigureAwait(false);
        }
        catch (Exception)
        {
            // swallow
        }
    }

    /// <summary>
    /// Session liveness beat. Auth is the stream token query only (never
    /// Bearer). Best-effort; never throws.
    /// </summary>
    public async Task HeartbeatAsync(string viewerId, string streamToken, CancellationToken ct = default)
    {
        var path = $"/api/v1/sessions/{Uri.EscapeDataString(viewerId)}/heartbeat?token={Uri.EscapeDataString(streamToken)}";
        try
        {
            using var request = new HttpRequestMessage(HttpMethod.Post, ServerUrl.Resolve(path, Server))
            {
                Content = new ByteArrayContent(Array.Empty<byte>()),
            };
            using var response = await _http.SendAsync(request, ct).ConfigureAwait(false);
        }
        catch (Exception)
        {
            // best-effort
        }
    }

    // ── DVR recordings ──────────────────────────────────────────────────────

    /// <summary>Everyone's recordings; <paramref name="tab"/> filters (null = all).</summary>
    public async Task<IReadOnlyList<Recording>> RecordingsAsync(RecordingTab? tab = null, CancellationToken ct = default)
    {
        var path = tab is { } t ? $"/api/v1/recordings?state={t.Query()}" : "/api/v1/recordings";
        return BowtieJson.Deserialize<List<Recording>>(
            await AuthedAsync(HttpMethod.Get, path, null, ct).ConfigureAwait(false));
    }

    /// <summary>Cancel a scheduled recording, or delete a recording and its files.</summary>
    public Task DeleteRecordingAsync(long id, CancellationToken ct = default) =>
        AuthedAsync(HttpMethod.Delete, $"/api/v1/recordings/{Id(id)}", null, ct);

    /// <summary>Stop a recording now, keeping what was recorded.</summary>
    public Task StopRecordingAsync(long id, CancellationToken ct = default) =>
        AuthedAsync(HttpMethod.Post, $"/api/v1/recordings/{Id(id)}/stop", null, ct);

    /// <summary>Server-relative, token-signed VOD playlist plus the caller's resume position.</summary>
    public async Task<RecordingPlayback> PlayRecordingAsync(long id, CancellationToken ct = default) =>
        BowtieJson.Deserialize<RecordingPlayback>(
            await AuthedAsync(HttpMethod.Post, $"/api/v1/recordings/{Id(id)}/play", null, ct).ConfigureAwait(false));

    public Task SaveRecordingPositionAsync(long id, int positionSec, CancellationToken ct = default) =>
        AuthedAsync(HttpMethod.Put, $"/api/v1/recordings/{Id(id)}/position",
            BowtieJson.Serialize(new PositionRequest(Math.Max(0, positionSec))), ct);

    /// <summary>Keep a recording from automatic deletion, or stop keeping it.</summary>
    public async Task<Recording> SetRecordingProtectedAsync(long id, bool isProtected, CancellationToken ct = default) =>
        BowtieJson.Deserialize<Recording>(
            await AuthedAsync(HttpMethod.Patch, $"/api/v1/recordings/{Id(id)}",
                BowtieJson.Serialize(new ProtectRequest(isProtected)), ct).ConfigureAwait(false));

    // ── Single-flight refresh ───────────────────────────────────────────────

    private void ApplyTokens(TokenPair pair)
    {
        _accessToken = pair.AccessToken;
        // Persist the new refresh token BEFORE any retry can fire.
        _store.Save(ServerUrl.Display(Server), pair.RefreshToken);
        _currentUser = pair.User;
    }

    private void ClearSessionKeepServer()
    {
        _accessToken = null;
        _currentUser = null;
        _store.Save(_store.LoadServer(), null);
    }

    private void EndSession()
    {
        ClearSessionKeepServer();
        SessionEnded?.Invoke(this, EventArgs.Empty);
    }

    /// <summary>
    /// <paramref name="failedAccessToken"/> is the Bearer that got 401. Waiters
    /// that get the lock after a successful rotation see a different token and
    /// skip refreshing again.
    /// </summary>
    private async Task SingleFlightRefreshAsync(string? failedAccessToken, CancellationToken ct)
    {
        await _refreshLock.WaitAsync(ct).ConfigureAwait(false);
        try
        {
            var current = _accessToken;
            if (current != null && current != failedAccessToken) return;

            var rt = _store.LoadRefreshToken();
            if (rt == null) throw new UnauthorizedException();
            try
            {
                await PerformRefreshAsync(rt, ct).ConfigureAwait(false);
            }
            catch (UnauthorizedException)
            {
                throw;
            }
            catch (OperationCanceledException) when (ct.IsCancellationRequested)
            {
                throw;
            }
            catch (Exception)
            {
                EndSession();
                throw new UnauthorizedException();
            }
        }
        finally
        {
            _refreshLock.Release();
        }
    }

    private async Task PerformRefreshAsync(string refreshToken, CancellationToken ct)
    {
        using var request = JsonRequest(HttpMethod.Post, "/api/v1/auth/refresh",
            BowtieJson.Serialize(new RefreshRequest(refreshToken)));
        HttpResponseMessage response;
        try
        {
            response = await _http.SendAsync(request, ct).ConfigureAwait(false);
        }
        catch (Exception e) when (IsTransport(e, ct))
        {
            throw new NetworkException(e);
        }
        using (response)
        {
            var body = await response.Content.ReadAsStringAsync(ct).ConfigureAwait(false);
            if (!response.IsSuccessStatusCode)
            {
                EndSession();
                throw new UnauthorizedException();
            }
            ApplyTokens(BowtieJson.Deserialize<TokenPair>(body));
        }
    }

    // ── HTTP helpers ────────────────────────────────────────────────────────

    private async Task<string> SendUnauthedAsync(HttpMethod method, string path, string? json, CancellationToken ct)
    {
        using var request = JsonRequest(method, path, json);
        try
        {
            using var response = await _http.SendAsync(request, ct).ConfigureAwait(false);
            return await HandleBodyAsync(response, path, ct).ConfigureAwait(false);
        }
        catch (Exception e) when (IsTransport(e, ct))
        {
            throw new NetworkException(e);
        }
    }

    /// <summary>Authenticated request. On 401: single-flight refresh, then one retry.</summary>
    private async Task<string> AuthedAsync(HttpMethod method, string path, string? json, CancellationToken ct)
    {
        var attachAuth = !IsStreamPath(path);
        try
        {
            // Snapshot the token actually sent: a concurrent refresh may rotate
            // it before this request's late 401 is handled.
            var tokenUsed = _accessToken;
            using (var firstRequest = Build(tokenUsed))
            using (var first = await _http.SendAsync(firstRequest, ct).ConfigureAwait(false))
            {
                if (first.StatusCode != HttpStatusCode.Unauthorized || !attachAuth)
                {
                    return await HandleBodyAsync(first, path, ct).ConfigureAwait(false);
                }
            }

            await SingleFlightRefreshAsync(tokenUsed, ct).ConfigureAwait(false);

            using var retryRequest = Build(_accessToken);
            using var retry = await _http.SendAsync(retryRequest, ct).ConfigureAwait(false);
            if (retry.StatusCode == HttpStatusCode.Unauthorized)
            {
                EndSession();
                throw new UnauthorizedException();
            }
            return await HandleBodyAsync(retry, path, ct).ConfigureAwait(false);
        }
        catch (Exception e) when (IsTransport(e, ct))
        {
            throw new NetworkException(e);
        }

        HttpRequestMessage Build(string? token)
        {
            var request = JsonRequest(method, path, json);
            if (attachAuth && token != null)
            {
                request.Headers.Authorization = new System.Net.Http.Headers.AuthenticationHeaderValue("Bearer", token);
            }
            return request;
        }
    }

    private HttpRequestMessage JsonRequest(HttpMethod method, string path, string? json)
    {
        var request = new HttpRequestMessage(method, ServerUrl.Resolve(path, Server));
        if (json != null)
        {
            request.Content = new StringContent(json, Encoding.UTF8, JsonMedia);
        }
        else if (method == HttpMethod.Post || method == HttpMethod.Put || method == HttpMethod.Patch)
        {
            request.Content = new StringContent("", Encoding.UTF8, JsonMedia);
        }
        return request;
    }

    private static async Task<string> HandleBodyAsync(HttpResponseMessage response, string path, CancellationToken ct)
    {
        var body = await response.Content.ReadAsStringAsync(ct).ConfigureAwait(false);
        if (response.IsSuccessStatusCode) return body;
        throw MapHttpError((int)response.StatusCode, body, path);
    }

    internal static BowtieException MapHttpError(int code, string body, string path)
    {
        switch (code)
        {
            case 401:
                return new UnauthorizedException();
            case 404:
                return new NotFoundException();
            case 409:
                // Only a schedule conflict carries `conflicts`; /play's 409 is a plain error.
                var conflict = TryDeserialize<RecordingConflictBody>(body);
                if (conflict?.Conflicts != null)
                {
                    return new RecordingConflictException(conflict.Error ?? "Not enough tuners", conflict.TunerCount, conflict.Conflicts);
                }
                return new ServerException(409, ErrorMessage(body) ?? "HTTP 409");
            case 422:
                return new NegotiationFailedException(ErrorMessage(body) ?? "negotiation failed");
            case 503:
                // A recordings 503 means the DVR is off, not that tuners are busy.
                if (path.StartsWith("/api/v1/recordings", StringComparison.Ordinal))
                {
                    return new ServerException(503, ErrorMessage(body) ?? "HTTP 503");
                }
                var busy = TryDeserialize<TunersBusyBody>(body);
                if (busy?.Sessions != null)
                {
                    return new TunersBusyException(busy.Sessions, busy.OtherInUse);
                }
                return new ServerException(503, ErrorMessage(body) ?? "HTTP 503");
            default:
                return new ServerException(code, ErrorMessage(body) ?? (body.Length > 0 ? body : $"HTTP {code}"));
        }
    }

    private static string? ErrorMessage(string body) =>
        TryDeserialize<ErrorBody>(body)?.Error is { Length: > 0 } e ? e : null;

    private static T? TryDeserialize<T>(string body) where T : class
    {
        if (string.IsNullOrWhiteSpace(body)) return null;
        try
        {
            return System.Text.Json.JsonSerializer.Deserialize<T>(body, BowtieJson.Options);
        }
        catch (System.Text.Json.JsonException)
        {
            return null;
        }
    }

    /// <summary>A transport failure (or a timeout), not a caller cancellation or a mapped error.</summary>
    private static bool IsTransport(Exception e, CancellationToken ct) => e switch
    {
        BowtieException => false,
        OperationCanceledException => !ct.IsCancellationRequested,
        HttpRequestException => true,
        IOException => true,
        _ => false,
    };

    internal static bool IsStreamPath(string path) => path.Contains("/api/v1/stream/", StringComparison.Ordinal);

    private static string Id(long id) => id.ToString(CultureInfo.InvariantCulture);

    internal static string Rfc3339(DateTimeOffset t) =>
        t.ToUniversalTime().ToString("yyyy-MM-dd'T'HH:mm:ss'Z'", CultureInfo.InvariantCulture);
}
