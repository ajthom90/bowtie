using System;
using System.IO;
using BowtieApp.Services;
using Microsoft.UI.Xaml;

namespace BowtieApp;

/// <summary>Application entry: sets up services and opens the main window.</summary>
public partial class App : Application
{
    private MainWindow? _window;

    public App()
    {
        // Unhandled errors go to %LOCALAPPDATA%\Bowtie\crash.log so a crash
        // can be reported (WinUI otherwise only leaves a generic event log entry).
        AppDomain.CurrentDomain.UnhandledException += (_, e) => LogCrash("AppDomain", e.ExceptionObject as Exception);
        UnhandledException += (_, e) => LogCrash("XAML", e.Exception);
        try
        {
            InitializeComponent();
            AppServices.Initialize();
        }
        catch (Exception ex)
        {
            LogCrash("startup", ex);
            throw;
        }
    }

    /// <summary>The app's single window.</summary>
    public static MainWindow MainWindow { get; private set; } = null!;

    protected override void OnLaunched(Microsoft.UI.Xaml.LaunchActivatedEventArgs args)
    {
        try
        {
            _window = new MainWindow();
            MainWindow = _window;
            _window.Activate();
        }
        catch (Exception ex)
        {
            LogCrash("launch", ex);
            throw;
        }
    }

    /// <summary>Appends an exception to the crash log; never throws.</summary>
    internal static void LogCrash(string where, Exception? ex)
    {
        try
        {
            var dir = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "Bowtie");
            Directory.CreateDirectory(dir);
            File.AppendAllText(Path.Combine(dir, "crash.log"),
                $"{DateTimeOffset.Now:O} [{where}] {ex}{Environment.NewLine}{Environment.NewLine}");
        }
        catch
        {
            // Nothing more we can do.
        }
    }
}
