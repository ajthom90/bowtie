using BowtieApp.Services;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media.Animation;
using Microsoft.UI.Xaml.Navigation;

namespace BowtieApp.Pages;

/// <summary>
/// Signed-in shell: Live TV and Recordings, plus the account menu. Cached
/// (NavigationCacheMode=Required) so coming back from the player keeps the
/// list where it was; a new sign-in rebuilds it.
/// </summary>
public sealed partial class ShellPage : Page
{
    private object? _signedInUser;

    public ShellPage()
    {
        InitializeComponent();
    }

    protected override void OnNavigatedTo(NavigationEventArgs e)
    {
        base.OnNavigatedTo(e);
        var user = AppServices.App.User;
        if (!ReferenceEquals(user, _signedInUser))
        {
            _signedInUser = user;
            AccountItem.Content = user?.Username ?? "Account";
            ServerMenuItem.Text = AppServices.App.ServerDisplay ?? "";
            ContentFrame.Navigate(typeof(ChannelsPage), null, new SuppressNavigationTransitionInfo());
            ContentFrame.BackStack.Clear();
            Nav.SelectedItem = ChannelsItem;
            return;
        }

        // Back from the player.
        if (ContentFrame.Content is ChannelsPage channels) channels.OnReturnedFromPlayer();
        if (ContentFrame.Content is RecordingsPage recordings) recordings.OnReturnedFromPlayer();
    }

    private void OnNavSelectionChanged(NavigationView sender, NavigationViewSelectionChangedEventArgs args)
    {
        var tag = (args.SelectedItem as NavigationViewItem)?.Tag as string;
        var page = tag == "recordings" ? typeof(RecordingsPage) : typeof(ChannelsPage);
        if (ContentFrame.CurrentSourcePageType != page)
        {
            ContentFrame.Navigate(page, null, new EntranceNavigationTransitionInfo());
        }
    }

    private void OnAccountTapped(object sender, TappedRoutedEventArgs e) =>
        FlyoutBase.ShowAttachedFlyout((FrameworkElement)sender);

    private async void OnSignOutClick(object sender, RoutedEventArgs e) => await AppServices.App.SignOutAsync();

    private void OnChangeServerClick(object sender, RoutedEventArgs e) => AppServices.App.ChangeServer();
}
