using System.ComponentModel;
using Bowtie.Core;
using Bowtie.Core.ViewModels;
using BowtieApp.Services;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Navigation;

namespace BowtieApp.Pages;

/// <summary>Recordings: Upcoming / Recorded / Missed / Shows, with series settings.</summary>
public sealed partial class RecordingsPage : Page
{
    private readonly RecordingsViewModel _vm;
    private bool _editingRule;

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
        // Removing from Continue watching resets the position: the "stopped at" line changes.
        ContinueStrip.ItemRemoved += (_, _) => _ = _vm.RefreshAsync();
    }

    protected override void OnNavigatedTo(NavigationEventArgs e)
    {
        base.OnNavigatedTo(e);
        Tabs.SelectedItem = _vm.ShowingShows ? ShowsTab : _vm.Tab switch
        {
            RecordingTab.Upcoming => UpcomingTab,
            RecordingTab.Missed => MissedTab,
            _ => RecordedTab,
        };
        _ = _vm.ShowingShows ? _vm.SelectShowsAsync() : _vm.SelectTabAsync(_vm.Tab);
        ContinueStrip.Reload();
    }

    /// <summary>Called by the shell when the player closes: positions changed.</summary>
    public async void OnReturnedFromPlayer()
    {
        ContinueStrip.ReloadAfterPlayer();
        // Let the player's last position save land so "stopped at" is current.
        await _vm.SavesSettledAsync(TimeSpan.FromSeconds(3));
        await _vm.RefreshAsync();
    }

    private void OnVmChanged(object? sender, PropertyChangedEventArgs e) => DispatcherQueue.TryEnqueue(Render);

    private void Render()
    {
        LoadingRing.IsActive = _vm.Status == RecordingsStatus.Loading;
        FailedPanel.Visibility = _vm.Status == RecordingsStatus.Failed ? Visibility.Visible : Visibility.Collapsed;
        FailedLabel.Text = _vm.Error ?? "";

        var now = DateTimeOffset.Now;
        var items = _vm.Status == RecordingsStatus.Loaded && !_vm.ShowingShows
            ? _vm.Items.Select(r => new RecordingItem(r, now)).ToList()
            : new List<RecordingItem>();
        RecordingsList.ItemsSource = items;
        RecordingsList.Visibility = _vm.ShowingShows ? Visibility.Collapsed : Visibility.Visible;
        RulesList.Visibility = _vm.ShowingShows ? Visibility.Visible : Visibility.Collapsed;
        RulesList.ItemsSource = _vm.Status == RecordingsStatus.Loaded && _vm.ShowingShows
            ? _vm.Rules.Select(r => new RecordingRuleItem(r)).ToList()
            : new List<RecordingRuleItem>();

        var empty = _vm.ShowingShows ? _vm.Rules.Count == 0 : items.Count == 0;
        EmptyLabel.Visibility = _vm.Status == RecordingsStatus.Loaded && empty ? Visibility.Visible : Visibility.Collapsed;
        EmptyLabel.Text = _vm.ShowingShows ? "No shows scheduled. To record a series, choose Record show in the guide on another app." : _vm.Tab switch
        {
            RecordingTab.Upcoming => "Nothing scheduled. To record a show, pick it in the guide on the web app or a TV app and choose Record.",
            RecordingTab.Missed => "No missed recordings.",
            _ => "No recordings yet.",
        };

        MessageBar.Message = _vm.Message ?? "";
        MessageBar.IsOpen = _vm.Message != null;
    }

    private void OnTabChanged(SelectorBar sender, SelectorBarSelectionChangedEventArgs args)
    {
        if (_vm == null) return;
        if (sender.SelectedItem == ShowsTab)
        {
            if (!_vm.ShowingShows) _ = _vm.SelectShowsAsync();
            return;
        }
        var tab = sender.SelectedItem == UpcomingTab ? RecordingTab.Upcoming
            : sender.SelectedItem == MissedTab ? RecordingTab.Missed
            : RecordingTab.Recorded;
        if (_vm.ShowingShows || tab != _vm.Tab) _ = _vm.SelectTabAsync(tab);
    }

    private void OnRefreshClick(object sender, RoutedEventArgs e)
    {
        _ = _vm.RefreshAsync();
        ContinueStrip.Reload();
    }

    private void OnMessageClosed(InfoBar sender, InfoBarClosedEventArgs args) => _vm.ClearMessage();

    private static Recording? RecordingOf(object sender) =>
        ((sender as FrameworkElement)?.DataContext as RecordingItem)?.Recording;

    private async void OnEditRuleClick(object sender, RoutedEventArgs e)
    {
        if (_editingRule || (sender as FrameworkElement)?.DataContext is not RecordingRuleItem item || !item.Rule.CanEdit) return;
        _editingRule = true;
        try
        {
            await EditRuleAsync(item.Rule);
        }
        finally
        {
            _editingRule = false;
        }
    }

    private async Task EditRuleAsync(RecordingRule rule)
    {
        var editor = new RecordingRuleEditor(rule);
        var options = await _vm.RuleChannelsAsync(rule);
        if (!IsLoaded) return;
        var episodes = new ComboBox
        {
            Header = "Episodes", ItemsSource = new[] { "New episodes only", "All episodes" },
            SelectedIndex = editor.NewOnly ? 0 : 1, HorizontalAlignment = HorizontalAlignment.Stretch,
        };
        var channel = new ComboBox
        {
            Header = "Channel", ItemsSource = options, DisplayMemberPath = nameof(RecordingRuleEditor.ChannelOption.Name),
            SelectedItem = options.First(c => c.Id == editor.ChannelId), HorizontalAlignment = HorizontalAlignment.Stretch,
        };
        var keep = new ComboBox
        {
            Header = "Keep", ItemsSource = new[] { "Keep all", "Keep latest N" },
            SelectedIndex = editor.KeepAll ? 0 : 1, HorizontalAlignment = HorizontalAlignment.Stretch,
        };
        var count = new TextBox
        {
            Header = "Episodes to keep", Text = editor.Count,
            Visibility = editor.KeepAll ? Visibility.Collapsed : Visibility.Visible,
        };
        keep.SelectionChanged += (_, _) => count.Visibility = keep.SelectedIndex == 0 ? Visibility.Collapsed : Visibility.Visible;
        var error = new InfoBar { Severity = InfoBarSeverity.Error, IsClosable = false };
        var fields = new StackPanel { Spacing = 12 };
        fields.Children.Add(episodes);
        fields.Children.Add(channel);
        fields.Children.Add(keep);
        fields.Children.Add(count);
        fields.Children.Add(new TextBlock { Text = RecordingRuleEditor.KeepHint, TextWrapping = TextWrapping.Wrap });
        fields.Children.Add(error);
        var dialog = new ContentDialog
        {
            XamlRoot = XamlRoot, Title = $"Edit {rule.Title}",
            Content = new ScrollViewer { Content = fields, VerticalScrollBarVisibility = ScrollBarVisibility.Auto },
            PrimaryButtonText = "Save", CloseButtonText = "Cancel", DefaultButton = ContentDialogButton.Primary,
        };
        var saving = false;
        dialog.Closing += (_, args) => { if (saving) args.Cancel = true; };
        dialog.PrimaryButtonClick += async (_, args) =>
        {
            var deferral = args.GetDeferral();
            args.Cancel = true;
            saving = true;
            foreach (var control in fields.Children.OfType<Control>()) control.IsEnabled = false;
            dialog.IsPrimaryButtonEnabled = false;
            try
            {
                editor.NewOnly = episodes.SelectedIndex == 0;
                editor.ChannelId = ((RecordingRuleEditor.ChannelOption)channel.SelectedItem).Id;
                editor.KeepAll = keep.SelectedIndex == 0;
                editor.Count = count.Text;
                args.Cancel = !await _vm.UpdateRuleAsync(rule, editor);
                error.Message = _vm.Message ?? "";
                error.IsOpen = args.Cancel;
            }
            finally
            {
                saving = false;
                foreach (var control in fields.Children.OfType<Control>()) control.IsEnabled = true;
                dialog.IsPrimaryButtonEnabled = true;
                deferral.Complete();
            }
        };
        await dialog.ShowAsync();
    }

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
        if (await _vm.DeleteAsync(r)) ContinueStrip.Reload();
    }
}
