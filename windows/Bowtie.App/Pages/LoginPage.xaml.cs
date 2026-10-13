using BowtieApp.Services;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;

namespace BowtieApp.Pages;

/// <summary>Username and password for the saved server.</summary>
public sealed partial class LoginPage : Page
{
    public LoginPage()
    {
        InitializeComponent();
        ServerText.Text = $"Server: {AppServices.App.ServerDisplay}";
        Loaded += (_, _) => UsernameBox.Focus(FocusState.Programmatic);
    }

    private void OnPasswordKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key == Windows.System.VirtualKey.Enter)
        {
            e.Handled = true;
            _ = SignInAsync();
        }
    }

    private void OnSignInClick(object sender, RoutedEventArgs e) => _ = SignInAsync();

    private void OnChangeServerClick(object sender, RoutedEventArgs e) => AppServices.App.ChangeServer();

    private async Task SignInAsync()
    {
        ErrorLabel.Visibility = Visibility.Collapsed;
        SignInButton.IsEnabled = false;
        Busy.IsActive = true;
        try
        {
            var error = await AppServices.App.SignInAsync(UsernameBox.Text, PasswordInput.Password);
            if (error != null)
            {
                ErrorLabel.Text = error;
                ErrorLabel.Visibility = Visibility.Visible;
                PasswordInput.Password = "";
            }
        }
        finally
        {
            SignInButton.IsEnabled = true;
            Busy.IsActive = false;
        }
    }
}
