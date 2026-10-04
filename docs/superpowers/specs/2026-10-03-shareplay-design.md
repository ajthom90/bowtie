# SharePlay (iPhone, iPad, Apple TV) — Design

Date: 2026-10-03. Requested: "Make sure the iOS/iPadOS support SharePlay.
Support it on tvOS too if possible." Decisions taken under the user's standing
instruction to go with recommendations.

## Goal

On a FaceTime call, one person shares a Bowtie channel; everyone else on the
call who has Bowtie and an account on the **same Bowtie server** watches the
same channel in sync. Pause, rewind (live rewind) and "Live" act for the whole
group, as with any SharePlay video.

## Constraints that shape the design

- **Live sync needs exact dates.** AVPlayerPlaybackCoordinator syncs live HLS
  by `EXT-X-PROGRAM-DATE-TIME`, which must come from one clock and be exact to
  a frame for every participant (Apple, WWDC22 110380). Two FFmpeg processes on
  the same channel (different qualities with Adaptive quality off) can't
  promise that.
  → **Everyone in a group joins the sharer's server session** (one FFmpeg,
  one set of playlists), and that session writes program dates.
- **Accounts.** Participants sign in to the same server with their own
  accounts; there is no guest access (it would bypass the admin's account
  list and limits). A participant who isn't signed in to that server sees
  "Sign in to <server name> to watch together."
- **Limits apply** to each participant (streams; joining costs no tuner).

## Design

### Server
1. **Program dates.** Add `program_date_time` to `-hls_flags` (all
   sessions). Dates come from the server clock at segment write; all
   participants read the same playlist, so they agree.
2. **Join a session.** `POST /api/v1/sessions` accepts optional
   `"joinSessionId"`. If that session exists, isn't terminated, is on
   `channelId`, and its video codec is in the client's caps, the viewer joins
   it (MaxHeight = min(session top rung, the joiner's own negotiated
   ceiling)). Otherwise the start proceeds normally (no error — the joiner
   still watches, just unsynced). Per-account limits apply as for any start.
3. **Server identity.** `GET /api/v1/version` gains `serverId` (random UUID
   created on first boot, stored in settings) and `serverName` (setting,
   default the host name), so a participant's app can tell whether it is
   signed in to the sharer's server whatever URL each uses (LAN, Tailscale,
   public).
4. `POST /api/v1/sessions` response `session` gains `id` (the server session
   ID) for the sharer to put in the activity.

### Apple apps (shared code in App/Shared, BowtieKit)
- `WatchChannelActivity: GroupActivity` (Codable): `serverId`, `serverName`,
  `channelId`, `channelName`, `sessionId`. Metadata: title = channel name,
  type `.watchTogether`, fallback URL none.
- **Sharing:** the player's "Share" menu (iOS) / SharePlay button: during a
  FaceTime call AVKit's system SharePlay UI appears automatically once the app
  calls `activity.prepareForActivation()`; outside a call, iOS shows
  `GroupActivitySharingController`. tvOS: activation from the player when a
  call is active (tvOS joins via the iPhone's FaceTime handoff).
- **Joining:** `for await session in WatchChannelActivity.sessions()` in the
  app model. On a new session: check `serverId` against the current server
  (and saved servers — switch if it's a saved server with a valid login);
  start the channel with `joinSessionId`; `player.playbackCoordinator
  .coordinateWithSession(session)`; `session.join()`.
- **Changing channel** while in a group: the sharer's zap updates the
  activity (`session.activity = …`); participants follow. A participant's own
  zap leaves the group (with a confirmation).
- **Ending:** leaving the player calls `session.leave()`.
- **Entitlement:** `com.apple.developer.group-session` for iOS and tvOS
  targets; Info.plist `NSSupportsGroupActivities = YES`.

### What the user must do
- Enable **Group Activities** for app ID `app.bowtie` in the Apple Developer
  portal (Xcode Cloud's managed signing should add it from the entitlement;
  if the build fails on provisioning, enable it by hand).
- Real test: two Apple devices on a FaceTime call (SharePlay can't be tested
  in the Simulator).

## Testing
- Server: `-hls_flags` contains `program_date_time` (golden); join a session
  by id (same channel/codec → same session; mismatched channel or codec → a
  normal start; limits still apply); `/version` serverId stable across
  restarts.
- BowtieKit: activity Codable round-trip; "can join" decision (serverId match,
  saved-server match, no login → message).
- PlayerModel: start with `joinSessionId` passes it through; zap as sharer
  updates the activity; zap as participant asks to leave.
- Device test by the user (FaceTime between iPhone and iPad / Apple TV).

## Out of scope
Android/web/Roku "watch together" (no SharePlay); guest access; chat.
