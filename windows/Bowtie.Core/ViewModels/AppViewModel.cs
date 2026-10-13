namespace Bowtie.Core.ViewModels;

public enum AppPhase
{
    /// <summary>No server yet: ask for one.</summary>
    Connect,
    /// <summary>Server known, no session: sign in.</summary>
    Login,
    /// <summary>Signing in with the saved refresh token.</summary>
    Checking,
    /// <summary>Signed in.</summary>
    Ready,
}

/// <summary>
/// Connection and sign-in state machine, and the root that owns the
/// <see cref="BowtieClient"/>.
///
/// fresh store → Connect; saved server without token → Login;
/// saved server + token → Checking → <see cref="StartAsync"/> → Ready | Login.
/// When the server can't be reached during Checking the token is kept and
/// <see cref="StartupError"/> offers a retry instead of signing out.
/// </summary>
public sealed class AppViewModel : ObservableObject
{
    private readonly ITokenStore _store;
    private readonly Func<Uri, BowtieClient> _clientFactory;
    private readonly HttpClient _healthHttp;

    private AppPhase _phase = AppPhase.Connect;
    private User? _user;
    private string? _startupError;
    private bool _busy;

    public AppViewModel(ITokenStore store, Func<Uri, BowtieClient> clientFactory, HttpClient? healthHttp = null)
    {
        _store = store;
        _clientFactory = clientFactory;
        _healthHttp = healthHttp ?? new HttpClient();

        var server = ServerUrl.Normalize(store.LoadServer());
        if (server == null)
        {
            _phase = AppPhase.Connect;
            return;
        }
        Client = clientFactory(server);
        _phase = store.LoadRefreshToken() != null ? AppPhase.Checking : AppPhase.Login;
    }

    public AppPhase Phase
    {
        get => _phase;
        private set => SetProperty(ref _phase, value);
    }

    /// <summary>The signed-in user (Ready only).</summary>
    public User? User
    {
        get => _user;
        private set => SetProperty(ref _user, value);
    }

    /// <summary>Why a saved sign-in couldn't be checked (server unreachable); null otherwise.</summary>
    public string? StartupError
    {
        get => _startupError;
        private set => SetProperty(ref _startupError, value);
    }

    public bool IsBusy
    {
        get => _busy;
        private set => SetProperty(ref _busy, value);
    }

    public BowtieClient? Client { get; private set; }

    /// <summary>The saved server as shown to the user, or null.</summary>
    public string? ServerDisplay => Client == null ? null : ServerUrl.Display(Client.Server);

    /// <summary>
    /// Normalize and health-check <paramref name="raw"/>, save it and move to Login.
    /// Returns an error message, or null on success.
    /// </summary>
    public async Task<string?> ConnectAsync(string raw, CancellationToken ct = default)
    {
        var url = ServerUrl.Normalize(raw);
        if (url == null) return "Enter a server address, like 192.168.1.20:8400.";
        IsBusy = true;
        try
        {
            if (!await ServerUrl.ValidateAsync(url, _healthHttp, ct: ct))
            {
                return $"Couldn't reach a Bowtie server at {ServerUrl.Display(url)}.";
            }
        }
        finally
        {
            IsBusy = false;
        }
        _store.Save(ServerUrl.Display(url), null);
        AttachClient(_clientFactory(url));
        OnPropertyChanged(nameof(ServerDisplay));
        Phase = AppPhase.Login;
        return null;
    }

    /// <summary>Checking → Ready with the saved token, or Login when it's refused.</summary>
    public async Task StartAsync(CancellationToken ct = default)
    {
        if (Phase != AppPhase.Checking || Client == null) return;
        StartupError = null;
        IsBusy = true;
        try
        {
            var user = await Client.BootstrapFromStoredTokenAsync(ct);
            AttachClient(Client);
            User = user;
            Phase = AppPhase.Ready;
        }
        catch (NetworkException)
        {
            StartupError = $"Couldn't reach {ServerDisplay}. Check that the server is running.";
        }
        catch (Exception) when (!ct.IsCancellationRequested)
        {
            Phase = AppPhase.Login;
        }
        finally
        {
            IsBusy = false;
        }
    }

    /// <summary>Sign in; returns an error message, or null on success.</summary>
    public async Task<string?> SignInAsync(string username, string password, CancellationToken ct = default)
    {
        if (Client == null) return "Connect to a server first.";
        if (username.Trim().Length == 0 || password.Length == 0) return "Enter your username and password.";
        IsBusy = true;
        try
        {
            var user = await Client.LoginAsync(username.Trim(), password, ct);
            AttachClient(Client);
            StartupError = null;
            User = user;
            Phase = AppPhase.Ready;
            return null;
        }
        catch (UnauthorizedException)
        {
            return "Wrong username or password.";
        }
        catch (Exception e) when (!ct.IsCancellationRequested)
        {
            return ErrorText.For(e);
        }
        finally
        {
            IsBusy = false;
        }
    }

    /// <summary>
    /// A quick sign-in (<see cref="QuickSignInViewModel"/>) was approved: the
    /// client already holds the session, so move to Ready as a password sign-in does.
    /// </summary>
    public void CompleteQuickSignIn(User user)
    {
        if (Client == null) return;
        AttachClient(Client);
        StartupError = null;
        User = user;
        Phase = AppPhase.Ready;
    }

    /// <summary>Sign out and keep the server.</summary>
    public async Task SignOutAsync()
    {
        if (Client != null) await Client.LogoutAsync();
        User = null;
        Phase = AppPhase.Login;
    }

    /// <summary>Forget the server and tokens; back to Connect.</summary>
    public void ChangeServer()
    {
        _store.Save(null, null);
        DetachClient();
        Client = null;
        User = null;
        StartupError = null;
        OnPropertyChanged(nameof(ServerDisplay));
        Phase = AppPhase.Connect;
    }

    /// <summary>Called (on the UI thread) when the client ended the session after a failed refresh.</summary>
    public void OnSessionEnded()
    {
        if (Phase != AppPhase.Ready) return;
        User = null;
        Phase = AppPhase.Login;
    }

    /// <summary>Raised when the client's refresh failed; may fire off the UI thread.</summary>
    public event EventHandler? SessionEnded;

    private void AttachClient(BowtieClient client)
    {
        DetachClient();
        Client = client;
        Client.SessionEnded += ForwardSessionEnded;
    }

    private void DetachClient()
    {
        if (Client != null) Client.SessionEnded -= ForwardSessionEnded;
    }

    private void ForwardSessionEnded(object? sender, EventArgs e) => SessionEnded?.Invoke(this, e);
}
