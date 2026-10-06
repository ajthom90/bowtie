using System.Globalization;

namespace Bowtie.Core;

/// <summary>A window or screen rectangle in physical pixels.</summary>
public readonly record struct PixelRect(int X, int Y, int Width, int Height)
{
    /// <summary>"x,y,width,height" for the settings file.</summary>
    public string Stored() => string.Join(",",
        new[] { X, Y, Width, Height }.Select(v => v.ToString(CultureInfo.InvariantCulture)));

    /// <summary>Reads <see cref="Stored"/>; null when missing, malformed or empty.</summary>
    public static PixelRect? Parse(string? stored)
    {
        var parts = stored?.Split(',');
        if (parts is not { Length: 4 }) return null;
        var values = new int[4];
        for (var i = 0; i < 4; i++)
        {
            if (!int.TryParse(parts[i].Trim(), NumberStyles.Integer, CultureInfo.InvariantCulture, out values[i]))
                return null;
        }
        return values[2] > 0 && values[3] > 0 ? new PixelRect(values[0], values[1], values[2], values[3]) : null;
    }
}

/// <summary>
/// Where the main window opens. Window APIs size in physical pixels, so the
/// default size (in view pixels) is multiplied by the display scale, then
/// clamped to the display's work area (screen minus taskbar).
/// </summary>
public static class WindowPlacement
{
    public const int DefaultWidth = 1280;
    public const int DefaultHeight = 800;

    /// <summary>A remembered window smaller than this (view pixels) is ignored.</summary>
    public const int MinimumRemembered = 320;

    /// <summary>
    /// The first-launch rectangle: the default size at <paramref name="scale"/>,
    /// no bigger than <paramref name="workArea"/>, centered in it.
    /// </summary>
    public static PixelRect Default(PixelRect workArea, double scale)
    {
        if (scale <= 0 || double.IsNaN(scale)) scale = 1;
        var width = Math.Min((int)Math.Round(DefaultWidth * scale), workArea.Width);
        var height = Math.Min((int)Math.Round(DefaultHeight * scale), workArea.Height);
        return new PixelRect(
            workArea.X + (workArea.Width - width) / 2,
            workArea.Y + (workArea.Height - height) / 2,
            width,
            height);
    }

    /// <summary>
    /// The remembered rectangle moved and shrunk to fit fully inside
    /// <paramref name="workArea"/>, or the <see cref="Default"/> when nothing
    /// usable is remembered.
    /// </summary>
    public static PixelRect Restore(PixelRect? saved, PixelRect workArea, double scale)
    {
        if (scale <= 0 || double.IsNaN(scale)) scale = 1;
        var min = (int)Math.Round(MinimumRemembered * scale);
        if (saved is not { } s || s.Width < min || s.Height < min) return Default(workArea, scale);

        var width = Math.Min(s.Width, workArea.Width);
        var height = Math.Min(s.Height, workArea.Height);
        var x = Shim.Clamp(s.X, workArea.X, workArea.X + workArea.Width - width);
        var y = Shim.Clamp(s.Y, workArea.Y, workArea.Y + workArea.Height - height);
        return new PixelRect(x, y, width, height);
    }
}
