using System;
using Windows.Security.ExchangeActiveSyncProvisioning;
using Windows.System.Profile;
using Windows.UI.Xaml;

namespace BowtieXbox.Services
{
    /// <summary>Which device this is, and the 10-foot layout numbers that follow.</summary>
    public static class Tv
    {
        /// <summary>Running on an Xbox console (vs a Windows PC).</summary>
        public static bool IsXbox { get; } =
            string.Equals(AnalyticsInfo.VersionInfo.DeviceFamily, "Windows.Xbox", StringComparison.OrdinalIgnoreCase);

        /// <summary>
        /// TV-safe margin for text and controls. The app draws edge to edge
        /// on Xbox (so video fills the screen); TVs may crop up to 5% of each
        /// edge, so content keeps 48 px left/right and 27 px top/bottom.
        /// </summary>
        public static Thickness SafeMargin => IsXbox ? new Thickness(48, 27, 48, 27) : new Thickness(32, 24, 32, 24);

        /// <summary>Shown to the person approving a quick sign-in ("Xbox (LIVINGROOM)").</summary>
        public static string DeviceName
        {
            get
            {
                var kind = IsXbox ? "Xbox" : "Windows PC";
                try
                {
                    var name = new EasClientDeviceInformation().FriendlyName;
                    if (!string.IsNullOrWhiteSpace(name)) return $"Bowtie on {kind} ({name.Trim()})";
                }
                catch (Exception)
                {
                    // name unavailable
                }
                return "Bowtie on " + kind;
            }
        }
    }
}
