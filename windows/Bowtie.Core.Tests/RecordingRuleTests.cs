using Bowtie.Core.ViewModels;

namespace Bowtie.Core.Tests;

public class RecordingRuleTests
{
    private const string RuleJson = """{"id":9,"title":"News","channelId":7,"channelName":"Seven","newOnly":true,"keepLatest":5,"canManage":true,"editable":true}""";
    private static RecordingRule Rule => BowtieJson.Deserialize<RecordingRule>(RuleJson);

    [Theory]
    [InlineData("{}", false)]
    [InlineData("{\"canManage\":true}", false)]
    [InlineData("{\"editable\":true}", false)]
    [InlineData("{\"editable\":false,\"canManage\":true}", false)]
    [InlineData("{\"editable\":true,\"canManage\":true}", true)]
    public void Edit_requires_server_support_and_management_permission(string json, bool canEdit) =>
        Assert.Equal(canEdit, BowtieJson.Deserialize<RecordingRule>(json).CanEdit);

    [Fact]
    public async Task Client_lists_rules_and_patches_only_supplied_fields_including_false_and_zero()
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        server.Json("GET", "/api/v1/recording-rules", $"[{RuleJson}]")
            .Json("PATCH", "/api/v1/recording-rules/9", RuleJson);
        Assert.True((await client.RecordingRulesAsync()).Single().Editable);
        Assert.Equal(Rule, await client.UpdateRecordingRuleAsync(9, new RecordingRulePatch(false, 0, 0)));
        await client.UpdateRecordingRuleAsync(9, new RecordingRulePatch(KeepLatest: 3));
        var requests = server.For("PATCH", "/api/v1/recording-rules/9").ToList();
        Assert.Equal("Bearer a1", requests[0].Authorization);
        Assert.Equal("""{"newOnly":false,"channelId":0,"keepLatest":0}""", requests[0].Body);
        Assert.Equal("""{"keepLatest":3}""", requests[1].Body);
    }

    [Theory]
    [InlineData("0")]
    [InlineData("-1")]
    [InlineData("1.5")]
    [InlineData("")]
    [InlineData("oops")]
    [InlineData("2147483648")]
    public void Keep_latest_requires_a_positive_whole_number(string count)
    {
        var editor = new RecordingRuleEditor(Rule) { Count = count };
        Assert.Null(editor.Patch());
        editor.KeepAll = true;
        Assert.Equal(0, editor.Patch()!.KeepLatest);
    }

    [Fact]
    public async Task Editor_loads_rule_channels_saves_and_refreshes_rules_and_upcoming()
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        server.Json("GET", "/api/v1/recording-rules", $"[{RuleJson}]")
            .Json("GET", "/api/v1/recordings", """
                [{"id":1,"ruleId":9,"channelId":8,"channelName":"Eight"},
                 {"id":2,"ruleId":9,"channelId":8,"channelName":"Eight"},
                 {"id":3,"ruleId":10,"channelId":10,"channelName":"Ten"}]
                """);
        using var vm = new RecordingsViewModel(client);
        await vm.SelectShowsAsync();
        Assert.True(vm.ShowingShows);
        Assert.Equal(Rule, Assert.Single(vm.Rules));
        var options = await vm.RuleChannelsAsync(Rule);
        Assert.Equal(new long[] { 0, 7, 8 }, options.Select(c => c.Id));
        Assert.Equal("Any channel", options[0].Name);
        var editor = new RecordingRuleEditor(Rule);
        Assert.True(editor.NewOnly);
        Assert.Equal(7, editor.ChannelId);
        Assert.False(editor.KeepAll);
        Assert.Equal("5", editor.Count);

        var updated = Rule with { NewOnly = false, ChannelId = 0, KeepLatest = 1 };
        server.Json("PATCH", "/api/v1/recording-rules/9", BowtieJson.Serialize(updated))
            .Json("GET", "/api/v1/recording-rules", BowtieJson.Serialize(new[] { updated }))
            .Json("GET", "/api/v1/recordings", "[]");
        editor.NewOnly = false;
        editor.ChannelId = 0;
        editor.Count = "1";
        Assert.True(await vm.UpdateRuleAsync(Rule, editor));
        Assert.Equal(updated, Assert.Single(vm.Rules));
        Assert.Empty(vm.Upcoming);
        Assert.Equal("/api/v1/recordings?state=upcoming", server.Requests.Last().PathAndQuery);
        Assert.Equal(2, server.For("GET", "/api/v1/recording-rules").Count());
    }

    [Fact]
    public async Task Failed_edit_shows_recording_errors_and_unavailable_upcoming_keeps_current_channel()
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        using var vm = new RecordingsViewModel(client);
        var options = await vm.RuleChannelsAsync(Rule);
        Assert.Equal(new long[] { 0, 7 }, options.Select(c => c.Id));
        server.Json("PATCH", "/api/v1/recording-rules/9", "{}", 403);
        Assert.False(await vm.UpdateRuleAsync(Rule, new RecordingRuleEditor(Rule)));
        Assert.Equal("Only the person who scheduled it or an admin can change it.", vm.Message);
        Assert.False(await vm.UpdateRuleAsync(Rule with { Editable = null }, new RecordingRuleEditor(Rule)));
        Assert.Single(server.For("PATCH", "/api/v1/recording-rules/9"));
        Assert.False(await vm.UpdateRuleAsync(Rule, new RecordingRuleEditor(Rule) { Count = "0" }));
        Assert.Equal(RecordingRuleEditor.InvalidCount, vm.Message);
        Assert.Single(server.For("PATCH", "/api/v1/recording-rules/9"));
    }
}
