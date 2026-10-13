namespace Bowtie.Core;

/// <summary>
/// Persists the server base URL and the refresh token. Access tokens are
/// memory-only (see <see cref="BowtieClient"/>). <see cref="Save"/> treats
/// null as "clear" for each field independently.
/// </summary>
public interface ITokenStore
{
    string? LoadServer();
    string? LoadRefreshToken();
    void Save(string? server, string? refreshToken);
}

/// <summary>In-memory store for tests and hosts without secure storage.</summary>
public sealed class InMemoryTokenStore : ITokenStore
{
    private readonly object _gate = new();
    private string? _server;
    private string? _refreshToken;

    public InMemoryTokenStore(string? server = null, string? refreshToken = null)
    {
        _server = server;
        _refreshToken = refreshToken;
    }

    public string? LoadServer()
    {
        lock (_gate) return _server;
    }

    public string? LoadRefreshToken()
    {
        lock (_gate) return _refreshToken;
    }

    public void Save(string? server, string? refreshToken)
    {
        lock (_gate)
        {
            _server = server;
            _refreshToken = refreshToken;
        }
    }
}
