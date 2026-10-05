using System;
using System.ComponentModel;
using System.Threading;
using Bowtie.Core.ViewModels;
using BowtieXbox.Services;
using Windows.System;
using Windows.UI.Core;
using Windows.UI.Xaml;
using Windows.UI.Xaml.Controls;
using Windows.UI.Xaml.Input;
using Windows.UI.Xaml.Media.Imaging;
using Windows.UI.Xaml.Navigation;

namespace BowtieXbox.Pages
{
    /// <summary>
    /// Sign-in, two ways side by side: the quick sign-in code + QR (approved
    /// from a phone, see <see cref="QuickSignInViewModel"/>) and a username /
    /// password form. Either one moves the app to the channel list.
    /// </summary>
    public sealed partial class SignInPage : Page
    {
        private QuickSignInViewModel? _quick;
        private CancellationTokenSource? _quickCts;
        private Uri? _shownQr;

        public SignInPage()
        {
            InitializeComponent();
        }

        protected override void OnNavigatedTo(NavigationEventArgs e)
        {
            base.OnNavigatedTo(e);
            ServerLabel.Text = AppServices.App.ServerDisplay ?? "";
            UserBox.Focus(FocusState.Programmatic);
            StartQuickSignIn();
        }

        protected override void OnNavigatedFrom(NavigationEventArgs e)
        {
            StopQuickSignIn();
            base.OnNavigatedFrom(e);
        }

        // ── Quick sign-in ───────────────────────────────────────────────────

        private async void StartQuickSignIn()
        {
            StopQuickSignIn();
            var client = AppServices.App.Client;
            if (client == null) return;
            var cts = new CancellationTokenSource();
            _quickCts = cts;
            _quick = new QuickSignInViewModel(client, Tv.DeviceName);
            _quick.PropertyChanged += OnQuickChanged;
            RenderQuick();

            var user = await _quick.RunAsync(cts.Token);
            if (cts.IsCancellationRequested) return;
            if (user != null) AppServices.App.CompleteQuickSignIn(user);
            else RenderQuick();
        }

        private void StopQuickSignIn()
        {
            _quickCts?.Cancel();
            _quickCts = null;
            if (_quick != null) _quick.PropertyChanged -= OnQuickChanged;
            _quick = null;
        }

        private void OnQuickChanged(object? sender, PropertyChangedEventArgs e)
        {
            if (Dispatcher.HasThreadAccess) RenderQuick();
            else _ = Dispatcher.RunAsync(CoreDispatcherPriority.Normal, RenderQuick);
        }

        private void RenderQuick()
        {
            var quick = _quick;
            if (quick == null) return;
            var waiting = quick.Status == QuickSignInStatus.Waiting;
            var failed = quick.Status == QuickSignInStatus.Failed;

            CodeLabel.Text = waiting ? quick.UserCode ?? "" : "";
            VerifyLabel.Text = waiting && quick.VerifyUrl != null ? StripCode(quick.VerifyUrl) : "";
            QrSpinner.IsActive = !waiting && !failed;
            if (waiting && quick.QrUri != null && quick.QrUri != _shownQr)
            {
                _shownQr = quick.QrUri;
                QrImage.Source = new BitmapImage(quick.QrUri);
            }
            else if (!waiting)
            {
                _shownQr = null;
                QrImage.Source = null;
            }
            QuickError.Text = failed ? quick.Error ?? "" : "";
            QuickError.Visibility = failed ? Visibility.Visible : Visibility.Collapsed;
            QuickRetryButton.Visibility = failed ? Visibility.Visible : Visibility.Collapsed;
        }

        /// <summary>"http://host:8400/link?code=X" → "http://host:8400/link" (the code is shown separately).</summary>
        private static string StripCode(string verifyUrl)
        {
            var q = verifyUrl.IndexOf('?');
            return q < 0 ? verifyUrl : verifyUrl.Substring(0, q);
        }

        private void OnQuickRetry(object sender, RoutedEventArgs e) => StartQuickSignIn();

        // ── Username and password ───────────────────────────────────────────

        private void OnPasswordKeyDown(object sender, KeyRoutedEventArgs e)
        {
            if (e.Key == VirtualKey.Enter)
            {
                e.Handled = true;
                OnSignIn(sender, e);
            }
        }

        private async void OnSignIn(object sender, RoutedEventArgs e)
        {
            if (!SignInButton.IsEnabled) return;
            SignInButton.IsEnabled = false;
            Spinner.IsActive = true;
            ErrorLabel.Visibility = Visibility.Collapsed;
            try
            {
                var error = await AppServices.App.SignInAsync(UserBox.Text, PasswordBox.Password);
                // Success changes the phase; App navigates to the channel list.
                if (error != null)
                {
                    ErrorLabel.Text = error;
                    ErrorLabel.Visibility = Visibility.Visible;
                }
            }
            finally
            {
                SignInButton.IsEnabled = true;
                Spinner.IsActive = false;
            }
        }

        private void OnChangeServer(object sender, RoutedEventArgs e) => AppServices.App.ChangeServer();
    }
}
