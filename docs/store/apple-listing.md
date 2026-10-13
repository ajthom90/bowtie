# App Store listing: Bowtie for iPhone, iPad, Mac and Apple TV

Text to paste into App Store Connect. The iOS, macOS and tvOS apps share the
bundle ID `app.bowtie`, so they're one app record with several platforms
(universal purchase). Xcode Cloud builds and uploads them.

## Name and subtitle

- Name: Bowtie Live TV (or "Bowtie" if available)
- Subtitle (30 characters): Live TV from your HDHomeRun

## Promotional text

Watch over-the-air TV from your own antenna and HDHomeRun tuner, anywhere,
through your self-hosted Bowtie server.

## Description

Bowtie brings over-the-air TV from your HDHomeRun tuner to your iPhone, iPad,
Mac and Apple TV. It connects to a Bowtie server that you run at home (free
and open source: https://github.com/ajthom90/bowtie). Bowtie doesn't include
any TV channels or a subscription: you need an HDHomeRun tuner, an antenna,
and a Bowtie server.

- Live TV with a channel guide, favorites and a Recent row
- Pause and rewind live TV, and jump back to live
- Pick video quality, audio track and captions
- Watch, keep and delete your DVR recordings, with Skip ad over detected
  commercial breaks and resume where you left off
- Search the guide, and filter it for sports, movies, news, kids and new
  episodes
- Sleep timer, AirPlay, Picture in Picture and SharePlay

## Keywords (100 characters)

HDHomeRun,live TV,antenna,OTA,DVR,tuner,self-hosted,broadcast,TV guide,recordings

## URLs

- Support URL: https://github.com/ajthom90/bowtie/issues
- Privacy policy URL: https://github.com/ajthom90/bowtie/blob/main/docs/privacy.md

## App Privacy

Data Not Collected. The apps have no analytics, ads or tracking, and they
send sign-in details and viewing activity only to the Bowtie server the user
enters, which the developer can't access (see docs/privacy.md).

## Category, rating and rights

- Primary category: Entertainment.
- Age rating: answer the questionnaire as for the Microsoft Store (live
  broadcast TV can show anything that airs).
- Content rights: the app shows third-party content, namely the user's own
  over-the-air broadcasts from their own tuner.

## App Review notes

Bowtie is a client for a self-hosted server that streams the user's own
over-the-air antenna channels from their HDHomeRun tuner. To review:

1. Enter the server address below and tap Validate.
2. Sign in with the review account below.
3. Pick a channel to watch live TV, or open Recordings to play a recording.

Live TV needs a free antenna tuner at the server; if every tuner is busy the
app says so and lists the channels you can join. Recordings always play.

(Add the server address, username and password in App Store Connect's sign-in
fields; never commit them here.)

## Screenshots

In `docs/store/screenshots/apple/`, taken against a real server with the
StoreScreenshotUITests (iOS) and TVStoreScreenshotUITests (tvOS) UI tests:

| Folder | Size | Store slot |
|---|---|---|
| `iphone-6.3/` | 1206 × 2622 | iPhone 6.3″ (the required iPhone size) |
| `iphone-6.9/` | 1320 × 2868 | iPhone 6.9″ (optional) |
| `ipad/` | 2064 × 2752 | iPad 13″ |
| `mac/` | 2880 × 1800 | Mac |
| `tv/` | 1920 × 1080 | Apple TV |

None has an alpha channel (App Store Connect rejects them); the Mac window
captures were flattened onto the app background.

Not yet made: iPhone Duo (1398 × 2034 outer, 2007 × 2853 inner), required
from April 2027 for builds with the iOS 27.1 SDK, and the optional iOS 27
product page header and search results art.

They avoid playing video: App Review can object to recognizable broadcast
shows in screenshots.
