using BowtieXbox.Services;
using Windows.System;
using Windows.UI.Xaml;
using Windows.UI.Xaml.Controls;
using Windows.UI.Xaml.Input;
using Windows.UI.Xaml.Navigation;

namespace BowtieXbox.Pages
{
    /// <summary>
    /// Asks for the server address (the on-screen keyboard opens when the box
    /// is activated with A), health-checks it and moves on to sign-in.
    /// </summary>
    public sealed partial class ConnectPage : Page
    {
        public ConnectPage()
        {
            InitializeComponent();
        }

        protected override void OnNavigatedTo(NavigationEventArgs e)
        {
            base.OnNavigatedTo(e);
            ServerBox.Focus(FocusState.Programmatic);
        }

        private void OnServerKeyDown(object sender, KeyRoutedEventArgs e)
        {
            if (e.Key == VirtualKey.Enter)
            {
                e.Handled = true;
                OnConnect(sender, e);
            }
        }

        private async void OnConnect(object sender, RoutedEventArgs e)
        {
            if (!ConnectButton.IsEnabled) return;
            ConnectButton.IsEnabled = false;
            Spinner.IsActive = true;
            ErrorLabel.Visibility = Visibility.Collapsed;
            try
            {
                var error = await AppServices.App.ConnectAsync(ServerBox.Text);
                // Success changes the phase; App navigates to sign-in.
                if (error != null)
                {
                    ErrorLabel.Text = error;
                    ErrorLabel.Visibility = Visibility.Visible;
                }
            }
            finally
            {
                ConnectButton.IsEnabled = true;
                Spinner.IsActive = false;
            }
        }
    }
}
