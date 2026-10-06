using System;
using System.ComponentModel;
using System.Threading.Tasks;
using Bowtie.Core;
using Bowtie.Core.ViewModels;
using BowtieXbox.Services;
using Windows.Media.Core;
using Windows.Media.Playback;
using Windows.Media.Streaming.Adaptive;
using Windows.System.Display;
using Windows.UI.Core;
using Windows.UI.Xaml;
using Windows.UI.Xaml.Automation.Peers;
using Windows.UI.Xaml.Controls;
using Windows.UI.Xaml.Input;
using Windows.UI.Xaml.Navigation;

namespace BowtieXbox.Pages
{
    /// <summary>
    /// Live TV. The session is driven by <see cref="PlayerViewModel"/> (create,
    /// 15 s heartbeats, 403 → one silent replace, DELETE on leave), as in the
    /// desktop app. HLS plays through <see cref="AdaptiveMediaSource"/>; its
    /// auth is the playlist's <c>?token=</c>, never a Bearer header.
    /// B / Back leaves (deleting the session); suspending the app does too.
    /// The heartbeat's reception shows under the title, and a weak-signal
    /// note stays up while it's weak. Failures show plain words; the
    /// technical detail goes to the app log.
    /// </summary>
    public sealed partial class PlayerPage : Page
    {
        private static readonly TimeSpan TitleShownFor = TimeSpan.FromSeconds(5);

        private readonly MediaPlayer _player;
        private readonly DispatcherTimer _titleTimer;
        private DisplayRequest? _displayRequest;

        private PlayerViewModel? _live;
        private AdaptiveMediaSource? _ams;
        private Uri? _loadedUri;
        private int _loadGeneration;
        private bool _opened;
        private bool _leaving;

        public PlayerPage()
        {
            InitializeComponent();

            _player = new MediaPlayer { AutoPlay = true };
            _player.MediaOpened += (_, __) => OnUi(OnMediaOpened);
            _player.MediaFailed += (_, args) =>
            {
                var detail = $"media failed: {args.Error} 0x{args.ExtendedErrorCode?.HResult:X8} {args.ErrorMessage}";
                OnUi(() => Fail(detail));
            };
            _player.PlaybackSession.PlaybackStateChanged += (session, __) =>
            {
                var state = session.PlaybackState;
                OnUi(() => OnPlaybackStateChanged(state));
            };
            PlayerElement.SetMediaPlayer(_player);

            _titleTimer = new DispatcherTimer { Interval = TitleShownFor };
            _titleTimer.Tick += (_, __) =>
            {
                _titleTimer.Stop();
                if (_live?.State is PlayerState.Playing && Overlay.Visibility == Visibility.Collapsed)
                {
                    TitleBar.Visibility = Visibility.Collapsed;
                }
            };

            // Any button brings the channel name back for a moment.
            PreviewKeyDown += (_, __) => ShowTitle();
        }

        // ── Lifecycle ───────────────────────────────────────────────────────

        protected override async void OnNavigatedTo(NavigationEventArgs e)
        {
            base.OnNavigatedTo(e);
            if (!(e.Parameter is Channel channel)) return;
            TitleLabel.Text = Describe(channel);
            ShowOverlay(busy: true, title: $"Tuning {Describe(channel)}…", body: null, retry: false);
            KeepScreenOn(true);

            var caps = await DeviceCaps.CurrentAsync();
            if (_leaving) return;
            _live = new PlayerViewModel(AppServices.Client, caps, AppServices.App.User?.MaxQuality ?? "");
            _live.PropertyChanged += OnLiveChanged;
            _live.Play(channel);
            Render();
        }

        protected override void OnNavigatedFrom(NavigationEventArgs e)
        {
            Leave();
            base.OnNavigatedFrom(e);
        }

        /// <summary>
        /// The app is being suspended: stop playback and DELETE the session
        /// (waiting up to 2 s) so the tuner frees now.
        /// </summary>
        public async Task SuspendAsync()
        {
            var live = _live;
            Leave();
            if (live == null) return;
            await Task.WhenAny(live.StopTask, Task.Delay(TimeSpan.FromSeconds(2)));
        }

        private void Leave()
        {
            if (_leaving) return;
            _leaving = true;
            _titleTimer.Stop();
            KeepScreenOn(false);
            TearDownMedia();
            // Detach before disposing: the element must not touch a closed player while unloading.
            PlayerElement.SetMediaPlayer(null);
            _player.Dispose();

            if (_live != null)
            {
                _live.PropertyChanged -= OnLiveChanged;
                var stale = _live.ChannelsStale;
                _live.Stop();
                _live.Dispose();
                if (stale) _ = AppServices.Channels.RefreshAsync();
            }
        }

        private void KeepScreenOn(bool on)
        {
            try
            {
                if (on && _displayRequest == null)
                {
                    _displayRequest = new DisplayRequest();
                    _displayRequest.RequestActive();
                }
                else if (!on && _displayRequest != null)
                {
                    _displayRequest.RequestRelease();
                    _displayRequest = null;
                }
            }
            catch (Exception)
            {
                // best-effort
            }
        }

        private void OnBack(object sender, RoutedEventArgs e)
        {
            if (Frame.CanGoBack) Frame.GoBack();
        }

        private void OnRetry(object sender, RoutedEventArgs e) => _live?.Retry();

        // ── Session state ───────────────────────────────────────────────────

        private void OnLiveChanged(object? sender, PropertyChangedEventArgs e)
        {
            // Each heartbeat updates the reading: redraw just that, so the
            // title bar doesn't pop back up every 15 seconds.
            switch (e.PropertyName)
            {
                case nameof(PlayerViewModel.Signal):
                case nameof(PlayerViewModel.ShowWeakSignalNote):
                case nameof(PlayerViewModel.WeakSignalMessage):
                case nameof(PlayerViewModel.SignalStats):
                    OnUi(RenderSignal);
                    break;
                default:
                    OnUi(Render);
                    break;
            }
        }

        private void Render()
        {
            if (_leaving || _live == null) return;
            if (_live.CurrentChannel is Channel channel) TitleLabel.Text = Describe(channel);
            RenderSignal();

            switch (_live.State)
            {
                case PlayerState.Starting _:
                    StopMedia();
                    ShowOverlay(busy: true, title: $"Tuning {TitleLabel.Text}…", body: null, retry: false);
                    break;
                case PlayerState.Playing _:
                    if (_live.PlaylistUri is Uri uri && uri != _loadedUri)
                    {
                        ShowOverlay(busy: true, title: "Starting the stream…", body: null, retry: false);
                        _ = LoadAsync(uri);
                    }
                    else if (_opened)
                    {
                        HideOverlay();
                    }
                    break;
                case PlayerState.Stalled _:
                    ShowOverlay(busy: true, title: "Buffering…", body: null, retry: false);
                    break;
                case PlayerState.Failed failed:
                    StopMedia();
                    ShowOverlay(busy: false, title: "Can't play this channel", body: failed.Message, retry: true);
                    break;
                case PlayerState.TunersBusy busy:
                    StopMedia();
                    ShowOverlay(busy: false, title: TunersBusyCopy.Title,
                        body: TunersBusyCopy.Body(busy.Sessions, busy.OtherInUse), retry: true);
                    break;
                default:
                    HideOverlay();
                    break;
            }
        }

        /// <summary>
        /// The reception line under the title and the weak-signal note;
        /// Narrator reads the note when it appears.
        /// </summary>
        private void RenderSignal()
        {
            if (_leaving || _live == null) return;
            var stats = _live.SignalStats;
            SignalStatsText.Text = stats ?? "";
            SignalStatsText.Visibility = stats != null ? Visibility.Visible : Visibility.Collapsed;

            var message = _live.WeakSignalMessage;
            WeakSignalText.Text = message ?? "";
            var visibility = message != null ? Visibility.Visible : Visibility.Collapsed;
            if (WeakSignalNote.Visibility == visibility) return;
            WeakSignalNote.Visibility = visibility;
            if (message != null)
            {
                var peer = FrameworkElementAutomationPeer.FromElement(WeakSignalText)
                           ?? FrameworkElementAutomationPeer.CreatePeerForElement(WeakSignalText);
                peer?.RaiseAutomationEvent(AutomationEvents.LiveRegionChanged);
            }
        }

        private void ShowOverlay(bool busy, string title, string? body, bool retry)
        {
            Overlay.Visibility = Visibility.Visible;
            TitleBar.Visibility = Visibility.Visible;
            Spinner.IsActive = busy;
            Spinner.Visibility = busy ? Visibility.Visible : Visibility.Collapsed;
            OverlayTitle.Text = title;
            OverlayBody.Text = body ?? "";
            OverlayBody.Visibility = string.IsNullOrEmpty(body) ? Visibility.Collapsed : Visibility.Visible;
            var hadButtons = OverlayButtons.Visibility == Visibility.Visible;
            OverlayButtons.Visibility = retry ? Visibility.Visible : Visibility.Collapsed;
            if (retry && !hadButtons) RetryButton.Focus(FocusState.Programmatic);
        }

        private void HideOverlay()
        {
            Overlay.Visibility = Visibility.Collapsed;
            OverlayButtons.Visibility = Visibility.Collapsed;
            Spinner.IsActive = false;
            ShowTitle();
        }

        private void ShowTitle()
        {
            if (_leaving) return;
            TitleBar.Visibility = Visibility.Visible;
            _titleTimer.Stop();
            _titleTimer.Start();
        }

        // ── Media ───────────────────────────────────────────────────────────

        private async Task LoadAsync(Uri uri)
        {
            var generation = ++_loadGeneration;
            TearDownMedia();
            _loadedUri = uri;

            AdaptiveMediaSourceCreationResult result;
            try
            {
                result = await AdaptiveMediaSource.CreateFromUriAsync(uri);
            }
            catch (Exception ex)
            {
                if (generation == _loadGeneration && !_leaving) Fail($"couldn't open the stream: {ex}");
                return;
            }
            if (generation != _loadGeneration || _leaving) return;

            if (result.Status != AdaptiveMediaSourceCreationStatus.Success || result.MediaSource == null)
            {
                if (result.HttpResponseMessage?.StatusCode == Windows.Web.Http.HttpStatusCode.Forbidden)
                {
                    AuthFailed();
                    return;
                }
                Fail($"couldn't open the stream: {result.Status} 0x{result.ExtendedError?.HResult:X8}");
                return;
            }

            _ams = result.MediaSource;
            _ams.DownloadFailed += OnDownloadFailed;
            _opened = false;
            _player.Source = new MediaPlaybackItem(MediaSource.CreateFromAdaptiveMediaSource(_ams));
            _player.Play();
        }

        private void OnDownloadFailed(AdaptiveMediaSource sender, AdaptiveMediaSourceDownloadFailedEventArgs args)
        {
            if (args.HttpResponseMessage?.StatusCode == Windows.Web.Http.HttpStatusCode.Forbidden)
            {
                OnUi(() =>
                {
                    if (ReferenceEquals(sender, _ams)) AuthFailed();
                });
            }
        }

        /// <summary>403 on the playlist or a segment: the stream token is no longer valid.</summary>
        private void AuthFailed()
        {
            if (_leaving || _live == null) return;
            _loadedUri = null;
            _live.OnPlaybackAuthError();
        }

        /// <summary>The player gave up: plain words on screen, <paramref name="detail"/> in the app log.</summary>
        private void Fail(string detail)
        {
            if (_leaving || _live == null) return;
            App.Log("player", detail);
            _loadedUri = null;
            _live.OnPlaybackFailed(ErrorText.StreamStopped);
        }

        private void OnMediaOpened()
        {
            if (_leaving) return;
            _opened = true;
            if (_live == null || _live.State is PlayerState.Playing) HideOverlay();
        }

        private void OnPlaybackStateChanged(MediaPlaybackState state)
        {
            if (_leaving || !_opened || _live == null) return;
            if (state == MediaPlaybackState.Buffering) _live.OnPlaybackStalled();
            else if (state == MediaPlaybackState.Playing) _live.OnPlaybackRecovered();
        }

        /// <summary>Stop showing video (session changes); keeps the player for the next stream.</summary>
        private void StopMedia()
        {
            _loadGeneration++;
            TearDownMedia();
            _loadedUri = null;
        }

        private void TearDownMedia()
        {
            _opened = false;
            try
            {
                _player.Pause();
                _player.Source = null;
            }
            catch (Exception)
            {
                // already disposed
            }
            if (_ams != null)
            {
                _ams.DownloadFailed -= OnDownloadFailed;
                _ams = null;
            }
        }

        // ── Helpers ─────────────────────────────────────────────────────────

        private void OnUi(Action action)
        {
            if (Dispatcher.HasThreadAccess) action();
            else _ = Dispatcher.RunAsync(CoreDispatcherPriority.Normal, () => action());
        }

        private static string Describe(Channel channel) =>
            string.IsNullOrEmpty(channel.GuideNumber) ? channel.Name : $"{channel.GuideNumber} {channel.Name}";
    }
}
