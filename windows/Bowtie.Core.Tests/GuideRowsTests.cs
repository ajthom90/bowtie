using Bowtie.Core.ViewModels;

namespace Bowtie.Core.Tests;

public class GuideRowsTests
{
    [Fact]
    public async Task Guide_duplicates_favorites_in_server_order_with_unique_keys_and_unique_zapping()
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        server.Json("GET", "/api/v1/channels", """
            [{"id":1,"guideNumber":"11.1","favorite":true},
             {"id":2,"guideNumber":"2.1","favorite":false},
             {"id":3,"guideNumber":"9.1","favorite":true}]
            """).Json("GET", "/api/v1/guide", "[]");
        var vm = new ChannelListViewModel(client);
        await vm.RefreshAsync();
        var rows = vm.GuideRows(vm.Rows);
        Assert.Equal(new[] { "favorites:3", "favorites:1", "all:1", "all:2", "all:3" }, rows.Select(r => r.Key));
        Assert.Equal(rows.Count, rows.Select(r => r.Key).Distinct().Count());
        Assert.Equal(new long[] { 3, 1, 2 }, vm.Rows.Select(r => r.Id));
        Assert.Equal("all:1", GuideRow.RestoreKey("all:1", rows));
        Assert.Equal("favorites:1", GuideRow.RestoreKey("favorites:1", rows));

        server.Json("DELETE", "/api/v1/me/favorites/1", "", 204);
        await vm.SetFavoriteAsync(1, false);
        rows = vm.GuideRows(vm.Rows);
        Assert.Equal("all:1", GuideRow.RestoreKey("favorites:1", rows));
        Assert.Equal(new long[] { 3, 1, 2 }, vm.Rows.Select(r => r.Id));
        Assert.False(rows.Single(r => r.Key == "all:1").Row.IsFavorite);

        server.Json("PUT", "/api/v1/me/favorites/2", "", 204);
        await vm.SetFavoriteAsync(2, true);
        rows = vm.GuideRows(vm.Rows);
        Assert.Equal(new long[] { 2, 3, 1 }, vm.Rows.Select(r => r.Id));
        var copies = rows.Where(r => r.Row.Id == 2).ToList();
        Assert.Equal(new[] { "favorites:2", "all:2" }, copies.Select(r => r.Key));
        Assert.All(copies, r => Assert.True(r.Row.IsFavorite));

        server.Json("DELETE", "/api/v1/me/favorites/2", "{}", 500);
        await vm.SetFavoriteAsync(2, false);
        Assert.Equal(rows, vm.GuideRows(vm.Rows));
        Assert.NotNull(vm.Message);

        var filtered = vm.GuideRows(vm.Rows.Where(r => r.Id == 2).ToList());
        Assert.Equal(new[] { "favorites:2", "all:2" }, filtered.Select(r => r.Key));
        Assert.Null(GuideRow.RestoreKey("all:1", filtered));
    }

    [Fact]
    public async Task Older_servers_keep_a_single_list_in_server_order()
    {
        var server = new FakeServer();
        var (client, _) = TestClients.SignedIn(server);
        server.Json("GET", "/api/v1/channels", """[{"id":2},{"id":1}]""")
            .Json("GET", "/api/v1/guide", "[]");
        var vm = new ChannelListViewModel(client);
        await vm.RefreshAsync();
        Assert.False(vm.FavoritesSupported);
        Assert.Equal(new[] { "all:2", "all:1" }, vm.GuideRows(vm.Rows).Select(r => r.Key));
        Assert.Equal(new long[] { 2, 1 }, vm.Rows.Select(r => r.Id));
    }
}
