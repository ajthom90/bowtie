using System;
using System.ComponentModel;
using System.Linq;
using System.Threading.Tasks;
using Bowtie.Core.ViewModels;
using BowtieXbox.Services;
using Windows.System;
using Windows.UI.Core;
using Windows.UI.Xaml;
using Windows.UI.Xaml.Controls;
using Windows.UI.Xaml.Input;
using Windows.UI.Xaml.Navigation;

namespace BowtieXbox.Pages
{
    /// <summary>
    /// The channel list: favorites first, then the rest, each with now/next
    /// from the guide. D-pad moves, A watches, Y stars or unstars.
    /// </summary>
    public sealed partial class ChannelsPage : Page
    {
        private const string Hint = "A to watch · Y to add or remove a favorite · B to go back";

        private ChannelListViewModel? _vm;
        private long? _focusChannelId;

        public ChannelsPage()
        {
            InitializeComponent();
            // Coming back from the player keeps this page (and its focus).
            NavigationCacheMode = NavigationCacheMode.Required;
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
            }
            UserLabel.Text = AppServices.App.User is { } user
                ? $"{user.Username} on {AppServices.App.ServerDisplay}"
                : AppServices.App.ServerDisplay ?? "";
            Render();
            await vm.RefreshIfStaleAsync();
            Render();
            FocusList();
        }

        private void OnVmChanged(object? sender, PropertyChangedEventArgs e)
        {
            if (Dispatcher.HasThreadAccess) OnVmChangedOnUi(e.PropertyName);
            else _ = Dispatcher.RunAsync(CoreDispatcherPriority.Normal, () => OnVmChangedOnUi(e.PropertyName));
        }

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
            switch (vm.Status)
            {
                case ChannelListStatus.Loading:
                    ShowStatus(busy: true, text: "Loading channels…", retry: false);
                    break;
                case ChannelListStatus.Empty:
                    ShowStatus(busy: false, text: "No channels yet. An admin can scan for channels in Bowtie's web app.", retry: true);
                    break;
                case ChannelListStatus.Failed:
                    ShowStatus(busy: false, text: vm.Error ?? "Couldn't load channels.", retry: true);
                    break;
                default:
                    StatusPanel.Visibility = Visibility.Collapsed;
                    Spinner.IsActive = false;
                    ChannelList.Visibility = Visibility.Visible;
                    var items = vm.Rows.Select(r => new ChannelItem(r, now)).ToList();
                    ChannelList.ItemsSource = items;
                    break;
            }
        }

        private void ShowStatus(bool busy, string text, bool retry)
        {
            ChannelList.Visibility = Visibility.Collapsed;
            StatusPanel.Visibility = Visibility.Visible;
            Spinner.IsActive = busy;
            StatusText.Text = text;
            RetryButton.Visibility = retry ? Visibility.Visible : Visibility.Collapsed;
            if (retry) RetryButton.Focus(FocusState.Programmatic);
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

        /// <summary>Focus the remembered channel (after a star toggle reorders the list), else the first.</summary>
        private void FocusList()
        {
            if (ChannelList.Visibility != Visibility.Visible || ChannelList.Items.Count == 0) return;
            var items = ChannelList.ItemsSource as System.Collections.Generic.List<ChannelItem>;
            var index = 0;
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
    }
}
