using System;
using System.ComponentModel;
using System.IO;
using Bowtie.Core.ViewModels;
using BowtieXbox.Pages;
using BowtieXbox.Services;
using Windows.ApplicationModel;
using Windows.ApplicationModel.Activation;
using Windows.Storage;
using Windows.System;
using Windows.UI.Core;
using Windows.UI.ViewManagement;
using Windows.UI.Xaml;
using Windows.UI.Xaml.Controls;

namespace BowtieXbox
{
    /// <summary>
    /// Entry point. Routes the root frame by the sign-in phase
    /// (Connect → SignIn → Channels), wires Back (controller B, Escape) and
    /// stops a live session when the app is suspended.
    /// </summary>
    sealed partial class App : Application
    {
        private Frame? _frame;

        public App()
        {
            InitializeComponent();
            // Controller-first: no mouse-cursor mode on Xbox; XY focus drives everything.
            RequiresPointerMode = ApplicationRequiresPointerMode.WhenRequested;
            if (Tv.IsXbox) FocusVisualKind = FocusVisualKind.Reveal;
            Suspending += OnSuspending;
            Resuming += OnResuming;
            UnhandledException += (_, e) => LogCrash("XAML", e.Exception);
            AppServices.Initialize();
        }

        protected override void OnLaunched(LaunchActivatedEventArgs e)
        {
            if (_frame == null)
            {
                Resources["SafeMargin"] = Tv.SafeMargin;
                if (Tv.IsXbox)
                {
                    // Draw to the screen edges (video goes full bleed); pages keep the safe margin.
                    ApplicationView.GetForCurrentView().SetDesiredBoundsMode(ApplicationViewBoundsMode.UseCoreWindow);
                }
                else
                {
                    ApplicationView.PreferredLaunchWindowingMode = ApplicationViewWindowingMode.FullScreen;
                }

                _frame = new Frame { Background = (Windows.UI.Xaml.Media.Brush)Resources["BowtieBackground"] };
                Window.Current.Content = _frame;

                SystemNavigationManager.GetForCurrentView().BackRequested += OnBackRequested;
                Window.Current.CoreWindow.KeyDown += OnKeyDown;
                AppServices.App.PropertyChanged += OnAppChanged;
                AppServices.App.SessionEnded += OnSessionEnded;
                Route();
            }
            Window.Current.Activate();
        }

        // ── Routing by phase ────────────────────────────────────────────────

        private void OnAppChanged(object? sender, PropertyChangedEventArgs e)
        {
            if (e.PropertyName == nameof(AppViewModel.Phase)) RunOnUi(Route);
        }

        private void OnSessionEnded(object? sender, EventArgs e) =>
            RunOnUi(() => AppServices.App.OnSessionEnded());

        private void Route()
        {
            if (_frame == null) return;
            var target = AppServices.App.Phase switch
            {
                AppPhase.Connect => typeof(ConnectPage),
                AppPhase.Login => typeof(SignInPage),
                AppPhase.Checking => typeof(StartupPage),
                _ => typeof(ChannelsPage),
            };
            if (target != typeof(ChannelsPage)) AppServices.Reset();
            if (_frame.Content?.GetType() == target) return;
            _frame.Navigate(target);
            // A phase change starts a new history: Back never returns to sign-in.
            _frame.BackStack.Clear();
        }

        private void RunOnUi(Action action)
        {
            var dispatcher = Window.Current?.Dispatcher;
            if (dispatcher == null || dispatcher.HasThreadAccess) action();
            else _ = dispatcher.RunAsync(CoreDispatcherPriority.Normal, () => action());
        }

        // ── Back: controller B / Back, Escape ───────────────────────────────

        private void OnBackRequested(object sender, BackRequestedEventArgs e)
        {
            // Unhandled at the root, Xbox goes back to Home (the expected behavior).
            if (GoBack()) e.Handled = true;
        }

        private void OnKeyDown(CoreWindow sender, KeyEventArgs e)
        {
            if (e.VirtualKey == VirtualKey.Escape && GoBack()) e.Handled = true;
        }

        private bool GoBack()
        {
            if (_frame == null || !_frame.CanGoBack) return false;
            _frame.GoBack();
            return true;
        }

        // ── Suspend / resume ────────────────────────────────────────────────

        /// <summary>Stop the live session so the tuner frees now, not after the server's idle timeout.</summary>
        private async void OnSuspending(object sender, SuspendingEventArgs e)
        {
            var deferral = e.SuspendingOperation.GetDeferral();
            try
            {
                if (_frame?.Content is PlayerPage player) await player.SuspendAsync();
            }
            catch (Exception ex)
            {
                LogCrash("suspend", ex);
            }
            finally
            {
                deferral.Complete();
            }
        }

        /// <summary>The player stopped its session on suspend: return to the channel list.</summary>
        private void OnResuming(object sender, object e) => RunOnUi(() =>
        {
            if (_frame?.Content is PlayerPage) GoBack();
        });

        /// <summary>Appends an exception to LocalState\crash.log; never throws.</summary>
        internal static void LogCrash(string where, Exception? ex)
        {
            try
            {
                var path = Path.Combine(ApplicationData.Current.LocalFolder.Path, "crash.log");
                File.AppendAllText(path, $"{DateTimeOffset.Now:O} [{where}] {ex}{Environment.NewLine}{Environment.NewLine}");
            }
            catch
            {
                // Nothing more we can do.
            }
        }
    }
}
