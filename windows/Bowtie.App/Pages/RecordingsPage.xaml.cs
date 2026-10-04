using System.ComponentModel;
using Bowtie.Core;
using Bowtie.Core.ViewModels;
using BowtieApp.Services;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Navigation;

namespace BowtieApp.Pages;

/// <summary>Recordings: Upcoming / Recorded / Missed, play with resume, stop, keep, delete.</summary>
public sealed partial class RecordingsPage : Page
{
    private readonly RecordingsViewModel _vm;

    public RecordingsPage()
    {
        _vm = AppServices.Recordings;
        InitializeComponent();
        Loaded += (_, _) =>
        {
            _vm.PropertyChanged += OnVmChanged;
            Render();
        };
        Unloaded += (_, _) => _vm.PropertyChanged -= OnVmChanged;
    }

    protected override void OnNavigatedTo(NavigationEventArgs e)
    {
        base.OnNavigatedTo(e);
        Tabs.SelectedItem = _vm.Tab switch
        {
            RecordingTab.Upcoming => UpcomingTab,
            RecordingTab.Missed => MissedTab,
            _ => RecordedTab,
        };
        _ = _vm.SelectTabAsync(_vm.Tab);
    }

    /// <summary>Called by the shell when the player closes: positions changed.</summary>
    public void OnReturnedFromPlayer() => _ = _vm.RefreshAsync();

    private void OnVmChanged(object? sender, PropertyChangedEventArgs e) => DispatcherQueue.TryEnqueue(Render);

    private void Render()
    {
        LoadingRing.IsActive = _vm.Status == RecordingsStatus.Loading;
        FailedPanel.Visibility = _vm.Status == RecordingsStatus.Failed ? Visibility.Visible : Visibility.Collapsed;
        FailedLabel.Text = _vm.Error ?? "";

        var now = DateTimeOffset.Now;
        var items = _vm.Status == RecordingsStatus.Loaded
            ? _vm.Items.Select(r => new RecordingItem(r, now)).ToList()
            : new List<RecordingItem>();
        RecordingsList.ItemsSource = items;

        EmptyLabel.Visibility = _vm.Status == RecordingsStatus.Loaded && items.Count == 0 ? Visibility.Visible : Visibility.Collapsed;
        EmptyLabel.Text = _vm.Tab switch
        {
            RecordingTab.Upcoming => "Nothing scheduled. To record a show, pick it in the guide on the web app or a TV app and choose Record.",
            RecordingTab.Missed => "No missed recordings.",
            _ => "No recordings yet.",
        };

        if (_vm.Message is { } message)
        {
            MessageBar.Message = message;
            MessageBar.IsOpen = true;
        }
    }

    private void OnTabChanged(SelectorBar sender, SelectorBarSelectionChangedEventArgs args)
    {
        if (_vm == null) return;
        var tab = sender.SelectedItem == UpcomingTab ? RecordingTab.Upcoming
            : sender.SelectedItem == MissedTab ? RecordingTab.Missed
            : RecordingTab.Recorded;
        if (tab != _vm.Tab) _ = _vm.SelectTabAsync(tab);
    }

    private void OnRefreshClick(object sender, RoutedEventArgs e) => _ = _vm.RefreshAsync();

    private void OnMessageClosed(InfoBar sender, InfoBarClosedEventArgs args) => _vm.ClearMessage();

    private static Recording? RecordingOf(object sender) =>
        ((sender as FrameworkElement)?.DataContext as RecordingItem)?.Recording;

    private async void OnPlayClick(object sender, RoutedEventArgs e)
    {
        if (RecordingOf(sender) is not { } r) return;
        var start = await _vm.PlayAsync(r);
        if (start == null) return;

        var resumeAt = 0;
        if (start.OfferResume)
        {
            var dialog = new ContentDialog
            {
                XamlRoot = XamlRoot,
                Title = r.Title,
                Content = $"You stopped at {RecordingLogic.FormatClock(start.ResumeAtSec)}.",
                PrimaryButtonText = "Resume",
                SecondaryButtonText = "Start over",
                CloseButtonText = "Cancel",
                DefaultButton = ContentDialogButton.Primary,
            };
            var choice = await dialog.ShowAsync();
            if (choice == ContentDialogResult.None) return;
            if (choice == ContentDialogResult.Primary) resumeAt = start.ResumeAtSec;
        }
        App.MainWindow.Frame.Navigate(typeof(PlayerPage), new PlayerRequest.Vod(start, resumeAt));
    }

    private async void OnStopClick(object sender, RoutedEventArgs e)
    {
        if (RecordingOf(sender) is { } r) await _vm.StopAsync(r);
    }

    private async void OnKeepClick(object sender, RoutedEventArgs e)
    {
        if (RecordingOf(sender) is { } r) await _vm.SetKeptAsync(r, !r.IsProtected);
    }

    private async void OnDeleteClick(object sender, RoutedEventArgs e)
    {
        if (RecordingOf(sender) is not { } r) return;
        if (RecordingLogic.DeleteNeedsConfirm(r))
        {
            var dialog = new ContentDialog
            {
                XamlRoot = XamlRoot,
                Title = $"Delete “{r.Title}”?",
                Content = "This deletes the recording for everyone. It can't be undone.",
                PrimaryButtonText = "Delete",
                CloseButtonText = "Cancel",
                DefaultButton = ContentDialogButton.Close,
            };
            if (await dialog.ShowAsync() != ContentDialogResult.Primary) return;
        }
        await _vm.DeleteAsync(r);
    }
}
