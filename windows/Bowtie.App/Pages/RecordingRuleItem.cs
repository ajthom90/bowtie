using Bowtie.Core;
using Microsoft.UI.Xaml;

namespace BowtieApp.Pages;

public sealed class RecordingRuleItem
{
    public RecordingRuleItem(RecordingRule rule) => Rule = rule;

    public RecordingRule Rule { get; }
    public string Title => Rule.Title;
    public string Summary => RecordingRuleEditor.Summary(Rule);
    public Visibility EditVisibility => Rule.CanEdit ? Visibility.Visible : Visibility.Collapsed;
}
