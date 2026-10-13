using Bowtie.Core;
using Microsoft.UI.Windowing;
using Windows.Media.Core;

namespace BowtieApp.Services;

/// <summary>Probes this PC's decoders and display for session negotiation.</summary>
public static class DeviceCaps
{
    private static ClientCaps? s_cached;

    public static async Task<ClientCaps> CurrentAsync(Microsoft.UI.Xaml.Window window)
    {
        if (s_cached != null) return s_cached;
        var hevc = await HasDecoderAsync(CodecKind.Video, CodecSubtypes.VideoFormatHevc);
        var ac3 = await HasDecoderAsync(CodecKind.Audio, CodecSubtypes.AudioFormatDolbyAC3);
        var eac3 = await HasDecoderAsync(CodecKind.Audio, CodecSubtypes.AudioFormatDolbyDDPlus);
        var height = 1080;
        try
        {
            var area = DisplayArea.GetFromWindowId(window.AppWindow.Id, DisplayAreaFallback.Primary);
            height = area.OuterBounds.Height;
        }
        catch (Exception)
        {
            // keep 1080
        }
        s_cached = Caps.Detect(hevc, ac3, eac3, height);
        return s_cached;
    }

    private static async Task<bool> HasDecoderAsync(CodecKind kind, string subtype)
    {
        try
        {
            var found = await new CodecQuery().FindAllAsync(kind, CodecCategory.Decoder, subtype);
            return found.Count > 0;
        }
        catch (Exception)
        {
            return false;
        }
    }
}
