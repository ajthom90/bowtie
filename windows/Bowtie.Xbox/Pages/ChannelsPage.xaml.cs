using System;
using System.Collections.Generic;
using System.ComponentModel;
using System.Linq;
using System.Threading.Tasks;
using Bowtie.Core;
using Bowtie.Core.ViewModels;
using BowtieXbox.Services;
using Windows.System;
using Windows.UI.Core;
using Windows.UI.Xaml;
using Windows.UI.Xaml.Automation.Peers;
using Windows.UI.Xaml.Controls;
using Windows.UI.Xaml.Input;
using Windows.UI.Xaml.Navigation;

namespace BowtieXbox.Pages
{
    /// <summary>
    /// The channel list: favorites first, then the rest, each with now/next
    /// from the guide. D-pad moves, A watches, Y stars or unstars.
    /// Channels that can't start because every tuner is busy are left out
    /// under a note; the list re-checks every 30 seconds while on screen and
    /// when the app comes back, keeping D-pad focus on the same channel (or
    /// its neighbor when that one drops out).
    /// </summary>
    public sealed partial class ChannelsPage : Page
    {
        private const string Hint = "A to watch · Y to add or remove a favorite · B to go back";

        private readonly DispatcherTimer _pollTimer;
        private ChannelListViewModel? _vm;
        private long? _focusChannelId;
        private string? _shown;
        private bool _polling;

        public ChannelsPage()
        {
            InitializeComponent();
            // Coming back from the player keeps this page (and its focus).
            NavigationCacheMode = NavigationCacheMode.Required;
            _pollTimer = new DispatcherTimer { Interval = ChannelListViewModel.RecheckInterval };
            _pollTimer.Tick += (_, __) => _ = PollAsync();
        }

        protected override async void OnNavigatedTo(NavigationEventArgs e)
        {
            base.OnNavigatedTo(e);
            var vm = AppServices.Channels;
            if (!ReferenceEquals(vm, _vm))
            {
                if (_vm != null) _vm.PropertyChanged -= OnVmChanged;
                _vm = vm;
                _vm.PropertyChanged += OnVmChanged;
                _shown = null;
            }
            UserLabel.Text = AppServices.App.User is { } user
                ? $"{user.Username} on {AppServices.App.ServerDisplay}"
                : AppServices.App.ServerDisplay ?? "";
            _pollTimer.Start();
            Application.Current.Resuming += OnResuming;
            Window.Current.VisibilityChanged += OnVisibilityChanged;
            Render();
            // Where the channel just watched sits now, in case the re-check drops it.
            var shownIndex = _focusChannelId is long id && ChannelList.ItemsSource is List<ChannelItem> shown
                ? Math.Max(0, shown.FindIndex(i => i.Channel.Id == id))
                : 0;
            // Back from the player a tuner just freed up: re-check now.
            await PollAsync();
            if (!ReferenceEquals(Frame?.Content, this)) return; // already left (A pressed meanwhile)
            Render();
            FocusList(shownIndex);
        }

        protected override void OnNavigatedFrom(NavigationEventArgs e)
        {
            _pollTimer.Stop();
            Application.Current.Resuming -= OnResuming;
            Window.Current.VisibilityChanged -= OnVisibilityChanged;
            base.OnNavigatedFrom(e);
        }

        // ── Re-checking busy tuners ─────────────────────────────────────────

        private void OnResuming(object sender, object e) => RunOnUi(() => _ = PollAsync());

        private void OnVisibilityChanged(object sender, VisibilityChangedEventArgs e)
        {
            if (e.Visible) _ = PollAsync();
        }

        /// <summary>One re-check at a time (a slow server mustn't stack them up).</summary>
        private async Task PollAsync()
        {
            if (_vm == null || _polling) return;
            _polling = true;
            try
            {
                await _vm.PollAsync();
            }
            catch (Exception ex)
            {
                App.LogCrash("channels poll", ex);
            }
            finally
            {
                _polling = false;
            }
        }

        // ── Rendering ───────────────────────────────────────────────────────

        private void OnVmChanged(object? sender, PropertyChangedEventArgs e) =>
            RunOnUi(() => OnVmChangedOnUi(e.PropertyName));

        private void OnVmChangedOnUi(string? property)
        {
            if (property == nameof(ChannelListViewModel.Message)) ShowMessage();
            else if (property == nameof(ChannelListViewModel.Rows) || property == nameof(ChannelListViewModel.Status)) Render();
        }

        private void Render()
        {
            var vm = _vm;
            if (vm == null) return;
            var now = DateTimeOffset.Now;
            var note = vm.Status == ChannelListStatus.Loaded ? vm.TunersNote : null;
            switch (vm.Status)
            {
                case ChannelListStatus.Loading:
                    ShowStatus(busy: true, text: "Loading channels…", retry: false);
                    break;
                case ChannelListStatus.Empty:
                    ShowStatus(busy: false, text: "No channels yet. An admin can scan for channels in Bowtie's web app.", retry: true);
                    break;
                case ChannelListStatus.Failed:
                    ShowStatus(busy: false, text: vm.Error ?? ErrorText.SomethingWentWrong, retry: true);
                    break;
                default:
                    if (vm.NoneWatchable)
                    {
                        // Nothing can start: the note is the message, and Try again holds focus.
                        ShowStatus(busy: false, text: ChannelListViewModel.AllTunersBusyNote, retry: true);
                        note = null;
                        break;
                    }
                    ShowList(vm.VisibleRows(vm.Rows, now).Select(r => new ChannelItem(r, now)).ToList());
                    break;
            }
            ShowTunersNote(note);
        }

        /// <summary>The busy-tuners note above the list; Narrator reads it when it appears.</summary>
        private void ShowTunersNote(string? note)
        {
            TunersLabel.Text = note ?? "";
            var visibility = note != null ? Visibility.Visible : Visibility.Collapsed;
            if (TunersLabel.Visibility == visibility) return;
            TunersLabel.Visibility = visibility;
            if (note != null) Announce(TunersLabel);
        }

        /// <summary>Narrator reads <paramref name="label"/> (a polite live region) now.</summary>
        private static void Announce(FrameworkElement label)
        {
            var peer = FrameworkElementAutomationPeer.FromElement(label)
                       ?? FrameworkElementAutomationPeer.CreatePeerForElement(label);
            peer?.RaiseAutomationEvent(AutomationEvents.LiveRegionChanged);
        }

        /// <summary>
        /// Shows <paramref name="items"/>, skipping identical redraws (the
        /// 30-second re-check usually changes nothing). When focus was in
        /// the list, it stays on the same channel, or moves to the one that
        /// took its place when that channel dropped out.
        /// </summary>
        private void ShowList(List<ChannelItem> items)
        {
            var shown = string.Join("\n", items.Select(i => $"{i.Channel.Id}|{i.Star}|{i.Now}|{i.Next}|{i.NoSignalVisibility}"));
            var wasVisible = ChannelList.Visibility == Visibility.Visible;
            if (wasVisible && shown == _shown) return;

            var focusedIndex = FocusedIndex();
            // Focus on Try again (about to hide) or nowhere: give it to the list.
            var focusInStatus = RetryButton.FocusState != FocusState.Unfocused || FocusManager.GetFocusedElement() == null;
            if (focusedIndex is int index && ChannelList.Items.Count > index && ChannelList.Items[index] is ChannelItem focused)
            {
                _focusChannelId = focused.Channel.Id;
            }

            StatusPanel.Visibility = Visibility.Collapsed;
            Spinner.IsActive = false;
            RetryButton.Visibility = Visibility.Collapsed;
            ChannelList.Visibility = Visibility.Visible;
            ChannelList.ItemsSource = items;
            _shown = shown;

            if (focusedIndex != null || focusInStatus) FocusList(focusedIndex ?? 0);
        }

        private void ShowStatus(bool busy, string text, bool retry)
        {
            var hadRetry = RetryButton.Visibility == Visibility.Visible;
            var focusInList = FocusedIndex() != null;
            ChannelList.Visibility = Visibility.Collapsed;
            StatusPanel.Visibility = Visibility.Visible;
            Spinner.IsActive = busy;
            var changed = StatusText.Text != text;
            StatusText.Text = text;
            if (changed) Announce(StatusText);
            RetryButton.Visibility = retry ? Visibility.Visible : Visibility.Collapsed;
            // Only on appearing (or when the list that held focus went away): re-checks mustn't steal focus.
            if (retry && (!hadRetry || focusInList)) RetryButton.Focus(FocusState.Programmatic);
        }

        private async void ShowMessage()
        {
            var message = _vm?.Message;
            if (message == null) return;
            _vm!.ConsumeMessage();
            HintLabel.Text = message;
            await Task.Delay(TimeSpan.FromSeconds(4));
            HintLabel.Text = Hint;
        }

        // ── Focus ───────────────────────────────────────────────────────────

        /// <summary>The index of the focused channel row, or null when focus is elsewhere.</summary>
        private int? FocusedIndex()
        {
            if (!(FocusManager.GetFocusedElement() is ListViewItem container)) return null;
            var index = ChannelList.IndexFromContainer(container);
            return index >= 0 ? index : (int?)null;
        }

        /// <summary>
        /// Focus the remembered channel (after a star toggle reorders the list
        /// or a re-check drops channels), else the row at <paramref name="fallbackIndex"/>
        /// (clamped, so a shorter list keeps focus near where it was).
        /// </summary>
        private void FocusList(int fallbackIndex = 0)
        {
            if (ChannelList.Visibility != Visibility.Visible || ChannelList.Items.Count == 0) return;
            var items = ChannelList.ItemsSource as List<ChannelItem>;
            var index = Math.Max(0, Math.Min(fallbackIndex, ChannelList.Items.Count - 1));
            if (_focusChannelId is long id && items != null)
            {
                var found = items.FindIndex(i => i.Channel.Id == id);
                if (found >= 0) index = found;
            }
            ChannelList.ScrollIntoView(ChannelList.Items[index]);
            ChannelList.UpdateLayout();
            if (ChannelList.ContainerFromIndex(index) is ListViewItem container)
            {
                container.Focus(FocusState.Programmatic);
            }
            else
            {
                ChannelList.Focus(FocusState.Programmatic);
            }
        }

        // ── Actions ─────────────────────────────────────────────────────────

        private void OnChannelClick(object sender, ItemClickEventArgs e)
        {
            if (e.ClickedItem is ChannelItem item)
            {
                _focusChannelId = item.Channel.Id;
                Frame.Navigate(typeof(PlayerPage), item.Channel);
            }
        }

        private async void OnListKeyDown(object sender, KeyRoutedEventArgs e)
        {
            if (e.Key != VirtualKey.GamepadY && e.Key != VirtualKey.F) return;
            if (!(e.OriginalSource is ListViewItem container) || !(container.Content is ChannelItem item)) return;
            e.Handled = true;
            if (_vm == null || !_vm.FavoritesSupported) return;
            _focusChannelId = item.Channel.Id;
            await _vm.ToggleFavoriteAsync(item.Channel.Id);
            Render();
            FocusList();
        }

        private async void OnRefresh(object sender, RoutedEventArgs e)
        {
            if (_vm == null) return;
            await _vm.RefreshAsync();
            Render();
            FocusList();
        }

        private async void OnSignOut(object sender, RoutedEventArgs e) => await AppServices.App.SignOutAsync();

        private void RunOnUi(Action action)
        {
            if (Dispatcher.HasThreadAccess) action();
            else _ = Dispatcher.RunAsync(CoreDispatcherPriority.Normal, () => action());
        }
    }
}
