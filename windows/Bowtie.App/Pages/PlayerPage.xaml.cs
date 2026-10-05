using System.ComponentModel;
using Bowtie.Core;
using Bowtie.Core.ViewModels;
using BowtieApp.Services;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Navigation;
using Windows.Media.Core;
using Windows.Media.Playback;
using Windows.Media.Streaming.Adaptive;

namespace BowtieApp.Pages;

/// <summary>
/// Live TV and recording playback.
///
/// HLS plays through <see cref="AdaptiveMediaSource"/> wrapped in a
/// <see cref="MediaPlaybackItem"/> (which exposes audio and caption tracks)
/// on a <see cref="MediaPlayerElement"/> with the system transport controls.
/// Stream auth is the playlist's <c>?token=</c>; no Bearer header is ever
/// added. Live sessions are driven by <see cref="PlayerViewModel"/> (session
/// replace, 15 s heartbeats, 403 → one silent replace); the live window
/// math (Go Live, behind-live label, skips) is <see cref="LiveEdge"/>.
/// The sleep timer (<see cref="SleepTimer"/>) leaves the player like Back;
/// recordings offer Skip ad over detected breaks (<see cref="CommercialSkipper"/>).
/// </summary>
public sealed partial class PlayerPage : Page
{
    private readonly MediaPlayer _player;
    private readonly DispatcherQueueTimer _tick;
    private readonly SleepTimer _sleep;
    private CommercialSkipper? _skipper;

    private PlayerRequest? _request;
    private PlayerViewModel? _live;
    private AdaptiveMediaSource? _ams;
    private MediaPlaybackItem? _item;
    private Uri? _loadedUri;
    private int _loadGeneration;
    private bool _opened;
    private bool _leaving;
    private bool _updatingQuality;
    private TimeSpan? _pendingResume;
    private DateTimeOffset _lastSave = DateTimeOffset.MinValue;

    public PlayerPage()
    {
        InitializeComponent();

        _player = new MediaPlayer { AutoPlay = true };
        _player.MediaOpened += (_, _) => DispatcherQueue.TryEnqueue(OnMediaOpened);
        _player.MediaFailed += (_, args) =>
        {
            var message = string.IsNullOrWhiteSpace(args.ErrorMessage)
                ? $"Playback failed ({args.Error})."
                : args.ErrorMessage;
            DispatcherQueue.TryEnqueue(() => Fail(message));
        };
        _player.PlaybackSession.PlaybackStateChanged += (session, _) =>
        {
            var state = session.PlaybackState;
            DispatcherQueue.TryEnqueue(() => OnPlaybackStateChanged(state));
        };
        PlayerElement.SetMediaPlayer(_player);

        _tick = DispatcherQueue.CreateTimer();
        _tick.Interval = TimeSpan.FromMilliseconds(500);
        _tick.Tick += (_, _) => OnTick();

        // Fires the same leave as Back: the live session is deleted (tuner freed)
        // or the recording position saved. Deferred so the tick finishes first.
        _sleep = new SleepTimer(() => DispatcherQueue.TryEnqueue(GoBack));
        _sleep.PropertyChanged += (_, _) => RenderSleep();

        PreviewKeyDown += OnPreviewKeyDown;
    }

    // ── Lifecycle ───────────────────────────────────────────────────────────

    protected override async void OnNavigatedTo(NavigationEventArgs e)
    {
        base.OnNavigatedTo(e);
        _request = e.Parameter as PlayerRequest;
        App.MainWindow.Closed += OnWindowClosed;
        _tick.Start();

        switch (_request)
        {
            case PlayerRequest.Live live:
                ShowOverlay(busy: true, title: $"Tuning {Describe(live.Channel)}…", body: null, retry: false);
                var caps = await DeviceCaps.CurrentAsync(App.MainWindow);
                if (_leaving) return;
                _live = new PlayerViewModel(AppServices.Client, caps, AppServices.App.User?.MaxQuality ?? "");
                _live.PropertyChanged += OnLiveChanged;
                SetupQuality();
                _live.Play(live.Channel);
                break;

            case PlayerRequest.Vod vod:
                TitleLabel.Text = vod.Start.Recording.Title;
                _skipper = new CommercialSkipper(vod.Start.Recording.Commercials);
                _pendingResume = vod.ResumeAtSec > 0 ? TimeSpan.FromSeconds(vod.ResumeAtSec) : null;
                ShowOverlay(busy: true, title: "Opening the recording…", body: null, retry: false);
                await LoadAsync(vod.Start.PlaylistUri);
                break;
        }
        Render();
    }

    protected override void OnNavigatedFrom(NavigationEventArgs e)
    {
        Leave();
        base.OnNavigatedFrom(e);
    }

    private void OnWindowClosed(object sender, WindowEventArgs args)
    {
        var live = _live;
        Leave();
        // Give the DELETE a moment so the tuner frees now rather than after the idle timeout.
        try
        {
            live?.StopTask.Wait(TimeSpan.FromSeconds(2));
        }
        catch (Exception)
        {
            // best-effort
        }
    }

    private void Leave()
    {
        if (_leaving) return;
        _leaving = true;
        _tick.Stop();
        _sleep.Cancel();
        App.MainWindow.Closed -= OnWindowClosed;
        App.MainWindow.SetFullScreen(false);

        if (_request is PlayerRequest.Vod vod && _opened)
        {
            AppServices.Recordings.SavePosition(vod.Start.Recording.Id, _player.PlaybackSession.Position);
        }
        TearDownMedia();
        // Detach before closing: the element must not touch a closed player while unloading.
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

    private void GoBack()
    {
        var frame = App.MainWindow.Frame;
        if (frame.CanGoBack) frame.GoBack();
    }

    // ── Live session state ──────────────────────────────────────────────────

    private void OnLiveChanged(object? sender, PropertyChangedEventArgs e) =>
        DispatcherQueue.TryEnqueue(Render);

    private void Render()
    {
        if (_leaving) return;
        var isLive = _request is PlayerRequest.Live;
        LiveBadge.Visibility = isLive ? LiveBadge.Visibility : Visibility.Collapsed;
        QualityBox.Visibility = isLive && _live != null ? Visibility.Visible : Visibility.Collapsed;
        if (_live == null) return;

        if (_live.CurrentChannel is { } channel) TitleLabel.Text = Describe(channel);

        switch (_live.State)
        {
            case PlayerState.Starting:
                StopMedia();
                ShowOverlay(busy: true, title: $"Tuning {TitleLabel.Text}…", body: null, retry: false);
                break;
            case PlayerState.Playing:
                if (_live.PlaylistUri is { } uri && uri != _loadedUri)
                {
                    ShowOverlay(busy: true, title: "Starting the stream…", body: null, retry: false);
                    _ = LoadAsync(uri);
                }
                else if (_opened)
                {
                    HideOverlay();
                }
                break;
            case PlayerState.Stalled:
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

    private void SetupQuality()
    {
        if (_live == null) return;
        _updatingQuality = true;
        QualityBox.Items.Clear();
        QualityBox.Items.Add(new ComboBoxItem { Content = GuideLogic.ProfileLabel(""), Tag = "" });
        foreach (var profile in _live.AllowedProfiles)
        {
            QualityBox.Items.Add(new ComboBoxItem { Content = GuideLogic.ProfileLabel(profile), Tag = profile });
        }
        QualityBox.SelectedIndex = 0;
        _updatingQuality = false;
    }

    private void OnQualityChanged(object sender, SelectionChangedEventArgs e)
    {
        if (_updatingQuality || _live == null) return;
        if (QualityBox.SelectedItem is ComboBoxItem { Tag: string profile } && profile != _live.SelectedProfile)
        {
            _live.SetProfile(profile);
        }
    }

    // ── Media ───────────────────────────────────────────────────────────────

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
            if (generation == _loadGeneration && !_leaving) Fail($"Couldn't open the stream: {ex.Message}");
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
            Fail($"Couldn't open the stream ({result.Status}).");
            return;
        }

        _ams = result.MediaSource;
        _ams.DownloadFailed += OnDownloadFailed;
        _item = new MediaPlaybackItem(MediaSource.CreateFromAdaptiveMediaSource(_ams));
        _item.AudioTracksChanged += (_, _) => DispatcherQueue.TryEnqueue(RenderTracks);
        _item.TimedMetadataTracksChanged += (_, _) => DispatcherQueue.TryEnqueue(RenderTracks);
        _opened = false;
        _player.Source = _item;
        _player.Play();
    }

    private void OnDownloadFailed(AdaptiveMediaSource sender, AdaptiveMediaSourceDownloadFailedEventArgs args)
    {
        if (args.HttpResponseMessage?.StatusCode == Windows.Web.Http.HttpStatusCode.Forbidden)
        {
            DispatcherQueue.TryEnqueue(() =>
            {
                if (ReferenceEquals(sender, _ams)) AuthFailed();
            });
        }
    }

    /// <summary>403 on the playlist or a segment: the stream token is no longer valid.</summary>
    private void AuthFailed()
    {
        if (_leaving) return;
        if (_live != null)
        {
            _loadedUri = null;
            _live.OnPlaybackAuthError();
        }
        else
        {
            StopMedia();
            ShowOverlay(busy: false, title: "Can't play this recording",
                body: "Playback authorization failed. Go back and press Play again.", retry: false);
        }
    }

    private void Fail(string message)
    {
        if (_leaving) return;
        if (_live != null)
        {
            _loadedUri = null;
            _live.OnPlaybackFailed(message);
        }
        else
        {
            StopMedia();
            ShowOverlay(busy: false, title: "Can't play this recording", body: message, retry: false);
        }
    }

    private void OnMediaOpened()
    {
        if (_leaving) return;
        _opened = true;
        if (_pendingResume is { } resume)
        {
            _player.PlaybackSession.Position = resume;
            _pendingResume = null;
        }
        if (_live == null || _live.State is PlayerState.Playing) HideOverlay();
        RenderTracks();
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
        _item = null;
        AudioButton.Visibility = Visibility.Collapsed;
        CaptionsButton.Visibility = Visibility.Collapsed;
    }

    // ── Audio and captions ──────────────────────────────────────────────────

    private void RenderTracks()
    {
        if (_leaving) return;
        var item = _item;
        AudioMenu.Items.Clear();
        CaptionsMenu.Items.Clear();
        if (item == null) return;

        var audio = item.AudioTracks;
        var choices = TrackChoices.Audio(audio.Select(t => ((string?)t.Language, (string?)t.Label)).ToList());
        foreach (var choice in choices)
        {
            var option = new RadioMenuFlyoutItem
            {
                Text = choice.Label,
                GroupName = "audio",
                IsChecked = audio.SelectedIndex == choice.Index,
            };
            var index = choice.Index;
            option.Click += (_, _) => item.AudioTracks.SelectedIndex = index;
            AudioMenu.Items.Add(option);
        }
        AudioButton.Visibility = choices.Count > 1 ? Visibility.Visible : Visibility.Collapsed;

        var captions = new List<(uint Index, string Label)>();
        for (var i = 0; i < item.TimedMetadataTracks.Count; i++)
        {
            var track = item.TimedMetadataTracks[i];
            if (track.TimedMetadataKind is TimedMetadataKind.Caption or TimedMetadataKind.Subtitle)
            {
                captions.Add(((uint)i, TrackChoices.TrackLabel(track.Language, track.Label, captions.Count, "Captions")));
            }
        }
        if (captions.Count == 0)
        {
            CaptionsButton.Visibility = Visibility.Collapsed;
            return;
        }

        var anyOn = captions.Any(c =>
            item.TimedMetadataTracks.GetPresentationMode(c.Index) == TimedMetadataTrackPresentationMode.PlatformPresented);
        var off = new RadioMenuFlyoutItem { Text = "Off", GroupName = "captions", IsChecked = !anyOn };
        off.Click += (_, _) => SetCaptions(item, captions, null);
        CaptionsMenu.Items.Add(off);
        foreach (var (index, label) in captions)
        {
            var option = new RadioMenuFlyoutItem
            {
                Text = label,
                GroupName = "captions",
                IsChecked = item.TimedMetadataTracks.GetPresentationMode(index) == TimedMetadataTrackPresentationMode.PlatformPresented,
            };
            option.Click += (_, _) => SetCaptions(item, captions, index);
            CaptionsMenu.Items.Add(option);
        }
        CaptionsButton.Visibility = Visibility.Visible;
    }

    private static void SetCaptions(MediaPlaybackItem item, List<(uint Index, string Label)> captions, uint? on)
    {
        foreach (var (index, _) in captions)
        {
            item.TimedMetadataTracks.SetPresentationMode(index,
                index == on ? TimedMetadataTrackPresentationMode.PlatformPresented : TimedMetadataTrackPresentationMode.Disabled);
        }
    }

    // ── Live window, skips, position saves ──────────────────────────────────

    /// <summary>The live seekable window in seconds, or null when not live (yet).</summary>
    private (double Start, double End, double Offset)? LiveWindow()
    {
        if (_ams is not { IsLive: true } || !_opened) return null;
        var ranges = _player.PlaybackSession.GetSeekableRanges();
        if (ranges.Count == 0) return null;
        var start = ranges[0].Start.TotalSeconds;
        var end = ranges[ranges.Count - 1].End.TotalSeconds;
        return (start, end, _ams.DesiredLiveOffset.TotalSeconds);
    }

    private void OnTick()
    {
        if (_leaving) return;
        var session = _player.PlaybackSession;
        var position = session.Position.TotalSeconds;

        if (LiveWindow() is { } w)
        {
            var target = LiveEdge.LiveTarget(w.Start, w.End, w.Offset);
            // Paused longer than the buffer: the window moved past us.
            if (session.PlaybackState == MediaPlaybackState.Playing && position < w.Start - 2)
            {
                session.Position = TimeSpan.FromSeconds(target);
                ShowNotice(LiveEdge.OutOfWindowNotice);
                position = target;
            }
            var behind = LiveEdge.SecondsBehind(w.Start, w.End, w.Offset, position);
            var atLive = LiveEdge.IsLive(behind);
            LiveBadge.Visibility = Visibility.Visible;
            LiveBadgeText.Text = atLive ? "LIVE" : LiveEdge.BehindLabel(behind);
            LiveBadge.Opacity = atLive ? 1 : 0.7;
            GoLiveButton.Visibility = atLive ? Visibility.Collapsed : Visibility.Visible;
        }
        else
        {
            LiveBadge.Visibility = Visibility.Collapsed;
            GoLiveButton.Visibility = Visibility.Collapsed;
        }

        _sleep.Tick();
        if (_leaving) return;
        RenderCommercials(session);

        if (_request is PlayerRequest.Vod vod && _opened &&
            session.PlaybackState == MediaPlaybackState.Playing &&
            DateTimeOffset.UtcNow - _lastSave >= RecordingLogic.PositionSaveInterval)
        {
            _lastSave = DateTimeOffset.UtcNow;
            AppServices.Recordings.SavePosition(vod.Start.Recording.Id, session.Position);
        }
    }

    // ── Commercials ─────────────────────────────────────────────────────────

    /// <summary>Skip ad while inside a break; auto-skip (only while playing, so scrubbing never jumps) once per break.</summary>
    private void RenderCommercials(MediaPlaybackSession session)
    {
        if (_skipper is not { Segments.Count: > 0 } skipper || !_opened)
        {
            SkipAdButton.Visibility = Visibility.Collapsed;
            return;
        }
        var at = session.Position.TotalSeconds;
        if (AppServices.Preferences.AutoSkipAds && session.PlaybackState == MediaPlaybackState.Playing &&
            skipper.AutoSkipTarget(at) is { } target)
        {
            session.Position = TimeSpan.FromSeconds(target);
            ShowNotice("Skipped ad");
            at = target;
        }
        SkipAdButton.Visibility = skipper.Active(at) != null ? Visibility.Visible : Visibility.Collapsed;
    }

    /// <summary>Skip ad pressed (button or S); false when not in a break.</summary>
    private bool SkipAd()
    {
        if (_skipper == null || !_opened) return false;
        var session = _player.PlaybackSession;
        if (_skipper.Skip(session.Position.TotalSeconds) is not { } target) return false;
        session.Position = TimeSpan.FromSeconds(target);
        SkipAdButton.Visibility = Visibility.Collapsed;
        return true;
    }

    // ── Sleep timer ─────────────────────────────────────────────────────────

    /// <summary>When the live program ends (for "End of this program"); null for recordings or without guide data.</summary>
    private DateTimeOffset? ProgramEnd(DateTimeOffset now) =>
        _live?.CurrentChannel is { } channel ? AppServices.Channels.ProgramEndFor(channel.Id, now) : null;

    /// <summary>Rebuilt on open so "End of this program" follows the current channel.</summary>
    private void OnSleepMenuOpening(object? sender, object e)
    {
        SleepMenu.Items.Clear();
        var now = DateTimeOffset.UtcNow;
        var status = _sleep.Status;
        if (status.Remaining is { } left)
        {
            SleepMenu.Items.Add(new MenuFlyoutItem { Text = $"Sleeping in {SleepTimer.Format(left)}", IsEnabled = false });
            SleepMenu.Items.Add(new MenuFlyoutSeparator());
        }
        var end = ProgramEnd(now);
        foreach (var option in SleepTimer.Options(end, now))
        {
            var item = new RadioMenuFlyoutItem
            {
                Text = SleepTimer.Label(option),
                GroupName = "sleep",
                IsChecked = status.Option == option,
            };
            item.Click += (_, _) =>
            {
                if (!_sleep.Start(option, ProgramEnd(DateTimeOffset.UtcNow)))
                {
                    ShowNotice("The guide doesn't know when this program ends.");
                }
            };
            SleepMenu.Items.Add(item);
        }
    }

    private void RenderSleep()
    {
        if (_leaving) return;
        var status = _sleep.Status;
        SleepLabel.Text = SleepTimer.ButtonLabel(status);
        if (status.Warning && status.Remaining is { } left)
        {
            SleepBar.Message = SleepTimer.PromptText(left);
            SleepBar.IsOpen = true;
        }
        else
        {
            SleepBar.IsOpen = false;
        }
    }

    private void OnKeepWatchingClick(object sender, RoutedEventArgs e) => _sleep.Extend();

    private void OnSkipAdClick(object sender, RoutedEventArgs e) => SkipAd();

    private void GoLive()
    {
        if (LiveWindow() is not { } w) return;
        _player.PlaybackSession.Position = TimeSpan.FromSeconds(LiveEdge.LiveTarget(w.Start, w.End, w.Offset));
        _player.Play();
    }

    private void Skip(double seconds)
    {
        if (!_opened) return;
        var session = _player.PlaybackSession;
        var current = session.Position.TotalSeconds;
        double target;
        if (LiveWindow() is { } w)
        {
            var live = LiveEdge.LiveTarget(w.Start, w.End, w.Offset);
            target = seconds < 0 ? LiveEdge.SkipBack(current, w.Start, -seconds) : LiveEdge.SkipForward(current, live, seconds);
        }
        else
        {
            var duration = session.NaturalDuration.TotalSeconds;
            target = Math.Clamp(current + seconds, 0, duration > 0 ? Math.Max(0, duration - 1) : current + Math.Max(0, seconds));
        }
        session.Position = TimeSpan.FromSeconds(target);
    }

    private void Zap(int step)
    {
        if (_live?.CurrentChannel is not { } current || _request is not PlayerRequest.Live live) return;
        var lineup = live.Lineup;
        if (lineup.Count < 2) return;
        var index = -1;
        for (var i = 0; i < lineup.Count; i++)
        {
            if (lineup[i].Id == current.Id)
            {
                index = i;
                break;
            }
        }
        var next = lineup[((index < 0 ? 0 : index + step) % lineup.Count + lineup.Count) % lineup.Count];
        _live.Play(next);
    }

    // ── Overlay and notices ─────────────────────────────────────────────────

    private void ShowOverlay(bool busy, string title, string? body, bool retry)
    {
        Overlay.Visibility = Visibility.Visible;
        OverlayRing.IsActive = busy;
        OverlayRing.Visibility = busy ? Visibility.Visible : Visibility.Collapsed;
        OverlayTitle.Text = title;
        OverlayBody.Text = body ?? "";
        OverlayBody.Visibility = string.IsNullOrEmpty(body) ? Visibility.Collapsed : Visibility.Visible;
        OverlayButtons.Visibility = busy ? Visibility.Collapsed : Visibility.Visible;
        RetryButton.Visibility = retry ? Visibility.Visible : Visibility.Collapsed;
    }

    private void HideOverlay()
    {
        Overlay.Visibility = Visibility.Collapsed;
        OverlayRing.IsActive = false;
    }

    private void ShowNotice(string message)
    {
        NoticeBar.Message = message;
        NoticeBar.IsOpen = true;
        var timer = DispatcherQueue.CreateTimer();
        timer.Interval = TimeSpan.FromSeconds(4);
        timer.IsRepeating = false;
        timer.Tick += (_, _) => NoticeBar.IsOpen = false;
        timer.Start();
    }

    private static string Describe(Channel c) => $"{c.GuideNumber} {c.Name}".Trim();

    // ── Input ───────────────────────────────────────────────────────────────

    private void ToggleFullScreen()
    {
        var on = !App.MainWindow.IsFullScreen;
        App.MainWindow.SetFullScreen(on);
        FullScreenIcon.Glyph = on ? "" : "";
    }

    private void OnPreviewKeyDown(object sender, KeyRoutedEventArgs e)
    {
        switch (e.Key)
        {
            case Windows.System.VirtualKey.Escape when App.MainWindow.IsFullScreen:
                ToggleFullScreen();
                break;
            case Windows.System.VirtualKey.Space:
                if (_player.PlaybackSession.PlaybackState == MediaPlaybackState.Playing) _player.Pause();
                else _player.Play();
                break;
            case Windows.System.VirtualKey.Left:
                Skip(-LiveEdge.SkipSeconds);
                break;
            case Windows.System.VirtualKey.Right:
                Skip(LiveEdge.SkipSeconds);
                break;
            case Windows.System.VirtualKey.End:
                GoLive();
                break;
            case Windows.System.VirtualKey.PageUp:
                Zap(-1);
                break;
            case Windows.System.VirtualKey.PageDown:
                Zap(1);
                break;
            case Windows.System.VirtualKey.S:
                if (!SkipAd()) return;
                break;
            default:
                return;
        }
        e.Handled = true;
    }

    private void OnVideoDoubleTapped(object sender, DoubleTappedRoutedEventArgs e) => ToggleFullScreen();

    private void OnFullScreenClick(object sender, RoutedEventArgs e) => ToggleFullScreen();

    private void OnBackClick(object sender, RoutedEventArgs e) => GoBack();

    private void OnGoLiveClick(object sender, RoutedEventArgs e) => GoLive();

    private void OnSkipBackClick(object sender, RoutedEventArgs e) => Skip(-LiveEdge.SkipSeconds);

    private void OnSkipForwardClick(object sender, RoutedEventArgs e) => Skip(LiveEdge.SkipSeconds);

    private void OnRetryClick(object sender, RoutedEventArgs e)
    {
        if (_live != null)
        {
            _loadedUri = null;
            _live.Retry();
        }
    }
}
