using BowtieApp.Services;
using Microsoft.UI.Xaml;

namespace BowtieApp;

/// <summary>Application entry: sets up services and opens the main window.</summary>
public partial class App : Application
{
    private MainWindow? _window;

    public App()
    {
        InitializeComponent();
        AppServices.Initialize();
    }

    /// <summary>The app's single window.</summary>
    public static MainWindow MainWindow { get; private set; } = null!;

    protected override void OnLaunched(Microsoft.UI.Xaml.LaunchActivatedEventArgs args)
    {
        _window = new MainWindow();
        MainWindow = _window;
        _window.Activate();
    }
}
