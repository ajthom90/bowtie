# Microsoft Store listing: Bowtie for Windows

Text to paste into Partner Center for the Windows app. See
`windows/README.md` ("Microsoft Store") for the submission steps.

## Product name

Bowtie Live TV (or "Bowtie" if available when you reserve the name)

## Short description (up to 1,000 characters is allowed; keep it short)

Watch live TV from your own HDHomeRun antenna tuner on your Windows PC,
through your self-hosted Bowtie server.

## Description

Bowtie brings over-the-air TV from your HDHomeRun tuner to your Windows PC.
It connects to a Bowtie server that you run at home (free and open source:
https://github.com/ajthom90/bowtie). Bowtie doesn't include any TV channels
or a subscription: you need an HDHomeRun tuner, an antenna, and a Bowtie
server.

- Live TV with a channel guide, favorites and a Recent row
- Pause and rewind live TV, and jump back to live
- Pick video quality, audio track and captions
- Watch, keep and delete your DVR recordings, with Skip ad over detected
  commercial breaks and resume where you left off
- Edit series recordings: new or all episodes, channel, how many to keep
- Guide filters for sports, movies, news, kids and new episodes
- Sleep timer, full screen, and keyboard shortcuts
- Works on Intel/AMD and ARM PCs

## What's new in this version

(Use the version's section from CHANGELOG.md, shortened.)

## Search terms (up to 7)

HDHomeRun, live TV, antenna, OTA, DVR, TV tuner, self-hosted

## Category

Entertainment

## Privacy policy URL

https://github.com/ajthom90/bowtie/blob/main/docs/privacy.md

## Website and support

- Website: https://github.com/ajthom90/bowtie
- Support contact: https://github.com/ajthom90/bowtie/issues

## Restricted capability justification (runFullTrust)

Bowtie is a WinUI 3 desktop app built with the Windows App SDK. Packaged
WinUI 3 desktop apps run as full-trust processes, which requires the
runFullTrust capability. The app doesn't use it to do anything beyond a
normal desktop app: it connects to the user's own Bowtie server, plays
video, and stores its settings and sign-in tokens (in the Windows
Credential Locker).

## Age rating questionnaire (IARC)

Answer as a media player that shows content from the user's own source:

- The app lets users watch broadcast TV from their own tuner; it doesn't
  include or sell content.
- No user-to-user communication or sharing, no location sharing, no
  purchases, no ads.

## Pricing and availability

- Price: Free
- Visibility: Public, or "Private audience / hidden" if you only want people
  with the direct link to find it.
- Markets: wherever HDHomeRun and over-the-air TV are relevant (at least the
  United States and Canada), or all markets.

## Screenshots (at least one; 1366×768 or larger, PNG)

In `docs/store/screenshots/windows/` (1920×1080; server address and
username hidden):

1. `01-live-tv.png`: Live TV with Favorites and All channels
2. `02-player.png`: live TV playing with the controls showing
3. `04-recordings.png`: Recordings → Recorded
4. `05-edit-series.png`: Recordings → Shows → Edit dialog
5. `06-sleep-timer-or-menu.png`: the sleep timer menu in the player

## Store logos

The package's own logos are used. Optionally add a 300×300 Store logo from
`docs/brand/`.
