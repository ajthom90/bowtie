using Bowtie.Core;
using Microsoft.UI.Xaml;

namespace BowtieApp.Pages;

/// <summary>Display data for one "Continue watching" card.</summary>
public sealed class ContinueItem
{
    public ContinueItem(Recording r)
    {
        Recording = r;
        Title = r.Title;
        Subtitle = r.Subtitle;
        SubtitleVisibility = r.Subtitle.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
        TimeLeft = ContinueWatching.RemainingText(r);
        Progress = ContinueWatching.Progress(r) * 100;
        ResumeLabel = ContinueWatching.ResumeLabel(r);
        RemoveLabel = ContinueWatching.RemoveLabel(r);
    }

    public Recording Recording { get; }
    public string Title { get; }
    public string Subtitle { get; }
    public Visibility SubtitleVisibility { get; }
    /// <summary>"23 min left".</summary>
    public string TimeLeft { get; }
    /// <summary>0–100 watched.</summary>
    public double Progress { get; }
    public string ResumeLabel { get; }
    public string RemoveLabel { get; }
}
