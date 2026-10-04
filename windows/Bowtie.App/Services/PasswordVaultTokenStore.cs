using Bowtie.Core;
using Windows.Security.Credentials;

namespace BowtieApp.Services;

/// <summary>
/// Keeps the server URL and refresh token in the Windows Credential Locker
/// (<see cref="PasswordVault"/>), encrypted per Windows user. Works packaged
/// and unpackaged. A credential's password can't be empty, so each value is
/// its own credential and "cleared" means removed.
/// </summary>
public sealed class PasswordVaultTokenStore : ITokenStore
{
    private const string Resource = "Bowtie";
    private const string ServerUser = "server";
    private const string RefreshUser = "refreshToken";

    private readonly PasswordVault _vault = new();
    private readonly object _gate = new();

    public string? LoadServer() => Read(ServerUser);

    public string? LoadRefreshToken() => Read(RefreshUser);

    public void Save(string? server, string? refreshToken)
    {
        lock (_gate)
        {
            Write(ServerUser, server);
            Write(RefreshUser, refreshToken);
        }
    }

    private string? Read(string user)
    {
        lock (_gate)
        {
            try
            {
                var credential = _vault.Retrieve(Resource, user);
                credential.RetrievePassword();
                return string.IsNullOrEmpty(credential.Password) ? null : credential.Password;
            }
            catch (Exception)
            {
                // Retrieve throws when there is no such credential.
                return null;
            }
        }
    }

    private void Write(string user, string? value)
    {
        try
        {
            var existing = _vault.Retrieve(Resource, user);
            _vault.Remove(existing);
        }
        catch (Exception)
        {
            // nothing saved yet
        }
        if (!string.IsNullOrEmpty(value))
        {
            _vault.Add(new PasswordCredential(Resource, user, value));
        }
    }
}
