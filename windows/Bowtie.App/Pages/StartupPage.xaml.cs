using BowtieApp.Services;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace BowtieApp.Pages;

/// <summary>Signs in with the saved refresh token; offers a retry when the server is unreachable.</summary>
public sealed partial class StartupPage : Page
{
    public StartupPage()
    {
        InitializeComponent();
        Loaded += (_, _) => _ = StartAsync();
    }

    private async Task StartAsync()
    {
        ErrorPanel.Visibility = Visibility.Collapsed;
        Busy.IsActive = true;
        await AppServices.App.StartAsync();
        Busy.IsActive = false;
        if (AppServices.App.StartupError is { } error)
        {
            ErrorLabel.Text = error;
            ErrorPanel.Visibility = Visibility.Visible;
        }
    }

    private void OnRetryClick(object sender, RoutedEventArgs e) => _ = StartAsync();

    private void OnChangeServerClick(object sender, RoutedEventArgs e) => AppServices.App.ChangeServer();
}
