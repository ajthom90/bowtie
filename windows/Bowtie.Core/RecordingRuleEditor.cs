using System.Globalization;

namespace Bowtie.Core;

/// <summary>Editable settings and validation shared by the series dialog and tests.</summary>
public sealed class RecordingRuleEditor
{
    public const string KeepHint = "The keep limit applies when new episodes finish recording. Saving deletes no recorded episodes.";
    public const string InvalidCount = "Enter a whole number of episodes greater than zero.";

    public RecordingRuleEditor(RecordingRule rule)
    {
        NewOnly = rule.NewOnly;
        ChannelId = rule.ChannelId;
        KeepAll = rule.KeepLatest == 0;
        Count = (rule.KeepLatest == 0 ? 5 : rule.KeepLatest).ToString(CultureInfo.InvariantCulture);
    }

    public bool NewOnly { get; set; }
    public long ChannelId { get; set; }
    public bool KeepAll { get; set; }
    public string Count { get; set; }

    public RecordingRulePatch? Patch()
    {
        var count = 0;
        if (!KeepAll && (!int.TryParse(Count, NumberStyles.Integer, CultureInfo.InvariantCulture, out count) || count < 1))
            return null;
        return new RecordingRulePatch(NewOnly, ChannelId, count);
    }

    public sealed record ChannelOption(long Id, string Name);

    public static IReadOnlyList<ChannelOption> Channels(RecordingRule rule, IReadOnlyList<Recording> upcoming)
    {
        var channels = new Dictionary<long, string> { [0] = "Any channel" };
        if (rule.ChannelId != 0) channels[rule.ChannelId] = rule.ChannelName;
        foreach (var r in upcoming.Where(r => r.RuleId == rule.Id && r.ChannelId != 0))
            channels[r.ChannelId] = r.ChannelName;
        return channels.Select(c => new ChannelOption(c.Key, c.Value)).ToList();
    }

    public static string Summary(RecordingRule rule) => string.Join(" · ",
        new[] { rule.ChannelId == 0 ? "Any channel" : rule.ChannelName.Length == 0 ? "This channel" : rule.ChannelName,
            rule.NewOnly ? "New episodes" : "All episodes",
            rule.KeepLatest == 0 ? "Keep all" : $"Keep latest {rule.KeepLatest}" });
}
