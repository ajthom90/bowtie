using System.ComponentModel;
using Bowtie.Core;
using Bowtie.Core.ViewModels;
using BowtieApp.Services;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Navigation;

namespace BowtieApp.Pages;

/// <summary>
/// Channel list: Recent row, favorites first, star toggles, and the guide
/// category filter (channels with nothing matching in the 4-hour window are
/// hidden; non-matching now/next lines dim). Channels that can't start
/// because every tuner is busy are hidden under a note. Refreshes on show,
/// every 5 minutes while visible, and re-checks the channels (and redraws
/// progress) every 30 seconds and when the window comes back to the front.
/// </summary>
public sealed partial class ChannelsPage : Page
{
    private readonly ChannelListViewModel _vm;
    private readonly DispatcherQueueTimer _timer;
    private bool _syncingFilter;

    public ChannelsPage()
    {
        // Before InitializeComponent: the filter bar can raise SelectionChanged while loading.
        _vm = AppServices.Channels;
        InitializeComponent();
        _timer = DispatcherQueue.CreateTimer();
        _timer.Interval = ChannelListViewModel.RecheckInterval;
        _timer.Tick += (_, _) =>
        {
            Render();
            _ = _vm.PollAsync();
        };
        Loaded += OnLoaded;
        Unloaded += OnUnloaded;
    }

    protected override void OnNavigatedTo(NavigationEventArgs e)
    {
        base.OnNavigatedTo(e);
        _ = _vm.PollAsync();
        ContinueStrip.Reload();
    }

    /// <summary>Called by the shell when the player closes: the Recent row and saved positions may have changed.</summary>
    public void OnReturnedFromPlayer()
    {
        _ = _vm.RefreshRecentsAsync();
        _ = _vm.PollAsync(); // leaving freed a tuner
        ContinueStrip.ReloadAfterPlayer();
    }

    private void OnLoaded(object sender, RoutedEventArgs e)
    {
        _vm.PropertyChanged += OnVmChanged;
        App.MainWindow.Activated += OnWindowActivated;
        _timer.Start();
        Render();
    }

    private void OnUnloaded(object sender, RoutedEventArgs e)
    {
        _vm.PropertyChanged -= OnVmChanged;
        App.MainWindow.Activated -= OnWindowActivated;
        _timer.Stop();
    }

    /// <summary>Back in front: a tuner may have freed up while the window was away.</summary>
    private void OnWindowActivated(object sender, WindowActivatedEventArgs args)
    {
        if (args.WindowActivationState != WindowActivationState.Deactivated) _ = _vm.PollAsync();
    }

    private void OnVmChanged(object? sender, PropertyChangedEventArgs e) =>
        DispatcherQueue.TryEnqueue(Render);

    private void Render()
    {
        var status = _vm.Status;
        LoadingRing.IsActive = status == ChannelListStatus.Loading && _vm.Rows.Count == 0;
        FailedPanel.Visibility = status == ChannelListStatus.Failed ? Visibility.Visible : Visibility.Collapsed;
        FailedLabel.Text = _vm.Error ?? "";
        EmptyLabel.Visibility = status == ChannelListStatus.Empty ? Visibility.Visible : Visibility.Collapsed;

        if (_vm.Message is { } message)
        {
            MessageBar.Message = message;
            MessageBar.IsOpen = true;
        }

        var now = DateTimeOffset.UtcNow;
        var supported = _vm.FavoritesSupported;
        var filter = _vm.Filter;
        SyncFilterBar(filter);
        var visible = _vm.VisibleRows(_vm.Rows, now);
        ChannelItem Item(ChannelRow r) =>
            new(r, _vm.LogoUri(r.Channel.LogoUrl), supported, now, _vm.Highlight(r, now));
        var favorites = visible.Where(r => r.IsFavorite).Select(Item).ToList();
        var others = visible.Where(r => !r.IsFavorite).Select(Item).ToList();
        var recents = _vm.VisibleRecents.Select(r => new RecentItem(r, _vm.LogoUri(r.LogoUrl))).ToList();

        FavoritesList.ItemsSource = favorites;
        OthersList.ItemsSource = others;
        RecentList.ItemsSource = recents;

        var showLists = status == ChannelListStatus.Loaded;
        FilterBar.Visibility = showLists ? Visibility.Visible : Visibility.Collapsed;
        TunersBar.Message = _vm.TunersNote ?? "";
        TunersBar.IsOpen = showLists && _vm.TunersNote != null;
        var filterEmpty = showLists && filter != GuideFilter.All && visible.Count == 0 && !_vm.NoneWatchable;
        FilterEmptyPanel.Visibility = filterEmpty ? Visibility.Visible : Visibility.Collapsed;
        FilterEmptyLabel.Text = filter.EmptyCopy();
        RecentPanel.Visibility = showLists && recents.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
        FavoritesHeader.Visibility = showLists && favorites.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
        AllHeader.Visibility = showLists && favorites.Count > 0 && others.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
    }

    private void OnRefreshClick(object sender, RoutedEventArgs e)
    {
        _ = _vm.RefreshAsync();
        ContinueStrip.Reload();
    }

    /// <summary>Select the chip for <paramref name="filter"/> without treating it as a user pick.</summary>
    private void SyncFilterBar(GuideFilter filter)
    {
        var item = FilterBar.Items.FirstOrDefault(i => i.Tag as string == filter.Stored());
        if (item == null || ReferenceEquals(FilterBar.SelectedItem, item)) return;
        _syncingFilter = true;
        FilterBar.SelectedItem = item;
        _syncingFilter = false;
    }

    private void OnFilterChanged(SelectorBar sender, SelectorBarSelectionChangedEventArgs args)
    {
        if (_syncingFilter || _vm == null) return;
        var picked = GuideFilters.Parse(sender.SelectedItem?.Tag as string);
        if (picked == _vm.Filter) return;
        _vm.SetFilter(picked); // redraws through PropertyChanged
    }

    private void OnShowAllClick(object sender, RoutedEventArgs e) => _vm.SetFilter(GuideFilter.All);

    private void OnMessageClosed(InfoBar sender, InfoBarClosedEventArgs args) => _vm.ConsumeMessage();

    private void OnChannelClick(object sender, RoutedEventArgs e)
    {
        if ((sender as FrameworkElement)?.DataContext is ChannelItem item) Watch(item.Channel);
    }

    private void OnStarClick(object sender, RoutedEventArgs e)
    {
        if ((sender as FrameworkElement)?.DataContext is ChannelItem item)
        {
            _ = _vm.ToggleFavoriteAsync(item.Channel.Id);
        }
    }

    private void OnRecentClick(object sender, RoutedEventArgs e)
    {
        if ((sender as FrameworkElement)?.DataContext is RecentItem item) Watch(_vm.ChannelFor(item.Recent));
    }

    private void Watch(Bowtie.Core.Channel channel)
    {
        var lineup = _vm.Rows.Select(r => r.Channel).ToList();
        App.MainWindow.Frame.Navigate(typeof(PlayerPage), new PlayerRequest.Live(channel, lineup));
    }
}
