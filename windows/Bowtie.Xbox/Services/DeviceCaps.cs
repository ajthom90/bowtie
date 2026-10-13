using System;
using System.Threading.Tasks;
using Bowtie.Core;
using Windows.Graphics.Display;
using Windows.Media.Core;

namespace BowtieXbox.Services
{
    /// <summary>Probes this device's decoders and display for session negotiation.</summary>
    public static class DeviceCaps
    {
        private static ClientCaps? s_cached;

        /// <summary>Call on the UI thread (reads the current view's display).</summary>
        public static async Task<ClientCaps> CurrentAsync()
        {
            if (s_cached != null) return s_cached;
            var height = 1080;
            try
            {
                height = (int)DisplayInformation.GetForCurrentView().ScreenHeightInRawPixels;
            }
            catch (Exception)
            {
                // keep 1080
            }
            var hevc = await HasDecoderAsync(CodecKind.Video, CodecSubtypes.VideoFormatHevc);
            var ac3 = await HasDecoderAsync(CodecKind.Audio, CodecSubtypes.AudioFormatDolbyAC3);
            var eac3 = await HasDecoderAsync(CodecKind.Audio, CodecSubtypes.AudioFormatDolbyDDPlus);
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
}
