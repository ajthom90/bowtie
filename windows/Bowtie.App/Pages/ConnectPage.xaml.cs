using BowtieApp.Services;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;

namespace BowtieApp.Pages;

/// <summary>First run: enter and check the server address.</summary>
public sealed partial class ConnectPage : Page
{
    public ConnectPage()
    {
        InitializeComponent();
        Loaded += (_, _) => ServerBox.Focus(FocusState.Programmatic);
    }

    private void OnServerKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key == Windows.System.VirtualKey.Enter)
        {
            e.Handled = true;
            _ = ConnectAsync();
        }
    }

    private void OnConnectClick(object sender, RoutedEventArgs e) => _ = ConnectAsync();

    private async Task ConnectAsync()
    {
        ErrorLabel.Visibility = Visibility.Collapsed;
        ConnectButton.IsEnabled = false;
        Busy.IsActive = true;
        try
        {
            var error = await AppServices.App.ConnectAsync(ServerBox.Text);
            if (error != null)
            {
                ErrorLabel.Text = error;
                ErrorLabel.Visibility = Visibility.Visible;
            }
        }
        finally
        {
            ConnectButton.IsEnabled = true;
            Busy.IsActive = false;
        }
    }
}
