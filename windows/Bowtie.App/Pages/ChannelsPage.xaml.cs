using System.ComponentModel;
using Bowtie.Core.ViewModels;
using BowtieApp.Services;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Navigation;

namespace BowtieApp.Pages;

/// <summary>
/// Channel list: Recent row, favorites first, star toggles. Refreshes on
/// show, every 5 minutes while visible, and redraws progress every minute.
/// </summary>
public sealed partial class ChannelsPage : Page
{
    private readonly ChannelListViewModel _vm;
    private readonly DispatcherQueueTimer _timer;

    public ChannelsPage()
    {
        InitializeComponent();
        _vm = AppServices.Channels;
        _timer = DispatcherQueue.CreateTimer();
        _timer.Interval = TimeSpan.FromMinutes(1);
        _timer.Tick += (_, _) =>
        {
            Render();
            _ = _vm.RefreshIfStaleAsync();
        };
        Loaded += OnLoaded;
        Unloaded += OnUnloaded;
    }

    protected override void OnNavigatedTo(NavigationEventArgs e)
    {
        base.OnNavigatedTo(e);
        _ = _vm.RefreshIfStaleAsync();
    }

    /// <summary>Called by the shell when the player closes: the Recent row may have changed.</summary>
    public void OnReturnedFromPlayer() => _ = _vm.RefreshRecentsAsync();

    private void OnLoaded(object sender, RoutedEventArgs e)
    {
        _vm.PropertyChanged += OnVmChanged;
        _timer.Start();
        Render();
    }

    private void OnUnloaded(object sender, RoutedEventArgs e)
    {
        _vm.PropertyChanged -= OnVmChanged;
        _timer.Stop();
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
        var favorites = _vm.Favorites.Select(r => new ChannelItem(r, _vm.LogoUri(r.Channel.LogoUrl), supported, now)).ToList();
        var others = _vm.Others.Select(r => new ChannelItem(r, _vm.LogoUri(r.Channel.LogoUrl), supported, now)).ToList();
        var recents = _vm.Recents.Select(r => new RecentItem(r, _vm.LogoUri(r.LogoUrl))).ToList();

        FavoritesList.ItemsSource = favorites;
        OthersList.ItemsSource = others;
        RecentList.ItemsSource = recents;

        var showLists = status == ChannelListStatus.Loaded;
        RecentPanel.Visibility = showLists && recents.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
        FavoritesHeader.Visibility = showLists && favorites.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
        AllHeader.Visibility = showLists && favorites.Count > 0 && others.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
    }

    private void OnRefreshClick(object sender, RoutedEventArgs e) => _ = _vm.RefreshAsync();

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
