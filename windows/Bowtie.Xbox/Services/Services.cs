using System;
using Bowtie.Core;
using Bowtie.Core.ViewModels;

namespace BowtieXbox.Services
{
    /// <summary>
    /// Composition root shared by the pages: the app view model (which owns
    /// the API client) and the channel list for the signed-in client.
    /// </summary>
    public static class AppServices
    {
        private static ChannelListViewModel? s_channels;
        private static BowtieClient? s_forClient;

        public static AppViewModel App { get; private set; } = null!;

        public static void Initialize()
        {
            var store = new PasswordVaultTokenStore();
            App = new AppViewModel(store, server => new BowtieClient(server, store));
        }

        public static BowtieClient Client => App.Client ?? throw new InvalidOperationException("Not connected");

        /// <summary>The channel list for the current client (dropped when the server or sign-in changes).</summary>
        public static ChannelListViewModel Channels
        {
            get
            {
                if (!ReferenceEquals(s_forClient, App.Client))
                {
                    s_channels = null;
                    s_forClient = App.Client;
                }
                return s_channels ??= new ChannelListViewModel(Client);
            }
        }

        /// <summary>Forget per-sign-in view models (sign-out, server change).</summary>
        public static void Reset()
        {
            s_channels = null;
            s_forClient = null;
        }
    }
}
