using BowtieXbox.Services;
using Windows.UI.Xaml;
using Windows.UI.Xaml.Controls;
using Windows.UI.Xaml.Navigation;

namespace BowtieXbox.Pages
{
    /// <summary>Signs in with the saved refresh token; offers a retry when the server can't be reached.</summary>
    public sealed partial class StartupPage : Page
    {
        public StartupPage()
        {
            InitializeComponent();
        }

        protected override async void OnNavigatedTo(NavigationEventArgs e)
        {
            base.OnNavigatedTo(e);
            await StartAsync();
        }

        private async System.Threading.Tasks.Task StartAsync()
        {
            var app = AppServices.App;
            Spinner.IsActive = true;
            ErrorButtons.Visibility = Visibility.Collapsed;
            StatusText.Text = $"Signing in to {app.ServerDisplay}…";
            await app.StartAsync();
            // Success or a refused token changes the phase (App routes away).
            if (app.StartupError is string error)
            {
                Spinner.IsActive = false;
                StatusText.Text = error;
                ErrorButtons.Visibility = Visibility.Visible;
                RetryButton.Focus(FocusState.Programmatic);
            }
        }

        private async void OnRetry(object sender, RoutedEventArgs e) => await StartAsync();

        private void OnChangeServer(object sender, RoutedEventArgs e) => AppServices.App.ChangeServer();
    }
}
