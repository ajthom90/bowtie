using System;
using System.Collections.Generic;
using Bowtie.Core;
using Bowtie.Core.ViewModels;
using Windows.UI.Xaml;

namespace BowtieXbox.Pages
{
    /// <summary>One guide copy: section key, number, name, now/next, favorite star.</summary>
    public sealed class ChannelItem
    {
        public ChannelItem(GuideRow entry, DateTimeOffset at)
        {
            Entry = entry;
            var row = entry.Row;
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

            var spoken = new List<string> { string.IsNullOrEmpty(Number) ? Name : $"{Number} {Name}" };
            if (row.IsFavorite) spoken.Add("favorite");
            if (row.Channel.HasNoSignal) spoken.Add("no signal");
            if (now != null) spoken.Add($"now {now.Title}");
            if (next != null) spoken.Add($"next {next.Start.ToLocalTime():t} {next.Title}");
            _spoken = string.Join(", ", spoken);
        }

        private readonly string _spoken;

        public GuideRow Entry { get; }
        public string Key => Entry.Key;
        public Channel Channel { get; }
        public string Number { get; }
        public string Name { get; }
        public string Star { get; }
        public string Now { get; }
        public string Next { get; }
        public double Progress { get; }
        public Visibility ProgressVisibility { get; }
        public Visibility NoSignalVisibility { get; }

        /// <summary>
        /// What Narrator reads for the row (a list item's UI Automation name is
        /// its item's ToString): "9.1 FOX 9, favorite, now …, next 2:00 PM …".
        /// </summary>
        public override string ToString() => _spoken;
    }

    /// <summary>A list section ("Favorites" or "All channels"); its header is a group header.</summary>
    public sealed class ChannelGroup : List<ChannelItem>
    {
        public ChannelGroup(string header) => Header = header;

        public string Header { get; }

        /// <summary>The group header's UI Automation name.</summary>
        public override string ToString() => Header;
    }
}
