using System.ComponentModel;
using Bowtie.Core.ViewModels;
using BowtieApp.Services;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace BowtieApp.Pages;

/// <summary>
/// "Continue watching" strip (top of Live TV and Recordings): part-watched
/// recordings, most recent first. Click resumes where you stopped; the
/// context menu (right-click, Shift+F10) removes one by resetting its
/// position. Hidden when there's nothing to continue.
/// </summary>
public sealed partial class ContinueWatchingStrip : UserControl
{
    private ContinueWatchingViewModel? _vm;
    private bool _starting;

    public ContinueWatchingStrip()
    {
        InitializeComponent();
        Loaded += OnLoaded;
        Unloaded += OnUnloaded;
    }

    /// <summary>A card was removed (its position is now 0), so lists showing positions can refresh.</summary>
    public event EventHandler? ItemRemoved;

    /// <summary>Reload from the server (page shown).</summary>
    public void Reload() => _ = Vm.LoadAsync();

    /// <summary>Reload once the player's last position save has landed.</summary>
    public void ReloadAfterPlayer() => _ = AppServices.ReloadContinueAfterPlayerAsync();

    /// <summary>The current sign-in's view model (a new sign-in replaces it).</summary>
    private ContinueWatchingViewModel Vm
    {
        get
        {
            var current = AppServices.Continue;
            if (!ReferenceEquals(current, _vm))
            {
                if (_vm != null) _vm.PropertyChanged -= OnVmChanged;
                _vm = current;
                if (IsLoaded) _vm.PropertyChanged += OnVmChanged;
            }
            return current;
        }
    }

    private void OnLoaded(object sender, RoutedEventArgs e)
    {
        var vm = Vm;
        vm.PropertyChanged -= OnVmChanged; // never twice
        vm.PropertyChanged += OnVmChanged;
        Render();
    }

    private void OnUnloaded(object sender, RoutedEventArgs e)
    {
        if (_vm != null) _vm.PropertyChanged -= OnVmChanged;
    }

    private void OnVmChanged(object? sender, PropertyChangedEventArgs e) => DispatcherQueue.TryEnqueue(Render);

    // Uses the view model it's attached to (not AppServices): this can run
    // after sign-out, when there's no client to make a new one from.
    private void Render()
    {
        if (_vm is not { } vm) return;
        var items = vm.Items.Select(r => new ContinueItem(r)).ToList();
        ItemsList.ItemsSource = items;
        Visibility = items.Count > 0 || vm.Message != null ? Visibility.Visible : Visibility.Collapsed;
        if (vm.Message is { } message)
        {
            MessageBar.Message = message;
            MessageBar.IsOpen = true;
        }
        else
        {
            MessageBar.IsOpen = false;
        }
    }

    private void OnMessageClosed(InfoBar sender, InfoBarClosedEventArgs args) => _vm?.ClearMessage();

    private async void OnResumeClick(object sender, RoutedEventArgs e)
    {
        if ((sender as FrameworkElement)?.Tag is not ContinueItem item || _starting || _vm is not { } vm) return;
        _starting = true;
        try
        {
            var start = await vm.PlayAsync(item.Recording);
            if (start == null) return;
            App.MainWindow.Frame.Navigate(typeof(PlayerPage), new PlayerRequest.Vod(start, start.ResumeAtSec));
        }
        finally
        {
            _starting = false;
        }
    }

    private async void OnRemoveClick(object sender, RoutedEventArgs e)
    {
        if ((sender as FrameworkElement)?.Tag is not ContinueItem item || _vm is not { } vm) return;
        if (await vm.RemoveAsync(item.Recording)) ItemRemoved?.Invoke(this, EventArgs.Empty);
    }
}
