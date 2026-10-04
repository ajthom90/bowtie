using System.ComponentModel;
using Bowtie.Core.ViewModels;
using BowtieApp.Pages;
using BowtieApp.Services;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Media.Animation;

namespace BowtieApp;

/// <summary>
/// The app window: one root frame that follows the sign-in phase
/// (Connect → Login → Ready), plus full-screen helpers for the player.
/// </summary>
public sealed partial class MainWindow : Window
{
    private AppPhase? _shownPhase;

    public MainWindow()
    {
        InitializeComponent();

        AppWindow.SetIcon(Path.Combine(AppContext.BaseDirectory, "Assets", "AppIcon.ico"));
        AppWindow.Resize(new Windows.Graphics.SizeInt32(1280, 800));

        AppServices.App.PropertyChanged += OnAppPropertyChanged;
        AppServices.App.SessionEnded += (_, _) =>
            DispatcherQueue.TryEnqueue(() => AppServices.App.OnSessionEnded());

        ShowPhase(AppServices.App.Phase);
    }

    /// <summary>The root frame (the player navigates over the shell in it).</summary>
    public Microsoft.UI.Xaml.Controls.Frame Frame => RootFrame;

    public bool IsFullScreen => AppWindow.Presenter.Kind == AppWindowPresenterKind.FullScreen;

    public void SetFullScreen(bool on)
    {
        if (on == IsFullScreen) return;
        AppWindow.SetPresenter(on ? AppWindowPresenterKind.FullScreen : AppWindowPresenterKind.Default);
    }

    private void OnAppPropertyChanged(object? sender, PropertyChangedEventArgs e)
    {
        if (e.PropertyName == nameof(AppViewModel.Phase))
        {
            DispatcherQueue.TryEnqueue(() => ShowPhase(AppServices.App.Phase));
        }
    }

    private void ShowPhase(AppPhase phase)
    {
        if (_shownPhase == phase) return;
        _shownPhase = phase;
        SetFullScreen(false);
        if (phase != AppPhase.Ready) AppServices.Reset();

        var page = phase switch
        {
            AppPhase.Connect => typeof(ConnectPage),
            AppPhase.Login => typeof(LoginPage),
            AppPhase.Checking => typeof(StartupPage),
            _ => typeof(ShellPage),
        };
        RootFrame.Navigate(page, null, new DrillInNavigationTransitionInfo());
        RootFrame.BackStack.Clear();
    }
}
