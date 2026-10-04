using Bowtie.Core;
using Bowtie.Core.ViewModels;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Media;
using Microsoft.UI.Xaml.Media.Imaging;

namespace BowtieApp.Pages;

/// <summary>Display data for one channel row (built on the UI thread).</summary>
public sealed class ChannelItem
{
    private const string StarOutline = "";
    private const string StarFilled = "";

    /// <summary>Opacity of a now/next line outside the guide filter.</summary>
    private const double Dimmed = 0.4;

    public ChannelItem(ChannelRow row, Uri? logo, bool favoritesSupported, DateTimeOffset now,
        GuideFilters.RowHighlight? highlight = null)
    {
        Channel = row.Channel;
        Number = row.Channel.GuideNumber;
        Name = row.Channel.Name;
        var current = row.NowNext.Now;
        var next = row.NowNext.Next;
        NowTitle = current?.Title ?? (row.Channel.HasNoSignal ? "No signal last time" : "");
        NextLine = next == null ? "" : $"Next: {next.Title}";
        Progress = GuideLogic.Progress(current, now) * 100;
        ProgressVisibility = current == null ? Visibility.Collapsed : Visibility.Visible;
        Logo = logo == null ? null : new BitmapImage(logo) { DecodePixelWidth = 96 };
        LogoVisibility = logo == null ? Visibility.Collapsed : Visibility.Visible;
        NumberVisibility = logo == null ? Visibility.Visible : Visibility.Collapsed;
        StarVisibility = favoritesSupported ? Visibility.Visible : Visibility.Collapsed;
        StarGlyph = row.IsFavorite ? StarFilled : StarOutline;
        StarLabel = row.IsFavorite ? $"Remove {Name} from favorites" : $"Add {Name} to favorites";
        StarBrush = row.IsFavorite ? Res("BowtieAmberBrush") : Res("TextFillColorSecondaryBrush");
        NoSignalVisibility = row.Channel.HasNoSignal ? Visibility.Visible : Visibility.Collapsed;
        var h = highlight ?? new GuideFilters.RowHighlight(true, true, null);
        NowOpacity = h.NowMatches || current == null ? 1 : Dimmed;
        NextOpacity = h.NextMatches || next == null ? 0.7 : 0.7 * Dimmed;
        LaterLine = h.Later == null ? "" : GuideFilters.LaterLine(h.Later, TimeZoneInfo.Local);
        LaterVisibility = h.Later == null ? Visibility.Collapsed : Visibility.Visible;

        var spoken = $"{Number} {Name}";
        if (!string.IsNullOrEmpty(NowTitle))
        {
            spoken += $", now {NowTitle}";
            if (current != null && !h.NowMatches) spoken += " (outside the filter)";
        }
        if (LaterLine.Length > 0) spoken += $", {LaterLine}";
        AccessibleName = spoken;
    }

    /// <summary>An app resource brush, or gray when it's missing.</summary>
    internal static Brush Res(string key) =>
        Application.Current.Resources.TryGetValue(key, out var value) && value is Brush brush
            ? brush
            : new SolidColorBrush(Microsoft.UI.Colors.Gray);

    public Channel Channel { get; }
    public string Number { get; }
    public string Name { get; }
    public string NowTitle { get; }
    public string NextLine { get; }
    public double Progress { get; }
    public Visibility ProgressVisibility { get; }
    public ImageSource? Logo { get; }
    public Visibility LogoVisibility { get; }
    public Visibility NumberVisibility { get; }
    public Visibility StarVisibility { get; }
    public string StarGlyph { get; }
    public string StarLabel { get; }
    public Brush StarBrush { get; }
    public Visibility NoSignalVisibility { get; }
    public string AccessibleName { get; }
    public double NowOpacity { get; }
    public double NextOpacity { get; }
    /// <summary>"Later: Title · 8:00 PM" when the filter matches nothing now or next.</summary>
    public string LaterLine { get; }
    public Visibility LaterVisibility { get; }
}

/// <summary>Display data for one Recent tile.</summary>
public sealed class RecentItem
{
    public RecentItem(RecentChannel recent, Uri? logo)
    {
        Recent = recent;
        Number = recent.GuideNumber;
        Name = recent.Name;
        Logo = logo == null ? null : new BitmapImage(logo) { DecodePixelWidth = 96 };
        LogoVisibility = logo == null ? Visibility.Collapsed : Visibility.Visible;
    }

    public RecentChannel Recent { get; }
    public string Number { get; }
    public string Name { get; }
    public ImageSource? Logo { get; }
    public Visibility LogoVisibility { get; }
}
