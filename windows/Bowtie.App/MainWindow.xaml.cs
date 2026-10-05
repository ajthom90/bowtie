using System.ComponentModel;
using System.Runtime.InteropServices;
using Bowtie.Core;
using Bowtie.Core.ViewModels;
using BowtieApp.Pages;
using BowtieApp.Services;
using Microsoft.UI;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Media.Animation;
using Windows.Graphics;

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
        PlaceWindow();
        AppWindow.Closing += (_, _) => RememberPlacement();

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

    /// <summary>
    /// Opens where the window was last closed, or 1280×800 view pixels
    /// centered on first launch. AppWindow sizes in physical pixels, so the
    /// default is scaled by the display's DPI and clamped to its work area.
    /// </summary>
    private void PlaceWindow()
    {
        try
        {
            var saved = AppServices.Preferences.WindowBounds;
            var display = saved is { } s
                ? DisplayArea.GetFromRect(new RectInt32(s.X, s.Y, s.Width, s.Height), DisplayAreaFallback.Primary)
                : DisplayArea.GetFromWindowId(AppWindow.Id, DisplayAreaFallback.Primary);
            var work = display.WorkArea;
            var hwnd = Win32Interop.GetWindowFromWindowId(AppWindow.Id);
            var dpi = GetDpiForWindow(hwnd);
            var scale = dpi == 0 ? 1.0 : dpi / 96.0;
            var rect = WindowPlacement.Restore(saved, new PixelRect(work.X, work.Y, work.Width, work.Height), scale);
            AppWindow.MoveAndResize(new RectInt32(rect.X, rect.Y, rect.Width, rect.Height));
        }
        catch (Exception ex)
        {
            // Never fail startup over window placement: keep the system default.
            App.LogCrash("window placement", ex);
        }
    }

    /// <summary>Saves the window's size and position unless it's full screen, maximized or minimized.</summary>
    private void RememberPlacement()
    {
        if (AppWindow.Presenter is not OverlappedPresenter { State: OverlappedPresenterState.Restored }) return;
        var pos = AppWindow.Position;
        var size = AppWindow.Size;
        AppServices.Preferences.WindowBounds = new PixelRect(pos.X, pos.Y, size.Width, size.Height);
    }

    [DllImport("user32.dll")]
    private static extern uint GetDpiForWindow(IntPtr hwnd);

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
