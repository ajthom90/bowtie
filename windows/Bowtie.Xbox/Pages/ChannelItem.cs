using System;
using Bowtie.Core;
using Bowtie.Core.ViewModels;
using Windows.UI.Xaml;

namespace BowtieXbox.Pages
{
    /// <summary>One channel row as the list shows it: number, name, now/next, favorite star.</summary>
    public sealed class ChannelItem
    {
        public ChannelItem(ChannelRow row, DateTimeOffset at)
        {
            Channel = row.Channel;
            Number = row.Channel.GuideNumber;
            Name = row.Channel.Name;
            Star = row.IsFavorite ? "★" : "";
            var now = row.NowNext.Now;
            var next = row.NowNext.Next;
            Now = now == null ? "" : $"Now: {now.Title}";
            Next = next == null ? "" : $"Next {next.Start.ToLocalTime():t}: {next.Title}";
            NoSignalVisibility = row.Channel.HasNoSignal ? Visibility.Visible : Visibility.Collapsed;
            Progress = GuideLogic.Progress(now, at) * 100;
            ProgressVisibility = now == null ? Visibility.Collapsed : Visibility.Visible;
        }

        public Channel Channel { get; }
        public string Number { get; }
        public string Name { get; }
        public string Star { get; }
        public string Now { get; }
        public string Next { get; }
        public double Progress { get; }
        public Visibility ProgressVisibility { get; }
        public Visibility NoSignalVisibility { get; }
    }
}
