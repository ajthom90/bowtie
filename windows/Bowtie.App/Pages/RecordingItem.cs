using Bowtie.Core;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Media;

namespace BowtieApp.Pages;

/// <summary>Display data for one recording row.</summary>
public sealed class RecordingItem
{
    public RecordingItem(Recording r, DateTimeOffset now)
    {
        Recording = r;
        Title = r.Title;
        Subtitle = r.Subtitle;
        SubtitleVisibility = Show(r.Subtitle.Length > 0);
        var when = RecordingLogic.FormatWhen(r.Start, r.Stop, now, TimeZoneInfo.Local);
        WhenLine = r.ChannelName.Length > 0 ? $"{when} · {r.ChannelName}" : when;

        var badges = RecordingLogic.Badges(r);
        Badges = string.Join(" · ", badges.Select(b => b.Text));
        BadgesVisibility = Show(badges.Count > 0);
        var tone = badges.Count > 0 ? badges[0].Tone : BadgeTone.Neutral;
        BadgeBrush = tone switch
        {
            BadgeTone.Live => ChannelItem.Res("BowtieLiveBrush"),
            BadgeTone.Alert => ChannelItem.Res("SystemFillColorCriticalBrush"),
            BadgeTone.Warn => ChannelItem.Res("SystemFillColorCautionBrush"),
            BadgeTone.Good => ChannelItem.Res("SystemFillColorSuccessBrush"),
            _ => ChannelItem.Res("TextFillColorSecondaryBrush"),
        };

        Status = RecordingLogic.StatusLine(r) ?? "";
        StatusVisibility = Show(Status.Length > 0);
        Detail = RecordingLogic.DetailLine(r) ?? "";
        DetailVisibility = Show(Detail.Length > 0);

        var actions = RecordingLogic.Actions(r);
        PlayVisibility = Show(actions.Contains(RecordingAction.Play));
        StopVisibility = Show(actions.Contains(RecordingAction.Stop));
        KeepVisibility = Show(actions.Contains(RecordingAction.Keep));
        DeleteVisibility = Show(actions.Contains(RecordingAction.Delete));
        KeepLabel = RecordingLogic.ActionLabel(r, RecordingAction.Keep);
        DeleteLabel = RecordingLogic.ActionLabel(r, RecordingAction.Delete);
    }

    public Recording Recording { get; }
    public string Title { get; }
    public string Subtitle { get; }
    public Visibility SubtitleVisibility { get; }
    public string WhenLine { get; }
    public string Badges { get; }
    public Visibility BadgesVisibility { get; }
    public Brush BadgeBrush { get; }
    public string Status { get; }
    public Visibility StatusVisibility { get; }
    public string Detail { get; }
    public Visibility DetailVisibility { get; }
    public Visibility PlayVisibility { get; }
    public Visibility StopVisibility { get; }
    public Visibility KeepVisibility { get; }
    public Visibility DeleteVisibility { get; }
    public string KeepLabel { get; }
    public string DeleteLabel { get; }

    private static Visibility Show(bool on) => on ? Visibility.Visible : Visibility.Collapsed;
}
