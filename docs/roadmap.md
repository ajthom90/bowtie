# Roadmap

What people who cut the cord with an antenna ask for most, what Bowtie does
today, and what is still open. Sources: the feature sets of Channels DVR,
Plex Live TV, Jellyfin/Emby Live TV, Tablo and HDHomeRun's own app, and the
requests that come up most in their forums and subreddits.

## What people want, and where Bowtie stands

| Want | Bowtie |
|------|--------|
| Watch live TV on every screen in the house | Web, iPhone/iPad, Apple TV, Mac, Android, Android TV/Fire TV, Roku; Windows in progress |
| Watch away from home | Yes — remote-access guide (Caddy, Cloudflare Tunnel, Tailscale) |
| A real guide without paying | Free HDHomeRun guide (2–3 days), XMLTV, or Schedules Direct (14 days) |
| Pause and rewind live TV | Yes, up to 60 minutes |
| Record shows and whole series | Yes — one-off, series ("new episodes only"), keep latest N, padding |
| Skip commercials | Yes — Comskip finds the breaks; Skip ad / auto-skip in every app |
| Easy sign-in on a TV | Yes — scan a QR code with your phone |
| Several games at once | Multiview on the web (up to 4) |
| Use it in other apps (Kodi, TiviMate, Xbox via Kodi) | Yes — personal M3U + XMLTV feed |
| Kids' accounts | Yes — parental ratings, blocked channels, per-account stream limits |
| Watch together remotely | SharePlay on Apple devices |
| Fall asleep with the TV on | Sleep timer in every TV and mobile app |
| Don't lose my setup | Admin → Settings → Download backup |
| Know when the disk is filling up | Admin → Recordings storage gauge |

## Next, in order

1. **Commercial detection tuning.** Shipped in 0.14.0 with "Find ads
   again"; next is a way to correct a break by hand.
2. **Chromecast and AirPlay from the web app.** Send a channel to a TV from a
   phone browser. Chromecast needs the Cast SDK and a device to test on.
3. **Multiview on TVs.** Apple TV and Android TV, 2–4 tiles; limited by tuner
   count and by the boxes' decoders.
4. **Notifications.** "Your recording failed", "disk almost full", "a show you
   like starts in 5 minutes" — push on Apple/Android, email optional.
5. **Recording quality options.** Keep the original broadcast (MPEG-2, large)
   or re-encode to H.264/HEVC to save space.
6. **More free guide data.** Over-the-air PSIP/EIT from the tuner itself
   (about 12 hours, no account) as a last-resort fallback.

## Platforms people ask about

- **Xbox:** no native app; use Kodi for Xbox with the M3U/XMLTV feed
  (Account → IPTV feed).
- **PlayStation:** no way to install third-party apps; Plex-style casting is
  not available either. Not planned.
- **Samsung (Tizen) / LG (webOS) TVs:** would need their own web-based apps
  and developer accounts; the web app works in their browsers meanwhile.
- **Windows:** native app built (WinUI 3); waiting on a test on a real PC.
