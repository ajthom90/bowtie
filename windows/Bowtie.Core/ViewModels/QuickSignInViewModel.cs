namespace Bowtie.Core.ViewModels;

public enum QuickSignInStatus
{
    Idle,
    /// <summary>Asking the server for a code.</summary>
    Starting,
    /// <summary>Showing <see cref="QuickSignInViewModel.UserCode"/>; polling until it's approved.</summary>
    Waiting,
    SignedIn,
    /// <summary>The server refused a code (<see cref="QuickSignInViewModel.Error"/>); offer a retry.</summary>
    Failed,
}

/// <summary>
/// Quick sign-in for TV apps (OpenAPI <c>/auth/device</c>): show a code and
/// QR, poll every <c>interval</c> seconds until a signed-in phone approves it,
/// replace an expired code with a new one. A poll that can't reach the server
/// just tries again on the next beat. On approval the client already holds
/// the session; the caller hands the user to
/// <see cref="AppViewModel.CompleteQuickSignIn"/>.
/// </summary>
public sealed class QuickSignInViewModel : ObservableObject
{
    private readonly BowtieClient _client;
    private readonly string _deviceName;
    private readonly Func<TimeSpan, CancellationToken, Task> _delay;

    private QuickSignInStatus _status = QuickSignInStatus.Idle;
    private string? _userCode;
    private string? _verifyUrl;
    private Uri? _qrUri;
    private string? _error;

    public QuickSignInViewModel(BowtieClient client, string deviceName, Func<TimeSpan, CancellationToken, Task>? delay = null)
    {
        _client = client;
        _deviceName = deviceName;
        _delay = delay ?? Task.Delay;
    }

    public QuickSignInStatus Status
    {
        get => _status;
        private set => SetProperty(ref _status, value);
    }

    /// <summary>The code to type at the verify page ("BCDF-2345"); null until one arrives.</summary>
    public string? UserCode
    {
        get => _userCode;
        private set => SetProperty(ref _userCode, value);
    }

    /// <summary>The web app's /link page for this code.</summary>
    public string? VerifyUrl
    {
        get => _verifyUrl;
        private set => SetProperty(ref _verifyUrl, value);
    }

    /// <summary>Absolute URL of the QR code PNG (no auth); null when none.</summary>
    public Uri? QrUri
    {
        get => _qrUri;
        private set => SetProperty(ref _qrUri, value);
    }

    /// <summary>Why the last code request failed (Failed only).</summary>
    public string? Error
    {
        get => _error;
        private set => SetProperty(ref _error, value);
    }

    /// <summary>
    /// Run until approved (returns the user), a code request fails (null,
    /// <see cref="Status"/> Failed) or <paramref name="ct"/> is cancelled (null).
    /// </summary>
    public async Task<User?> RunAsync(CancellationToken ct = default)
    {
        Error = null;
        try
        {
            while (true)
            {
                Status = QuickSignInStatus.Starting;
                DeviceSignIn start;
                try
                {
                    start = await _client.StartDeviceSignInAsync(_deviceName, ct);
                }
                catch (Exception e) when (e is not OperationCanceledException)
                {
                    UserCode = null;
                    VerifyUrl = null;
                    QrUri = null;
                    Error = ErrorText.For(e);
                    Status = QuickSignInStatus.Failed;
                    return null;
                }

                UserCode = start.UserCode;
                VerifyUrl = start.VerifyUrl;
                QrUri = _client.DeviceQrUri(start);
                Status = QuickSignInStatus.Waiting;

                var interval = TimeSpan.FromSeconds(Math.Max(1, start.Interval));
                var user = await PollUntilDoneAsync(start.DeviceCode, interval, ct);
                if (user != null)
                {
                    Status = QuickSignInStatus.SignedIn;
                    return user;
                }
                // Expired: loop for a fresh code.
            }
        }
        catch (OperationCanceledException) when (ct.IsCancellationRequested)
        {
            Status = QuickSignInStatus.Idle;
            return null;
        }
    }

    /// <summary>The user once approved; null when the code expired.</summary>
    private async Task<User?> PollUntilDoneAsync(string deviceCode, TimeSpan interval, CancellationToken ct)
    {
        while (true)
        {
            await _delay(interval, ct);
            ct.ThrowIfCancellationRequested();
            DevicePoll poll;
            try
            {
                poll = await _client.PollDeviceSignInAsync(deviceCode, ct);
            }
            catch (Exception e) when (e is NetworkException || (e is ServerException s && s.Status >= 500))
            {
                // Server unreachable or hiccuping: try again on the next beat.
                continue;
            }
            switch (poll)
            {
                case DevicePoll.SignedIn signedIn:
                    return signedIn.User;
                case DevicePoll.Expired:
                    return null;
            }
            ct.ThrowIfCancellationRequested();
        }
    }
}
