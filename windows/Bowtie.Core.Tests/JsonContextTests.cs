namespace Bowtie.Core.Tests;

/// <summary>
/// The source-generated <see cref="BowtieJsonContext"/> for the busy-tuner and
/// signal wire types. The UWP Xbox app (.NET Native, .NET Standard 2.0) has no
/// reflection fallback, so a type missing from the context fails there.
/// </summary>
public class JsonContextTests
{
    [Theory]
    [InlineData(typeof(Channel))]
    [InlineData(typeof(List<Channel>))]
    [InlineData(typeof(ReceptionSignal))]
    [InlineData(typeof(HeartbeatBody))]
    public void Wire_types_have_generated_metadata(Type type) =>
        Assert.NotNull(BowtieJsonContext.Default.GetTypeInfo(type));

    [Fact]
    public void Reception_signal_round_trips()
    {
        var signal = new ReceptionSignal { Strength = 96, Quality = 46, SymbolQuality = 0, Weak = true };

        var json = BowtieJson.Serialize(signal);

        Assert.Equal("""{"strength":96,"quality":46,"symbolQuality":0,"weak":true}""", json);
        Assert.Equal(signal, BowtieJson.Deserialize<ReceptionSignal>(json));
    }

    [Fact]
    public void Heartbeat_body_reads_the_signal_or_unknown()
    {
        var body = BowtieJson.Deserialize<HeartbeatBody>(
            """{"signal":{"strength":96,"quality":46,"symbolQuality":0,"weak":true},"extra":1}""");

        Assert.Equal(new ReceptionSignal { Strength = 96, Quality = 46, SymbolQuality = 0, Weak = true }, body.Signal);
        Assert.Null(BowtieJson.Deserialize<HeartbeatBody>("""{"signal":null}""").Signal);
        Assert.Null(BowtieJson.Deserialize<HeartbeatBody>("{}").Signal);
    }

    [Fact]
    public void Channel_watchable_round_trips_and_defaults_to_watchable()
    {
        var busy = BowtieJson.Deserialize<Channel>(
            """{"id":1,"guideNumber":"2.1","name":"A","logoUrl":"","watchable":false}""");
        var older = BowtieJson.Deserialize<Channel>("""{"id":2,"guideNumber":"4.1","name":"B","logoUrl":""}""");

        Assert.False(busy.IsWatchable);
        Assert.Equal(busy, BowtieJson.Deserialize<Channel>(BowtieJson.Serialize(busy)));
        Assert.Null(older.Watchable);
        Assert.True(older.IsWatchable);
    }
}
