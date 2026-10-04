using System.Collections.Concurrent;
using System.Net;
using System.Text;

namespace Bowtie.Core.Tests;

/// <summary>A request the fake server saw.</summary>
public sealed record SeenRequest(string Method, string PathAndQuery, string? Authorization, string Body);

/// <summary>
/// In-process HTTP fake: routes "METHOD /path" (query ignored) to handlers and
/// records every request. Unrouted requests answer 404.
/// </summary>
public sealed class FakeServer : HttpMessageHandler
{
    public static readonly Uri BaseUri = new("http://bowtie.test:8400/");

    private readonly ConcurrentDictionary<string, Func<SeenRequest, Task<HttpResponseMessage>>> _routes = new();

    public ConcurrentQueue<SeenRequest> Requests { get; } = new();

    public FakeServer On(string method, string path, Func<SeenRequest, HttpResponseMessage> handler)
    {
        _routes[$"{method} {path}"] = r => Task.FromResult(handler(r));
        return this;
    }

    public FakeServer OnAsync(string method, string path, Func<SeenRequest, Task<HttpResponseMessage>> handler)
    {
        _routes[$"{method} {path}"] = handler;
        return this;
    }

    public FakeServer Json(string method, string path, string json, int status = 200) =>
        On(method, path, _ => Response(status, json));

    public IEnumerable<SeenRequest> For(string method, string path) =>
        Requests.Where(r => r.Method == method && PathOf(r.PathAndQuery) == path);

    public HttpClient Client() => new(this);

    public static HttpResponseMessage Response(int status, string? json = null)
    {
        var response = new HttpResponseMessage((HttpStatusCode)status);
        response.Content = new StringContent(json ?? "", Encoding.UTF8, "application/json");
        return response;
    }

    protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken ct)
    {
        var body = request.Content == null ? "" : await request.Content.ReadAsStringAsync(ct);
        var seen = new SeenRequest(
            request.Method.Method,
            request.RequestUri!.PathAndQuery,
            request.Headers.Authorization?.ToString(),
            body);
        Requests.Enqueue(seen);
        var key = $"{seen.Method} {PathOf(seen.PathAndQuery)}";
        return _routes.TryGetValue(key, out var handler) ? await handler(seen) : Response(404, "{\"error\":\"not found\"}");
    }

    private static string PathOf(string pathAndQuery)
    {
        var q = pathAndQuery.IndexOf('?');
        return q < 0 ? pathAndQuery : pathAndQuery[..q];
    }

    // ── Canned bodies ───────────────────────────────────────────────────────

    public static string TokenJson(string access, string refresh, string user = "alice", string maxQuality = "") =>
        $$$"""{"accessToken":"{{{access}}}","refreshToken":"{{{refresh}}}","user":{"id":1,"username":"{{{user}}}","role":"viewer","maxQuality":"{{{maxQuality}}}","maxStreams":0,"maxTuners":0}}""";

    public static string SessionJson(string viewerId, string token = "tok") =>
        $$$"""{"viewerId":"{{{viewerId}}}","playlistUrl":"/api/v1/stream/{{{viewerId}}}/index.m3u8?token={{{token}}}","session":{"videoCodec":"h264","profile":"","backend":"cpu","channelName":"News"}}""";
}

/// <summary>Helpers for building a signed-in client against a <see cref="FakeServer"/>.</summary>
public static class TestClients
{
    public static (BowtieClient Client, InMemoryTokenStore Store) SignedIn(FakeServer server, string access = "a1", string refresh = "r1")
    {
        server.Json("POST", "/api/v1/auth/login", FakeServer.TokenJson(access, refresh));
        var store = new InMemoryTokenStore("http://bowtie.test:8400", null);
        var client = new BowtieClient(FakeServer.BaseUri, store, server.Client());
        client.LoginAsync("alice", "pw").GetAwaiter().GetResult();
        while (server.Requests.TryDequeue(out _)) { }
        return (client, store);
    }
}
