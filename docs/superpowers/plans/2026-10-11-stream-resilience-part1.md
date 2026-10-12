# Stream Resilience Part 1 (Server) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Live streams survive dirty broadcasts, stalls and transcoder restarts. That means no duplicate-frame storms, no audio holes, no needless FFmpeg kills and no broken timeline. Every served playlist carries a Bowtie-owned wall-clock timeline that matches across renditions: PDT, discontinuities and the discontinuity sequence. A timeline endpoint serves clients that can't read PDT.

**Architecture:**
- **Test harness first.** It adds:
  - `pts-jump` faults: PTS+DTS and PTS-only;
  - a multi-rendition testplayer with a pure consistency checker and ffprobe audio checks.
    - The checker pairs renditions by PDT overlap and requires paired segments to carry the same discontinuity number.
    - The player measures A/V skew on the same PDT pairing and counts every fetch failure.
    - It opens a coverage window at the end of each fault cluster (faults ending less than 8 s apart form one cluster; only an outage-type fault hides a fault nested inside it). Each window must have A/V-paired segments in every rendition and in every discontinuity, one-segment discontinuities included, plus progress to the end of observation. Waiting comparisons are drained when observation ends, and a cue never matched to a video segment is an error;
  - a "production" e2e layout: a software ladder, every audio track, the AC-3 copy and captions;
  - new fault scenarios marked `xfail`. Each is pinned to the check that must fail until the task that fixes it.
- **FFmpeg changes.** Input and audio hardening come first. Then the timestamp option is chosen by a gate on FFmpeg 5.1 (the production image's FFmpeg).
  - Each candidate is a scratch branch, and the argv logged from inside FFmpeg's container proves which one ran.
  - The gate runs the ptsjump variants, the splice scenario, a production-layout fault run, captions on a real clip with injected faults, and real over-the-air clips judged against their signal logs.
  - It counts FFmpeg's own "timestamp discontinuity" re-bases (with a positive control) and its duplicate bursts.
- **Ingest changes.** A stuck transcoder is drained first. It is killed only if no bytes reach FFmpeg for a further grace period. New subscribers join at a complete video key (sequence header, or SPS/PPS plus IDR).
- **Session timeline.** A per-session `stream.Timeline` observer reads every producer playlist on its own clock, independent of client requests.
  - **Per-run publication gating.** A restarted FFmpeg's run is held until every required rendition (every rendition ever published) has a publishable segment staged, with its first PTS read. It then opens and publishes as one new discontinuity for all of them in one locked pass. A run that dies before then is discarded and never served; the required set never shrinks.
  - **Liveness.** A run that publishes nothing within the playlist timeout of its process start, while FFmpeg is fed, is expired atomically (it can no longer open) and restarted through the existing kill path. The third consecutive expiry ends the session: the tuner is released and viewers get the existing "session not found", so clients reconnect into a fresh session.
  - Each served playlist's MEDIA-SEQUENCE has a monotonic floor, captions included, and the reference's stamps are kept while the caption playlist still lists them.
  - So every PTS reset a client sees carries `#EXT-X-DISCONTINUITY`, and every rendition gives the same broadcast moment the same discontinuity number, even while older segments are still in the window.
  - The run's media offsets come from each segment's real first PTS (failed reads are retried; a first segment never falls back to EXTINF), with the base fixed at gate open. Gaps come from the reference rendition only, and are kept until every rendition, including one whose playlist is late, has passed them.
  - Each rendition's segments are stamped from the run's wall-clock start plus their media offset. Each published segment keeps its epoch, boundary flag and Bowtie's own media sequence number.
  - Serving only reads the observer's snapshot. The snapshot also backs the owner-only `GET /api/v1/sessions/{viewerId}/timeline`.
- **Cleanup and final gate.** A caption sweeper deletes stray `.vtt` files. A one-hour real-broadcast soak confirms the timestamp option.

**Tech Stack:**
- Go 1.22 (`server/`).
- FFmpeg 5.1.9 (Debian bookworm, the production image) and the host FFmpeg for e2e; ffprobe.
- Docker Desktop on the dev Mac.
- The fake HDHomeRun (`internal/hdhr/hdhrfake`) and testplayer.

**Spec:** `docs/superpowers/specs/2026-10-11-stream-resilience-and-resume-design.md` (Part 1 §1.1–1.6 and the Part 1 items under Testing). Evidence: `.superpowers/sdd/av-sync/investigation.md` §A–C and §4. Revision log: the reviewers' findings and the rulings on them are summarized in the commit message of this revision.

## Global Constraints

- VOD and concat inputs keep their current flags. Only the live pipe input (`JobSpec.Stdin != nil`) and its audio outputs change (spec §1.1).
- Main pipe input gets `-fflags +discardcorrupt+genpts -err_detect ignore_err -thread_queue_size 1024 -probesize 20M -analyzeduration 10M`. The caption `movie` input gets the same `-thread_queue_size`, probe and analyze options.
- Every live audio output gets `aresample=async=1:min_hard_comp=0.02:first_pts=0` (spec §1.1, "Every audio output"). AAC outputs get it directly. For AC-3 copies, Task 5 measures whether input hardening alone closes the holes. If it doesn't, they are re-encoded to AC-3 with the filter; the spec is never weakened silently.
- Timestamp option: chosen by the Task 6 gate on FFmpeg 5.1. The candidates, one scratch branch each, are:
  - `-dts_delta_threshold 1`;
  - `-dts_delta_threshold` between the largest splice step and 3 s (2.6);
  - `-fps_mode vfr` with a clamp that closes the PTS hole. The clamp covers both video layouts, AAC, re-encoded AC-3 and the caption input.

  The gate requires all of:
  - the PTS+DTS and PTS-only `ptsjump` variants at +3, +6 and +9 s, and the `splice` scenario, with no timeline inflation and no duplicate storm (warnings and `dup!` bursts);
  - zero "timestamp discontinuity" re-bases on splices (FFmpeg 5.1 debug logs), with separate positive controls for re-bases and for duplicated frames;
  - the production layout with faults, AC-3 included;
  - cue timing on a real clip with injected faults, with timed cues in every coverage window;
  - no false re-bases on the real OTA clips, judged against their signal logs.

  If both kinds of option pass, prefer `fps_mode`. There is no "raise the threshold after the soak" escape hatch.
- Stuck rule: drain after 30 s without **bytes written to FFmpeg's pipe**, then kill only after a further 30 s grace with no bytes written. A drain never counts as progress.
- Every FFmpeg flag must work on FFmpeg 5.1 (checked in Task 6 against `debian:bookworm-slim`'s 5.1.9).
- Update existing tests; don't delete them. Don't add Go module dependencies.
- Wall-clock format everywhere Bowtie writes one: RFC 3339 with milliseconds, `2006-01-02T15:04:05.000Z07:00`, in UTC.
- Never hard-code a host (no `njathome`, no IPs) in code or tests. Hosts come from env vars or flags at run time (`HDHR_ADDR` for the household HDHomeRun).
- Every local test server runs with `BOWTIE_HDHOMERUN_GUIDE=off`. Never call api.hdhomerun.com. Never touch the production server.
- Real OTA clips live outside the repo and are never committed. Tests take their location from `BOWTIE_OTA_CLIPS` and skip when it's unset.
- Every commit message ends with the line `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`. Don't name a model anywhere else.
- Run Go commands from `server/` (the module root): `cd server && …`.

## Review Focus

1. **An ad splice re-based when it shouldn't be, or a corrupt PTS that isn't re-based.**
   - A viewer expects no frozen or duplicated video, no A/V offset and an unchanged Σ EXTINF across a real ad break or one corrupt timestamp.
   - Pinned by the Task 6 gate, which covers:
     - `ptsjump` and `ptsjump-ptsonly` at +3/+6/+9 s;
     - `splice` with zero re-base lines;
     - the OTA clip run (`TestOTAClips`).
   - The one-hour soak (Task 13) confirms it. It asserts re-bases per minute and the false re-base rule from the server log.
2. **Renditions stamped differently for the same moment, or overlapping PDT.**
   - Audio and video are cut at different points (4.032 s vs 4.004 s). FFmpeg publishes them at different times, their first segments start at different PTS, and clients poll them in any order.
   - A viewer expects PDT never to overlap within a rendition and the same moment to carry the same PDT, within one segment, in every rendition.
   - Pinned by:
     - Task 9: `TestTimelineStampsFromPTSAndMtime`, `TestTimelineUnequalDurationsNeverOverlap`, `TestTimelineStaggeredPublicationKeepsAlignment`, `TestTimelineEncoderDelayIsNotAGap` and `TestTimelineObservationTimingDoesNotMatter`;
     - Task 2: `TestConsistencyAlignsAnHourOfUnequalDurations`;
     - `pdtAligned` on the production-layout scenarios (Task 10).
3. **A timestamp reset without a DISCONTINUITY, or two discontinuity numbers for one moment.**
   - Partial runs (a restart that dies before every rendition has written), a rendition that misses runs, and a rendition whose playlist appears late must never leave an untagged PTS reset or renditions that disagree on the number, even while old segments are in the window.
   - Pinned by:
     - Task 9: `TestTimelinePartialRunIsNeverServed`, `TestTimelineRenditionMissingTwoRunsReturnsAligned`, `TestTimelineLateRenditionAtStartupJoinsEpochZero`, `TestTimelineGateWaitsForAPublishableSegmentInEveryRendition`, `TestTimelineDelayedRenditionGetsEveryGap`, `TestTimelineWaitsForFirstSegmentPTS`, `TestTimelineWaitsForEveryReferencePTS`, `TestTimelineNearAlignedCutsShareTheGapShift`, `TestTimelineAnchorCapNeverStampsFromIncompleteHistory` and `TestTimelineCaptionSequenceNeverGoesBack`;
     - Task 9: `TestTimelineStalledRunsExpireAtomically` and `TestTimelineStallBudgetOnlyResetsAfterHealthyOutput`; Task 10: `TestSessionTimelineGatesRestartedRuns`, `TestThirdGateStallEndsSessionAndFreesTuner`, `TestOpenThenStalledRunsUseUpTheBudget`, `TestOutageAndCachedJoinNeverExpire`, `TestContinuousFeedingResetsTheBudget`, `TestNoSignalAttachRetriesDontUseTheBudget` and `TestCachedBytesAreNotFresh` (a gate or a stalled run never holds the tuner forever, and an outage never ends a session);
     - Task 2: `TestConsistencyFlagsDifferentNumbersForTheSameMoment` and `TestConsistencyFlagsReturningRenditionNumberedFromZero`.
4. **A slow-but-alive transcoder killed while reading a large join, or a hung one never killed.**
   - Pinned by:
     - `TestSlowReaderOfLargeJoinIsNotStuck` and `TestSubQueueProgressCountsWrittenBytesNotPops` (Task 7);
     - `TestStuckSubDrainedThenClosedAfterGrace` (Task 7);
     - the `stuck-hung` scenario (Task 4).
5. **Checks that pass without comparing anything.**
   - A/V skew limits must rest on PDT-paired comparisons made after each fault cluster, in every rendition and discontinuity. Every fetch failure counts. A rendition that stops before the end fails. Captions need timed cues in every window, and an unmatched cue fails.
   - Pinned by `TestCoverageAfterFaults`, `TestFaultEndsClusters`, `TestCheckCoverageExpectations` (Task 4), `TestPlayerPairsAVByPDTAndCountsPerWindow` and `TestPlayerDrainsWaitingComparisons` (Task 3).
6. **The timeline endpoint leaking another user's session.** Only the owner's Bearer token sees it. Stream URL tokens, other users and admins get the same 404 as a missing viewer. Pinned by `TestSessionTimelineOwnerOnly` (Task 11).

---

## File map

| File | Responsibility | Tasks |
|---|---|---|
| `server/internal/hdhr/hdhrfake/faults/spec.go`, `engine.go` | `pts-jump` fault (PTS+DTS or PTS-only) | 1 |
| `server/internal/hdhr/hdhrfake/scenario/scenario.go`, `run.go` | pts-jump one-shot; `ptsOnly`; layout; new expectations; pinned xfail; `windowSegments` | 1, 4 |
| `server/internal/testplayer/consistency.go` (new) | pure cross-rendition playlist checks, PDT-overlap pairing | 2 |
| `server/internal/testplayer/player.go` | follow every rendition, probe segments, PDT-paired A/V skew, coverage windows, every fetch failure, tail gaps, cue timing, per-segment events | 3 |
| `server/internal/testplayer/coverage.go` (new) | per-window paired coverage, windows without timed cues, max tail gap | 4 |
| `server/internal/e2e/harness.go`, `scenarios_test.go`, `ffprobe_test.go` (new), `rebases_test.go` (new), `otasignal_test.go` (new), `otaclips_test.go` (new) | plumbing, production layout, ffprobe probe, FFmpeg log reader (re-bases, dup bursts, argv), signal-log judge, OTA clip gate | 4, 6 |
| `server/internal/e2e/testdata/scenarios/*.yaml` | new scenarios plus guards on existing ones | 4, 5, 6, 7, 10 |
| `server/internal/stream/manager.go` | `HLSListSize`, `ForceAdaptive`; timeline observer per session; RetimePlaylist; TimelineOf; caption sweep | 4, 10, 11, 12 |
| `server/internal/transcode/ffmpeg.go` | live input hardening, aresample (AAC and, if needed, AC-3), timestamp option | 5, 6 |
| `server/scripts/ffmpeg51` (new) | FFmpeg 5.1 (bookworm) through Docker for e2e on the Mac; logs argv, re-bases, dup bursts | 6 |
| `server/internal/stream/subqueue.go`, `ingest.go` | progress by bytes written; drain-then-kill; join at a complete video key; fresh (live) bytes vs cached copies, `IngestSub.FreshBytes` | 7, 8, 10 |
| `server/internal/stream/probe.go` | PMT video stream type | 8 |
| `server/internal/stream/hlsplaylist.go` (new), `timeline.go` (new) | playlist parse, segment first-PTS reader, server-side timeline observer with per-run publication gating | 9 |
| `server/internal/transcode/layout.go` | `Layout.Renditions()` | 10 |
| `server/internal/stream/session.go` | `session.timeline`, gate tick state (`lastFresh`, `lastTick`), observer stop; an expired run restarts FFmpeg through the kill path, and the third failed run without 60 s of healthy output ends the session (`manager.go` `maintain`, `restartSession`) | 10 |
| `server/internal/api/stream_handlers.go`, `server.go`, `docs/api/openapi.yaml` | retime on serve (snapshot only); owner-only timeline endpoint | 10, 11 |
| `server/internal/stream/captions_sweep.go` (new) | `.vtt` sweeper | 12 |
| `server/internal/e2e/realsoak_test.go` (new), `server/scripts/ffmpeg-soak` (new) | FFmpeg 5.1 in the production image; real-broadcast soak | 6, 13 |
| `server/internal/e2e/discriminator_test.go` (new) | restart discriminator server for AVPlayer and hls.js | 15 |
| `CHANGELOG.md`, `docs/deploy/truenas.md`, `docs/dev/fake-hdhomerun.md` | release notes, QSV watch list, harness docs | 1, 6, 14 |

## Expected failures before each fix

New scenarios start with `xfail:` and `xfailOn:`. The runner then requires a violation containing the `xfailOn` text, and fails on any other violation (Task 4). So a scenario can't "pass" its xfail because the session died. A fix forces removing the markers ("known gap fixed … remove xfail").

| Scenario | `xfailOn` (fails before the fix because) | Remove `xfail` in |
|---|---|---|
| `ptsjump` | `timeline excess`: duplicate-frame storm, Σ EXTINF grows by about 18 s | Task 6 |
| `ptsjump-ptsonly` | `timeline excess`: same with only the PTS wrong | Task 6 |
| `splice` | `timeline excess`: steps under FFmpeg's default 10 s threshold are filled with duplicates | Task 6 |
| `faults-production` | `timeline excess`: the same pts-jump and splice steps on the production layout (AC-3 and captions included) | Task 6 |
| `brief-dropout` | `timeline excess`: the 4 s hole is filled with duplicated frames | Task 6 |
| `threshold-dropout` | `timeline excess`: same for the 9 s and 12 s holes | Task 6 |
| `corrupt-audio` | `audio hole`: AAC (and AC-3 copy) holes where TEI packets are discarded | Task 5 |
| `stuck-recovers` | `ffmpeg restarts`: Bowtie kills FFmpeg at 30 s | Task 7 |
| `long-outage-restart` | `discontinuity sequence`: FFmpeg writes no `#EXT-X-DISCONTINUITY-SEQUENCE` | Task 10 |
| `stuck-hung`, `production-baseline`, `rebase-control` | none (guards; `rebase-control` is also the gate's positive control for re-base logging) | — |

**Rule when reality differs from the table:**
- A pre-fix run might pass the pinned check, for example if an input stall already makes FFmpeg skip instead of fill. In that case re-run the scenario once. If it still passes:
  - remove `xfail`/`xfailOn`, so the scenario becomes a guard;
  - say so in the commit message.
- A pre-fix run might also fail an extra expectation besides the pinned one, for example `pairedAfterEachFault` or `maxTailGapSeconds` on an xfail scenario. The run then fails as "unexpected (not the known gap)". In that case:
  - take that one expectation out of the scenario for now, and name it in the commit message;
  - the task that removes the xfail puts it back, and must pass with it.

  Never drop it for good.

Never loosen a threshold to make a fix pass. If a fix doesn't clear its row, stop and report the observed values.

**FFmpeg 5.1 experiment already run (input to Task 6, not a decision).**
- Setup: `debian:bookworm-slim` FFmpeg 5.1.9, libx264, and a 30.04 s clip.
- Fault: +9 s added to the PTS **and DTS** of one video PES, fed through a pipe at full speed.

| Variant | dup frames | Σ EXTINF | AAC hole |
|---|---|---|---|
| current argv | 270 | 39.04 s | 9.0 s |
| hardened input + aresample, no timestamp option | 270 | 39.04 s | filled |
| A: `-dts_delta_threshold 1` | 0 | 30.03 s | none |
| B: `-fps_mode:v vfr` | 0 | 39.04 s (9 s PTS hole kept) | none |
| C: `-fps_mode:v passthrough` | 0 | 39.04 s (9 s PTS hole kept) | none |

What this table does and doesn't show:
- FFmpeg 5.1's correction is computed from the **DTS**. A PTS-only error therefore isn't tested here.
- `splice`'s ±1.5–2.5 s steps exceed threshold 1, so A will likely re-base real splices, which the spec forbids.
- B/C keep the hole, so they fail "no inflation" unless something closes it.

Task 6 runs the full gate. Nothing above is a provisional choice.

---

### Task 1: `pts-jump` fault in the fake HDHomeRun

**Files:**
- Modify: `server/internal/hdhr/hdhrfake/faults/spec.go`
- Modify: `server/internal/hdhr/hdhrfake/faults/engine.go`
- Modify: `server/internal/hdhr/hdhrfake/scenario/scenario.go` (`overlaps`)
- Test: `server/internal/hdhr/hdhrfake/faults/engine_test.go`, `server/internal/hdhr/hdhrfake/scenario/scenario_test.go`
- Modify: `docs/dev/fake-hdhomerun.md` (fault table)

**Interfaces:**
- Produces:
  - `faults.PTSJump Kind = "pts-jump"`. `Spec.By` shifts the timestamps of **one** video PES per connection by `By` (90 kHz, mod 2^33).
  - `Spec.PTSOnly bool` (YAML `ptsOnly`, pts-jump only): shift the PTS and leave the DTS valid. It fires only on a PES that carries a DTS, because on a PTS-only PES the PTS is the DTS. The repo fixture and the synthetic sources carry DTS on every video PES (MPEG-2 with B-frames).
  - Default scope is the channel. It fires once on each connection that **emits** a video PES within 10 s (`dropFaultLife`) of the fault starting.
  - The shift is prepared before the stall and lock-quality checks, but only counts as fired when the packet is emitted. So a pts-jump during a signal fault fires on the first video PES after the lock returns (if that is within its 10 s life), instead of being lost on a held packet.
  - Scenario YAML: `{at: 8s, fault: pts-jump, by: 3s}` and `{at: 8s, fault: pts-jump, by: 3s, ptsOnly: true}`.

- [ ] **Step 1: Write the failing tests**

Append to `server/internal/hdhr/hdhrfake/faults/engine_test.go`:

```go
// pesPkt is a video PES start (PUSI) carrying PTS and DTS.
func pesPkt(pid uint16, pts, dts int64) []byte {
	p := pkt(pid)
	p[1] |= 0x40
	copy(p[4:], []byte{0, 0, 1, 0xE0, 0, 0, 0x80, 0xC0, 10, 0x31, 0, 1, 0, 1, 0x11, 0, 1, 0, 1})
	tsloop.SetPESTimestamps(p, pts, dts)
	return p
}

func TestPTSJumpShiftsOneVideoPES(t *testing.T) {
	e, _ := newEngine(t)
	mustAdd(t, e, ch, Spec{Fault: PTSJump, By: 3 * time.Second}, "c1")

	in := pesPkt(0x100, 1000, 900)
	orig := append([]byte(nil), in...)
	d := e.Decide("90.1", "c1", in, tsloop.TrackVideo)
	if d.Action != Emit || len(d.Packets) != 1 {
		t.Fatalf("decision %+v", d)
	}
	pts, dts, _, _ := tsloop.PESTimestamps(d.Packets[0])
	if pts != 1000+270000 || dts != 900+270000 {
		t.Fatalf("pts=%d dts=%d, want +270000", pts, dts)
	}
	if !bytes.Equal(in, orig) {
		t.Fatal("Decide modified the caller's packet")
	}
	// Fires once per connection: the next PES passes unchanged.
	d = e.Decide("90.1", "c1", pesPkt(0x100, 4000, 3900), tsloop.TrackVideo)
	if pts, _, _, _ := tsloop.PESTimestamps(d.Packets[0]); pts != 4000 {
		t.Fatalf("second PES pts=%d, want 4000", pts)
	}
	// Another connection on the channel gets its own one jump.
	d = e.Decide("90.1", "c2", pesPkt(0x100, 5000, 4900), tsloop.TrackVideo)
	if pts, _, _, _ := tsloop.PESTimestamps(d.Packets[0]); pts != 5000+270000 {
		t.Fatalf("c2 pts=%d, want jumped", pts)
	}
}

func TestPTSJumpSkipsAudioAndNonPESPackets(t *testing.T) {
	e, _ := newEngine(t)
	mustAdd(t, e, ch, Spec{Fault: PTSJump, By: time.Second}, "c1")
	a := e.Decide("90.1", "c1", pesPkt(0x101, 1000, 1000), tsloop.TrackAudio)
	if pts, _, _, _ := tsloop.PESTimestamps(a.Packets[0]); pts != 1000 {
		t.Fatalf("audio pts=%d, want untouched", pts)
	}
	mid := e.Decide("90.1", "c1", pkt(0x100), tsloop.TrackVideo) // no PUSI
	if !bytes.Equal(mid.Packets[0], pkt(0x100)) {
		t.Fatal("non-PES video packet changed")
	}
	v := e.Decide("90.1", "c1", pesPkt(0x100, 1000, 1000), tsloop.TrackVideo)
	if pts, _, _, _ := tsloop.PESTimestamps(v.Packets[0]); pts != 1000+90000 {
		t.Fatalf("first video PES pts=%d, want jumped", pts)
	}
}

func TestPTSJumpWrapsAt33Bits(t *testing.T) {
	e, _ := newEngine(t)
	mustAdd(t, e, ch, Spec{Fault: PTSJump, By: time.Second}, "c1")
	const max33 = int64(1)<<33 - 1
	d := e.Decide("90.1", "c1", pesPkt(0x100, max33-10, max33-10), tsloop.TrackVideo)
	if pts, _, _, _ := tsloop.PESTimestamps(d.Packets[0]); pts != 90000-11 {
		t.Fatalf("pts=%d, want wrapped %d", pts, 90000-11)
	}
}

func TestPTSJumpExpires(t *testing.T) {
	e, mc := newEngine(t)
	mustAdd(t, e, ch, Spec{Fault: PTSJump, By: time.Second}, "c1")
	mc.Advance(dropFaultLife)
	d := e.Decide("90.1", "c1", pesPkt(0x100, 1000, 1000), tsloop.TrackVideo)
	if pts, _, _, _ := tsloop.PESTimestamps(d.Packets[0]); pts != 1000 {
		t.Fatalf("pts=%d after expiry, want 1000", pts)
	}
}

func TestPTSJumpPTSOnlyKeepsDTS(t *testing.T) {
	e, _ := newEngine(t)
	mustAdd(t, e, ch, Spec{Fault: PTSJump, By: 3 * time.Second, PTSOnly: true}, "c1")
	// A PES without a DTS doesn't qualify (its PTS is its DTS): unchanged, shot kept.
	p := pkt(0x100)
	p[1] |= 0x40
	copy(p[4:], []byte{0, 0, 1, 0xE0, 0, 0, 0x80, 0x80, 5, 0x21, 0, 1, 0, 1})
	tsloop.SetPESTimestamps(p, 1000, 0)
	if pts, _, _, _ := tsloop.PESTimestamps(e.Decide("90.1", "c1", p, tsloop.TrackVideo).Packets[0]); pts != 1000 {
		t.Fatalf("PTS-only PES without DTS changed: pts=%d", pts)
	}
	d := e.Decide("90.1", "c1", pesPkt(0x100, 2000, 1900), tsloop.TrackVideo)
	pts, dts, _, _ := tsloop.PESTimestamps(d.Packets[0])
	if pts != 2000+270000 || dts != 1900 {
		t.Fatalf("pts=%d dts=%d, want pts+270000 and dts unchanged", pts, dts)
	}
}

// A pts-jump that starts while the signal is below lock isn't used up by held
// packets: it fires on the first video PES emitted after the lock returns.
func TestPTSJumpSurvivesHeldPackets(t *testing.T) {
	e, mc := newEngine(t)
	mustAdd(t, e, ch, Spec{Fault: Signal, Quality: intp(10), For: 2 * time.Second})
	mustAdd(t, e, ch, Spec{Fault: PTSJump, By: time.Second}, "c1")
	if d := e.Decide("90.1", "c1", pesPkt(0x100, 1000, 1000), tsloop.TrackVideo); d.Action != Hold {
		t.Fatalf("below lock: %+v, want Hold", d)
	}
	mc.Advance(2 * time.Second)
	d := e.Decide("90.1", "c1", pesPkt(0x100, 1000, 1000), tsloop.TrackVideo)
	if pts, _, _, _ := tsloop.PESTimestamps(d.Packets[0]); pts != 1000+90000 {
		t.Fatalf("after lock pts=%d, want jumped", pts)
	}
}

func TestPTSJumpNeedsBy(t *testing.T) {
	if err := (Spec{Fault: PTSJump}).Validate(); err == nil {
		t.Fatal("pts-jump without by validated")
	}
	if err := (Spec{Fault: Stall, PTSOnly: true}).Validate(); err == nil {
		t.Fatal("ptsOnly on a stall validated")
	}
}
```

Append to `server/internal/hdhr/hdhrfake/scenario/scenario_test.go`:

```go
func TestPTSJumpStepsNeverOverlap(t *testing.T) {
	_, err := Parse([]byte(`
name: pj
duration: 30s
timeline:
  - {at: 8s, fault: pts-jump, by: 3s}
  - {at: 9s, fault: pts-jump, by: 6s}
`))
	if err != nil {
		t.Fatalf("one-shot pts-jump steps rejected: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd server && go test ./internal/hdhr/hdhrfake/faults/ ./internal/hdhr/hdhrfake/scenario/ -run 'PTSJump' -v`
Expected: FAIL to compile with `undefined: PTSJump`.

- [ ] **Step 3: Implement**

In `faults/spec.go`, add the kind after `TimestampJump`:

```go
	// PTSJump shifts the PTS/DTS of one video PES per connection by By
	// (a single corrupt timestamp, as dirty broadcasts carry).
	PTSJump Kind = "pts-jump"
```

Change the `By` field comment to `// timestamp-jump, pts-jump` and add after `By`:

```go
	PTSOnly  bool          `yaml:"ptsOnly,omitempty" json:"ptsOnly,omitempty"` // pts-jump: leave the DTS valid
```

At the top of `Validate` (after the negative-duration check):

```go
	if s.PTSOnly && s.Fault != PTSJump {
		return errors.New("faults: ptsOnly is for pts-jump")
	}
```

In `Validate`, add a case before `case SourceSwitch:`:

```go
	case PTSJump:
		if s.By == 0 {
			return errors.New("faults: pts-jump needs a non-zero by")
		}
```

In `faults/engine.go` `Add`, extend the lifetime switch:

```go
	switch {
	case s.For > 0:
		f.until = now.Add(s.For)
	case s.Fault == Drop || s.Fault == PTSJump:
		f.until = now.Add(dropFaultLife)
	}
```

In `Decide`:

1. Move the `out`/`copied`/`own` block (currently after the `Slow` loop) up to right after the `Drop` loop, before the `Stall` loop. Its code is unchanged.
2. Right after it (still before the `Stall` loop and the lock-quality check), insert:

```go
	// pts-jump: prepared before the stall/lock checks so a held packet doesn't
	// lose the shot; it only counts as fired when this packet is emitted.
	var jumped *fault
	for _, f := range hits {
		if f.Spec.Fault != PTSJump || f.fired[conn] || track != tsloop.TrackVideo || jumped != nil {
			continue
		}
		pts, dts, hasPTS, hasDTS := tsloop.PESTimestamps(pkt)
		if !hasPTS || (f.Spec.PTSOnly && !hasDTS) {
			continue
		}
		by := int64(f.Spec.By/time.Microsecond) * 9 / 100 // 90 kHz ticks
		if !f.Spec.PTSOnly {
			dts += by
		}
		own()
		tsloop.SetPESTimestamps(out, pts+by, dts) // writeTS masks to 33 bits; absent DTS is left absent
		jumped = f
	}
	emit := func(d Decision) Decision {
		if jumped != nil {
			jumped.fired[conn] = true
		}
		return d
	}
```

3. Change the two final returns of `Decide` to go through `emit`:

```go
	if junk != nil {
		return emit(Decision{Action: Emit, Packets: [][]byte{junk, out}})
	}
	return emit(Decision{Action: Emit, Packets: [][]byte{out}})
```

   Every `Hold`/`Skip`/`Close` return stays as it is, so the shot isn't used.

(`pruneLocked` needs no change: a pts-jump expires after its 10 s life whether or not it fired.)

In `scenario/scenario.go` `overlaps`, treat pts-jump as one-shot:

```go
	if a.Fault != b.Fault || a.Scope != b.Scope || a.Fault == faults.Drop || a.Fault == faults.PTSJump || a.Fault.IsTimeline() {
```

In `docs/dev/fake-hdhomerun.md`, add a row to the fault table next to `timestamp-jump`:

```markdown
| `pts-jump` | `by`, `ptsOnly` | Adds `by` to the PTS and DTS (or, with `ptsOnly: true`, only the PTS of a PES that carries a DTS) of one emitted video PES on each connection (fires once per connection within 10 s; 33-bit wrap; survives a held lock). Models a single corrupt timestamp. |
```

(Match the existing table's column layout; if the table has a different column set, put the same text into its columns.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd server && go test ./internal/hdhr/hdhrfake/... -v -run 'PTSJump|Overlap'` then `cd server && go test ./internal/hdhr/hdhrfake/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/hdhr/hdhrfake/faults server/internal/hdhr/hdhrfake/scenario docs/dev/fake-hdhomerun.md
git commit -m "test(fakehdhr): pts-jump fault shifts one video PES per connection

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: testplayer consistency checker (pure)

**Files:**
- Create: `server/internal/testplayer/consistency.go`
- Test: `server/internal/testplayer/consistency_test.go`

**Interfaces:**
- Produces (package `testplayer`):
  - `type SegmentObs struct { Seq int64; Duration float64; PDT time.Time; Discontinuity bool; DNum int64; URI string }`. `DNum` is the discontinuity number a client computes: `DISCONTINUITY-SEQUENCE` plus the tags up to and including the segment.
  - `type Snapshot struct { Rendition string; At time.Time; MediaSequence, DiscontinuitySequence int64; Segments []SegmentObs }`
  - `func ParseSnapshot(rendition string, body []byte, at time.Time) Snapshot`. It accepts RFC 3339 PDTs and FFmpeg's `+0000` form.
  - `type PDTSeg struct { Start time.Time; Dur float64; DNum int64 }` with `End() time.Time`.
  - `func PairByPDT(a PDTSeg, others []PDTSeg, tol time.Duration) (int, bool)`. `others` is sorted by `Start`. It returns the segment whose PDT range overlaps `a`'s the most; with no overlap, the one starting nearest `a` within `tol`. Callers pass one segment (`a`'s duration) as `tol`. Task 3 uses the same function to pair audio with video for first-PTS skew.
  - `type Consistency struct { RenditionsMonotonic, DiscontinuitySequenceOK, PDTMonotonic, PDTAligned, DiscontinuitiesAligned bool; MaxDiscontinuitySequence int64; Problems []string }`
  - `func CheckConsistency(snaps []Snapshot) Consistency`. `snaps` is in poll order; renditions are grouped by name.
- Renditions are paired by PDT-range overlap, never by (epoch, index). Index keys break when segment durations differ (4.004 s video against 4.032 s audio drifts a whole segment in under ten minutes) and when one rendition has a segment the other lacks. The two checks that pin the original bug:
  - `DiscontinuitiesAligned`: two segments paired by PDT must have the same `DNum`. This is the invariant Task 9 enforces: every rendition gives the same broadcast moment the same discontinuity number, including while older segments are still in the window. Each discontinuity must also start within 500 ms in every rendition, and every rendition must end on the same number.
  - `PDTAligned`: inside the other rendition's observed span, every segment must have a PDT partner within one segment. A hole in one rendition that the other doesn't share fails. A gap the timeline jumped is in both renditions, so it leaves nothing unpaired.

- [ ] **Step 1: Write the failing tests**

Create `server/internal/testplayer/consistency_test.go`:

```go
package testplayer

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

var c0 = time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC)

func at(sec float64) time.Time { return c0.Add(time.Duration(sec * float64(time.Second))) }

type ent struct {
	pdt  time.Time
	disc bool
	dur  float64 // 0 means 4 s
}

// mk renders a media playlist.
func mk(name string, mseq, dseq int64, es ...ent) string {
	var b strings.Builder
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:6\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:%d\n", mseq)
	if dseq > 0 {
		fmt.Fprintf(&b, "#EXT-X-DISCONTINUITY-SEQUENCE:%d\n", dseq)
	}
	for i, e := range es {
		if e.disc {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		d := e.dur
		if d == 0 {
			d = 4
		}
		fmt.Fprintf(&b, "#EXT-X-PROGRAM-DATE-TIME:%s\n#EXTINF:%.6f,\n/s/%s_%05d.ts?token=x\n",
			e.pdt.UTC().Format("2006-01-02T15:04:05.000Z07:00"), d, name, mseq+int64(i))
	}
	return b.String()
}

func snap(name, body string, n int) Snapshot {
	return ParseSnapshot(name, []byte(body), at(float64(n)))
}

// restartRun is two renditions across a restart at 70 s: video seq 0..2 then
// 3.. (new run); audio had one more segment before FFmpeg died (0..3 then 4..).
func restartRun(audioBoundary time.Time) []Snapshot {
	return []Snapshot{
		snap("v480", mk("v480", 0, 0, ent{pdt: at(0)}, ent{pdt: at(4)}, ent{pdt: at(8)}, ent{pdt: at(70), disc: true}), 1),
		snap("aac0", mk("aac0", 0, 0, ent{pdt: at(0)}, ent{pdt: at(4)}, ent{pdt: at(8)}, ent{pdt: at(12)}, ent{pdt: audioBoundary, disc: true}), 1),
		snap("v480", mk("v480", 3, 0, ent{pdt: at(70), disc: true}, ent{pdt: at(74)}), 2),
		snap("aac0", mk("aac0", 4, 0, ent{pdt: audioBoundary, disc: true}, ent{pdt: audioBoundary.Add(4 * time.Second)}), 2),
		snap("v480", mk("v480", 4, 1, ent{pdt: at(74)}, ent{pdt: at(78)}), 3),
		snap("aac0", mk("aac0", 5, 1, ent{pdt: audioBoundary.Add(4 * time.Second)}, ent{pdt: audioBoundary.Add(8 * time.Second)}), 3),
	}
}

func allOK(c Consistency) bool {
	return c.RenditionsMonotonic && c.DiscontinuitySequenceOK && c.PDTMonotonic && c.PDTAligned && c.DiscontinuitiesAligned
}

func TestConsistencyPairsByPDTWhenAudioWasAhead(t *testing.T) {
	c := CheckConsistency(restartRun(at(70)))
	if !allOK(c) {
		t.Fatalf("consistent run flagged: %+v", c)
	}
	if c.MaxDiscontinuitySequence != 1 {
		t.Fatalf("MaxDiscontinuitySequence=%d, want 1", c.MaxDiscontinuitySequence)
	}
}

func TestConsistencyFlagsMisalignedBoundary(t *testing.T) {
	c := CheckConsistency(restartRun(at(74)))
	if c.DiscontinuitiesAligned {
		t.Fatalf("audio boundary 4 s late not flagged: %+v", c)
	}
}

// The original bug: after a restart one rendition carries a tag the other
// doesn't, so the same broadcast moment has two discontinuity numbers.
func TestConsistencyFlagsDifferentNumbersForTheSameMoment(t *testing.T) {
	c := CheckConsistency([]Snapshot{
		snap("v480", mk("v480", 0, 0, ent{pdt: at(0)}, ent{pdt: at(4)}, ent{pdt: at(70), disc: true}, ent{pdt: at(74)}), 1),
		snap("aac0", mk("aac0", 0, 0, ent{pdt: at(0)}, ent{pdt: at(4)}, ent{pdt: at(70)}, ent{pdt: at(74)}), 1),
	})
	if c.DiscontinuitiesAligned {
		t.Fatalf("discontinuity 1 vs 0 at 70 s not flagged: %+v", c)
	}
	if !strings.Contains(strings.Join(c.Problems, "\n"), "is discontinuity 0, v480 says 1") {
		t.Fatalf("problem does not name the numbers: %q", c.Problems)
	}
}

// The original bug, second form: a rendition that missed runs and came back
// under DISCONTINUITY-SEQUENCE 0 while the other's old segments are still
// in its window.
func TestConsistencyFlagsReturningRenditionNumberedFromZero(t *testing.T) {
	c := CheckConsistency([]Snapshot{
		snap("v480", mk("v480", 0, 0, ent{pdt: at(0)}, ent{pdt: at(40), disc: true}, ent{pdt: at(80), disc: true}, ent{pdt: at(84)}), 1),
		snap("aac0", mk("aac0", 0, 0, ent{pdt: at(0)}, ent{pdt: at(80), disc: true}, ent{pdt: at(84)}), 1),
	})
	if c.DiscontinuitiesAligned {
		t.Fatalf("audio at 80 s as discontinuity 1 vs video 2 not flagged: %+v", c)
	}
}

func TestConsistencyFlagsCoverageHole(t *testing.T) {
	// Audio skips 8–20 s while video, with no gap, has it. (A hole shorter
	// than one segment is inside the pairing tolerance.)
	c := CheckConsistency([]Snapshot{
		snap("v480", mk("v480", 0, 0, ent{pdt: at(0)}, ent{pdt: at(4)}, ent{pdt: at(8)}, ent{pdt: at(12)}, ent{pdt: at(16)}, ent{pdt: at(20)}), 1),
		snap("aac0", mk("aac0", 0, 0, ent{pdt: at(0)}, ent{pdt: at(4)}, ent{pdt: at(20)}, ent{pdt: at(24)}), 1),
	})
	if c.PDTAligned {
		t.Fatalf("audio hole at 8–20 s not flagged: %+v", c)
	}
}

func TestConsistencyAllowsAGapBothRenditionsJumped(t *testing.T) {
	c := CheckConsistency([]Snapshot{
		snap("v480", mk("v480", 0, 0, ent{pdt: at(0)}, ent{pdt: at(4)}, ent{pdt: at(30)}, ent{pdt: at(34)}), 1),
		snap("aac0", mk("aac0", 0, 0, ent{pdt: at(0)}, ent{pdt: at(4)}, ent{pdt: at(30.2)}, ent{pdt: at(34.2)}), 1),
	})
	if !allOK(c) {
		t.Fatalf("shared gap flagged: %+v", c)
	}
}

// An hour of 4.004 s video against 4.032 s audio drifts by whole segments;
// (run, index) keys would misalign, PDT overlap must not.
func TestConsistencyAlignsAnHourOfUnequalDurations(t *testing.T) {
	var v, a []ent
	for i := 0; float64(i)*4.004 < 3600; i++ {
		v = append(v, ent{pdt: at(float64(i) * 4.004), dur: 4.004})
	}
	for i := 0; float64(i)*4.032 < 3600; i++ {
		a = append(a, ent{pdt: at(float64(i) * 4.032), dur: 4.032})
	}
	c := CheckConsistency([]Snapshot{snap("v480", mk("v480", 0, 0, v...), 1), snap("aac0", mk("aac0", 0, 0, a...), 1)})
	if !allOK(c) {
		t.Fatalf("drifting durations flagged: %+v", c.Problems)
	}
}

func TestPairByPDT(t *testing.T) {
	others := []PDTSeg{{Start: at(0), Dur: 4}, {Start: at(4), Dur: 4}, {Start: at(20), Dur: 4}}
	for _, tc := range []struct {
		start float64
		want  int
		ok    bool
	}{
		{0.1, 0, true},  // mostly overlaps the first
		{2.5, 1, true},  // overlaps the second more
		{8, 1, true},    // no overlap; nearest start within one segment
		{13, -1, false}, // nothing within one segment
		{19, 2, true},
	} {
		got, ok := PairByPDT(PDTSeg{Start: at(tc.start), Dur: 4}, others, 4*time.Second)
		if got != tc.want || ok != tc.ok {
			t.Errorf("start %v: got (%d,%v), want (%d,%v)", tc.start, got, ok, tc.want, tc.ok)
		}
	}
}

func TestConsistencyFlagsPDTBackwards(t *testing.T) {
	c := CheckConsistency([]Snapshot{
		snap("v480", mk("v480", 0, 0, ent{pdt: at(0)}, ent{pdt: at(4)}, ent{pdt: at(6), disc: true}), 1),
	})
	if c.PDTMonotonic {
		t.Fatalf("PDT 6 s after a segment ending at 8 s not flagged: %+v", c)
	}
}

func TestConsistencyFlagsStuckDiscontinuitySequence(t *testing.T) {
	c := CheckConsistency([]Snapshot{
		snap("v480", mk("v480", 2, 0, ent{pdt: at(8), disc: true}, ent{pdt: at(12)}), 1),
		// The tagged segment slid out but the sequence stayed 0.
		snap("v480", mk("v480", 3, 0, ent{pdt: at(12)}, ent{pdt: at(16)}), 2),
	})
	if c.DiscontinuitySequenceOK {
		t.Fatalf("missing DISCONTINUITY-SEQUENCE increment not flagged: %+v", c)
	}
}

func TestConsistencyFlagsMediaSequenceBackwards(t *testing.T) {
	c := CheckConsistency([]Snapshot{
		snap("v480", mk("v480", 5, 0, ent{pdt: at(20)}), 1),
		snap("v480", mk("v480", 0, 0, ent{pdt: at(24)}), 2),
	})
	if c.RenditionsMonotonic {
		t.Fatalf("MEDIA-SEQUENCE reset not flagged: %+v", c)
	}
}

func TestConsistencyRequiresPDT(t *testing.T) {
	c := CheckConsistency([]Snapshot{snap("v480", "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:4.0,\na.ts\n", 1)})
	if c.PDTMonotonic {
		t.Fatal("playlist without PDT passed pdtMonotonic")
	}
}

func TestParseSnapshotReadsFFmpegPDT(t *testing.T) {
	s := ParseSnapshot("v480", []byte("#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:7\n#EXT-X-DISCONTINUITY-SEQUENCE:2\n"+
		"#EXT-X-PROGRAM-DATE-TIME:2026-10-11T20:00:04.500+0000\n#EXTINF:4.004000,\nv480_00007.ts\n"+
		"#EXT-X-DISCONTINUITY\n#EXT-X-PROGRAM-DATE-TIME:2026-10-11T20:00:30.000+0000\n#EXTINF:4.0,\nv480_00008.ts\n"), at(0))
	if s.MediaSequence != 7 || s.DiscontinuitySequence != 2 || len(s.Segments) != 2 {
		t.Fatalf("snapshot %+v", s)
	}
	g := s.Segments[0]
	if g.Seq != 7 || g.Duration != 4.004 || !g.PDT.Equal(at(4.5)) || g.URI != "v480_00007.ts" || g.DNum != 2 {
		t.Fatalf("segment %+v", g)
	}
	if h := s.Segments[1]; !h.Discontinuity || h.DNum != 3 {
		t.Fatalf("tagged segment %+v, want DNum 3", h)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd server && go test ./internal/testplayer/ -run 'Consistency|ParseSnapshot|PairByPDT' -v`
Expected: FAIL to compile with `undefined: ParseSnapshot`.

- [ ] **Step 3: Implement**

Create `server/internal/testplayer/consistency.go`:

```go
package testplayer

import (
	"bufio"
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// maxAlign is how far apart two renditions may start the same discontinuity.
const maxAlign = 500 * time.Millisecond

// pdtSlack absorbs millisecond rounding in served PDTs.
const pdtSlack = 5 * time.Millisecond

// maxProblemsPerKind keeps a broken run's report readable.
const maxProblemsPerKind = 5

// SegmentObs is one segment as a media playlist listed it.
type SegmentObs struct {
	Seq           int64
	Duration      float64
	PDT           time.Time // zero when the segment had no #EXT-X-PROGRAM-DATE-TIME
	Discontinuity bool
	// DNum is the discontinuity number a client computes for the segment:
	// DISCONTINUITY-SEQUENCE plus the tags up to and including it.
	DNum int64
	URI  string
}

// Snapshot is one poll of one rendition's media playlist.
type Snapshot struct {
	Rendition             string
	At                    time.Time
	MediaSequence         int64
	DiscontinuitySequence int64
	Segments              []SegmentObs
}

// PDTSeg is a segment's place on the wall clock and its discontinuity number.
type PDTSeg struct {
	Start time.Time
	Dur   float64
	DNum  int64
}

// End is when the segment ends on the wall clock.
func (s PDTSeg) End() time.Time { return s.Start.Add(time.Duration(s.Dur * float64(time.Second))) }

// PairByPDT returns the index in others (sorted by Start, not overlapping)
// of the segment whose PDT range overlaps a's the most. With no overlap it
// is the one starting nearest to a, within tol (one segment). False when
// none qualifies.
func PairByPDT(a PDTSeg, others []PDTSeg, tol time.Duration) (int, bool) {
	best, bestOv, bestDist := -1, time.Duration(0), time.Duration(1<<62)
	i := sort.Search(len(others), func(i int) bool { return !others[i].End().Before(a.Start.Add(-tol)) })
	for ; i < len(others) && !others[i].Start.After(a.End().Add(tol)); i++ {
		o := others[i]
		lo, hi := a.Start, a.End()
		if o.Start.After(lo) {
			lo = o.Start
		}
		if o.End().Before(hi) {
			hi = o.End()
		}
		ov := hi.Sub(lo)
		dist := o.Start.Sub(a.Start)
		if dist < 0 {
			dist = -dist
		}
		switch {
		case ov > bestOv:
			best, bestOv, bestDist = i, ov, dist
		case bestOv <= 0 && dist <= tol && dist < bestDist:
			best, bestDist = i, dist
		}
	}
	return best, best >= 0
}

// pdtLayouts: RFC 3339 (Bowtie) and FFmpeg's "+0000" offset.
var pdtLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05.000-0700", "2006-01-02T15:04:05-0700"}

func parsePDT(v string) (time.Time, bool) {
	for _, l := range pdtLayouts {
		if t, err := time.Parse(l, v); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ParseSnapshot reads the parts of a media playlist the checks use.
func ParseSnapshot(rendition string, body []byte, at time.Time) Snapshot {
	s := Snapshot{Rendition: rendition, At: at}
	var cur SegmentObs
	idx := int64(0)
	dnum := int64(-1) // set once DISCONTINUITY-SEQUENCE (or the first segment) is seen
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			s.MediaSequence, _ = strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64)
		case strings.HasPrefix(line, "#EXT-X-DISCONTINUITY-SEQUENCE:"):
			s.DiscontinuitySequence, _ = strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-DISCONTINUITY-SEQUENCE:"), 10, 64)
		case line == "#EXT-X-DISCONTINUITY":
			cur.Discontinuity = true
		case strings.HasPrefix(line, "#EXT-X-PROGRAM-DATE-TIME:"):
			if t, ok := parsePDT(strings.TrimPrefix(line, "#EXT-X-PROGRAM-DATE-TIME:")); ok {
				cur.PDT = t
			}
		case strings.HasPrefix(line, "#EXTINF:"):
			v := strings.TrimPrefix(line, "#EXTINF:")
			if i := strings.IndexByte(v, ','); i >= 0 {
				v = v[:i]
			}
			cur.Duration, _ = strconv.ParseFloat(v, 64)
		case line == "" || strings.HasPrefix(line, "#"):
		default:
			if dnum < 0 {
				dnum = s.DiscontinuitySequence
			}
			if cur.Discontinuity {
				dnum++
			}
			cur.DNum = dnum
			cur.Seq = s.MediaSequence + idx
			cur.URI = line
			s.Segments = append(s.Segments, cur)
			cur = SegmentObs{}
			idx++
		}
	}
	return s
}

// Consistency is what a viewer switching renditions or seeking by wall
// clock would rely on, checked across every poll of every rendition.
type Consistency struct {
	// RenditionsMonotonic: no rendition's MEDIA-SEQUENCE or newest segment went down.
	RenditionsMonotonic bool
	// DiscontinuitySequenceOK: each DISCONTINUITY-SEQUENCE equals the previous
	// one plus the tagged segments that slid out, and tags never move.
	DiscontinuitySequenceOK bool
	// MaxDiscontinuitySequence is the largest DISCONTINUITY-SEQUENCE served.
	MaxDiscontinuitySequence int64
	// PDTMonotonic: every segment has a PDT, a segment's PDT never changes, and
	// no segment starts before the previous one ends.
	PDTMonotonic bool
	// PDTAligned: wherever two renditions both have segments, each segment
	// of one overlaps (within one segment) a segment of the other.
	PDTAligned bool
	// DiscontinuitiesAligned: segments paired by PDT overlap have the same
	// discontinuity number, each discontinuity starts at the same PDT
	// (±500 ms) in every rendition, and every rendition ends on the same number.
	DiscontinuitiesAligned bool
	Problems               []string
}

type segRec struct {
	dnum int64
	pdt  time.Time
	dur  float64
	disc bool
}

type rendView struct {
	snaps    []Snapshot
	segs     map[int64]segRec    // seq → first observation
	boundary map[int64]time.Time // discontinuity number → PDT of its tagged first segment
	final    int64
	ordered  []PDTSeg // segments with a PDT, by start
}

// CheckConsistency checks snapshots (in poll order) within and across renditions.
func CheckConsistency(snaps []Snapshot) Consistency {
	c := Consistency{RenditionsMonotonic: true, DiscontinuitySequenceOK: true, PDTMonotonic: true, PDTAligned: true, DiscontinuitiesAligned: true}
	counts := map[string]int{}
	problem := func(kind string, flag *bool, format string, a ...any) {
		*flag = false
		counts[kind]++
		if counts[kind] <= maxProblemsPerKind {
			c.Problems = append(c.Problems, kind+": "+fmt.Sprintf(format, a...))
		}
	}

	views := map[string]*rendView{}
	var names []string
	for _, s := range snaps {
		v := views[s.Rendition]
		if v == nil {
			v = &rendView{segs: map[int64]segRec{}, boundary: map[int64]time.Time{}}
			views[s.Rendition] = v
			names = append(names, s.Rendition)
		}
		v.snaps = append(v.snaps, s)
	}
	sort.Strings(names)

	for _, name := range names {
		v := views[name]
		noPDT := false
		for i, s := range v.snaps {
			if s.DiscontinuitySequence > c.MaxDiscontinuitySequence {
				c.MaxDiscontinuitySequence = s.DiscontinuitySequence
			}
			if i > 0 {
				p := v.snaps[i-1]
				if s.MediaSequence < p.MediaSequence {
					problem("sequence", &c.RenditionsMonotonic, "%s: MEDIA-SEQUENCE %d after %d", name, s.MediaSequence, p.MediaSequence)
				}
				if len(s.Segments) > 0 && len(p.Segments) > 0 && s.Segments[len(s.Segments)-1].Seq < p.Segments[len(p.Segments)-1].Seq {
					problem("sequence", &c.RenditionsMonotonic, "%s: newest segment went down", name)
				}
				if s.DiscontinuitySequence < p.DiscontinuitySequence {
					problem("dseq", &c.DiscontinuitySequenceOK, "%s: DISCONTINUITY-SEQUENCE %d after %d", name, s.DiscontinuitySequence, p.DiscontinuitySequence)
				}
				lastP := p.MediaSequence + int64(len(p.Segments)) - 1
				if s.MediaSequence <= lastP+1 { // the window did not slide past unseen segments
					want := p.DiscontinuitySequence
					for _, g := range p.Segments {
						if g.Discontinuity && g.Seq < s.MediaSequence {
							want++
						}
					}
					if s.DiscontinuitySequence != want {
						problem("dseq", &c.DiscontinuitySequenceOK, "%s: DISCONTINUITY-SEQUENCE %d at MEDIA-SEQUENCE %d, want %d", name, s.DiscontinuitySequence, s.MediaSequence, want)
					}
				}
			}
			for _, g := range s.Segments {
				if g.Discontinuity && !g.PDT.IsZero() {
					v.boundary[g.DNum] = g.PDT
				}
				if g.PDT.IsZero() && !noPDT {
					noPDT = true
					problem("pdt", &c.PDTMonotonic, "%s: segment %d has no PROGRAM-DATE-TIME", name, g.Seq)
				}
				if old, ok := v.segs[g.Seq]; ok {
					if old.disc != g.Discontinuity {
						problem("dseq", &c.DiscontinuitySequenceOK, "%s: DISCONTINUITY on segment %d came and went", name, g.Seq)
					}
					if old.dnum != g.DNum {
						problem("dseq", &c.DiscontinuitySequenceOK, "%s: segment %d moved from discontinuity %d to %d", name, g.Seq, old.dnum, g.DNum)
					}
					if d := g.PDT.Sub(old.pdt); d > pdtSlack || d < -pdtSlack {
						problem("pdt", &c.PDTMonotonic, "%s: PDT of segment %d changed by %v", name, g.Seq, d)
					}
					continue
				}
				v.segs[g.Seq] = segRec{dnum: g.DNum, pdt: g.PDT, dur: g.Duration, disc: g.Discontinuity}
			}
			if n := len(s.Segments); n > 0 {
				v.final = s.Segments[n-1].DNum
			} else {
				v.final = s.DiscontinuitySequence
			}
		}
		seqs := make([]int64, 0, len(v.segs))
		for q := range v.segs {
			seqs = append(seqs, q)
		}
		sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
		for i, q := range seqs {
			r := v.segs[q]
			if !r.pdt.IsZero() {
				v.ordered = append(v.ordered, PDTSeg{Start: r.pdt, Dur: r.dur, DNum: r.dnum})
			}
			if i == 0 {
				continue
			}
			p := v.segs[seqs[i-1]]
			if p.pdt.IsZero() || r.pdt.IsZero() {
				continue
			}
			end := p.pdt.Add(time.Duration(p.dur * float64(time.Second)))
			if r.pdt.Before(end.Add(-pdtSlack)) {
				problem("pdt", &c.PDTMonotonic, "%s: segment %d starts %v before segment %d ends", name, q, end.Sub(r.pdt), seqs[i-1])
			}
		}
	}

	for i := range names {
		for j := range names {
			if i == j {
				continue
			}
			a, b := views[names[i]], views[names[j]]
			if len(b.ordered) == 0 {
				continue
			}
			first, last := b.ordered[0].Start, b.ordered[len(b.ordered)-1].End()
			for _, s := range a.ordered {
				tol := time.Duration(s.Dur * float64(time.Second))
				k, ok := PairByPDT(s, b.ordered, tol)
				if !ok {
					// Only inside the other rendition's observed span: segments
					// beyond its window edges were never compared. A gap the
					// timeline jumped is in both renditions, so it leaves no
					// segment here to miss.
					if !s.Start.Before(first) && !s.End().After(last) {
						problem("align", &c.PDTAligned, "%s: no %s segment within one segment of %s", names[i], names[j], s.Start.Format(time.RFC3339Nano))
					}
					continue
				}
				if i < j && b.ordered[k].DNum != s.DNum {
					problem("boundary", &c.DiscontinuitiesAligned, "%s at %s is discontinuity %d, %s says %d",
						names[i], s.Start.Format(time.RFC3339Nano), s.DNum, names[j], b.ordered[k].DNum)
				}
			}
			if i > j {
				continue
			}
			for e, ta := range a.boundary {
				if tb, ok := b.boundary[e]; ok {
					if d := ta.Sub(tb); d > maxAlign || d < -maxAlign {
						problem("boundary", &c.DiscontinuitiesAligned, "%s/%s: discontinuity %d starts %v apart", names[i], names[j], e, d)
					}
				}
			}
			if a.final != b.final {
				problem("boundary", &c.DiscontinuitiesAligned, "%s ends on discontinuity %d, %s on %d", names[i], a.final, names[j], b.final)
			}
		}
	}
	return c
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd server && go test ./internal/testplayer/ -v -run 'Consistency|ParseSnapshot|PairByPDT'` then `cd server && go test -race ./internal/testplayer/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/testplayer/consistency.go server/internal/testplayer/consistency_test.go
git commit -m "test(testplayer): cross-rendition playlist consistency checks

Pair renditions by PDT overlap and require paired segments to carry the
same discontinuity number.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: testplayer follows every rendition, probes segments, measures coverage per window

**Files:**
- Modify (replace): `server/internal/testplayer/player.go`
- Test: `server/internal/testplayer/player_renditions_test.go` (new)

**Interfaces:**
- Consumes: `ParseSnapshot`, `CheckConsistency`, `PairByPDT`, `PDTSeg`, `Snapshot`, `SegmentObs.DNum` (Task 2).
- Produces:
  - `type Packet struct { PTS, Dur float64 }` (seconds) and `type SegmentProbe func(ctx context.Context, seg []byte) ([]Packet, error)`, which lists the packets of the segment's first stream.
  - `Config.AllRenditions bool`, `Config.Probe SegmentProbe`, `Config.OnSegment func(SegmentEvent)`.
  - `type SegmentEvent struct { At time.Time; Rendition string; Seq, DNum int64; AudioHole, AVSkew time.Duration; Paired bool; TimelineExcess float64 }`. It is called after each probed segment (time-local measurements for the soak).
  - `type CoverageWindow struct { Start, End time.Time; Segments map[string]int; Epochs, Paired map[string]map[int64]int; Cues, CuesTimed map[string]int }`. Window 0 starts at `Start`. `func (p *Player) Mark()` closes the current window and opens the next; Task 4 calls it at the end of **each** fault cluster.
  - New `Report` fields:
    - `Renditions []string` (primary first);
    - `TimelineExcess float64`;
    - `MaxAudioHole`, `AudioHoleBy map[string]time.Duration`;
    - `MaxAVStartSkew time.Duration`, `AVPairs int`;
    - `ProbedAudio, ProbedVideo int`;
    - `Consistency Consistency`;
    - `FetchErrors, ProbeErrors, PlaylistErrors int`;
    - `RenditionSegments, SegmentsSinceMark map[string]int`;
    - `Windows []CoverageWindow`;
    - `TailGap map[string]time.Duration`;
    - `Cues, CuesTimed, CueErrors int`.
- **A/V pairing is PDT overlap, the same as Task 2.** When an audio or video segment is probed, it is paired (`PairByPDT`, tolerance one segment) with the probed segments of every rendition of the other kind. The pair counts only when both segments carry the same discontinuity number; a different number is a Task 2 consistency failure. If the only candidate is a neighbour that doesn't overlap and the other rendition hasn't yet reached this segment's end, the pairing waits: the overlapping segment pairs when it is probed.
  - **Drain at the end of observation.** When `Run` returns, every probed segment that is still unpaired is compared once more, and only an overlapping segment pairs it then. Every pending caption comparison is retried too.
  - Skew is `|(ptsA − ptsV) − (pdtA − pdtV)|`, with the PTS difference unwrapped over 33 bits. On a correct timeline the PDT difference is exactly the PTS difference, so the skew is the A/V offset the viewer sees, whatever the segment durations.
  - Each pairing adds one to `Paired[rendition][dnum]` of the window **in which each segment was fetched**, once per segment. A window therefore shows paired coverage only for segments that arrived after the fault, and a rendition that stopped shows none.
- **Every fetch failure counts** in `FetchErrors = PlaylistErrors + SegmentErrors + ProbeErrors`:
  - `PlaylistErrors`: any failed playlist fetch, primary or secondary (transport error or non-200), except a secondary's 404 before it was first served (not written yet). A primary 404/410 is `SessionGone`, which every scenario checks separately.
  - `SegmentErrors`: any rendition (bad status, empty, not TS, not `WEBVTT`).
  - `ProbeErrors`: any failed or empty probe.
- **Progress through the end:** `TailGap[rendition]` is the time from that rendition's last new segment to the end of the run (or from `Start` if it delivered none). Task 4 and the soak (Task 13) bound it per rendition.
- **Captions:** `TYPE=SUBTITLES` renditions (`<rung>_vtt`) feed the consistency checks, and their segments must start `WEBVTT`. Each cue's start is mapped to PTS through `X-TIMESTAMP-MAP` (Bowtie writes `MPEGTS:126000,LOCAL:00:00:00.000`). It must lie within ±1 s of the PTS range of the rung's PDT-paired probed segment. `Cues` and `Windows[i].Cues` count cues. `CuesTimed` and `Windows[i].CuesTimed` count the cues checked against a video segment. `CueErrors` counts the cues outside the range, without a map, or never matched.
  - A caption segment whose video segment isn't probed yet waits in a pending list, bounded at 64 segments. The list is retried after every poll. When it is full, the overflow counts as `CueErrors`. Whatever is still pending at the drain counts as `CueErrors` too: an unmatched cue is a failure, not a skip. Synthetic fake sources carry no captions, so `Cues` is 0 there; Task 6's caption row runs on OTA clips.
- Timeline excess is Σ EXTINF of the primary rendition's segments that first appear after the first poll that listed any, minus the wall time between that poll and the poll that first listed the newest of them.
- Audio holes are measured within a segment, and from the end of the previous segment when the sequence numbers are consecutive and the segment carries no DISCONTINUITY. A rendition is audio when its name starts with `aac` or `ac3`.
- Memory: each rendition keeps its last 64 probed segments for pairing, so an hour's soak stays bounded.

- [ ] **Step 1: Write the failing tests**

Create `server/internal/testplayer/player_renditions_test.go`:

```go
package testplayer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestPlayerFollowsEveryRenditionAndProbes(t *testing.T) {
	var mu sync.Mutex
	polls := 0
	segRe := regexp.MustCompile(`/s/(v480|aac0)_(\d{5})\.ts$`)
	pdt := func(i int) string {
		return time.Date(2026, 10, 11, 20, 0, 4*i, 0, time.UTC).Format("2006-01-02T15:04:05.000Z07:00")
	}
	media := func(name string, n int) string {
		var b strings.Builder
		b.WriteString("#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n")
		for i := 0; i <= n; i++ {
			fmt.Fprintf(&b, "#EXT-X-PROGRAM-DATE-TIME:%s\n#EXTINF:4.000,\n/s/%s_%05d.ts\n", pdt(i), name, i)
		}
		return b.String()
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			fmt.Fprint(w, `{"viewerId":"v1","playlistUrl":"/s/index.m3u8?token=tk"}`)
		case r.URL.Path == "/s/index.m3u8":
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"aac\",NAME=\"English\",URI=\"aac0.m3u8?token=tk\"\n"+
				"#EXT-X-STREAM-INF:BANDWIDTH=1,AUDIO=\"aac\"\nv480.m3u8?token=tk\n")
		case r.URL.Path == "/s/v480.m3u8" || r.URL.Path == "/s/aac0.m3u8":
			mu.Lock()
			if r.URL.Path == "/s/v480.m3u8" && polls < 4 {
				polls++
			}
			n := polls
			mu.Unlock()
			fmt.Fprint(w, media(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/s/"), ".m3u8"), n))
		case segRe.MatchString(r.URL.Path):
			_, _ = w.Write(append([]byte{0x47}, r.URL.Path...))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	probe := func(_ context.Context, seg []byte) ([]Packet, error) {
		m := segRe.FindStringSubmatch(string(seg[1:]))
		if m == nil {
			return nil, fmt.Errorf("unexpected segment %q", seg)
		}
		seq, _ := strconv.Atoi(m[2])
		start := float64(seq) * 4
		if m[1] == "aac0" && seq == 2 {
			start += 0.2 // a 200 ms hole before audio segment 2
		}
		var pk []Packet
		for ts := start; ts < float64(seq+1)*4-1e-9; ts += 0.5 {
			pk = append(pk, Packet{PTS: ts, Dur: 0.5})
		}
		return pk, nil
	}

	p, err := Start(context.Background(), Config{
		BaseURL: srv.URL, Token: "t", ChannelID: 1, PollEvery: 20 * time.Millisecond,
		AllRenditions: true, Probe: probe,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	p.Run(ctx)
	r := p.Report()

	if got := strings.Join(r.Renditions, ","); got != "v480,aac0" {
		t.Fatalf("renditions %q, want v480,aac0", got)
	}
	if r.ProbedAudio != 5 || r.ProbedVideo != 5 {
		t.Fatalf("probed audio=%d video=%d, want 5 and 5", r.ProbedAudio, r.ProbedVideo)
	}
	near := func(got, want time.Duration) bool {
		d := got - want
		return d > -time.Millisecond && d < time.Millisecond
	}
	if !near(r.MaxAudioHole, 200*time.Millisecond) {
		t.Fatalf("MaxAudioHole=%v, want 200ms", r.MaxAudioHole)
	}
	if !near(r.MaxAVStartSkew, 200*time.Millisecond) || r.AVPairs != 5 {
		t.Fatalf("MaxAVStartSkew=%v over %d pairs, want 200ms over 5", r.MaxAVStartSkew, r.AVPairs)
	}
	if got := r.Windows[0].Paired; got["v480"][0] != 5 || got["aac0"][0] != 5 {
		t.Fatalf("paired coverage %v, want 5 per rendition in discontinuity 0", got)
	}
	c := r.Consistency
	if !c.PDTMonotonic || !c.PDTAligned || !c.RenditionsMonotonic || !c.DiscontinuitySequenceOK || !c.DiscontinuitiesAligned {
		t.Fatalf("consistency %+v", c)
	}
	if r.TimelineExcess <= 0 {
		t.Fatalf("TimelineExcess=%v: 16 s of segments appeared in well under a second", r.TimelineExcess)
	}
	if len(r.Errors) != 0 || r.FetchErrors != 0 {
		t.Fatalf("errors %v (FetchErrors %d)", r.Errors, r.FetchErrors)
	}
}

func vttTime(sec float64) string {
	ms := int64(sec*1000 + 0.5)
	return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

// After a kill the audio numbering runs one ahead of the video; pairing by
// PDT still compares the same moment. Coverage, errors and cues are counted
// per window.
func TestPlayerPairsAVByPDTAndCountsPerWindow(t *testing.T) {
	var mu sync.Mutex
	polls := 0
	segRe := regexp.MustCompile(`/s/(v480|aac0)_(\d{5})\.ts$|/s/v480_vtt_(\d+)\.vtt$`)
	restart := map[string]int{"v480": 2, "aac0": 3, "v480_vtt": 2}
	// Video: 0,1 then a restart at 2. Audio: 0,1,2 then a restart at 3. The
	// restart is the same moment, 68 s, in both.
	pdtOf := func(name string, i int) float64 {
		if i >= restart[name] {
			return 68 + 4*float64(i-restart[name])
		}
		return 4 * float64(i)
	}
	// Both renditions' PTS restart at 100 s.
	ptsOf := func(name string, i int) float64 {
		if i >= restart[name] {
			return 100 + 4*float64(i-restart[name])
		}
		return 4 * float64(i)
	}
	media := func(name string, n int) string {
		var b strings.Builder
		b.WriteString("#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-DISCONTINUITY-SEQUENCE:0\n")
		for i := 0; i <= n+restart[name]-2; i++ {
			if i == restart[name] {
				b.WriteString("#EXT-X-DISCONTINUITY\n")
			}
			at := time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC).Add(time.Duration(pdtOf(name, i) * float64(time.Second)))
			fmt.Fprintf(&b, "#EXT-X-PROGRAM-DATE-TIME:%s\n#EXTINF:4.000,\n", at.Format("2006-01-02T15:04:05.000Z07:00"))
			if name == "v480_vtt" {
				fmt.Fprintf(&b, "/s/v480_vtt_%d.vtt\n", i)
			} else {
				fmt.Fprintf(&b, "/s/%s_%05d.ts\n", name, i)
			}
		}
		return b.String()
	}
	gone := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/sessions":
			fmt.Fprint(w, `{"viewerId":"v1","playlistUrl":"/s/index.m3u8?token=tk"}`)
		case r.URL.Path == "/s/index.m3u8":
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID=\"aac\",NAME=\"English\",URI=\"aac0.m3u8?token=tk\"\n"+
				"#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"cc\",NAME=\"CC\",URI=\"v480_vtt.m3u8?token=tk\"\n"+
				"#EXT-X-STREAM-INF:BANDWIDTH=1,AUDIO=\"aac\",SUBTITLES=\"cc\"\nv480.m3u8?token=tk\n")
		case r.URL.Path == "/s/v480.m3u8":
			if polls < 4 {
				polls++
			}
			fmt.Fprint(w, media("v480", polls))
		case r.URL.Path == "/s/aac0.m3u8":
			if gone {
				http.NotFound(w, r) // went missing after it was served: an error
				return
			}
			fmt.Fprint(w, media("aac0", polls))
		case r.URL.Path == "/s/v480_vtt.m3u8":
			fmt.Fprint(w, media("v480_vtt", polls))
		case strings.HasSuffix(r.URL.Path, ".vtt"):
			i, _ := strconv.Atoi(segRe.FindStringSubmatch(r.URL.Path)[3])
			// One cue half a second into the segment, on FFmpeg's clock
			// (Bowtie maps local 0 to PTS 1.4 s). Segment 3's cue is 10 s off.
			cue := ptsOf("v480_vtt", i) + 0.5 - 1.4
			if i == 3 {
				cue += 10
			}
			fmt.Fprintf(w, "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:126000,LOCAL:00:00:00.000\n\n%s --> %s\nhello\n\n", vttTime(cue), vttTime(cue+1))
		case segRe.MatchString(r.URL.Path):
			_, _ = w.Write(append([]byte{0x47}, r.URL.Path...))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	probe := func(_ context.Context, seg []byte) ([]Packet, error) {
		m := segRe.FindStringSubmatch(string(seg[1:]))
		seq, _ := strconv.Atoi(m[2])
		return []Packet{{PTS: ptsOf(m[1], seq), Dur: 4}}, nil
	}
	var events []SegmentEvent
	p, err := Start(context.Background(), Config{
		BaseURL: srv.URL, Token: "t", ChannelID: 1, PollEvery: 20 * time.Millisecond,
		AllRenditions: true, Probe: probe,
		OnSegment: func(e SegmentEvent) { events = append(events, e) }, // Run goroutine only
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	p.Run(ctx)
	cancel()
	p.Mark()
	mu.Lock()
	polls, gone = 6, true // two more segments in video and captions; audio disappears
	mu.Unlock()
	ctx, cancel = context.WithTimeout(context.Background(), 300*time.Millisecond)
	p.Run(ctx)
	cancel()
	r := p.Report()

	if got := strings.Join(r.Renditions, ","); got != "v480,aac0,v480_vtt" {
		t.Fatalf("renditions %q", got)
	}
	if r.MaxAVStartSkew > time.Millisecond || r.AVPairs == 0 {
		t.Fatalf("MaxAVStartSkew=%v over %d pairs, want 0 over some (paired by PDT)", r.MaxAVStartSkew, r.AVPairs)
	}
	if len(r.Windows) != 2 {
		t.Fatalf("%d windows, want 2", len(r.Windows))
	}
	w0, w1 := r.Windows[0], r.Windows[1]
	if w0.Paired["aac0"][1] == 0 || w0.Paired["v480"][1] == 0 {
		t.Fatalf("window 0 paired %v, want both renditions paired in discontinuity 1", w0.Paired)
	}
	if w1.Segments["v480"] != 2 || w1.Segments["v480_vtt"] != 2 || w1.Segments["aac0"] != 0 || len(w1.Paired["v480"]) != 0 {
		t.Fatalf("window 1 segments %v paired %v: video alone must not count as paired", w1.Segments, w1.Paired)
	}
	if r.SegmentsSinceMark["v480"] != 2 {
		t.Fatalf("since mark %v", r.SegmentsSinceMark)
	}
	if r.PlaylistErrors == 0 || r.FetchErrors < r.PlaylistErrors {
		t.Fatalf("a rendition that went missing was not counted: playlist %d fetch %d", r.PlaylistErrors, r.FetchErrors)
	}
	if r.TailGap["aac0"]-r.TailGap["v480"] < 150*time.Millisecond {
		t.Fatalf("tail gaps %v: audio stopped, video did not", r.TailGap)
	}
	if r.Cues != 6 || r.CuesTimed != 6 || r.CueErrors != 1 {
		t.Fatalf("cues %d timed %d errors %d, want 6, 6 and 1 (segment 3)", r.Cues, r.CuesTimed, r.CueErrors)
	}
	if w0.Cues["v480_vtt"] != 4 || w1.Cues["v480_vtt"] != 2 {
		t.Fatalf("cues per window %v / %v", w0.Cues, w1.Cues)
	}
	if len(events) == 0 || events[0].Rendition == "" {
		t.Fatalf("events %+v", events)
	}
}

// Cues that arrive before their video segment wait for it; cues that never
// find one, and comparisons still waiting, are settled when observation ends.
func TestPlayerDrainsWaitingComparisons(t *testing.T) {
	t0 := time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC)
	now := time.Now()
	v, a, cc := newRendition("v480", "", now), newRendition("aac0", "", now), newRendition("v480_vtt", "", now)
	p := &Player{primary: v, others: []*rendition{a, cc}, windows: []*CoverageWindow{newWindow(now)}}
	seg := func(i int) SegmentObs {
		return SegmentObs{Seq: int64(i), Duration: 4, PDT: t0.Add(time.Duration(4*i) * time.Second)}
	}
	vtt := func(cue float64) []byte {
		return []byte(fmt.Sprintf("WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:126000,LOCAL:00:00:00.000\n\n%s --> %s\nhi\n", vttTime(cue), vttTime(cue+1)))
	}
	// Caption segment 0 arrives before video segment 0 is probed: it waits.
	p.cues(cc, seg(0), 0, vtt(100.5-1.4))
	if r := p.Report(); r.Cues != 1 || r.CuesTimed != 0 || r.CueErrors != 0 {
		t.Fatalf("before the video: cues %d timed %d errors %d", r.Cues, r.CuesTimed, r.CueErrors)
	}
	p.mu.Lock()
	v.addProbed(&probed{seg: PDTSeg{Start: seg(0).PDT, Dur: 4}, pts: 100, end: 104})
	p.retryCuesLocked()
	p.mu.Unlock()
	if r := p.Report(); r.CuesTimed != 1 || r.Windows[0].CuesTimed["v480_vtt"] != 1 || r.CueErrors != 0 {
		t.Fatalf("after the video: timed %d (window %v) errors %d", r.CuesTimed, r.Windows[0].CuesTimed, r.CueErrors)
	}
	// Caption segment 5 never gets a video segment: unmatched at the end.
	p.cues(cc, seg(5), 0, vtt(120.5-1.4))
	// Audio segment 0 was probed but its pairing never ran: drain pairs it.
	p.mu.Lock()
	a.addProbed(&probed{seg: PDTSeg{Start: seg(0).PDT, Dur: 4}, pts: 100.03, end: 104})
	p.mu.Unlock()
	p.drain()
	r := p.Report()
	if r.CueErrors != 1 || !strings.Contains(strings.Join(r.Errors, "\n"), "no probed v480 segment") {
		t.Fatalf("unmatched cue: errors %d %q", r.CueErrors, r.Errors)
	}
	if r.AVPairs != 1 || r.Windows[0].Paired["aac0"][0] != 1 || r.MaxAVStartSkew != 30*time.Millisecond {
		t.Fatalf("drain pairing: pairs %d paired %v skew %v", r.AVPairs, r.Windows[0].Paired, r.MaxAVStartSkew)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd server && go test ./internal/testplayer/ -run 'TestPlayerFollowsEveryRenditionAndProbes|TestPlayerPairsAVByPDTAndCountsPerWindow|TestPlayerDrainsWaitingComparisons' -v`
Expected: FAIL to compile with `unknown field AllRenditions` / `undefined: Packet`.

- [ ] **Step 3: Implement**

Replace `server/internal/testplayer/player.go` with the following. Everything the existing `player_test.go` tests rely on is unchanged:
- `Start`, `Run`, `Stop`, `heartbeat`, `get`, `resolveRef`;
- the counters `Polls`, `SegmentsFetched`, `SegmentErrors`, `BackwardJumps`, `Discontinuities`, `Newest`, `MaxGap`, `SessionGone`, `EndList`.

The primary playlist is now parsed by `ParseSnapshot`, so `entry`, `parsePlaylist` and `firstURI` are gone; nothing else used them.

```go
// Package testplayer is a virtual HLS client for Bowtie: it creates a session,
// polls the playlist and downloads new segments like a player would, and
// reports what a viewer would have experienced (stalls, sequence resets,
// discontinuities, a vanished session).
package testplayer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config configures a Player.
type Config struct {
	BaseURL   string // e.g. http://127.0.0.1:1234
	Token     string // bearer JWT
	ChannelID int64
	// Caps is the POST /sessions caps object; default h264/aac at ≤480p.
	Caps map[string]any
	// PollEvery is the playlist poll interval; default 1s.
	PollEvery time.Duration
	// HeartbeatEvery is the session heartbeat interval; default 15s.
	HeartbeatEvery time.Duration
	Client         *http.Client
	// AllRenditions follows every rendition the master lists (all variants,
	// audio and subtitle renditions), not only the first variant.
	AllRenditions bool
	// Probe, when set, runs on every new audio and video segment.
	Probe SegmentProbe
	// OnSegment, when set, is called (on the Run goroutine) after each
	// probed segment with time-local measurements.
	OnSegment func(SegmentEvent)
}

// Packet is one demuxed packet of a probed segment, in seconds.
type Packet struct{ PTS, Dur float64 }

// SegmentProbe lists the packets of the first stream in a TS segment.
type SegmentProbe func(ctx context.Context, seg []byte) ([]Packet, error)

// SegmentEvent is one probed segment, for time-local analysis (the soak).
type SegmentEvent struct {
	At             time.Time
	Rendition      string
	Seq            int64
	DNum           int64
	AudioHole      time.Duration // largest hole in/before this audio segment
	AVSkew         time.Duration // skew against the PDT-paired segment of the other kind
	Paired         bool          // a segment of the other kind was paired with this one
	TimelineExcess float64       // the primary's running excess at this moment
}

// CoverageWindow is what each rendition delivered between two Marks. Window
// 0 starts at Start; each Mark (a fault's end) opens the next.
type CoverageWindow struct {
	Start, End time.Time
	// Segments counts segments fetched OK per rendition.
	Segments map[string]int
	// Epochs counts them per rendition and discontinuity number.
	Epochs map[string]map[int64]int
	// Paired counts, per audio/video rendition and discontinuity number,
	// this window's probed segments that were PDT-paired with a probed
	// segment of the other kind (so their A/V skew was measured).
	Paired map[string]map[int64]int
	// Cues counts WebVTT cues per subtitle rendition; CuesTimed those
	// timed against their video segment (by the window the cue's segment
	// was fetched in).
	Cues, CuesTimed map[string]int
}

// StartError is returned by Start when session creation is refused.
type StartError struct {
	Status int
	Body   string
}

func (e *StartError) Error() string {
	return fmt.Sprintf("start session: HTTP %d: %s", e.Status, e.Body)
}

// Report is the viewer experience of one run.
type Report struct {
	Polls           int
	SegmentsFetched int
	SegmentErrors   int
	BackwardJumps   int     // newest segment number went down between polls
	Discontinuities int     // #EXT-X-DISCONTINUITY on newly seen segments
	Newest          []int64 // newest absolute segment number at each poll
	// MaxGap is the longest stretch without a new segment, including the
	// stretch from the last new segment to the end of the run.
	MaxGap      time.Duration
	SessionGone bool
	// EndList: a polled playlist contained #EXT-X-ENDLIST, which tells a real
	// player the stream is over (it stops reloading).
	EndList bool
	Errors  []string

	// Renditions are the media playlists followed, primary first.
	Renditions []string
	// TimelineExcess is Σ EXTINF of the primary rendition's segments that
	// appeared after the first poll, minus the wall time they took to
	// appear (seconds). ≈0 on a clean stream; negative by the length of an
	// outage the transcoder skipped.
	TimelineExcess float64
	// MaxAudioHole is the largest gap between consecutive audio packets;
	// AudioHoleBy is the same per audio rendition (aac0, ac30, …).
	MaxAudioHole time.Duration
	AudioHoleBy  map[string]time.Duration
	// MaxAVStartSkew is the largest |(ptsA − ptsV) − (pdtA − pdtV)| over
	// audio and video segments paired by PDT overlap (PairByPDT) with the
	// same discontinuity number. AVPairs counts those comparisons.
	MaxAVStartSkew           time.Duration
	AVPairs                  int
	ProbedAudio, ProbedVideo int
	// Consistency checks every polled media playlist (CheckConsistency).
	Consistency Consistency
	// FetchErrors counts every failed fetch: PlaylistErrors (any rendition,
	// except a secondary's 404 before it was first served), SegmentErrors
	// (any rendition) and ProbeErrors.
	FetchErrors                 int
	ProbeErrors, PlaylistErrors int
	// RenditionSegments counts segments fetched OK per rendition;
	// SegmentsSinceMark is the last window's Segments.
	RenditionSegments, SegmentsSinceMark map[string]int
	// Windows are the coverage windows, oldest first (see Mark).
	Windows []CoverageWindow
	// TailGap is, per rendition, the time from its last new segment to the
	// end of the run (from Start when it delivered none).
	TailGap map[string]time.Duration
	// Cues is the number of WebVTT cues fetched; CuesTimed the cues whose
	// time was checked against the PDT-paired video segment's PTS range
	// (±1 s); CueErrors those outside it or without X-TIMESTAMP-MAP.
	Cues, CuesTimed, CueErrors int
}

// Player is one virtual viewer.
type Player struct {
	cfg         Config
	viewerID    string
	playlistURL string
	streamToken string

	mu       sync.Mutex
	report   Report
	highest  int64
	lastNew  time.Time
	stopped  time.Time
	prevNews int64

	started time.Time
	primary *rendition
	others  []*rendition
	snaps   []Snapshot
	windows []*CoverageWindow
	// pendingCues wait for the video segment they are timed against.
	pendingCues []pendingCue

	excessStarted bool
	excessBase    int64 // newest segment at the first poll that listed any
	excessFirst   time.Time
	excessLast    time.Time
	excessSum     float64
}

// Start creates a session for cfg.ChannelID.
func Start(ctx context.Context, cfg Config) (*Player, error) {
	if cfg.Caps == nil {
		cfg.Caps = map[string]any{"videoCodecs": []string{"h264"}, "audioCodecs": []string{"aac"}, "maxHeight": 480}
	}
	if cfg.PollEvery <= 0 {
		cfg.PollEvery = time.Second
	}
	if cfg.HeartbeatEvery <= 0 {
		cfg.HeartbeatEvery = 15 * time.Second
	}
	if cfg.Client == nil {
		cfg.Client = &http.Client{Timeout: 30 * time.Second}
	}
	body, _ := json.Marshal(map[string]any{"channelId": cfg.ChannelID, "caps": cfg.Caps})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL+"/api/v1/sessions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	resp, err := cfg.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, &StartError{Status: resp.StatusCode, Body: string(raw)}
	}
	var out struct {
		ViewerID    string `json:"viewerId"`
		PlaylistURL string `json:"playlistUrl"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode session: %w", err)
	}
	u, err := url.Parse(out.PlaylistURL)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	return &Player{
		cfg:         cfg,
		viewerID:    out.ViewerID,
		playlistURL: out.PlaylistURL,
		streamToken: u.Query().Get("token"),
		highest:     -1,
		prevNews:    -1,
		lastNew:     now,
		started:     now,
		primary:     newRendition(renditionName(out.PlaylistURL), out.PlaylistURL, now),
		windows:     []*CoverageWindow{newWindow(now)},
	}, nil
}

// ViewerID returns the session's viewer id.
func (p *Player) ViewerID() string { return p.viewerID }

// Run polls until ctx is done or the session disappears.
func (p *Player) Run(ctx context.Context) {
	defer func() {
		p.drain()
		p.mu.Lock()
		p.stopped = time.Now()
		p.mu.Unlock()
	}()
	poll := time.NewTicker(p.cfg.PollEvery)
	defer poll.Stop()
	hb := time.NewTicker(p.cfg.HeartbeatEvery)
	defer hb.Stop()
	if p.poll(ctx) {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-hb.C:
			p.heartbeat(ctx)
		case <-poll.C:
			if p.poll(ctx) {
				return
			}
		}
	}
}

// Stop deletes the session (best effort).
func (p *Player) Stop(ctx context.Context) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, p.cfg.BaseURL+"/api/v1/sessions/"+p.viewerID, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+p.cfg.Token)
	if resp, err := p.cfg.Client.Do(req); err == nil {
		_ = resp.Body.Close()
	}
}

// Report returns the run so far.
func (p *Player) Report() Report {
	p.mu.Lock()
	defer p.mu.Unlock()
	r := p.report
	r.Newest = append([]int64(nil), r.Newest...)
	r.Errors = append([]string(nil), r.Errors...)
	end := p.stopped
	if end.IsZero() {
		end = time.Now()
	}
	if tail := end.Sub(p.lastNew); tail > r.MaxGap {
		r.MaxGap = tail
	}
	all := append([]*rendition{p.primary}, p.others...)
	r.RenditionSegments = map[string]int{}
	r.TailGap = map[string]time.Duration{}
	for _, o := range all {
		r.Renditions = append(r.Renditions, o.name)
		r.RenditionSegments[o.name] = o.fetched
		r.TailGap[o.name] = end.Sub(o.lastOK)
	}
	for i, w := range p.windows {
		c := CoverageWindow{Start: w.Start, End: end, Segments: map[string]int{},
			Epochs: map[string]map[int64]int{}, Paired: map[string]map[int64]int{}, Cues: map[string]int{}, CuesTimed: map[string]int{}}
		if i+1 < len(p.windows) {
			c.End = p.windows[i+1].Start
		}
		for _, o := range all {
			c.Segments[o.name] = w.Segments[o.name]
		}
		for k, m := range w.Epochs {
			c.Epochs[k] = copyCounts(m)
		}
		for k, m := range w.Paired {
			c.Paired[k] = copyCounts(m)
		}
		for k, n := range w.Cues {
			c.Cues[k] = n
		}
		for k, n := range w.CuesTimed {
			c.CuesTimed[k] = n
		}
		r.Windows = append(r.Windows, c)
	}
	r.SegmentsSinceMark = r.Windows[len(r.Windows)-1].Segments
	if r.AudioHoleBy != nil {
		m := map[string]time.Duration{}
		for k, v := range r.AudioHoleBy {
			m[k] = v
		}
		r.AudioHoleBy = m
	}
	if !p.excessLast.IsZero() {
		r.TimelineExcess = p.excessSum - p.excessLast.Sub(p.excessFirst).Seconds()
	}
	r.FetchErrors = r.PlaylistErrors + r.SegmentErrors + r.ProbeErrors
	r.Consistency = CheckConsistency(p.snaps)
	return r
}

// Mark closes the current coverage window and opens the next. Scenarios
// call it at the end of each fault, so each fault's aftermath is checked
// on its own.
func (p *Player) Mark() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.windows = append(p.windows, newWindow(time.Now()))
}

func newWindow(at time.Time) *CoverageWindow {
	return &CoverageWindow{Start: at, Segments: map[string]int{}, Epochs: map[string]map[int64]int{},
		Paired: map[string]map[int64]int{}, Cues: map[string]int{}, CuesTimed: map[string]int{}}
}

func copyCounts(m map[int64]int) map[int64]int {
	out := make(map[int64]int, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func bump(m map[string]map[int64]int, name string, dnum int64) {
	if m[name] == nil {
		m[name] = map[int64]int{}
	}
	m[name][dnum]++
}

// fetchedLocked records a segment fetched OK in rendition o.
func (p *Player) fetchedLocked(o *rendition, g SegmentObs, at time.Time) int {
	o.fetched++
	o.lastOK = at
	w := p.windows[len(p.windows)-1]
	w.Segments[o.name]++
	bump(w.Epochs, o.name, g.DNum)
	return len(p.windows) - 1
}

func (p *Player) errorf(format string, a ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.report.Errors = append(p.report.Errors, fmt.Sprintf(format, a...))
}

func (p *Player) heartbeat(ctx context.Context) {
	u := p.cfg.BaseURL + "/api/v1/sessions/" + p.viewerID + "/heartbeat?token=" + url.QueryEscape(p.streamToken)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return
	}
	resp, err := p.cfg.Client.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			p.errorf("heartbeat: %v", err)
		}
		return
	}
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		p.errorf("heartbeat: HTTP %d", resp.StatusCode)
	}
}

// poll fetches the primary playlist, its new segments and (AllRenditions)
// every other rendition; it returns true when the session is gone.
func (p *Player) poll(ctx context.Context) bool {
	pollAt := time.Now()
	body, status, err := p.get(ctx, p.playlistURL)
	p.mu.Lock()
	p.report.Polls++
	p.mu.Unlock()
	if err != nil {
		if ctx.Err() == nil {
			p.playlistErrorf("playlist: %v", err)
		}
		return false
	}
	if status == http.StatusNotFound || status == http.StatusGone {
		p.mu.Lock()
		p.report.SessionGone = true
		p.mu.Unlock()
		return true
	}
	if status != http.StatusOK {
		p.playlistErrorf("playlist: HTTP %d", status)
		return false
	}
	if bytes.Contains(body, []byte("#EXT-X-STREAM-INF")) {
		// Master playlist: follow the first variant, as a real player does,
		// and (AllRenditions) every other rendition too.
		refs := renditionRefs(body)
		if len(refs) > 0 {
			p.mu.Lock()
			base := p.playlistURL
			p.playlistURL = resolveRef(base, refs[0])
			p.primary.name, p.primary.url = renditionName(refs[0]), p.playlistURL
			if p.cfg.AllRenditions && p.others == nil {
				seen := map[string]bool{p.primary.name: true}
				for _, ref := range refs[1:] {
					if n := renditionName(ref); !seen[n] {
						seen[n] = true
						p.others = append(p.others, newRendition(n, resolveRef(base, ref), p.started))
					}
				}
			}
			p.mu.Unlock()
		}
		return false
	}
	if bytes.Contains(body, []byte("#EXT-X-ENDLIST")) {
		p.mu.Lock()
		p.report.EndList = true
		p.mu.Unlock()
	}
	snap := ParseSnapshot(p.primary.name, body, pollAt)
	p.mu.Lock()
	p.snaps = append(p.snaps, snap)
	others := append([]*rendition(nil), p.others...)
	if n := len(snap.Segments); n > 0 {
		newest := snap.Segments[n-1].Seq
		p.report.Newest = append(p.report.Newest, newest)
		if p.prevNews >= 0 && newest < p.prevNews {
			p.report.BackwardJumps++
		}
		p.prevNews = newest
		if !p.excessStarted {
			p.excessStarted, p.excessBase, p.excessFirst = true, newest, pollAt
		}
	}
	p.mu.Unlock()

	for _, g := range snap.Segments {
		p.mu.Lock()
		isNew := g.Seq > p.highest
		p.mu.Unlock()
		if !isNew {
			continue
		}
		seg, segStatus, err := p.get(ctx, g.URI)
		ok := err == nil && segStatus == http.StatusOK && len(seg) > 0 && seg[0] == 0x47
		now := time.Now()
		p.mu.Lock()
		win := -1
		if ok {
			p.report.SegmentsFetched++
			win = p.fetchedLocked(p.primary, g, now)
		} else {
			p.report.SegmentErrors++
		}
		if g.Discontinuity {
			p.report.Discontinuities++
		}
		if gap := now.Sub(p.lastNew); gap > p.report.MaxGap {
			p.report.MaxGap = gap
		}
		p.lastNew = now
		p.highest = g.Seq
		if g.Seq > p.excessBase {
			p.excessSum += g.Duration
			p.excessLast = pollAt
		}
		p.mu.Unlock()
		if ok {
			p.probe(ctx, p.primary, g, win, seg)
		}
	}
	for _, r := range others {
		p.pollRendition(ctx, r)
	}
	p.mu.Lock()
	p.retryCuesLocked()
	p.mu.Unlock()
	return false
}

// playlistErrorf records a failed playlist fetch.
func (p *Player) playlistErrorf(format string, a ...any) {
	p.mu.Lock()
	p.report.PlaylistErrors++
	p.mu.Unlock()
	p.errorf(format, a...)
}

// pollRendition polls a non-primary rendition: snapshot, new segments,
// probes and cues. Only the Run goroutine touches r.highest.
func (p *Player) pollRendition(ctx context.Context, r *rendition) {
	body, status, err := p.get(ctx, r.url)
	switch {
	case err != nil:
		if ctx.Err() == nil {
			p.playlistErrorf("%s playlist: %v", r.name, err)
		}
		return
	case status == http.StatusNotFound && !r.everOK:
		return // not written yet
	case status != http.StatusOK:
		p.playlistErrorf("%s playlist: HTTP %d", r.name, status)
		return
	}
	r.everOK = true
	s := ParseSnapshot(r.name, body, time.Now())
	p.mu.Lock()
	p.snaps = append(p.snaps, s)
	p.mu.Unlock()
	for _, g := range s.Segments {
		if g.Seq <= r.highest {
			continue
		}
		r.highest = g.Seq
		seg, st, err := p.get(ctx, g.URI)
		sub := isSubtitleRendition(r.name)
		valid := len(seg) > 0 && seg[0] == 0x47
		if sub {
			valid = bytes.HasPrefix(seg, []byte("WEBVTT"))
		}
		if err != nil || st != http.StatusOK || !valid {
			p.mu.Lock()
			p.report.SegmentErrors++
			p.mu.Unlock()
			if ctx.Err() == nil {
				p.errorf("%s segment %d: HTTP %d, %v", r.name, g.Seq, st, err)
			}
			continue
		}
		p.mu.Lock()
		win := p.fetchedLocked(r, g, time.Now())
		p.mu.Unlock()
		if sub {
			p.cues(r, g, win, seg)
		} else {
			p.probe(ctx, r, g, win, seg)
		}
	}
}

// probe measures audio holes and the A/V skew of one fetched segment of
// rendition r, fetched in coverage window win.
func (p *Player) probe(ctx context.Context, r *rendition, g SegmentObs, win int, seg []byte) {
	if p.cfg.Probe == nil {
		return
	}
	pk, err := p.cfg.Probe(ctx, seg)
	if err != nil || len(pk) == 0 {
		if ctx.Err() == nil {
			p.mu.Lock()
			p.report.ProbeErrors++
			p.mu.Unlock()
			p.errorf("probe %s segment %d: %v (%d packets)", r.name, g.Seq, err, len(pk))
		}
		return
	}
	ev := SegmentEvent{At: time.Now(), Rendition: r.name, Seq: g.Seq, DNum: g.DNum}
	defer func() {
		if p.cfg.OnSegment != nil {
			p.cfg.OnSegment(ev)
		}
	}()
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.excessLast.IsZero() {
		ev.TimelineExcess = p.excessSum - p.excessLast.Sub(p.excessFirst).Seconds()
	}
	audio := isAudioRendition(r.name)
	if audio {
		p.report.ProbedAudio++
		hole := time.Duration(0)
		if r.lastSeq == g.Seq-1 && !g.Discontinuity {
			hole = holeOf(pk[0].PTS - r.lastEnd)
		}
		for i := 1; i < len(pk); i++ {
			if h := holeOf(pk[i].PTS - (pk[i-1].PTS + pk[i-1].Dur)); h > hole {
				hole = h
			}
		}
		ev.AudioHole = hole
		if hole > p.report.MaxAudioHole {
			p.report.MaxAudioHole = hole
		}
		if p.report.AudioHoleBy == nil {
			p.report.AudioHoleBy = map[string]time.Duration{}
		}
		if hole > p.report.AudioHoleBy[r.name] {
			p.report.AudioHoleBy[r.name] = hole
		}
		last := pk[len(pk)-1]
		r.lastSeq, r.lastEnd = g.Seq, last.PTS+last.Dur
	} else {
		p.report.ProbedVideo++
	}
	last := pk[len(pk)-1]
	rec := &probed{seg: PDTSeg{Start: g.PDT, Dur: g.Duration, DNum: g.DNum}, pts: pk[0].PTS, end: last.PTS + last.Dur, win: win}
	if g.PDT.IsZero() {
		return // no wall-clock place: cannot be paired (Consistency flags it)
	}
	r.addProbed(rec)
	ev.Paired, ev.AVSkew = p.pairLocked(r, rec, false)
}

// pairLocked pairs rec (of rendition r) with the PDT-overlapping probed
// segment of every rendition of the other kind and measures the skew.
// A non-overlapping neighbour (within one segment) is accepted only once
// the other rendition has passed rec's end; before that, the overlapping
// segment may not be probed yet and pairs with rec when it is. final (the
// end of observation) retries with overlapping segments only: a rendition
// that stopped leaves its last moments unpaired, which coverage reports.
func (p *Player) pairLocked(r *rendition, rec *probed, final bool) (bool, time.Duration) {
	paired, max := false, time.Duration(0)
	audio := isAudioRendition(r.name)
	for _, o := range append([]*rendition{p.primary}, p.others...) {
		if o == r || isSubtitleRendition(o.name) || isAudioRendition(o.name) == audio || len(o.probed) == 0 {
			continue
		}
		k, ok := PairByPDT(rec.seg, o.pdtSegs(), durOf(rec.seg.Dur))
		if !ok || o.probed[k].seg.DNum != rec.seg.DNum {
			continue // unpaired, or a numbering mismatch Consistency reports
		}
		other := o.probed[k]
		if !overlaps(rec.seg, other.seg) && (final || o.probed[len(o.probed)-1].seg.End().Before(rec.seg.End())) {
			continue
		}
		s := absSeconds(ptsDiff(rec.pts, other.pts) - rec.seg.Start.Sub(other.seg.Start).Seconds())
		p.report.AVPairs++
		paired = true
		if s > max {
			max = s
		}
		if s > p.report.MaxAVStartSkew {
			p.report.MaxAVStartSkew = s
		}
		p.pairedLocked(r, rec)
		p.pairedLocked(o, other)
	}
	return paired, max
}

// drain finishes the comparisons still waiting at the end of observation:
// every unpaired probed segment is retried against everything probed, and
// cues still waiting for their video segment are timed or counted as
// unmatched (CueErrors). Run calls it on return.
func (p *Player) drain() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, r := range append([]*rendition{p.primary}, p.others...) {
		for _, rec := range r.probed {
			if !rec.paired {
				p.pairLocked(r, rec, true)
			}
		}
	}
	waiting := p.pendingCues
	p.pendingCues = nil
	for _, c := range waiting {
		if !p.timeCuesLocked(c) {
			p.report.CueErrors += len(c.starts)
			p.cueErrorf("%s segment %d: %d cue(s) with no probed %s segment to time them against", c.r.name, c.g.Seq, len(c.starts), strings.TrimSuffix(c.r.name, "_vtt"))
		}
	}
}

// pairedLocked counts rec once in the Paired coverage of the window it was
// fetched in.
func (p *Player) pairedLocked(r *rendition, rec *probed) {
	if rec.paired {
		return
	}
	rec.paired = true
	bump(p.windows[rec.win].Paired, r.name, rec.seg.DNum)
}

var (
	cueRe    = regexp.MustCompile(`^((?:\d+:)?\d{2}:\d{2}\.\d{3})\s+-->`)
	tsMapRe  = regexp.MustCompile(`X-TIMESTAMP-MAP=.*`)
	mpegtsRe = regexp.MustCompile(`MPEGTS:(\d+)`)
	localRe  = regexp.MustCompile(`LOCAL:((?:\d+:)?\d{2}:\d{2}\.\d{3})`)
)

// cueTolerance is how far a cue may start outside its video segment.
const cueTolerance = 1.0

// maxPendingCues bounds the WebVTT segments waiting for their video segment.
const maxPendingCues = 64

// pendingCue is a WebVTT segment's cues, waiting to be timed.
type pendingCue struct {
	r             *rendition
	g             SegmentObs
	win           int
	starts        []float64
	mpegts, local float64
}

// cues counts a WebVTT segment's cues and checks each cue's start, mapped
// to PTS through X-TIMESTAMP-MAP, against the PTS range of the probed rung
// segment it PDT-pairs with. When that segment isn't probed yet, the cues
// wait (retried after every poll; drain counts what never matches).
func (p *Player) cues(r *rendition, g SegmentObs, win int, body []byte) {
	c := pendingCue{r: r, g: g, win: win}
	mapped := false
	sc := bufio.NewScanner(bytes.NewReader(body))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if m := tsMapRe.FindString(line); m != "" {
			a, b := mpegtsRe.FindStringSubmatch(m), localRe.FindStringSubmatch(m)
			if a != nil && b != nil {
				n, _ := strconv.ParseFloat(a[1], 64)
				mapped, c.mpegts, c.local = true, n/90000, vttSeconds(b[1])
			}
			continue
		}
		if m := cueRe.FindStringSubmatch(line); m != nil {
			c.starts = append(c.starts, vttSeconds(m[1]))
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.report.Cues += len(c.starts)
	p.windows[win].Cues[r.name] += len(c.starts)
	switch {
	case len(c.starts) == 0:
	case !mapped:
		p.report.CueErrors += len(c.starts)
		p.cueErrorf("%s segment %d: cues without X-TIMESTAMP-MAP", r.name, g.Seq)
	case p.timeCuesLocked(c):
	case len(p.pendingCues) >= maxPendingCues:
		p.report.CueErrors += len(c.starts)
		p.cueErrorf("%s segment %d: %d cue(s) never matched a video segment", r.name, g.Seq, len(c.starts))
	default:
		p.pendingCues = append(p.pendingCues, c)
	}
}

// retryCuesLocked times the waiting cues whose video segment has arrived.
func (p *Player) retryCuesLocked() {
	waiting := p.pendingCues[:0]
	for _, c := range p.pendingCues {
		if !p.timeCuesLocked(c) {
			waiting = append(waiting, c)
		}
	}
	p.pendingCues = waiting
}

// timeCuesLocked times c against the rung's PDT-paired probed segment; false
// when there is none yet.
func (p *Player) timeCuesLocked(c pendingCue) bool {
	var rung *rendition
	for _, o := range append([]*rendition{p.primary}, p.others...) {
		if o.name == strings.TrimSuffix(c.r.name, "_vtt") {
			rung = o
		}
	}
	if rung == nil || c.g.PDT.IsZero() || len(rung.probed) == 0 {
		return false
	}
	k, ok := PairByPDT(PDTSeg{Start: c.g.PDT, Dur: c.g.Duration, DNum: c.g.DNum}, rung.pdtSegs(), durOf(c.g.Duration))
	if !ok || !overlaps(rung.probed[k].seg, PDTSeg{Start: c.g.PDT, Dur: c.g.Duration}) {
		return false
	}
	v := rung.probed[k]
	for _, start := range c.starts {
		pts := c.mpegts + (start - c.local)
		p.report.CuesTimed++
		p.windows[c.win].CuesTimed[c.r.name]++
		lo, hi := ptsDiff(pts, v.pts), ptsDiff(pts, v.end)
		if lo < -cueTolerance || hi > cueTolerance {
			p.report.CueErrors++
			p.cueErrorf("%s segment %d: cue at PTS %.3f outside %s segment %.3f–%.3f", c.r.name, c.g.Seq, pts, rung.name, v.pts, v.end)
		}
	}
	return true
}

// cueErrorf records a cue problem (the first few only).
func (p *Player) cueErrorf(format string, a ...any) {
	if p.report.CueErrors <= 5 {
		p.report.Errors = append(p.report.Errors, fmt.Sprintf(format, a...))
	}
}

// vttSeconds parses [hh:]mm:ss.mmm.
func vttSeconds(v string) float64 {
	parts := strings.Split(v, ":")
	sec := 0.0
	for _, part := range parts {
		f, _ := strconv.ParseFloat(part, 64)
		sec = sec*60 + f
	}
	return sec
}

// ptsPeriod is the 33-bit MPEG-TS clock's wrap, in seconds.
const ptsPeriod = float64(1<<33) / 90000

// ptsDiff is a − b, unwrapped to the nearest 33-bit period.
func ptsDiff(a, b float64) float64 {
	d := math.Mod(a-b, ptsPeriod)
	switch {
	case d > ptsPeriod/2:
		d -= ptsPeriod
	case d < -ptsPeriod/2:
		d += ptsPeriod
	}
	return d
}

func overlaps(a, b PDTSeg) bool { return a.Start.Before(b.End()) && b.Start.Before(a.End()) }

func durOf(sec float64) time.Duration { return time.Duration(sec * float64(time.Second)) }

func (p *Player) get(ctx context.Context, ref string) ([]byte, int, error) {
	u := ref
	if !strings.HasPrefix(ref, "http://") && !strings.HasPrefix(ref, "https://") {
		u = p.cfg.BaseURL + ref
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := p.cfg.Client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	return b, resp.StatusCode, err
}

// maxProbed bounds the probed segments kept per rendition for pairing.
const maxProbed = 64

// probed is one probed segment: its wall-clock place and its PTS range.
type probed struct {
	seg      PDTSeg
	pts, end float64
	win      int
	paired   bool
}

// rendition is one followed media playlist and its probe state.
type rendition struct {
	name    string
	url     string
	highest int64
	lastSeq int64   // last probed audio segment (-2: none)
	lastEnd float64 // end PTS of that segment
	everOK  bool    // the playlist was served at least once
	fetched int     // segments fetched OK
	lastOK  time.Time
	probed  []*probed // by PDT
}

func newRendition(name, url string, at time.Time) *rendition {
	return &rendition{name: name, url: url, highest: -1, lastSeq: -2, lastOK: at}
}

func (r *rendition) addProbed(rec *probed) {
	r.probed = append(r.probed, rec)
	if len(r.probed) > maxProbed {
		r.probed = append([]*probed(nil), r.probed[len(r.probed)-maxProbed:]...)
	}
}

func (r *rendition) pdtSegs() []PDTSeg {
	out := make([]PDTSeg, len(r.probed))
	for i, x := range r.probed {
		out[i] = x.seg
	}
	return out
}

func isSubtitleRendition(name string) bool { return strings.HasSuffix(name, "_vtt") }

func isAudioRendition(name string) bool {
	return strings.HasPrefix(name, "aac") || strings.HasPrefix(name, "ac3")
}

// renditionName is a playlist reference's file name without .m3u8 or query.
func renditionName(ref string) string {
	if q := strings.IndexByte(ref, '?'); q >= 0 {
		ref = ref[:q]
	}
	ref = ref[strings.LastIndexByte(ref, '/')+1:]
	return strings.TrimSuffix(ref, ".m3u8")
}

var mediaURIRe = regexp.MustCompile(`URI="([^"]+)"`)

// renditionRefs lists a master's variants (in order), then its audio and
// subtitle renditions.
func renditionRefs(master []byte) []string {
	var variants, media []string
	sc := bufio.NewScanner(bytes.NewReader(master))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "#EXT-X-MEDIA:") &&
			(strings.Contains(line, "TYPE=AUDIO") || strings.Contains(line, "TYPE=SUBTITLES")):
			if m := mediaURIRe.FindStringSubmatch(line); m != nil {
				media = append(media, m[1])
			}
		case line != "" && !strings.HasPrefix(line, "#"):
			variants = append(variants, line)
		}
	}
	return append(variants, media...)
}

func holeOf(sec float64) time.Duration {
	if sec <= 0 {
		return 0
	}
	return time.Duration(sec * float64(time.Second))
}

func absSeconds(sec float64) time.Duration {
	if sec < 0 {
		sec = -sec
	}
	return time.Duration(sec * float64(time.Second))
}

// resolveRef resolves a playlist reference against the playlist it came from.
func resolveRef(base, ref string) string {
	if strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		return ref
	}
	if q := strings.IndexByte(base, '?'); q >= 0 {
		base = base[:q]
	}
	return base[:strings.LastIndexByte(base, '/')+1] + ref
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd server && go test ./internal/testplayer/ -v -race`
Expected: PASS, including every existing `player_test.go` test (their reports gain fields; none of their assertions change).

- [ ] **Step 5: Commit**

```bash
git add server/internal/testplayer
git commit -m "test(testplayer): follow every rendition, pair A/V by PDT, count coverage per window

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Scenario expectations, e2e plumbing and the new fault scenarios

**Files:**
- Create: `server/internal/testplayer/coverage.go`
- Test: `server/internal/testplayer/coverage_test.go`
- Modify: `server/internal/hdhr/hdhrfake/scenario/scenario.go`, `run.go`
- Test: `server/internal/hdhr/hdhrfake/scenario/scenario_test.go`
- Modify: `server/internal/stream/manager.go` (`ManagerDeps.HLSListSize`)
- Test: `server/internal/stream/manager_test.go`
- Modify: `server/internal/e2e/harness.go`, `server/internal/e2e/scenarios_test.go`
- Create: `server/internal/e2e/ffprobe_test.go`
- Create: `server/internal/e2e/testdata/scenarios/{ptsjump,ptsjump-ptsonly,corrupt-audio,brief-dropout,threshold-dropout,splice,long-outage-restart,stuck-recovers,stuck-hung,production-baseline}.yaml`
- Modify: `server/internal/e2e/testdata/scenarios/transcoder-restart.yaml`

**Interfaces:**
- Consumes: the testplayer `Config.AllRenditions`, `Config.Probe`, `Packet`, `SegmentProbe`, `Mark` and the `Report` fields (Task 3).
- Produces:
  - testplayer coverage. The checked windows are every window opened by a `Mark` (the end of a fault cluster; see `FaultEnds`), plus the last window. The last window is the only one when a run has no faults. Three `Report` methods use them:
    - `func (r Report) UnpairedAfterFaults() []string` lists coverage holes in the checked windows. A hole is an audio or video rendition with no A/V-paired segment in the window, or a discontinuity number it delivered there with none of its segments paired. A one-segment discontinuity counts: a short restart that never paired is a hole. The single exemption is one segment of the rendition's last discontinuity in the last window, where observation ended before its partner could arrive. A run without both audio and video renditions is one hole.
    - `func (r Report) UntimedCueWindows() []string` lists the checked windows in which a subtitle rendition had no cue timed against its video segment (`CoverageWindow.CuesTimed`). Cues that never matched a video segment are already `CueErrors` (Task 3's drain).
    - `func (r Report) MaxTailGap() (string, time.Duration)`.
  - `scenario.Scenario.WindowSegments int` (`windowSegments`: the session's `-hls_list_size`; 0 = default 30; otherwise ≥ 3).
  - `scenario.Expect` gains `RenditionsMonotonic, DiscontinuitiesAligned, DiscontinuitySequenceOK, PDTMonotonic, PDTAligned *bool`, `MinDiscontinuitySequence *int64`, `MaxTimelineExcessSeconds, MinTimelineExcessSeconds, MaxAudioHoleMs, MaxAVStartSkewMs *float64` and `MinFfmpegRestarts *int`. YAML keys are the lowerCamel field names.
  - `scenario.Observed` gains the matching fields: `RenditionsMonotonic`, `DiscontinuitiesAligned`, `DiscontinuitySequenceOK`, `MaxDiscontinuitySequence`, `PDTMonotonic`, `PDTAligned`, `TimelineExcess`, `MaxAudioHole`, `MaxAVStartSkew`, `ProbedAudio`, `ProbedVideo`.
  - `scenario.Expect` also gains:
    - `XFailOn string` (`xfailOn`, required with `xfail`): the text of the violation that must occur. Any other violation fails the xfail run.
    - `MinSegmentsAfterFault *int` (`minSegmentsAfterFault`): every followed rendition fetched at least this many segments after the last fault ended.
    - `PairedAfterEachFault *bool` (`pairedAfterEachFault`): `UnpairedAfterFaults` is empty. Every audio and video rendition had its A/V skew measured after **each** fault cluster, in every discontinuity it delivered there, not just somewhere in the run.
    - `MaxTailGapSeconds *float64` (`maxTailGapSeconds`): every rendition kept delivering until the end of observation. Its last new segment is at most this old when the run ends.
    - `CaptionsTimed *bool` (`captionsTimed`): captions are expected. Every checked window has timed cues (`UntimedCueWindows` is empty), and no cue fell outside its PDT-paired video segment or went unmatched (`CueErrors == 0`).
    - `NoFetchErrors *bool` (`noFetchErrors`): `Report.FetchErrors` is 0 (every playlist, segment and probe failure in any rendition; Task 3), and every listed rendition delivered a segment.
    - `MinRenditions *int` (`minRenditions`).
    - `MaxStartSeconds *float64` (`maxStartSeconds`): from POST /sessions to its answer, which waits for the first playlist.
  - `scenario.Observed` gains:
    - `MinSegmentsAfterFault int`, `FetchErrors int`, `RenditionsCovered bool`, `Renditions int`, `StartLatency time.Duration`;
    - `AVPairs int`, `UnpairedCoverage []string`;
    - `MaxTailGap time.Duration`, `MaxTailGapRendition string`;
    - `Cues, CuesTimed, CueErrors int`, `UntimedCueWindows []string`.
  - `scenario.BowtieSpec.Layout string` (`bowtie: {layout: production}`). The production layout is what a real server serves: multitrack (every audio track as AAC, the AC-3 copy, captions) and a ladder, even with the software encoder. The default stays one rung and one AAC.
  - `const scenario.FaultClusterGap = 8 * time.Second` and `func (s *Scenario) FaultEnds() []time.Duration`: the end of each fault cluster, ascending. The e2e runner calls `Mark` at each one.
    - A fault ends at a timeline step's `at` + `for`, the slow-consumer window's end, or the kill or close time.
    - Ends closer together than 8 s form one explicit cluster. Its window opens at the cluster's last end and must show coverage for all of its faults.
    - Only a delivery-blocking fault suppresses the window of a fault that ends inside it: `signal`, `stall`, `hang`, `pid-loss`, the slow consumer, a kill or a close. A kill during an outage therefore opens no window, because nothing can arrive until the outage is over; the outage's own end opens one.
    - Corruption and timestamp faults (`corrupt`, `drop`, `slow`, `pts-jump`, `timestamp-jump`, …) never suppress a window. A pts-jump inside a long corruption burst gets its own window.
    - `func (s *Scenario) LastFaultEnd() time.Duration` is the last end (0 without faults).
  - `stream.ManagerDeps.HLSListSize int` overrides `-hls_list_size` for new sessions (tests).
  - `stream.ManagerDeps.ForceAdaptive bool` (tests) makes every start use the ladder, whatever the settings and encoder say (libx264 normally never ladders).
  - `e2e.Options.Production bool` sets `Multitrack: true`, `ForceAdaptive: true` and a 3 s `TrackProbeTimeout`, so the PMT's audio tracks are known.
  - `e2e.Options.HLSListSize int`; `(*Harness).PlayerConfig(t, guide) testplayer.Config`; `e2e.FFprobe(bin string) testplayer.SegmentProbe` and `ffprobePath()` (env `BOWTIE_FFPROBE_PATH`, default `ffprobe`), both under the `ffmpeg` build tag.

- [ ] **Step 1: Write the failing tests**

Append to `server/internal/hdhr/hdhrfake/scenario/scenario_test.go`:

```go
func TestCheckTimelineExpectations(t *testing.T) {
	yes, f := true, func(v float64) *float64 { return &v }
	one, ds := 1, int64(1)
	e := &Expect{
		RenditionsMonotonic: &yes, DiscontinuitiesAligned: &yes, DiscontinuitySequenceOK: &yes,
		PDTMonotonic: &yes, PDTAligned: &yes, MinDiscontinuitySequence: &ds,
		MaxTimelineExcessSeconds: f(2), MinTimelineExcessSeconds: f(-2),
		MaxAudioHoleMs: f(50), MaxAVStartSkewMs: f(100), MinFfmpegRestarts: &one,
	}
	good := Observed{
		RenditionsMonotonic: true, DiscontinuitiesAligned: true, DiscontinuitySequenceOK: true,
		PDTMonotonic: true, PDTAligned: true, MaxDiscontinuitySequence: 1, TimelineExcess: 0.5,
		MaxAudioHole: 20 * time.Millisecond, MaxAVStartSkew: 40 * time.Millisecond,
		ProbedAudio: 3, ProbedVideo: 3, AVPairs: 3, FfmpegRestarts: 1,
	}
	if v := e.Check(good); len(v) != 0 {
		t.Fatalf("good run violations: %v", v)
	}
	bad := Observed{TimelineExcess: 9, MaxAudioHole: 200 * time.Millisecond, ProbedAudio: 1, ProbedVideo: 1,
		MaxAVStartSkew: 300 * time.Millisecond}
	// Every check but the minimum excess fails: 10 violations.
	if v := e.Check(bad); len(v) != 10 {
		t.Fatalf("bad run: %d violations %v, want 10", len(v), v)
	}
	if v := e.Check(Observed{TimelineExcess: -9}); !strings.Contains(strings.Join(v, ";"), "timeline excess") {
		t.Fatalf("low excess not reported: %v", v)
	}
	if v := (&Expect{MaxAudioHoleMs: f(50)}).Check(Observed{}); len(v) != 1 || !strings.Contains(v[0], "no audio") {
		t.Fatalf("unprobed audio must be a violation: %v", v)
	}
}

func TestParseWindowSegments(t *testing.T) {
	s, err := Parse([]byte("name: w\nduration: 10s\nwindowSegments: 8\n"))
	if err != nil || s.WindowSegments != 8 {
		t.Fatalf("windowSegments: %v %+v", err, s)
	}
	if _, err := Parse([]byte("name: w\nduration: 10s\nwindowSegments: 2\n")); err == nil {
		t.Fatal("windowSegments 2 accepted")
	}
}
```

```go
func TestXFailNeedsXFailOnAndNewExpectations(t *testing.T) {
	if _, err := Parse([]byte("name: x\nduration: 5s\nexpect:\n  maxGapSeconds: 5\n  xfail: why\n")); err == nil {
		t.Fatal("xfail without xfailOn accepted")
	}
	s, err := Parse([]byte("name: x\nduration: 40s\ntimeline:\n  - {at: 10s, fault: stall, for: 4s}\n" +
		"bowtie: {layout: production, killTranscoderAt: 20s}\n" +
		"expect:\n  xfail: why\n  xfailOn: timeline excess\n  maxTimelineExcessSeconds: 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Bowtie.Layout != "production" || s.LastFaultEnd() != 20*time.Second {
		t.Fatalf("layout %q, last fault end %v", s.Bowtie.Layout, s.LastFaultEnd())
	}
	// The stall ends at 14 s and the kill comes 6 s later: one cluster.
	if got := s.FaultEnds(); len(got) != 1 || got[0] != 20*time.Second {
		t.Fatalf("fault ends %v, want [20s]", got)
	}
	if _, err := Parse([]byte("name: x\nduration: 5s\nbowtie: {layout: fancy}\n")); err == nil {
		t.Fatal("unknown layout accepted")
	}
	three, two, yes := 3, 2, true
	e := &Expect{MinSegmentsAfterFault: &three, NoFetchErrors: &yes, MinRenditions: &two, MaxStartSeconds: func(v float64) *float64 { return &v }(8)}
	if v := e.Check(Observed{MinSegmentsAfterFault: 3, RenditionsCovered: true, Renditions: 2, StartLatency: time.Second}); len(v) != 0 {
		t.Fatalf("good: %v", v)
	}
	if v := e.Check(Observed{MinSegmentsAfterFault: 1, FetchErrors: 1, Renditions: 1, StartLatency: 9 * time.Second}); len(v) != 4 {
		t.Fatalf("bad: %d violations %v, want 4", len(v), v)
	}
}
```

```go
func TestCheckCoverageExpectations(t *testing.T) {
	yes, f := true, func(v float64) *float64 { return &v }
	e := &Expect{PairedAfterEachFault: &yes, MaxTailGapSeconds: f(12), CaptionsTimed: &yes, MaxAVStartSkewMs: f(100)}
	good := Observed{AVPairs: 4, ProbedAudio: 4, ProbedVideo: 4, MaxTailGap: 5 * time.Second, MaxTailGapRendition: "aac0", Cues: 10, CuesTimed: 10}
	if v := e.Check(good); len(v) != 0 {
		t.Fatalf("good: %v", v)
	}
	bad := Observed{
		ProbedAudio: 4, ProbedVideo: 4, // probed, but never paired
		UnpairedCoverage: []string{"window 2: aac0 has no A/V-paired segment (0 fetched)"},
		MaxTailGap:       30 * time.Second, MaxTailGapRendition: "aac0",
		Cues: 3, CuesTimed: 2, CueErrors: 1, UntimedCueWindows: []string{"window 1: v480_vtt has no timed cues (1 cues)"},
	}
	v := e.Check(bad)
	joined := strings.Join(v, "\n")
	for _, want := range []string{"no audio/video segment pairs", "window 2: aac0", "aac0 stopped 30.0s before the end", "cues"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %v", want, v)
		}
	}
	if len(v) != 4 {
		t.Fatalf("%d violations %v, want 4", len(v), v)
	}
}
```

```go
func TestFaultEndsClusters(t *testing.T) {
	for _, tc := range []struct{ name, yaml, want string }{
		{"spaced point faults", "timeline:\n  - {at: 8s, fault: pts-jump, by: 3s}\n  - {at: 18s, fault: pts-jump, by: 3s}\n  - {at: 28s, fault: pts-jump, by: 3s}\n", "[8s 18s 28s]"},
		{"8 s apart is not a cluster", "timeline:\n  - {at: 30s, fault: timestamp-jump, by: 2s}\n  - {at: 38s, fault: timestamp-jump, by: 2s}\n", "[30s 38s]"},
		{"closer than 8 s is one cluster", "timeline:\n  - {at: 30s, fault: timestamp-jump, by: 2s}\n  - {at: 35s, fault: timestamp-jump, by: 2s}\n", "[35s]"},
		{"kill inside an outage", "timeline:\n  - {at: 10s, fault: signal, quality: 10, for: 65s}\nbowtie: {killTranscoderAt: 30s}\n", "[1m15s]"},
		{"jump inside corruption", "timeline:\n  - {at: 10s, fault: corrupt, type: tei, rate: 0.01, for: 50s}\n  - {at: 12s, fault: pts-jump, by: 3s}\n", "[12s 1m0s]"},
		{"jump inside the slow consumer", "timeline:\n  - {at: 20s, fault: pts-jump, by: 3s}\nbowtie: {slowConsumer: {at: 10s, for: 40s}}\n", "[50s]"},
		{"no faults", "", "[]"},
	} {
		s, err := Parse([]byte("name: f\nduration: 100s\n" + tc.yaml))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := fmt.Sprint(s.FaultEnds()); got != tc.want {
			t.Errorf("%s: fault ends %s, want %s", tc.name, got, tc.want)
		}
	}
}
```

Add `"fmt"` and `"strings"` to the test file's imports if they aren't there.

Create `server/internal/testplayer/coverage_test.go`:

```go
package testplayer

import (
	"strings"
	"testing"
	"time"
)

func TestCoverageAfterFaults(t *testing.T) {
	t0 := time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC)
	win := func(start, end float64, paired, epochs map[string]map[int64]int, timed int) CoverageWindow {
		segs := map[string]int{}
		for n, m := range epochs {
			for _, c := range m {
				segs[n] += c
			}
		}
		return CoverageWindow{
			Start: t0.Add(time.Duration(start * float64(time.Second))), End: t0.Add(time.Duration(end * float64(time.Second))),
			Segments: segs, Epochs: epochs, Paired: paired,
			Cues: map[string]int{"v480_vtt": timed + 1}, CuesTimed: map[string]int{"v480_vtt": timed},
		}
	}
	r := Report{
		Renditions: []string{"v480", "aac0", "v480_vtt"},
		TailGap:    map[string]time.Duration{"v480": time.Second, "aac0": 9 * time.Second, "v480_vtt": 2 * time.Second},
		Windows: []CoverageWindow{
			// Before the first fault cluster: not checked.
			win(0, 10, nil, map[string]map[int64]int{"v480": {0: 2}}, 0),
			// After a restart: audio's epoch 1 delivered segments but none
			// paired, and video's one-segment epoch 2 never paired (a short
			// restart; not exempt, because it isn't the end of observation).
			win(10, 40,
				map[string]map[int64]int{"v480": {0: 1, 1: 2}, "aac0": {0: 1}},
				map[string]map[int64]int{"v480": {0: 1, 1: 5, 2: 1}, "aac0": {0: 1, 1: 5}}, 3),
			// The last window: video's single segment of its last epoch is
			// exempt; audio stopped.
			win(40, 52, map[string]map[int64]int{"v480": {3: 2}}, map[string]map[int64]int{"v480": {3: 2, 4: 1}}, 0),
		},
	}
	got := strings.Join(r.UnpairedAfterFaults(), "\n")
	want := "window 1: v480 discontinuity 2 has 1 segment(s), none A/V-paired\n" +
		"window 1: aac0 discontinuity 1 has 5 segment(s), none A/V-paired\n" +
		"window 2: aac0 has no A/V-paired segment (0 fetched)"
	if got != want {
		t.Fatalf("unpaired:\n%s\nwant:\n%s", got, want)
	}
	if got := strings.Join(r.UntimedCueWindows(), "\n"); got != "window 2: v480_vtt has no timed cues (1 cues)" {
		t.Fatalf("untimed: %q", got)
	}
	if n, g := r.MaxTailGap(); n != "aac0" || g != 9*time.Second {
		t.Fatalf("max tail gap %s %v", n, g)
	}
	if got := (Report{Renditions: []string{"v480"}, Windows: r.Windows[:1]}).UnpairedAfterFaults(); len(got) != 1 {
		t.Fatalf("video alone must be reported: %v", got)
	}
	// With no faults, the only window is the last and is checked.
	if got := (Report{Renditions: []string{"v480", "aac0"}, Windows: r.Windows[:1]}).UnpairedAfterFaults(); len(got) != 2 {
		t.Fatalf("a run without faults is checked as one window: %v", got)
	}
}
```

Append to `server/internal/stream/manager_test.go`:

```go
func TestHLSListSizeOverride(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	im, _ := newManagerIngest(clock, nil)
	t.Cleanup(im.Shutdown)
	m := NewManager(ManagerDeps{
		TrackProbeTimeout: time.Millisecond,
		Cfg:               cfg, Store: st,
		StreamURL: func(ch store.Channel) (string, error) { return "http://127.0.0.1:5004/auto/v" + ch.GuideNumber, nil },
		Caps:      softwareCaps(), Runner: runner, Clock: clock.Now, Ingest: im,
		HLSListSize: 8,
	})
	if _, err := m.Start(context.Background(), user, chID, clientCaps("")); err != nil {
		t.Fatal(err)
	}
	if got := runner.LastSpec().HLSListSize; got != 8 {
		t.Fatalf("HLSListSize=%d, want 8", got)
	}
}
```

And:

```go
func TestForceAdaptiveLaddersSoftware(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	im, _ := newManagerIngest(clock, nil)
	t.Cleanup(im.Shutdown)
	m := NewManager(ManagerDeps{
		TrackProbeTimeout: time.Millisecond,
		Cfg:               cfg, Store: st,
		StreamURL: func(ch store.Channel) (string, error) { return "http://127.0.0.1:5004/auto/v" + ch.GuideNumber, nil },
		Caps:      softwareCaps(), Runner: runner, Clock: clock.Now, Ingest: im,
		ForceAdaptive: true,
	})
	if _, err := m.Start(context.Background(), user, chID, clientCaps("")); err != nil {
		t.Fatal(err)
	}
	if got := len(runner.LastSpec().Layout.Rungs); got < 2 {
		t.Fatalf("%d rungs with ForceAdaptive and libx264, want a ladder", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd server && go test ./internal/testplayer/ ./internal/hdhr/hdhrfake/scenario/ ./internal/stream/ -run 'TestCoverageAfterFaults|TestCheckTimelineExpectations|TestCheckCoverageExpectations|TestParseWindowSegments|TestXFailNeedsXFailOn|TestFaultEndsClusters|TestHLSListSizeOverride|TestForceAdaptive' -v`
Expected: FAIL to compile (`r.UnpairedAfterFaults undefined`, `s.FaultEnds undefined`, `unknown field RenditionsMonotonic`, `unknown field HLSListSize`).

- [ ] **Step 3: Implement the coverage helpers, scenario and manager changes**

Create `server/internal/testplayer/coverage.go`:

```go
package testplayer

import (
	"fmt"
	"sort"
	"time"
)

// checkedWindows are the windows whose coverage is checked: every window
// opened by a Mark (the end of a fault cluster; scenario.FaultEnds spaces
// them at least 8 s apart) and the last window, which is the only one
// when nothing was marked.
func (r Report) checkedWindows() []int {
	var out []int
	for i := range r.Windows {
		if i > 0 || i == len(r.Windows)-1 {
			out = append(out, i)
		}
	}
	return out
}

// UnpairedAfterFaults lists coverage holes in the checked windows: an audio
// or video rendition with no segment PDT-paired with the other kind, or a
// discontinuity number it delivered in the window with none of its
// segments paired. The only exemption is a single segment of the
// rendition's last discontinuity in the last window: observation ended
// before its partner could arrive. Empty means every rendition's A/V
// alignment was measured after every fault cluster, in every discontinuity.
func (r Report) UnpairedAfterFaults() []string {
	var av []string
	audio, video := false, false
	for _, name := range r.Renditions {
		if isSubtitleRendition(name) {
			continue
		}
		av = append(av, name)
		if isAudioRendition(name) {
			audio = true
		} else {
			video = true
		}
	}
	if !audio || !video {
		return []string{fmt.Sprintf("renditions %v: no audio and video to pair", r.Renditions)}
	}
	final := map[string]int64{} // each rendition's last discontinuity number
	for _, w := range r.Windows {
		for name, m := range w.Epochs {
			for d := range m {
				if f, ok := final[name]; !ok || d > f {
					final[name] = d
				}
			}
		}
	}
	var out []string
	for _, i := range r.checkedWindows() {
		w, last := r.Windows[i], i == len(r.Windows)-1
		for _, name := range av {
			paired := 0
			for _, n := range w.Paired[name] {
				paired += n
			}
			if paired == 0 {
				out = append(out, fmt.Sprintf("window %d: %s has no A/V-paired segment (%d fetched)", i, name, w.Segments[name]))
				continue
			}
			var dnums []int64
			for d := range w.Epochs[name] {
				dnums = append(dnums, d)
			}
			sort.Slice(dnums, func(a, b int) bool { return dnums[a] < dnums[b] })
			for _, d := range dnums {
				n := w.Epochs[name][d]
				if w.Paired[name][d] > 0 || (last && d == final[name] && n == 1) {
					continue
				}
				out = append(out, fmt.Sprintf("window %d: %s discontinuity %d has %d segment(s), none A/V-paired", i, name, d, n))
			}
		}
	}
	return out
}

// UntimedCueWindows lists checked windows in which a subtitle rendition
// had no cue timed against its video segment (no cues, or none matched).
func (r Report) UntimedCueWindows() []string {
	var out []string
	for _, i := range r.checkedWindows() {
		for _, name := range r.Renditions {
			if isSubtitleRendition(name) && r.Windows[i].CuesTimed[name] == 0 {
				out = append(out, fmt.Sprintf("window %d: %s has no timed cues (%d cues)", i, name, r.Windows[i].Cues[name]))
			}
		}
	}
	return out
}

// MaxTailGap is the longest TailGap and its rendition.
func (r Report) MaxTailGap() (string, time.Duration) {
	var name string
	var max time.Duration
	for _, n := range r.Renditions {
		if g := r.TailGap[n]; g > max || name == "" {
			name, max = n, g
		}
	}
	return name, max
}
```

`scenario/scenario.go`: add to `Scenario` after `PTSWrapIn`:

```go
	// WindowSegments is the session's -hls_list_size (0: Bowtie's default).
	// Small windows let a run watch a restart slide out of the playlist.
	WindowSegments int `yaml:"windowSegments"`
```

Add to `Expect` after `TunersBusy`:

```go
	// Timeline and rendition checks (testplayer.Consistency).
	RenditionsMonotonic      *bool  `yaml:"renditionsMonotonic"`
	DiscontinuitiesAligned   *bool  `yaml:"discontinuitiesAligned"`
	DiscontinuitySequenceOK  *bool  `yaml:"discontinuitySequenceOK"`
	MinDiscontinuitySequence *int64 `yaml:"minDiscontinuitySequence"`
	PDTMonotonic             *bool  `yaml:"pdtMonotonic"`
	PDTAligned               *bool  `yaml:"pdtAligned"`
	// Σ EXTINF minus wall time, in seconds (negative: an outage was skipped).
	MaxTimelineExcessSeconds *float64 `yaml:"maxTimelineExcessSeconds"`
	MinTimelineExcessSeconds *float64 `yaml:"minTimelineExcessSeconds"`
	// ffprobe checks; a run that probed no audio violates them.
	MaxAudioHoleMs    *float64 `yaml:"maxAudioHoleMs"`
	MaxAVStartSkewMs  *float64 `yaml:"maxAVStartSkewMs"`
	MinFfmpegRestarts *int     `yaml:"minFfmpegRestarts"`
	// XFailOn is the violation text an xfail run must show; other
	// violations fail it (a dead session can't pass for the known gap).
	XFailOn string `yaml:"xfailOn"`
	// Progress and coverage after the faults (every followed rendition).
	MinSegmentsAfterFault *int     `yaml:"minSegmentsAfterFault"`
	PairedAfterEachFault  *bool    `yaml:"pairedAfterEachFault"`
	MaxTailGapSeconds     *float64 `yaml:"maxTailGapSeconds"`
	CaptionsTimed         *bool    `yaml:"captionsTimed"`
	NoFetchErrors         *bool    `yaml:"noFetchErrors"`
	MinRenditions         *int     `yaml:"minRenditions"`
	MaxStartSeconds       *float64 `yaml:"maxStartSeconds"`
```

Add to `BowtieSpec`:

```go
	// Layout: "" (one rung, first audio as AAC) or "production" (ladder even
	// with libx264, every audio track, the AC-3 copy, captions).
	Layout string `yaml:"layout"`
```

Add to `scenario.go`:

```go
// FaultClusterGap: fault ends closer together than this form one cluster,
// with one coverage window after the cluster's last end.
const FaultClusterGap = 8 * time.Second

// blocksDelivery reports whether a fault kind stops segments from arriving
// while it lasts (an outage), as opposed to damaging what arrives.
func blocksDelivery(k faults.Kind) bool {
	switch k {
	case faults.Signal, faults.Stall, faults.Hang, faults.PIDLoss:
		return true
	}
	return false
}

// FaultEnds lists where the e2e runner opens a coverage window: the end of
// each fault cluster, ascending. A fault ends at a timeline step's at + for,
// the slow-consumer window's end, or the kill or close time. Ends closer
// together than FaultClusterGap form one cluster, whose window opens at
// its last end and must show coverage for all of them. An end strictly
// inside a delivery-blocking fault (an outage, a stall, a hung tuner, a
// lost PID, FFmpeg not reading) opens no window: nothing can arrive until
// that fault is over, and its own end opens one. Corruption and timestamp
// faults never suppress another fault's window.
func (s *Scenario) FaultEnds() []time.Duration {
	type span struct {
		from, to time.Duration
		blocks   bool
	}
	var spans []span
	for _, st := range s.Timeline {
		spans = append(spans, span{st.At, st.At + st.Spec.For, blocksDelivery(st.Spec.Fault)})
	}
	if b := s.Bowtie; b != nil {
		if w := b.SlowConsumer; w != nil {
			spans = append(spans, span{w.At, w.At + w.For, true})
		}
		for _, t := range []time.Duration{b.KillTranscoderAt, b.CloseTranscoderInputAt} {
			if t > 0 {
				spans = append(spans, span{t, t, true})
			}
		}
	}
	var ends []time.Duration
	for _, a := range spans {
		inside := false
		for _, b := range spans {
			if b.blocks && b.from < a.to && a.to < b.to {
				inside = true
			}
		}
		if !inside {
			ends = append(ends, a.to)
		}
	}
	sort.Slice(ends, func(i, j int) bool { return ends[i] < ends[j] })
	var out []time.Duration
	for i, e := range ends {
		if i+1 < len(ends) && ends[i+1]-e < FaultClusterGap {
			continue // the cluster goes on: its window opens at a later end
		}
		out = append(out, e)
	}
	return out
}

// LastFaultEnd is when the last fault of the run is over (0: no faults).
func (s *Scenario) LastFaultEnd() time.Duration {
	ends := s.FaultEnds()
	if len(ends) == 0 {
		return 0
	}
	return ends[len(ends)-1]
}
```

(`Step` embeds `faults.Spec`, so `st.Spec.For` and `st.Spec.Fault` are the step's `for` and `fault`. Add `"sort"` to `scenario.go`'s imports if it isn't there.)

In `validate()`, after the duration check:

```go
	if s.WindowSegments < 0 || (s.WindowSegments > 0 && s.WindowSegments < 3) {
		return fmt.Errorf("scenario %s: windowSegments %d (want 0 or ≥ 3)", s.Name, s.WindowSegments)
	}
	if s.Bowtie != nil && s.Bowtie.Layout != "" && s.Bowtie.Layout != "production" {
		return fmt.Errorf("scenario %s: layout %q (want production or empty)", s.Name, s.Bowtie.Layout)
	}
	if e := s.Expect; e != nil && e.XFail != "" && e.XFailOn == "" {
		return fmt.Errorf("scenario %s: xfail needs xfailOn (the violation that must occur)", s.Name)
	}
```

(Put the `xfailOn` check next to the existing xfail/flaky checks, which run on the same `Expect`.)

Replace `empty()`:

```go
func (e *Expect) empty() bool {
	return e.SequenceMonotonic == nil && e.MaxGapSeconds == nil && e.SessionSurvives == nil &&
		e.MaxFfmpegRestarts == nil && e.TunersBusy == nil &&
		e.RenditionsMonotonic == nil && e.DiscontinuitiesAligned == nil && e.DiscontinuitySequenceOK == nil &&
		e.MinDiscontinuitySequence == nil && e.PDTMonotonic == nil && e.PDTAligned == nil &&
		e.MaxTimelineExcessSeconds == nil && e.MinTimelineExcessSeconds == nil &&
		e.MaxAudioHoleMs == nil && e.MaxAVStartSkewMs == nil && e.MinFfmpegRestarts == nil &&
		e.MinSegmentsAfterFault == nil && e.NoFetchErrors == nil && e.MinRenditions == nil && e.MaxStartSeconds == nil &&
		e.PairedAfterEachFault == nil && e.MaxTailGapSeconds == nil && e.CaptionsTimed == nil
}
```

`scenario/run.go`: extend `Observed`:

```go
	RenditionsMonotonic      bool
	DiscontinuitiesAligned   bool
	DiscontinuitySequenceOK  bool
	MaxDiscontinuitySequence int64
	PDTMonotonic             bool
	PDTAligned               bool
	TimelineExcess           float64 // seconds
	MaxAudioHole             time.Duration
	MaxAVStartSkew           time.Duration
	ProbedAudio, ProbedVideo int
	MinSegmentsAfterFault    int // fewest segments any rendition fetched after the last fault
	FetchErrors              int // testplayer Report.FetchErrors: every failed fetch
	AVPairs                  int
	UnpairedCoverage         []string // testplayer Report.UnpairedAfterFaults
	MaxTailGap               time.Duration
	MaxTailGapRendition      string
	Cues, CuesTimed, CueErrors int
	UntimedCueWindows          []string
	RenditionsCovered        bool
	Renditions               int
	StartLatency             time.Duration
```

Append to `Check` before `return v`:

```go
	flag := func(want *bool, got bool, what string) {
		if want != nil && *want != got {
			v = append(v, fmt.Sprintf("%s = %v, want %v", what, got, *want))
		}
	}
	flag(e.RenditionsMonotonic, o.RenditionsMonotonic, "renditions monotonic")
	flag(e.DiscontinuitiesAligned, o.DiscontinuitiesAligned, "discontinuities aligned")
	flag(e.DiscontinuitySequenceOK, o.DiscontinuitySequenceOK, "discontinuity sequence ok")
	flag(e.PDTMonotonic, o.PDTMonotonic, "PDT monotonic")
	flag(e.PDTAligned, o.PDTAligned, "PDT aligned")
	if e.MinDiscontinuitySequence != nil && o.MaxDiscontinuitySequence < *e.MinDiscontinuitySequence {
		v = append(v, fmt.Sprintf("discontinuity sequence reached %d, want ≥ %d", o.MaxDiscontinuitySequence, *e.MinDiscontinuitySequence))
	}
	if e.MaxTimelineExcessSeconds != nil && o.TimelineExcess > *e.MaxTimelineExcessSeconds {
		v = append(v, fmt.Sprintf("timeline excess = %.1fs, want ≤ %.1fs", o.TimelineExcess, *e.MaxTimelineExcessSeconds))
	}
	if e.MinTimelineExcessSeconds != nil && o.TimelineExcess < *e.MinTimelineExcessSeconds {
		v = append(v, fmt.Sprintf("timeline excess = %.1fs, want ≥ %.1fs", o.TimelineExcess, *e.MinTimelineExcessSeconds))
	}
	ms := func(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
	if e.MaxAudioHoleMs != nil {
		switch {
		case o.ProbedAudio == 0:
			v = append(v, "no audio segments probed")
		case ms(o.MaxAudioHole) > *e.MaxAudioHoleMs:
			v = append(v, fmt.Sprintf("audio hole = %.0fms, want ≤ %.0fms", ms(o.MaxAudioHole), *e.MaxAudioHoleMs))
		}
	}
	if e.MaxAVStartSkewMs != nil {
		switch {
		case o.AVPairs == 0:
			v = append(v, "no audio/video segment pairs probed")
		case ms(o.MaxAVStartSkew) > *e.MaxAVStartSkewMs:
			v = append(v, fmt.Sprintf("A/V start skew = %.0fms, want ≤ %.0fms", ms(o.MaxAVStartSkew), *e.MaxAVStartSkewMs))
		}
	}
	if e.MinFfmpegRestarts != nil && o.FfmpegRestarts < *e.MinFfmpegRestarts {
		v = append(v, fmt.Sprintf("ffmpeg restarts = %d, want ≥ %d", o.FfmpegRestarts, *e.MinFfmpegRestarts))
	}
	if e.MinSegmentsAfterFault != nil && o.MinSegmentsAfterFault < *e.MinSegmentsAfterFault {
		v = append(v, fmt.Sprintf("segments after the last fault = %d in the slowest rendition, want ≥ %d", o.MinSegmentsAfterFault, *e.MinSegmentsAfterFault))
	}
	if e.PairedAfterEachFault != nil && *e.PairedAfterEachFault && len(o.UnpairedCoverage) > 0 {
		v = append(v, fmt.Sprintf("A/V coverage after faults: %s", strings.Join(firstN(o.UnpairedCoverage, 3), "; ")))
	}
	if e.MaxTailGapSeconds != nil && o.MaxTailGap.Seconds() > *e.MaxTailGapSeconds {
		v = append(v, fmt.Sprintf("%s stopped %.1fs before the end, want ≤ %.1fs", o.MaxTailGapRendition, o.MaxTailGap.Seconds(), *e.MaxTailGapSeconds))
	}
	if e.CaptionsTimed != nil && *e.CaptionsTimed && (o.CuesTimed == 0 || o.CueErrors > 0 || len(o.UntimedCueWindows) > 0) {
		v = append(v, fmt.Sprintf("cues = %d, timed = %d, mistimed or unmatched = %d, windows without timed cues: %s",
			o.Cues, o.CuesTimed, o.CueErrors, strings.Join(firstN(o.UntimedCueWindows, 3), "; ")))
	}
	if e.NoFetchErrors != nil && *e.NoFetchErrors && (o.FetchErrors > 0 || !o.RenditionsCovered) {
		v = append(v, fmt.Sprintf("fetch errors = %d, every rendition delivered = %v", o.FetchErrors, o.RenditionsCovered))
	}
	if e.MinRenditions != nil && o.Renditions < *e.MinRenditions {
		v = append(v, fmt.Sprintf("renditions followed = %d, want ≥ %d", o.Renditions, *e.MinRenditions))
	}
	if e.MaxStartSeconds != nil && o.StartLatency.Seconds() > *e.MaxStartSeconds {
		v = append(v, fmt.Sprintf("start latency = %.1fs, want ≤ %.1fs", o.StartLatency.Seconds(), *e.MaxStartSeconds))
	}
```

Add to `run.go` (and `"strings"` to its imports if it isn't there):

```go
func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
```

`stream/manager.go`: add to `ManagerDeps` after `TrackProbeTimeout`:

```go
	// HLSListSize, when > 0, overrides -hls_list_size for new sessions
	// (tests that watch a restart slide out of a short window).
	HLSListSize int
```

Add a `listSizeOverride int` field to `Manager` (next to `trackProbe`), set it in `NewManager` (`listSizeOverride: deps.HLSListSize,`), and make it the first line of `hlsListSizeForStart`:

```go
	if m.listSizeOverride > 0 {
		return m.listSizeOverride, nil
	}
```

Add to `ManagerDeps`:

```go
	// ForceAdaptive (tests) makes every start use the ladder, whatever the
	// settings and the encoder say: the e2e production layout ladders libx264.
	ForceAdaptive bool
```

Add `forceAdaptive bool` to `Manager`, set it in `NewManager`, and make it the first line of `adaptiveForStart`:

```go
	if m.forceAdaptive {
		return true, nil
	}
```

Run: `cd server && go test ./internal/testplayer/ ./internal/hdhr/hdhrfake/scenario/ ./internal/stream/ -run 'TestCoverage|TestCheck|TestParse|TestXFail|TestFaultEnds|TestHLSListSize|TestForceAdaptive' -v`
Expected: PASS.

- [ ] **Step 4: e2e plumbing**

`server/internal/e2e/harness.go`: add to `Options`:

```go
	// HLSListSize overrides the sessions' -hls_list_size (0: default 30).
	HLSListSize int
```

Also add:

```go
	// Production runs the layout a real server serves: multitrack (AAC per
	// track, the AC-3 copy, captions) and a ladder even with libx264.
	Production bool
```

In `New`'s `stream.NewManager(stream.ManagerDeps{…})` call, pass `HLSListSize: o.HLSListSize,`, `Multitrack: o.Production,` and `ForceAdaptive: o.Production,`. Replace `TrackProbeTimeout: time.Millisecond, // tests: no PMT wait` with a variable:

```go
	probeWait := time.Millisecond // tests: no PMT wait
	if o.Production {
		probeWait = 3 * time.Second // the layout needs the PMT's audio tracks
	}
```

and `TrackProbeTimeout: probeWait,`. Replace `PlayerWith` and add `PlayerConfig`:

```go
// PlayerConfig is the testplayer config for a viewer of a channel (by guide number).
func (h *Harness) PlayerConfig(t testing.TB, guide string) testplayer.Config {
	t.Helper()
	id, ok := h.Channels[guide]
	if !ok {
		t.Fatalf("e2e: no channel %s", guide)
	}
	return testplayer.Config{BaseURL: h.Server.URL, Token: h.AdminToken, ChannelID: id, PollEvery: h.pollEvery}
}

// PlayerWith starts a viewer using client for HTTP (nil = default).
func (h *Harness) PlayerWith(t testing.TB, guide string, client *http.Client) (*testplayer.Player, error) {
	t.Helper()
	cfg := h.PlayerConfig(t, guide)
	cfg.Client = client
	return testplayer.Start(context.Background(), cfg)
}
```

Create `server/internal/e2e/ffprobe_test.go`:

```go
//go:build ffmpeg

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/ajthom90/bowtie/server/internal/testplayer"
)

// ffprobePath is the host ffprobe (segments are plain MPEG-TS; any version works).
func ffprobePath() string {
	if p := os.Getenv("BOWTIE_FFPROBE_PATH"); p != "" {
		return p
	}
	return "ffprobe"
}

// FFprobe returns a testplayer probe listing the first stream's packets.
func FFprobe(bin string) testplayer.SegmentProbe {
	return func(ctx context.Context, seg []byte) ([]testplayer.Packet, error) {
		cmd := exec.CommandContext(ctx, bin, "-v", "error", "-select_streams", "0",
			"-show_entries", "packet=pts_time,duration_time", "-of", "csv=p=0", "-i", "pipe:0")
		cmd.Stdin = bytes.NewReader(seg)
		out, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("ffprobe: %w", err)
		}
		return parseProbeCSV(out), nil
	}
}

// parseProbeCSV reads "pts_time,duration_time" lines, skipping blank or
// incomplete ones (ffprobe can print N/A or a trailing comma).
func parseProbeCSV(out []byte) []testplayer.Packet {
	var pk []testplayer.Packet
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(strings.TrimRight(strings.TrimSpace(line), ","), ",")
		if len(f) < 2 {
			continue
		}
		pts, err1 := strconv.ParseFloat(f[0], 64)
		dur, err2 := strconv.ParseFloat(f[1], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		pk = append(pk, testplayer.Packet{PTS: pts, Dur: dur})
	}
	return pk
}

func TestParseProbeCSV(t *testing.T) {
	got := parseProbeCSV([]byte("1.400000,0.021333\n\nN/A,0.021333\n1.421333,0.021333,\n"))
	if len(got) != 2 || got[0].PTS != 1.4 || got[1].Dur != 0.021333 {
		t.Fatalf("parsed %+v", got)
	}
}
```

`server/internal/e2e/scenarios_test.go` `runScenario`:
- In the `New(t, Options{…})` call add `HLSListSize: sc.WindowSegments,` and `Production: sc.Bowtie != nil && sc.Bowtie.Layout == "production",`.
- Replace `p, err := h.Player(t, sc.Channel)` with:

```go
	cfg := h.PlayerConfig(t, sc.Channel)
	cfg.AllRenditions = true
	cfg.Probe = FFprobe(ffprobePath())
	startAt := time.Now()
	p, err := testplayer.Start(context.Background(), cfg)
	obs.StartLatency = time.Since(startAt)
```

- In the `default:` branch, right after `defer cancel()`, open a coverage window at the end of each fault:

```go
		runStart := time.Now()
		go func() {
			for _, end := range sc.FaultEnds() {
				select {
				case <-ctx.Done():
					return
				case <-time.After(time.Until(runStart.Add(end))):
					p.Mark()
					t.Logf("coverage window opened at %v (a fault is over)", end)
				}
			}
		}()
```

- After `obs.FfmpegRestarts = count.Starts() - 1` add:

```go
		c := r.Consistency
		obs.RenditionsMonotonic = c.RenditionsMonotonic && r.BackwardJumps == 0
		obs.DiscontinuitiesAligned = c.DiscontinuitiesAligned
		obs.DiscontinuitySequenceOK = c.DiscontinuitySequenceOK
		obs.MaxDiscontinuitySequence = c.MaxDiscontinuitySequence
		obs.PDTMonotonic = c.PDTMonotonic
		obs.PDTAligned = c.PDTAligned
		obs.TimelineExcess = r.TimelineExcess
		obs.MaxAudioHole = r.MaxAudioHole
		obs.MaxAVStartSkew = r.MaxAVStartSkew
		obs.ProbedAudio, obs.ProbedVideo = r.ProbedAudio, r.ProbedVideo
		obs.FetchErrors = r.FetchErrors
		obs.AVPairs = r.AVPairs
		obs.UnpairedCoverage = r.UnpairedAfterFaults()
		obs.MaxTailGapRendition, obs.MaxTailGap = r.MaxTailGap()
		obs.Cues, obs.CuesTimed, obs.CueErrors = r.Cues, r.CuesTimed, r.CueErrors
		obs.UntimedCueWindows = r.UntimedCueWindows()
		obs.Renditions = len(r.Renditions)
		obs.RenditionsCovered = true
		obs.MinSegmentsAfterFault = -1
		for _, name := range r.Renditions {
			if r.RenditionSegments[name] == 0 {
				obs.RenditionsCovered = false
			}
			if n := r.SegmentsSinceMark[name]; obs.MinSegmentsAfterFault < 0 || n < obs.MinSegmentsAfterFault {
				obs.MinSegmentsAfterFault = n
			}
		}
		t.Logf("player: since mark %v, total %v, audioHoleBy=%v fetchErrors=%d (playlist %d, segment %d, probe %d) tailGap=%v start=%v",
			r.SegmentsSinceMark, r.RenditionSegments, r.AudioHoleBy, r.FetchErrors, r.PlaylistErrors, r.SegmentErrors, r.ProbeErrors,
			r.TailGap, obs.StartLatency.Round(100*time.Millisecond))
		for i, w := range r.Windows {
			t.Logf("window %d (%v): segments %v paired %v cues %v timed %v", i, w.End.Sub(w.Start).Round(time.Second), w.Segments, w.Paired, w.Cues, w.CuesTimed)
		}
		for _, u := range obs.UnpairedCoverage {
			t.Logf("coverage: %s", u)
		}
		for _, pr := range c.Problems {
			t.Logf("consistency: %s", pr)
		}
		t.Logf("player: renditions=%v excess=%.2fs audioHole=%v avSkew=%v over %d pairs probed=%d/%d cues=%d (%d timed, %d mistimed or unmatched)",
			r.Renditions, r.TimelineExcess, r.MaxAudioHole, r.MaxAVStartSkew, r.AVPairs, r.ProbedAudio, r.ProbedVideo, r.Cues, r.CuesTimed, r.CueErrors)
```

- Replace the xfail block at the end of `runScenario`:

```go
	violations := sc.Expect.Check(obs)
	if sc.Expect.XFail != "" {
		var pinned, other []string
		for _, v := range violations {
			if strings.Contains(v, sc.Expect.XFailOn) {
				pinned = append(pinned, v)
			} else {
				other = append(other, v)
			}
		}
		if len(pinned) == 0 {
			t.Fatalf("known gap fixed (%s): no %q violation; remove xfail from %s", sc.Expect.XFail, sc.Expect.XFailOn, sc.Name)
		}
		t.Logf("xfail (%s): %s", sc.Expect.XFail, strings.Join(pinned, "; "))
		for _, v := range other {
			t.Errorf("unexpected (not the known gap): %s", v)
		}
		return
	}
```

- [ ] **Step 5: Add the scenarios**

Create these files in `server/internal/e2e/testdata/scenarios/`.

`ptsjump.yaml`:

```yaml
name: ptsjump
duration: 45s
timeline:
  # One bad video PES timestamp each time (dirty broadcast): no
  # duplicate-frame storm, no timeline growth, no restart.
  - {at: 8s, fault: pts-jump, by: 3s}
  - {at: 18s, fault: pts-jump, by: 6s}
  - {at: 28s, fault: pts-jump, by: 9s}
expect:
  xfail: "duplicate-frame storm on a single bad PTS (fixed in Task 6)"
  xfailOn: "timeline excess"
  sessionSurvives: true
  sequenceMonotonic: true
  maxFfmpegRestarts: 0
  maxTimelineExcessSeconds: 2
  minTimelineExcessSeconds: -2
  maxAVStartSkewMs: 100
  pairedAfterEachFault: true
  minSegmentsAfterFault: 3
  maxTailGapSeconds: 12
  noFetchErrors: true
```

`ptsjump-ptsonly.yaml` (the PTS is wrong but the DTS is valid; FFmpeg 5.1 computes its correction from the DTS):

```yaml
name: ptsjump-ptsonly
duration: 45s
timeline:
  - {at: 8s, fault: pts-jump, by: 3s, ptsOnly: true}
  - {at: 18s, fault: pts-jump, by: 6s, ptsOnly: true}
  - {at: 28s, fault: pts-jump, by: 9s, ptsOnly: true}
expect:
  xfail: "a single bad PTS (valid DTS) inflates the timeline (fixed in Task 6)"
  xfailOn: "timeline excess"
  sessionSurvives: true
  sequenceMonotonic: true
  maxFfmpegRestarts: 0
  maxTimelineExcessSeconds: 2
  minTimelineExcessSeconds: -2
  maxAVStartSkewMs: 100
  pairedAfterEachFault: true
  minSegmentsAfterFault: 3
  maxTailGapSeconds: 12
  noFetchErrors: true
```

`production-baseline.yaml` (guard: the layout a real server serves, clean signal):

```yaml
name: production-baseline
duration: 40s
bowtie: {layout: production}
expect:
  sessionSurvives: true
  sequenceMonotonic: true
  maxFfmpegRestarts: 0
  renditionsMonotonic: true
  minRenditions: 5   # v480, v360, aac0, ac30, v480_vtt (480i source)
  noFetchErrors: true
  minSegmentsAfterFault: 5
  maxTailGapSeconds: 12
  maxAudioHoleMs: 50
  maxAVStartSkewMs: 100
  pairedAfterEachFault: true
  maxStartSeconds: 10
```

`corrupt-audio.yaml`:

```yaml
name: corrupt-audio
duration: 35s
timeline:
  - {at: 10s, fault: corrupt, type: tei, rate: 0.01, for: 10s}
bowtie: {layout: production}  # AAC and the AC-3 copy are both probed
expect:
  xfail: "audio holes where TEI packets are discarded (fixed in Task 5)"
  xfailOn: "audio hole"
  minSegmentsAfterFault: 3
  maxTailGapSeconds: 12
  noFetchErrors: true
  sessionSurvives: true
  sequenceMonotonic: true
  maxFfmpegRestarts: 0
  maxAudioHoleMs: 50
```

`brief-dropout.yaml`:

```yaml
name: brief-dropout
duration: 40s
timeline:
  # 4 s without packets, under the ingest's 8 s idle timeout.
  - {at: 10s, fault: stall, for: 4s}
expect:
  xfail: "the hole is filled with duplicated frames instead of skipped (fixed in Task 6)"
  xfailOn: "timeline excess"
  minSegmentsAfterFault: 3
  maxTailGapSeconds: 12
  noFetchErrors: true
  sessionSurvives: true
  sequenceMonotonic: true
  maxFfmpegRestarts: 0
  maxTimelineExcessSeconds: -2
  minTimelineExcessSeconds: -7
  maxAudioHoleMs: 50
```

`threshold-dropout.yaml`:

```yaml
name: threshold-dropout
duration: 70s
timeline:
  # Around the ingest's 8 s idle timeout (it reconnects; FFmpeg keeps running).
  - {at: 10s, fault: stall, for: 9s}
  - {at: 35s, fault: stall, for: 12s}
expect:
  xfail: "the holes are filled with duplicated frames instead of skipped (fixed in Task 6)"
  xfailOn: "timeline excess"
  minSegmentsAfterFault: 3
  maxTailGapSeconds: 12
  noFetchErrors: true
  sessionSurvives: true
  sequenceMonotonic: true
  maxFfmpegRestarts: 0
  maxTimelineExcessSeconds: -15
  minTimelineExcessSeconds: -30
  maxAudioHoleMs: 50
```

`splice.yaml`:

```yaml
name: splice
duration: 50s
timeline:
  # Ad-insertion timestamp steps; the content itself continues.
  - {at: 10s, fault: timestamp-jump, by: 1500ms}
  - {at: 20s, fault: timestamp-jump, by: 2500ms}
  - {at: 30s, fault: timestamp-jump, by: -2s}
  - {at: 38s, fault: timestamp-jump, by: 2s}
expect:
  xfail: "steps under FFmpeg's 10 s threshold are filled with duplicates (fixed in Task 6)"
  xfailOn: "timeline excess"
  minSegmentsAfterFault: 3
  maxTailGapSeconds: 12
  noFetchErrors: true
  sessionSurvives: true
  sequenceMonotonic: true
  maxFfmpegRestarts: 0
  maxTimelineExcessSeconds: 1.5
  minTimelineExcessSeconds: -1.5
  maxAudioHoleMs: 50
  maxAVStartSkewMs: 100
  pairedAfterEachFault: true
```

`long-outage-restart.yaml`:

```yaml
name: long-outage-restart
duration: 140s
windowSegments: 8
timeline:
  # 65 s without lock: the tuner sends nothing.
  - {at: 10s, fault: signal, quality: 10, for: 65s}
bowtie:
  # Production layout: every rendition must agree on the restart.
  layout: production
  # Force the append restart inside the outage: the new FFmpeg run starts
  # producing when the signal returns, and the 32 s window later slides
  # past the restart.
  killTranscoderAt: 30s
expect:
  xfail: "FFmpeg serves no DISCONTINUITY-SEQUENCE; Bowtie owns the timeline in Task 10"
  xfailOn: "discontinuity sequence"
  minSegmentsAfterFault: 5
  maxTailGapSeconds: 12
  sessionSurvives: true
  sequenceMonotonic: true
  renditionsMonotonic: true
  minFfmpegRestarts: 1
  minDiscontinuitySequence: 1
  discontinuitySequenceOK: true
```

`discontinuitiesAligned`, `pdtMonotonic` and `pdtAligned` join this scenario in Task 10. Before then, FFmpeg's own PDT and tags after a kill would fail them with violations other than the pinned one.

`stuck-recovers.yaml`:

```yaml
name: stuck-recovers
duration: 80s
bowtie:
  # FFmpeg reads nothing for 40 s, then resumes: drained at 30 s, never killed.
  slowConsumer: {at: 10s, for: 40s}
expect:
  xfail: "Bowtie kills FFmpeg after 30 s without reads (fixed in Task 7)"
  xfailOn: "ffmpeg restarts"
  minSegmentsAfterFault: 3
  maxTailGapSeconds: 12
  sessionSurvives: true
  sequenceMonotonic: true
  maxFfmpegRestarts: 0
```

`stuck-hung.yaml`:

```yaml
name: stuck-hung
duration: 100s
bowtie:
  # FFmpeg never reads again (a wedged encoder): it must still be restarted.
  slowConsumer: {at: 10s, for: 75s}
expect:
  sessionSurvives: true
  minFfmpegRestarts: 1
  minSegmentsAfterFault: 2
  maxTailGapSeconds: 12
```

Append to `transcoder-restart.yaml`'s `expect:` block:

```yaml
  renditionsMonotonic: true
  minSegmentsAfterFault: 3
  maxTailGapSeconds: 12
```

Append to `baseline.yaml` and `baseline-1080i.yaml`'s `expect:` blocks (start latency guard for Task 5's larger probe; progress through the end):

```yaml
  maxStartSeconds: 10
  maxTailGapSeconds: 12
```

`maxTailGapSeconds: 12` (three segments) is on every scenario that requires progress, so a rendition that stops near the end fails even when it delivered enough earlier. `pairedAfterEachFault` is on the scenarios whose faults must not disturb A/V alignment. Each of their fault windows must contain A/V-paired segments in every rendition and discontinuity, so a skew limit can't pass on pre-fault comparisons alone. Task 10 adds it to the restart scenarios. On an xfail scenario these are extra expectations: if one fails before its fix, the run fails as "unexpected (not the known gap)". Record that in the expected-failures table, as its rule says.

Before committing, read the `start=` value that Step 6 logs for both. If either is over 6 s before any FFmpeg change, report it: `playlistTimeout` is 15 s, so there's little headroom.

- [ ] **Step 6: Run the suite and check the pre-fix state**

Run: `cd server && go vet ./... && go test ./internal/testplayer/ ./internal/hdhr/... ./internal/stream/`
Expected: PASS.

Run: `cd server && go test -count=1 -tags ffmpeg -p 1 -parallel 2 -timeout 30m -run TestScenarios -v ./internal/e2e/ 2>&1 | tee "$TMPDIR/scen-task4.log" | grep -E '^\s*--- (PASS|FAIL|SKIP)|xfail \(|known gap|consistency:'`
Expected: every scenario `--- PASS`. Each xfail scenario logs `xfail (…)` with the violation the "Expected failures" table names. `stuck-hung` and `transcoder-restart` pass as guards. Apply the table's rule where reality differs.

- [ ] **Step 7: Commit**

```bash
git add server/internal/hdhr/hdhrfake/scenario server/internal/stream/manager.go server/internal/stream/manager_test.go server/internal/e2e
git commit -m "test(e2e): timeline, rendition and audio checks; new fault scenarios marked xfail

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: FFmpeg live input and audio hardening

**Files:**
- Modify: `server/internal/transcode/ffmpeg.go`
- Test: `server/internal/transcode/ffmpeg_test.go`
- Modify (only if Step 6 is needed): `server/internal/transcode/layout.go` (comment)
- Modify: `server/internal/e2e/testdata/scenarios/corrupt-audio.yaml`, `baseline.yaml`

**Interfaces:**
- Produces: unexported `liveInputArgs() []string` and `const liveAudioFilter = "aresample=async=1:min_hard_comp=0.02:first_pts=0"` in `transcode`. Task 6 adds the timestamp option to `liveInputArgs`.
- Argv contract (live pipe only):
  - `… -thread_queue_size 1024 -probesize 20M -analyzeduration 10M -fflags +discardcorrupt+genpts -err_detect ignore_err -i pipe:0`
  - captions: `-thread_queue_size 1024 -data_field first -f lavfi -i movie='pipe\:3':format_opts='probesize=20M\:analyzeduration=10M'[out0+subcc]`
  - each AAC output n: `-c:a:n aac -ac:a:n 2 -b:a:n Xk -filter:a:n aresample=async=1:min_hard_comp=0.02:first_pts=0`.
  - AC-3 copies: the spec says "every audio output". A stream copy can't be filtered, so Step 5 measures the `ac30` hole on `corrupt-audio`, which runs the production layout.
    - If input hardening alone keeps it ≤ 50 ms, the copies stay copies. Record that in the commit message, with the measured hole.
    - Otherwise, Step 6 re-encodes them: `-c:a:n ac3 -b:a:n 384k -filter:a:n <liveAudioFilter>`. This is the same channel layout, so the 5.1 group stays 5.1.
- Start latency: `-probesize 20M -analyzeduration 10M` are upper bounds, not waits. But on a damaged start they can use up most of `playlistTimeout` (15 s). Step 5 compares the `start=` latency of `baseline`, `baseline-1080i` and `production-baseline` before and after (guard: `maxStartSeconds: 10`). Never raise `analyzeduration` without raising `playlistTimeout` too.

- [ ] **Step 1: Write the failing test**

Append to `server/internal/transcode/ffmpeg_test.go`:

```go
const liveAudio = "aresample=async=1:min_hard_comp=0.02:first_pts=0"

// The live pipe is hardened; URL and VOD (concat) inputs keep their flags.
func TestBuildArgsLiveInputHardening(t *testing.T) {
	d := transcode.Decision{VideoCodec: "h264", VideoEncoder: "libx264", Backend: transcode.BackendSoftware,
		Profile: transcode.Profile{Name: "low", Height: 480, VideoKbps: 1500, AudioKbps: 96}}
	live := transcode.BuildArgs(transcode.JobSpec{Stdin: nonNilStdin(), OutDir: "/tmp/out", D: d})
	mustContainSeq(t, live, "-thread_queue_size", "1024", "-probesize", "20M", "-analyzeduration", "10M",
		"-fflags", "+discardcorrupt+genpts", "-err_detect", "ignore_err", "-i", "pipe:0")
	mustContainSeq(t, live, "-c:a:0", "aac", "-ac:a:0", "2", "-b:a:0", "96k", "-filter:a:0", liveAudio)

	url := transcode.BuildArgs(transcode.JobSpec{InputURL: "http://hdhr/auto/v7.1", OutDir: "/tmp/out", D: d})
	vod := transcode.BuildArgs(transcode.JobSpec{InputURL: "/r/parts.txt", VOD: true, OutDir: "/tmp/out", D: d})
	for name, args := range map[string][]string{"url": url, "vod": vod} {
		for _, a := range args {
			if a == "-err_detect" || a == "-thread_queue_size" || a == "-probesize" || strings.HasPrefix(a, "aresample") {
				t.Errorf("%s input got live-only option %q", name, a)
			}
		}
	}
	mustContainSeq(t, vod, "-fflags", "+discardcorrupt+genpts", "-f", "concat", "-safe", "0", "-i", "/r/parts.txt")
}
```

In `TestBuildArgsLadderQSVWithAudioAndCaptions`, replace the caption and audio assertions with:

```go
	mustContainSeq(t, got, "-thread_queue_size", "1024", "-data_field", "first", "-f", "lavfi", "-i",
		"movie='pipe\\:3':format_opts='probesize=20M\\:analyzeduration=10M'[out0+subcc]")
	mustContainSeq(t, got, "-c:a:0", "aac", "-ac:a:0", "2", "-b:a:0", "128k", "-filter:a:0", liveAudio,
		"-c:a:1", "aac", "-ac:a:1", "2", "-b:a:1", "128k", "-filter:a:1", liveAudio,
		"-c:a:2", "copy", "-c:a:3", "copy")
	for _, a := range got {
		if a == "-filter:a:2" || a == "-filter:a:3" {
			t.Fatalf("AC-3 copy got an audio filter: %q", got)
		}
	}
```

In `TestCommandWiresStdin`, change the `-fflags` check to `containsAdjacent(cmd.Args, "-fflags", "+discardcorrupt+genpts")` and its message to match.

Update the five goldens in `TestBuildArgsStdinPipeAcrossBackends` (input options, plus the filter after each AAC line), only inside that function:

```bash
cd server && perl -i -pe 'if (/^func TestBuildArgsStdinPipeAcrossBackends/../^}/) {
  s/"-fflags", "\+discardcorrupt", "-i", "pipe:0",/"-thread_queue_size", "1024", "-probesize", "20M", "-analyzeduration", "10M",\n\t\t\t\t"-fflags", "+discardcorrupt+genpts", "-err_detect", "ignore_err", "-i", "pipe:0",/;
  s/("-c:a:0", "aac", "-ac:a:0", "2", "-b:a:0", "\d+k",)$/$1\n\t\t\t\t"-filter:a:0", "aresample=async=1:min_hard_comp=0.02:first_pts=0",/;
}' internal/transcode/ffmpeg_test.go && gofmt -l internal/transcode/
```

Also update the comment above that test to `// backends emit the hardened live input (hw flags unchanged; URL absent).`

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd server && go test ./internal/transcode/ -run 'TestBuildArgs|TestCommandWiresStdin' -v`
Expected: FAIL: `TestBuildArgsLiveInputHardening` (args missing), the five `TestBuildArgsStdinPipeAcrossBackends` subtests, the ladder test and `TestCommandWiresStdin`.

Before changing `ffmpeg.go`, record the current start latencies (the baseline for Step 5):

Run: `cd server && go test -count=1 -tags ffmpeg -p 1 -parallel 2 -timeout 30m -run 'TestScenarios/(baseline|baseline-1080i|production-baseline)$' -v ./internal/e2e/ 2>&1 | grep -E 'start='`

- [ ] **Step 3: Implement**

In `server/internal/transcode/ffmpeg.go`, add above `BuildArgs`:

```go
// liveAudioFilter keeps live AAC sample-continuous: it pads holes (discarded
// packets, re-based timestamps) with silence and trims overlaps, so audio
// stays on the video's timestamps. AC-3 outputs are copies and can't be filtered.
const liveAudioFilter = "aresample=async=1:min_hard_comp=0.02:first_pts=0"

// liveInputArgs harden the live pipe (dirty ATSC transport streams): genpts
// fills missing timestamps, ignore_err keeps decoding past damaged frames,
// the larger probe still finds every PMT stream when the first seconds are
// damaged, and the thread queue absorbs the burst after a stall.
func liveInputArgs() []string {
	return []string{
		"-thread_queue_size", "1024", "-probesize", "20M", "-analyzeduration", "10M",
		"-fflags", "+discardcorrupt+genpts", "-err_detect", "ignore_err",
	}
}
```

Replace the pipe-input branch:

```go
	if s.Stdin != nil {
		// Pipe input from ingest fan-out.
		args = append(args, liveInputArgs()...)
		args = append(args, "-i", "pipe:0")
	} else if s.VOD {
```

Replace the caption input line:

```go
		args = append(args, "-thread_queue_size", "1024", "-data_field", "first", "-f", "lavfi", "-i",
			"movie='pipe\\:3':format_opts='probesize=20M\\:analyzeduration=10M'[out0+subcc]")
```

Replace the AAC loop:

```go
	for range aac {
		args = append(args,
			fmt.Sprintf("-c:a:%d", n), "aac",
			fmt.Sprintf("-ac:a:%d", n), "2",
			fmt.Sprintf("-b:a:%d", n), fmt.Sprintf("%dk", akbps),
		)
		if s.Stdin != nil {
			args = append(args, fmt.Sprintf("-filter:a:%d", n), liveAudioFilter)
		}
		n++
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd server && go test ./internal/transcode/ -v -run 'TestBuildArgs|TestCommand'` then `cd server && go test -tags ffmpeg ./internal/transcode/`
Expected: PASS. The `ffmpeg`-tagged run executes the new main-input argv on the host FFmpeg. It doesn't cover the caption `movie` input, because `ffmpeg_e2e_test.go` never sets `Layout.Captions`. Captions run for real in the `production-baseline` and `corrupt-audio` scenarios (Step 5), and on FFmpeg 5.1 in Task 6's in-image run.

- [ ] **Step 5: Re-run the scenarios, measure the AC-3 copy and start latency**

Run: `cd server && go test -count=1 -tags ffmpeg -p 1 -parallel 2 -timeout 30m -run 'TestScenarios/(corrupt-audio|baseline|baseline-1080i|production-baseline|brief-dropout|threshold-dropout)$' -v ./internal/e2e/ 2>&1 | grep -E -- '--- |xfail \(|known gap|observed:|audioHoleBy=|start='`

Expected:
- Start latency stays under 10 s, and within 1 s of Step 2's numbers. If it grows by more, stop and report.
- Look at `audioHoleBy` for `corrupt-audio`:
  - If `aac0` ≤ 50 ms **and** `ac30` ≤ 50 ms, `corrupt-audio` fails with `known gap fixed … remove xfail`. Delete its `xfail:`/`xfailOn:` lines and skip Step 6. Note "AC-3 copy: input hardening alone keeps holes ≤ 50 ms (measured N ms)" for the commit message.
  - If `ac30` > 50 ms, do Step 6.
- `brief-dropout` and `threshold-dropout` still log `xfail (…)` with a timeline-excess violation.

Add `maxAudioHoleMs: 50` to `baseline.yaml`'s `expect:` block. Re-run the same command: all PASS (or only `corrupt-audio` still xfail, pending Step 6).

- [ ] **Step 6: Only if the AC-3 copy kept holes: re-encode AC-3 with the filter**

Add to `TestBuildArgsLiveInputHardening` a case for a layout with an AC-3 track:

```go
	l := transcode.Layout{Rungs: []transcode.Rung{{Height: 480, VideoKbps: 1500}},
		Audio: []transcode.AudioTrack{{Lang: "eng", AC3: true}}, AC3Copy: true}
	ac3 := transcode.BuildArgs(transcode.JobSpec{Stdin: nonNilStdin(), OutDir: "/tmp/out", D: d, Layout: l})
	mustContainSeq(t, ac3, "-c:a:1", "ac3", "-b:a:1", "384k", "-filter:a:1", liveAudio)
```

and in `TestBuildArgsLadderQSVWithAudioAndCaptions` change `"-c:a:2", "copy", "-c:a:3", "copy"` to `"-c:a:2", "ac3", "-b:a:2", "384k", "-filter:a:2", liveAudio, "-c:a:3", "ac3", "-b:a:3", "384k", "-filter:a:3", liveAudio`. Then drop that test's "AC-3 copy got an audio filter" loop.

Run: `cd server && go test ./internal/transcode/ -run TestBuildArgs -v`
Expected: FAIL.

In `ffmpeg.go`, replace the AC-3 loop:

```go
	for range l.AC3Indexes() {
		if s.Stdin != nil {
			// Live: re-encode so the filter can fill holes (a copy can't be
			// filtered); same layout, so the 5.1 group stays 5.1.
			args = append(args, fmt.Sprintf("-c:a:%d", n), "ac3", fmt.Sprintf("-b:a:%d", n), "384k",
				fmt.Sprintf("-filter:a:%d", n), liveAudioFilter)
		} else {
			args = append(args, fmt.Sprintf("-c:a:%d", n), "copy")
		}
		n++
	}
```

and rename the comments that call the 5.1 group a "copy" where they describe live sessions (`layout.go`'s `AC3Tracks` comment: "copied (VOD) or re-encoded (live) into the 5.1 group").

Run: `cd server && go test ./internal/transcode/ && go test -count=1 -tags ffmpeg -p 1 -parallel 2 -timeout 30m -run 'TestScenarios/(corrupt-audio|production-baseline)$' -v ./internal/e2e/ 2>&1 | grep -E -- '--- |known gap|audioHoleBy='`
Expected: unit tests PASS; `corrupt-audio` reports `known gap fixed`. Remove its `xfail:`/`xfailOn:` lines, re-run: PASS with `ac30` ≤ 50 ms. If `ac30` still exceeds 50 ms, stop and report the values. Don't weaken the requirement.

- [ ] **Step 7: Commit**

```bash
git add server/internal/transcode server/internal/e2e/testdata/scenarios
git commit -m "fix(transcode): harden the live pipe input and keep live AAC sample-continuous

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Gate the timestamp option on FFmpeg 5.1 and apply it

**Files:**
- Create: `server/scripts/ffmpeg51` and `server/scripts/ffmpeg-soak` (executable; Task 13 uses `ffmpeg-soak` too)
- Create: `server/internal/e2e/rebases_test.go`, `server/internal/e2e/otasignal_test.go`, `server/internal/e2e/otaclips_test.go`
- Create: `server/internal/e2e/testdata/scenarios/{rebase-control,faults-production}.yaml`
- Modify: `server/internal/e2e/scenarios_test.go` (`runScenario` returns the source's start and logs the FFmpeg log summary)
- Modify: `server/internal/transcode/ffmpeg.go` (`liveInputArgs`, or for candidate C `videoGraph`, the caption input and the audio filters)
- Test: `server/internal/transcode/ffmpeg_test.go`
- Modify: `server/internal/e2e/testdata/scenarios/{ptsjump,ptsjump-ptsonly,splice,brief-dropout,threshold-dropout,faults-production,baseline}.yaml`
- Modify: `docs/dev/fake-hdhomerun.md` (running the suite on FFmpeg 5.1, in the image, and on OTA clips)

**Interfaces:**
- Consumes:
  - `liveInputArgs()` and `liveAudioFilter` (Task 5);
  - the scenarios, `runScenario`, `pairedAfterEachFault`, `maxTailGapSeconds` and `captionsTimed` (Task 4);
  - the testplayer cue checks (Task 3).
- Produces:
  - `server/scripts/ffmpeg51`: a drop-in `BOWTIE_FFMPEG_PATH` that runs `debian:bookworm-slim`'s FFmpeg 5.1.9 in Docker.
    - `FF51_LOGLEVEL` replaces the `-loglevel` value.
    - `FF51_LOGDIR`, when set, makes it write an `argv: …` line to `$FF51_LOGDIR/ffmpeg-<pid>.log`, followed by every stderr line matching `timestamp discontinuity|frames duplicated| dup!|Invalid frame dimensions|\[(warning|error|fatal)\]`, each prefixed with the Unix time in milliseconds.
    - It refuses the fd-3 caption input; the production layout runs in the image (Step 5).
    - It has no way to inject arguments. A candidate exists only as a scratch branch whose `BuildArgs` emits it, and the logged argv proves which one ran.
  - `server/scripts/ffmpeg-soak`: the in-image wrapper for test and soak images only, never production.
    - It raises `-loglevel warning` to `level+debug`. FFmpeg 5.1 logs "timestamp discontinuity" at debug level and each duplicate burst (`*** N dup!`) at verbose level.
    - It keeps only those lines, and writes the same `argv:` and Unix-ms lines as `ffmpeg51`.
  - `ffLogSince(t, start) ffLog` and `rebasesSince(t, start) []rebase` (`rebases_test.go`, build tag `ffmpeg`).
    - `type ffLog struct { Rebases []rebase; DupFrames, MaxDupBurst, DupWarnings int; Argv []string }`.
    - `type rebase struct { At time.Duration; Line string }`.
  - `runScenario(t, sc) time.Time` returns when the fake's channel timeline started, which is a clip's offset 0. It logs `ff: rebases=N dupFrames=N maxDupBurst=N dupWarnings=N` and each `rebase at +…`.
  - `TestOTAClips` (build tag `ffmpeg`) plays real OTA clips through the production layout. It fails on any re-base not within ±10 s of a signal dip in the capture's tuner log.
    - Clips are named `ch<N>.ts`, or `ch<N>@<seconds>.ts` for a piece cut that far into the capture.
    - Environment: `BOWTIE_OTA_CLIPS` (skips when unset), `BOWTIE_OTA_SIGNAL_DIR` (required with it), `BOWTIE_OTA_CLIP_FILES`, `BOWTIE_OTA_FOR`.
  - `TestOTAClipsWithFaults` (build tag `ffmpeg`) is the caption row. It injects the `faults-production` steps into one real clip with captions (`BOWTIE_OTA_FAULT_CLIP`; skips when unset) and requires `captionsTimed`, so cues keep arriving after every fault and stay on the video's timeline.
  - `rebase-control.yaml`: a +15 s step, over FFmpeg's 10 s default, that every candidate must log as a re-base. This is the positive control for the logging.
  - `faults-production.yaml`: `ptsjump` and `splice` steps on the production layout (ladder, AAC, the AC-3 output, captions). It is xfail until this task's fix.
  - The chosen option, recorded in `ffmpeg.go` and its comment.

**Candidates.** Each is a scratch branch off this task's Step 2 commit, carrying Task 5's argv plus:
- **A**: `-dts_delta_threshold 1` in `liveInputArgs`. It is likely to re-base the ±1.5–2.5 s splices, which fails the gate.
- **A′**: `-dts_delta_threshold 2.6`, above every splice step and below the smallest `ptsjump` step (+3 s).
- **C**: `-fps_mode:v vfr` plus a hole-closing clamp. A step over 0.5 s is closed frame by frame, without duplicating frames or logging a re-base, while `aresample` still fills smaller audio holes. Every live output gets the clamp:
  - **Video, both layouts.** `setpts='if(eq(N\,0)\,PTS\,PREV_OUTPTS+clip(PTS-PREV_INPTS\,1\,0.5/TB))'` is the first filter of `videoGraph`'s `[0:v]` chain, ahead of the deinterlacer. The one-rung chain and the ladder's `split` both start there (`ffmpeg.go:84` always uses `-filter_complex`). `setpts` only rewrites timestamps and has no format restriction, so it runs on hardware frames too.
  - **AAC outputs.** `asetpts='if(eq(N\,0)\,PTS\,PREV_OUTPTS+clip(PTS-PREV_INPTS\,0\,0.5/TB))'` goes ahead of `aresample`.
  - **AC-3 outputs.** A stream copy can't be filtered, and an unclamped copy would drift from the clamped video by every step. C therefore re-encodes AC-3 (`-c:a:n ac3 -b:a:n 384k`) with the same audio clamp ahead of `liveAudioFilter`.
  - **Captions.** The caption input decodes the same TS from fd 3 and sees the same steps. C clamps it inside its graph, `movie='pipe\:3',<video clamp>[out0+subcc]`. The cue times come from those frames' timestamps, so they follow the corrected video.

**Captions under A and A′.** `-dts_delta_threshold` acts on the demuxer of input 0 only. The `lavfi` caption input has no `AVFMT_TS_DISCONT`, so FFmpeg never re-bases it. After a step that A or A′ re-bases, the cues may sit off the video by the size of the step. The caption row (`TestOTAClipsWithFaults`, `captionsTimed`) measures this for every candidate rather than assuming it. No candidate is excused from captions.

**The gate.** A candidate passes only if every row holds on FFmpeg 5.1.

| Check | Required |
|---|---|
| argv proof | every live FFmpeg run in the candidate's logs (`argv:` lines with `pipe:0`) carries its marker: A `-dts_delta_threshold 1 `, A′ `-dts_delta_threshold 2.6`, C `PREV_OUTPTS`. The base carries none |
| positive controls | Re-bases and duplicates get separate controls, because a step that FFmpeg re-bases leaves no gap and so duplicates no frames. **Re-base:** `rebase-control` logs at least one re-base, in both halves, for every candidate. **Duplicates:** the **base** candidate's `ptsjump` shows `dupFrames` ≥ 60 (its xfail is that duplicate storm). If either control reads zero, the logging is broken: stop and fix it before judging anything |
| `ptsjump`, `ptsjump-ptsonly` (+3/+6/+9 s) | every expectation: \|excess\| ≤ 2 s, ≤ 50 ms audio holes, ≤ 100 ms A/V skew paired after **each** fault, no restarts, progress to the end |
| no duplicate storm | in `ptsjump`, `ptsjump-ptsonly` and `splice`: zero `frames duplicated` warnings, no `dup!` burst over 15 frames (½ s), and total duplicated frames ≤ the same candidate's `baseline` total + 30 |
| `splice` (±1.5–2.5 s steps) | every expectation (\|excess\| ≤ 1.5 s, audio holes, skew paired after each fault), **and zero `timestamp discontinuity` lines** during the run |
| `brief-dropout`, `threshold-dropout`, `baseline` | every expectation |
| `production-baseline`, `corrupt-audio`, `faults-production` (image) | every expectation; `faults-production` pairs `aac0` **and `ac30`** with the video after each fault |
| caption row (image) | `TestOTAClipsWithFaults` passes: every expectation, `captionsTimed`, `pairedAfterEachFault` |
| OTA clips (image) | `TestOTAClips` passes: no restarts, excess ≤ +2 s, audio holes ≤ 50 ms, skew ≤ 100 ms, progress to the end, and no re-base outside ±10 s of a signal dip in the capture's tuner log (a re-base anywhere else is a false re-base, such as an ad break) |

**Choosing:**
- If several candidates pass, prefer C. It is the `fps_mode` family, which the spec prefers ("less magic").
- Between A and A′, prefer A′ (it doesn't fire on splices).
- If none passes, stop. Report the full table: candidate × check, with values, re-base counts and duplicate counts.
  - The splice requirements may conflict: zero re-bases and no inflation on a pure timestamp step.
  - The caption row may rule out A and A′ outright.
  - Either way it is the controller's call, not this task's. Don't loosen a threshold, and don't pick a "least bad" option.
- There is no later "raise the threshold" step: the Task 13 soak confirms the choice or sends it back here.

**Deadlines.** Every command below is bounded:
- Go runs pass `-timeout` (`-test.timeout` in the image). That bounds the tests, not the stages around them.
- Every stage also runs under an outer deadline, `dl <seconds>`. It is a `perl alarm` wrapper (macOS has no `timeout`): the alarm survives `exec`, so the command is killed with SIGALRM and `dl` exits 142.
  - Standalone FFmpeg and ffprobe calls. `ffmpeg51`'s watchdog kills the container when the wrapper dies.
  - `docker build` (30 min) and `go test -c` (10 min).
  - The scenario half's `go test` (125 min, over its 120 min `-timeout`).
  - The image half's `docker run` (305 min, over its 300 min `-test.timeout`). Killing the docker client leaves the container running, so the run is named `gate-<candidate>-run` and is always removed with `docker rm -f` afterwards.
- A stage that fails or hits its deadline prints one line, `GATE-ERROR <candidate> <stage> exit=<code>`, switches back to the task branch and exits 1. A test run that completes with failing tests is not a stage failure: those verdicts are the gate's data. A test stage fails only when it exits with anything other than 0 or 1, or its log has no `--- ` result line.
- Every FFmpeg call that writes a file passes `-y`.
- `clipDuration` uses a 30 s context.

- [ ] **Step 1: Create the wrappers and smoke-test 5.1**

Create `server/scripts/ffmpeg51` and `chmod +x` it:

```bash
#!/usr/bin/env bash
# ffmpeg51: FFmpeg 5.1 from Debian bookworm (the production image's FFmpeg),
# run in Docker as a drop-in BOWTIE_FFMPEG_PATH for e2e tests on a Mac:
#
#   BOWTIE_FFMPEG_PATH=$PWD/scripts/ffmpeg51 go test -tags ffmpeg ./internal/e2e/
#
# Env:
#   FF51_LOGLEVEL    replaces the value after -loglevel (e.g. level+debug)
#   FF51_LOGDIR      append the argv, then re-base/dup/warning lines, each
#                    prefixed with Unix ms, to $FF51_LOGDIR/ffmpeg-<pid>.log
#                    (stderr still passes through)
#
# Candidates are never injected here: each gate candidate is a scratch
# branch whose BuildArgs emits its options, and the logged argv proves it.
#
# $TMPDIR, the synthetic-source cache and $PWD are mounted at the same paths,
# so absolute paths work. Bowtie stops FFmpeg with SIGKILL, which kills this
# script but not the container, so a watchdog kills the container when this
# script dies. The caption input (fd 3) can't cross into the container: the
# production layout runs on 5.1 inside the image instead (see the docs).
set -u
image=bowtie-ff51:bookworm
if ! docker image inspect "$image" >/dev/null 2>&1; then
  printf 'FROM debian:bookworm-slim\nRUN apt-get update && apt-get install -y --no-install-recommends ffmpeg && rm -rf /var/lib/apt/lists/*\n' |
    docker build -q -t "$image" - >/dev/null || exit 1
fi

args=()
prev=""
for a in "$@"; do
  case "$a" in
  *'pipe\:3'*)
    echo "ffmpeg51: the caption input on fd 3 can't run through Docker" >&2
    exit 2
    ;;
  esac
  if [ "$prev" = "-loglevel" ] && [ -n "${FF51_LOGLEVEL:-}" ]; then
    a=$FF51_LOGLEVEL
  fi
  args+=("$a")
  prev=$a
done

mounts=()
for d in "${TMPDIR:-/tmp}" "$HOME/Library/Caches/bowtie-fakehdhr" "$PWD"; do
  if [ -d "$d" ]; then
    mounts+=(-v "$d:$d")
  fi
done

name="ffmpeg51-$$-$RANDOM"
if [ -n "${FF51_LOGDIR:-}" ]; then
  mkdir -p "$FF51_LOGDIR"
  log="$FF51_LOGDIR/ffmpeg-$$.log"
  perl -MTime::HiRes=time -e 'printf "%d argv: %s\n", time() * 1000, join(" ", @ARGV)' -- ${args[@]+"${args[@]}"} >>"$log"
  exec 8> >(perl -MTime::HiRes=time -ne '
    BEGIN { $| = 1; open(L, ">>", shift @ARGV) or die "ffmpeg51: log: $!"; select((select(L), $| = 1)[0]) }
    print STDERR $_;
    printf L "%d %s", time() * 1000, $_ if /timestamp discontinuity|frames duplicated| dup!|Invalid frame dimensions|\[(warning|error|fatal)\]/;
  ' "$log")
else
  exec 8>&2
fi
# A background job's stdin is /dev/null unless redirected explicitly.
exec 9<&0
docker run --rm -i --init --network none --name "$name" -w "$PWD" \
  ${mounts[@]+"${mounts[@]}"} "$image" ffmpeg ${args[@]+"${args[@]}"} <&9 2>&8 &
child=$!
exec 9<&-
self=$$
(
  while kill -0 "$self" 2>/dev/null; do sleep 0.2; done
  docker kill "$name" >/dev/null 2>&1
) </dev/null >/dev/null 2>&1 &
wait "$child"
```

Create `server/scripts/ffmpeg-soak` and `chmod +x` it:

```bash
#!/bin/bash
# ffmpeg-soak: FFmpeg for test and soak images only (never production).
# Raises "-loglevel warning" to level+debug so FFmpeg 5.1's "timestamp
# discontinuity" lines (debug) and "*** N dup!" bursts (verbose) are logged,
# and keeps only the lines that the gate and the soak count. With
# FF51_LOGDIR set, also writes the argv (proof of which candidate ran) and
# those lines, each prefixed with Unix ms, to $FF51_LOGDIR/ffmpeg-<pid>.log,
# in the format scripts/ffmpeg51 writes.
args=()
prev=""
for a in "$@"; do
  if [ "$prev" = "-loglevel" ] && [ "$a" = "warning" ]; then
    a="level+debug"
  fi
  args+=("$a")
  prev=$a
done
log=""
if [ -n "${FF51_LOGDIR:-}" ]; then
  mkdir -p "$FF51_LOGDIR"
  log="$FF51_LOGDIR/ffmpeg-$$.log"
  printf '%s argv: %s\n' "$(date +%s%3N)" "${args[*]}" >>"$log"
fi
# keep prints the counted lines to stderr and, with FF51_LOGDIR, to $log.
keep() {
  while IFS= read -r line; do
    case "$line" in
    *"timestamp discontinuity"* | *"frames duplicated"* | *" dup!"* | *"Invalid frame dimensions"* | *"[warning]"* | *"[error]"* | *"[fatal]"*)
      printf '%s\n' "$line" >&2
      if [ -n "$log" ]; then printf '%s %s\n' "$(date +%s%3N)" "$line" >>"$log"; fi
      ;;
    esac
  done
}
exec /usr/bin/ffmpeg "${args[@]}" 2> >(keep)
```

The stderr filter is a function (`>(keep)`), not an inline `case` inside `>( … )`. An inline case's `pattern)` closes the process substitution early in some bash versions ("bad substitution").

Smoke tests (`dl` bounds each one):

```bash
cd server && dl() { perl -e 'alarm shift @ARGV; exec @ARGV or die "exec $ARGV[0]: $!\n"' "$@"; }
dl 600 scripts/ffmpeg51 -version | head -1
```

Expected: `ffmpeg version 5.1.9-0+deb12u1 …` (the first run builds the image).

Run Task 5's argv, every candidate's options (both C layouts, through `-filter_complex` as `BuildArgs` emits them) and both caption-graph forms on 5.1:

```bash
cd server && dl() { perl -e 'alarm shift @ARGV; exec @ARGV or die "exec $ARGV[0]: $!\n"' "$@"; } && \
d="${TMPDIR%/}/ff51-flags" && mkdir -p "$d" && \
V="setpts='if(eq(N\,0)\,PTS\,PREV_OUTPTS+clip(PTS-PREV_INPTS\,1\,0.5/TB))'" && \
A="asetpts='if(eq(N\,0)\,PTS\,PREV_OUTPTS+clip(PTS-PREV_INPTS\,0\,0.5/TB))'" && \
R="aresample=async=1:min_hard_comp=0.02:first_pts=0" && \
dl 120 scripts/ffmpeg51 -y -hide_banner -loglevel error -f lavfi -i testsrc=d=2:s=320x240 -f lavfi -i sine=d=2 \
  -c:v mpeg2video -c:a ac3 -f mpegts "$d/in.ts" && \
for opt in "" "-dts_delta_threshold 1" "-dts_delta_threshold 2.6"; do
  dl 120 scripts/ffmpeg51 -y -hide_banner -loglevel warning -nostats $opt \
    -thread_queue_size 1024 -probesize 20M -analyzeduration 10M -fflags +discardcorrupt+genpts \
    -err_detect ignore_err -i pipe:0 -filter_complex "[0:v]yadif=0:-1:0,scale=-2:240[v0]" -map "[v0]" -map 0:a \
    -c:v libx264 -c:a:0 aac -ac:a:0 2 -b:a:0 96k -filter:a:0 "$R" -f null - < "$d/in.ts" || exit 1
done && \
dl 120 scripts/ffmpeg51 -y -hide_banner -loglevel warning -nostats -i "$d/in.ts" \
  -filter_complex "[0:v]$V,yadif=0:-1:0,scale=-2:240[v0]" -map "[v0]" -map 0:a -map 0:a -fps_mode:v vfr \
  -c:v libx264 -c:a:0 aac -filter:a:0 "$A,$R" -c:a:1 ac3 -b:a:1 384k -filter:a:1 "$A,$R" -f null - && \
dl 120 scripts/ffmpeg51 -y -hide_banner -loglevel warning -nostats -i "$d/in.ts" \
  -filter_complex "[0:v]$V,yadif=0:-1:0,split=2[s0][s1];[s0]scale=-2:240[v0];[s1]scale=-2:120[v1]" \
  -map "[v0]" -map "[v1]" -fps_mode:v vfr -c:v libx264 -f null - && \
for cc in "movie='$d/in.ts'" "movie='$d/in.ts',$V"; do
  dl 120 scripts/ffmpeg51 -y -hide_banner -loglevel warning -thread_queue_size 1024 -data_field first -f lavfi \
    -i "$cc[out0+subcc]" -map 0:v -f null - || exit 1
done && echo FLAGS-OK
```

Expected: `FLAGS-OK`, with no `Unrecognized option`, `Option not found` or expression parse errors. (The synthetic file has no captions; the last loop proves that both caption graphs parse.) If C's expressions or graphs don't parse on 5.1, C is out of the gate. Record that and continue with A and A′.

Kill test: the container must die when the wrapper is SIGKILLed.

```bash
cd server && (scripts/ffmpeg51 -hide_banner -f lavfi -re -i testsrc -t 60 -f null - & pid=$!; sleep 3; kill -9 $pid; sleep 2; docker ps --filter name=ffmpeg51- --format '{{.Names}}' | wc -l)
```

Expected: `0`.

Log test. Re-base and duplication get separate positive controls, because FFmpeg corrects a re-based step before encoding, so a re-based step leaves no gap for CFR to fill:
- `jump.ts` has a 20 s step, over the 10 s default. It must log a re-base.
- `gap.ts` has a 3 s step, under the default. FFmpeg keeps the gap, and CFR output at 25 fps fills it with duplicates.

```bash
cd server && dl() { perl -e 'alarm shift @ARGV; exec @ARGV or die "dl: exec $ARGV[0]: $!\n"' "$@"; } && \
d="${TMPDIR%/}/ff51-flags" && mkdir -p "$d" && \
for k in jump:20 gap:3; do
  n=${k%%:*} step=${k#*:}
  dl 120 scripts/ffmpeg51 -y -hide_banner -loglevel error -f lavfi -i testsrc=d=6:s=320x240 \
    -vf "setpts=PTS+if(gte(T\,3)\,$step/TB\,0)" -c:v mpeg2video -f mpegts "$d/$n.ts" || { echo "GATE-ERROR logtest make-$n"; break; }
  L="${TMPDIR%/}/ff51-logtest-$n"; rm -rf "$L"
  FF51_LOGDIR="$L" FF51_LOGLEVEL=level+debug dl 120 scripts/ffmpeg51 -hide_banner -loglevel warning \
    -i "$d/$n.ts" -c:v libx264 -fps_mode:v cfr -r 25 -f null -
  echo "$n: argv=$(cat "$L"/*.log | grep -c ' argv: ') rebases=$(cat "$L"/*.log | grep -c 'timestamp discontinuity') dups=$(cat "$L"/*.log | grep -c ' dup!')"
done
```

Expected:
- `jump: argv=1 rebases=` at least 1. Its `dups` count doesn't matter: the re-base removed the gap.
- `gap: argv=1 rebases=0 dups=` at least 1. A 3 s gap at 25 fps is about 75 duplicated frames.

If a count is 0, fix the wrapper's grep or log level before going on. The gate depends on these lines.

- [ ] **Step 2: Log readers, OTA clip tests and the two gate scenarios**

Create `server/internal/e2e/rebases_test.go`:

```go
//go:build ffmpeg

package e2e

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// rebase is one FFmpeg "timestamp discontinuity" line, offset from a start.
type rebase struct {
	At   time.Duration
	Line string
}

// ffLog is what the FFmpeg wrappers (scripts/ffmpeg51, scripts/ffmpeg-soak)
// logged to FF51_LOGDIR after a start.
type ffLog struct {
	Rebases []rebase
	// DupFrames sums FFmpeg 5.1's per-burst "*** N dup!" lines (verbose
	// level); MaxDupBurst is the largest N; DupWarnings counts "More than
	// 1000 frames duplicated".
	DupFrames, MaxDupBurst, DupWarnings int
	// Argv are the "argv:" lines, one per FFmpeg run.
	Argv []string
}

var dupRe = regexp.MustCompile(`\*\*\* (\d+) dup!`)

// ffLogSince reads FF51_LOGDIR for lines logged at or after start. The
// zero ffLog when FF51_LOGDIR is unset.
func ffLogSince(t testing.TB, start time.Time) ffLog {
	t.Helper()
	var out ffLog
	dir := os.Getenv("FF51_LOGDIR")
	if dir == "" {
		return out
	}
	files, _ := filepath.Glob(filepath.Join(dir, "ffmpeg-*.log"))
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(fh)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			ms, rest, ok := strings.Cut(sc.Text(), " ")
			v, err := strconv.ParseInt(ms, 10, 64)
			if !ok || err != nil {
				continue
			}
			at := time.UnixMilli(v)
			if at.Before(start) {
				continue
			}
			switch {
			case strings.HasPrefix(rest, "argv: "):
				out.Argv = append(out.Argv, strings.TrimPrefix(rest, "argv: "))
			case strings.Contains(rest, "timestamp discontinuity"):
				out.Rebases = append(out.Rebases, rebase{At: at.Sub(start), Line: rest})
			case strings.Contains(rest, "frames duplicated"):
				out.DupWarnings++
			default:
				if m := dupRe.FindStringSubmatch(rest); m != nil {
					n, _ := strconv.Atoi(m[1])
					out.DupFrames += n
					if n > out.MaxDupBurst {
						out.MaxDupBurst = n
					}
				}
			}
		}
		_ = fh.Close()
	}
	return out
}

// rebasesSince is the re-bases of ffLogSince.
func rebasesSince(t testing.TB, start time.Time) []rebase {
	t.Helper()
	return ffLogSince(t, start).Rebases
}

func TestFFLogSince(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FF51_LOGDIR", dir)
	start := time.UnixMilli(1_760_000_000_000)
	body := "1759999999000 [debug] [mpegts @ 0x1] timestamp discontinuity (stream id=0): -270000, new offset= 0\n" +
		"1760000000001 argv: -hide_banner -loglevel level+debug -i pipe:0 -dts_delta_threshold 2.6\n" +
		"1760000002500 [debug] timestamp discontinuity (stream id=0): 270000, new offset= -270000\n" +
		"1760000003000 [verbose] *** 2 dup!\n" +
		"1760000003100 [verbose] *** 87 dup!\n" +
		"1760000004000 [warning] More than 1000 frames duplicated\n"
	if err := os.WriteFile(filepath.Join(dir, "ffmpeg-1.log"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := ffLogSince(t, start)
	if len(got.Rebases) != 1 || got.Rebases[0].At != 2500*time.Millisecond {
		t.Fatalf("rebases %+v", got.Rebases)
	}
	if got.DupFrames != 89 || got.MaxDupBurst != 87 || got.DupWarnings != 1 {
		t.Fatalf("dups %d (max %d), warnings %d", got.DupFrames, got.MaxDupBurst, got.DupWarnings)
	}
	if len(got.Argv) != 1 || !strings.Contains(got.Argv[0], "-dts_delta_threshold 2.6") {
		t.Fatalf("argv %q", got.Argv)
	}
}
```

Create `server/internal/e2e/otasignal_test.go`:

```go
//go:build ffmpeg

package e2e

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// rebaseDipWindow is how close to a logged signal dip a re-base must be to
// count as true (the TSVs sample the tuner every 10 s).
const rebaseDipWindow = 10 * time.Second

// otaClip is a clip file named ch<N>.ts, or ch<N>@<seconds>.ts for a piece
// cut that many seconds into the capture.
type otaClip struct {
	Channel string
	Offset  time.Duration
}

func parseClipName(base string) (otaClip, error) {
	name := strings.TrimSuffix(base, ".ts")
	if !strings.HasPrefix(name, "ch") || name == base {
		return otaClip{}, fmt.Errorf("clip %q: want ch<N>.ts or ch<N>@<seconds>.ts", base)
	}
	ch, off, cut := strings.Cut(strings.TrimPrefix(name, "ch"), "@")
	c := otaClip{Channel: ch}
	if cut {
		sec, err := strconv.ParseFloat(off, 64)
		if err != nil {
			return otaClip{}, fmt.Errorf("clip %q: offset %q", base, off)
		}
		c.Offset = time.Duration(sec * float64(time.Second))
	}
	return c, nil
}

// signalLog is a capture's tuner log, ch<N>.signal.tsv: one row per poll,
// "unix-seconds  tuner  strength%  quality%  symbol-quality%  rate". The
// first row is the capture's start.
type signalLog struct {
	Start time.Time
	Dips  []time.Time // rows with symbol quality < 100 or quality 0
}

func readSignalLog(path string) (signalLog, error) {
	f, err := os.Open(path)
	if err != nil {
		return signalLog{}, err
	}
	defer func() { _ = f.Close() }()
	var l signalLog
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		c := strings.Split(strings.TrimSpace(sc.Text()), "\t")
		if len(c) < 5 {
			continue
		}
		sec, err1 := strconv.ParseInt(c[0], 10, 64)
		quality, err2 := strconv.Atoi(c[3])
		symbol, err3 := strconv.Atoi(c[4])
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		at := time.Unix(sec, 0)
		if l.Start.IsZero() {
			l.Start = at
		}
		if symbol < 100 || quality == 0 {
			l.Dips = append(l.Dips, at)
		}
	}
	if l.Start.IsZero() {
		return l, fmt.Errorf("%s: no rows", path)
	}
	return l, sc.Err()
}

// falseRebases returns the re-bases (offsets from the clip piece's start)
// that are not within rebaseDipWindow of a dip.
func falseRebases(l signalLog, clip otaClip, rebases []rebase) []rebase {
	var out []rebase
	for _, rb := range rebases {
		at := l.Start.Add(clip.Offset + rb.At)
		ok := false
		for _, d := range l.Dips {
			if diff := at.Sub(d); diff >= -rebaseDipWindow && diff <= rebaseDipWindow {
				ok = true
				break
			}
		}
		if !ok {
			out = append(out, rb)
		}
	}
	return out
}

// signalLogFor finds a clip's tuner log in BOWTIE_OTA_SIGNAL_DIR.
func signalLogFor(t testing.TB, clip otaClip) signalLog {
	t.Helper()
	dir := os.Getenv("BOWTIE_OTA_SIGNAL_DIR")
	if dir == "" {
		t.Fatal("BOWTIE_OTA_SIGNAL_DIR not set: re-bases can't be judged without the capture's signal log")
	}
	l, err := readSignalLog(filepath.Join(dir, "ch"+clip.Channel+".signal.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestRebasesJudgedAgainstSignalDips(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BOWTIE_OTA_SIGNAL_DIR", dir)
	tsv := "1791773101\ttuner1\t49\t0\t0\t0\n" + // tuning: a dip at the start
		"1791773111\ttuner1\t57\t84\t100\t1508672\n" +
		"1791773656\ttuner1\t57\t83\t16\t903904\n" + // symbol errors 555 s in
		"1791773666\ttuner1\t58\t80\t100\t1095008\n"
	if err := os.WriteFile(filepath.Join(dir, "ch2.2.signal.tsv"), []byte(tsv), 0o644); err != nil {
		t.Fatal(err)
	}
	clip, err := parseClipName("ch2.2@500.ts")
	if err != nil || clip.Channel != "2.2" || clip.Offset != 500*time.Second {
		t.Fatalf("clip %+v %v", clip, err)
	}
	if c, err := parseClipName("ch9.1.ts"); err != nil || c.Channel != "9.1" || c.Offset != 0 {
		t.Fatalf("whole clip %+v %v", c, err)
	}
	if _, err := parseClipName("news.ts"); err == nil {
		t.Fatal("unnamed clip accepted")
	}
	l := signalLogFor(t, clip)
	got := falseRebases(l, clip, []rebase{
		{At: 50 * time.Second, Line: "near the dip (550 s, dip at 555 s)"},
		{At: 120 * time.Second, Line: "ad break (620 s)"},
	})
	if len(got) != 1 || got[0].At != 120*time.Second {
		t.Fatalf("false re-bases %+v, want only the one at +120 s", got)
	}
}
```

In `runScenario` (Task 4), change the signature to `func runScenario(t *testing.T, sc *scenario.Scenario) time.Time`. Existing callers ignore the result, which Go allows. Add `sourceStart := time.Now()` as its first line, before `New` starts the fake (the fake's channel timeline starts inside `New`), and `return sourceStart` at each return. Next to the `startAt := time.Now()` line from Task 4, add:

```go
	defer func() {
		ff := ffLogSince(t, startAt)
		t.Logf("ff: rebases=%d dupFrames=%d maxDupBurst=%d dupWarnings=%d", len(ff.Rebases), ff.DupFrames, ff.MaxDupBurst, ff.DupWarnings)
		for _, rb := range ff.Rebases {
			t.Logf("rebase at +%v: %s", rb.At.Round(100*time.Millisecond), rb.Line)
		}
	}()
```

Create `server/internal/e2e/otaclips_test.go`:

```go
//go:build ffmpeg

package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake/scenario"
)

// otaFor is BOWTIE_OTA_FOR (default 10m), the per-clip cap.
func otaFor(t *testing.T) time.Duration {
	t.Helper()
	if v := os.Getenv("BOWTIE_OTA_FOR"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	return 10 * time.Minute
}

// TestOTAClips plays real over-the-air captures through the production
// layout. The clips are never committed: BOWTIE_OTA_CLIPS names their
// directory (skipped when unset), BOWTIE_OTA_SIGNAL_DIR the capture's tuner
// logs (ch<N>.signal.tsv); BOWTIE_OTA_CLIP_FILES picks some (comma-separated
// base names); BOWTIE_OTA_FOR caps each run. A timestamp re-base more than
// 10 s from a logged signal dip is a false re-base (an ad break, a splice)
// and fails the clip.
func TestOTAClips(t *testing.T) {
	dir := os.Getenv("BOWTIE_OTA_CLIPS")
	if dir == "" {
		t.Skip("BOWTIE_OTA_CLIPS not set")
	}
	if os.Getenv("FF51_LOGDIR") == "" {
		t.Fatal("FF51_LOGDIR not set: re-bases can't be judged without the FFmpeg log")
	}
	maxFor := otaFor(t)
	var clips []string
	if v := os.Getenv("BOWTIE_OTA_CLIP_FILES"); v != "" {
		for _, n := range strings.Split(v, ",") {
			clips = append(clips, filepath.Join(dir, strings.TrimSpace(n)))
		}
	} else {
		clips, _ = filepath.Glob(filepath.Join(dir, "*.ts"))
	}
	if len(clips) == 0 {
		t.Fatalf("no clips in %s", dir)
	}
	for _, path := range clips {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			clip, err := parseClipName(filepath.Base(path))
			if err != nil {
				t.Fatal(err)
			}
			signal := signalLogFor(t, clip)
			// Leave 20 s for setup: the fake's timeline starts before the
			// viewer, and a clip that loops would re-base at the seam.
			d := clipDuration(t, path) - 20*time.Second
			if d > maxFor {
				d = maxFor
			}
			if d < 30*time.Second {
				t.Skipf("clip shorter than 50 s")
			}
			sc, err := scenario.Parse([]byte(fmt.Sprintf(`name: ota-%s
source: %q
duration: %s
bowtie: {layout: production}
expect:
  sessionSurvives: true
  sequenceMonotonic: true
  maxFfmpegRestarts: 0
  maxTimelineExcessSeconds: 2
  maxAudioHoleMs: 50
  maxAVStartSkewMs: 100
  pairedAfterEachFault: true
  minSegmentsAfterFault: 5
  maxTailGapSeconds: 12
  noFetchErrors: true
`, strings.TrimSuffix(filepath.Base(path), ".ts"), path, d)))
			if err != nil {
				t.Fatal(err)
			}
			sourceStart := runScenario(t, sc)
			rebases := rebasesSince(t, sourceStart)
			bad := falseRebases(signal, clip, rebases)
			for _, rb := range rebases {
				verdict := "true (near a signal dip)"
				for _, b := range bad {
					if b == rb {
						verdict = "FALSE"
					}
				}
				t.Logf("clip re-base at +%v: %s: %s", (clip.Offset + rb.At).Round(time.Second), verdict, rb.Line)
			}
			if len(bad) > 0 {
				t.Errorf("%d false re-base(s) of %d: none within ±%v of a signal dip", len(bad), len(rebases), rebaseDipWindow)
			}
		})
	}
}

// TestOTAClipsWithFaults is the caption row of the Task 6 gate: the
// faults-production timestamp faults injected into a real clip with
// captions (BOWTIE_OTA_FAULT_CLIP; skipped when unset). Cues must keep
// arriving after every fault and stay within their video segment.
func TestOTAClipsWithFaults(t *testing.T) {
	path := os.Getenv("BOWTIE_OTA_FAULT_CLIP")
	if path == "" {
		t.Skip("BOWTIE_OTA_FAULT_CLIP not set")
	}
	if clipDuration(t, path) < 95*time.Second {
		t.Fatalf("%s: want a clip of at least 95 s", path)
	}
	sc, err := scenario.Parse([]byte(fmt.Sprintf(`name: ota-faults-%s
source: %q
duration: 75s
bowtie: {layout: production}
timeline:
  - {at: 10s, fault: pts-jump, by: 3s}
  - {at: 25s, fault: timestamp-jump, by: 2500ms}
  - {at: 40s, fault: timestamp-jump, by: -2s}
  - {at: 55s, fault: pts-jump, by: 9s}
expect:
  sessionSurvives: true
  sequenceMonotonic: true
  maxFfmpegRestarts: 0
  maxTimelineExcessSeconds: 2
  minTimelineExcessSeconds: -2
  maxAudioHoleMs: 50
  maxAVStartSkewMs: 100
  pairedAfterEachFault: true
  captionsTimed: true
  minSegmentsAfterFault: 3
  maxTailGapSeconds: 12
  noFetchErrors: true
`, strings.TrimSuffix(filepath.Base(path), ".ts"), path)))
	if err != nil {
		t.Fatal(err)
	}
	runScenario(t, sc)
}

// clipDuration is the clip's container duration (ffprobe, 30 s deadline).
func clipDuration(t *testing.T, path string) time.Duration {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffprobePath(), "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path).Output()
	if err != nil {
		t.Fatalf("ffprobe %s: %v", path, err)
	}
	sec, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		t.Fatalf("duration of %s: %q", path, out)
	}
	return time.Duration(sec * float64(time.Second))
}
```

Create `server/internal/e2e/testdata/scenarios/rebase-control.yaml`:

```yaml
name: rebase-control
duration: 35s
timeline:
  # A 15 s step, over FFmpeg's 10 s default threshold: every candidate
  # re-bases it, so the gate's re-base logging must show it.
  - {at: 10s, fault: timestamp-jump, by: 15s}
expect:
  sessionSurvives: true
  sequenceMonotonic: true
  minSegmentsAfterFault: 3
  maxTailGapSeconds: 12
```

Create `server/internal/e2e/testdata/scenarios/faults-production.yaml`:

```yaml
name: faults-production
duration: 75s
bowtie: {layout: production}   # ladder, AAC, the AC-3 output, captions
timeline:
  - {at: 10s, fault: pts-jump, by: 3s}
  - {at: 25s, fault: timestamp-jump, by: 2500ms}
  - {at: 40s, fault: timestamp-jump, by: -2s}
  - {at: 55s, fault: pts-jump, by: 9s}
expect:
  xfail: "single bad PTS and splices inflate the timeline (fixed in Task 6)"
  xfailOn: "timeline excess"
  sessionSurvives: true
  sequenceMonotonic: true
  maxFfmpegRestarts: 0
  maxTimelineExcessSeconds: 2
  minTimelineExcessSeconds: -2
  maxAudioHoleMs: 50
  maxAVStartSkewMs: 100   # aac0 and ac30 against the video
  pairedAfterEachFault: true
  minRenditions: 5
  minSegmentsAfterFault: 3
  maxTailGapSeconds: 12
  noFetchErrors: true
```

Run: `cd server && go vet -tags ffmpeg ./internal/e2e/ && go test -tags ffmpeg -run 'TestFFLogSince|TestRebasesJudgedAgainstSignalDips|TestOTAClips' -v ./internal/e2e/`
Expected: the two reader tests PASS; `TestOTAClips` and `TestOTAClipsWithFaults` SKIP.

Run: `cd server && go test -count=1 -tags ffmpeg -timeout 10m -run 'TestScenarios/(rebase-control|faults-production)$' -v ./internal/e2e/ 2>&1 | grep -E -- '--- |xfail \(|unexpected|ff: '`
Expected: `rebase-control` PASS. `faults-production` passes as an xfail on `timeline excess`; if another violation shows up first, record it in the expected-failures table, as its rule says.

Commit the tools before the experiment:

```bash
git add server/scripts server/internal/e2e
git commit -m "test(e2e): FFmpeg 5.1 wrappers, FFmpeg log readers, OTA clip runs and gate scenarios

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

- [ ] **Step 3: One scratch branch per candidate**

Each candidate is a branch off the Step 2 commit with its own code committed. Never inject options from outside; the logged argv shows what actually ran. Golden tests break on these branches, which is expected. They are never merged.

```bash
cd "$(git rev-parse --show-toplevel)" && task=$(git branch --show-current) && base=$(git rev-parse HEAD)
git branch -f scratch/gate-base "$base"
for c in A:1 A2:2.6; do
  v=${c%%:*} thr=${c#*:}
  git switch -q -C "scratch/gate-$v" "$base" || exit 1
  perl -0pi -e 's/(func liveInputArgs\(\) \[\]string \{\n\treturn \[\]string\{\n)/$1\t\t"-dts_delta_threshold", "'"$thr"'",\n/' server/internal/transcode/ffmpeg.go
  grep -q "\"-dts_delta_threshold\", \"$thr\"" server/internal/transcode/ffmpeg.go || { echo "gate-$v: edit failed"; exit 1; }
  (cd server && go build ./...) || exit 1
  git commit -qam "scratch: gate candidate $v (never merged)"
done
git switch -q -C scratch/gate-C "$base"
```

On `scratch/gate-C`, apply the **C** code from Step 7 to `ffmpeg.go`: the constants, `videoGraph`'s `pre`, the caption input, `-fps_mode:v vfr`, and the AAC and AC-3 audio. Then:

```bash
(cd server && go build ./... && go vet ./internal/transcode/) && grep -q PREV_OUTPTS server/internal/transcode/ffmpeg.go && \
git commit -qam "scratch: gate candidate C (never merged)" && git switch -q "$task"
```

- [ ] **Step 4: Run the gate, scenario half (wrapper, default layout)**

Both halves use these shell functions. Define them once in the shell that runs Steps 4 and 5:

```bash
dl() { perl -e 'alarm shift @ARGV; exec @ARGV or die "dl: exec $ARGV[0]: $!\n"' "$@"; }
# gate_error CANDIDATE STAGE CODE: the one failure line; back to the task branch.
gate_error() { echo "GATE-ERROR $1 $2 exit=$3"; docker rm -f "gate-$1-run" >/dev/null 2>&1; git switch -q "$task"; exit 1; }
# test_ran CODE LOG: a test stage finished (0 = all passed, 1 = some failed) and logged results.
test_ran() { { [ "$1" -eq 0 ] || [ "$1" -eq 1 ]; } && grep -q -- '--- ' "$2"; }
# argv_proof CANDIDATE DIR: every live FFmpeg run logged in DIR carries the candidate's marker.
argv_proof() {
  live=$(cat "$2"/*.log 2>/dev/null | grep -c ' argv: .*pipe:0')
  runs() { cat "$1"/*.log 2>/dev/null | grep ' argv: .*pipe:0'; }
  case $1 in
  base) marked=$(runs "$2" | grep -cE 'dts_delta_threshold|PREV_OUTPTS') ; want=0 ;;
  A) marked=$(runs "$2" | grep -c -- '-dts_delta_threshold 1 ') ; want=$live ;;
  A2) marked=$(runs "$2" | grep -c -- '-dts_delta_threshold 2.6 ') ; want=$live ;;
  C) marked=$(runs "$2" | grep -c 'PREV_OUTPTS') ; want=$live ;;
  *) echo "argv proof: unknown candidate $1"; return 1 ;;
  esac
  echo "argv proof ($2): $live live runs, $marked marked, want $want"
  runs "$2" | head -1
  [ "$live" -gt 0 ] && [ "$marked" -eq "$want" ]
}
```

```bash
cd "$(git rev-parse --show-toplevel)" && task=$(git branch --show-current)
for v in base A A2 C; do
  git switch -q "scratch/gate-$v" || gate_error "$v" switch $?
  L="${TMPDIR%/}/gate-$v"; rm -rf "$L"; mkdir -p "$L"
  (cd server && FF51_LOGDIR="$L/ff" FF51_LOGLEVEL=level+debug BOWTIE_FFMPEG_PATH="$PWD/scripts/ffmpeg51" \
    dl 7500 go test -count=1 -tags ffmpeg -p 1 -parallel 1 -timeout 120m \
    -run 'TestScenarios/(rebase-control|ptsjump|ptsjump-ptsonly|splice|brief-dropout|threshold-dropout|baseline)$' -v ./internal/e2e/ \
    > "$L/scenarios.log" 2>&1); rc=$?
  test_ran $rc "$L/scenarios.log" || gate_error "$v" scenarios $rc
  echo "== $v"
  grep -E -- '--- (PASS|FAIL)|observed:|player: renditions|fetchErrors=|coverage:|ff: |rebase at|xfail \(|known gap|unexpected' "$L/scenarios.log"
  argv_proof "$v" "$L/ff" || gate_error "$v" argv-proof 1
done
git switch -q "$task"
```

- `-parallel 1` keeps each scenario's FFmpeg lines apart, because `ffLogSince` reads every log written after the scenario's start.
- The xfail markers are still in place, so judge the gate's rows from the `observed:`, `coverage:` and `ff:` lines. Ignore the xfail verdicts.
- If `rebase-control` shows `ff: rebases=0` for any candidate, or the base candidate's `ptsjump` shows `dupFrames` under 60, stop: the logging is broken.
- `GATE-ERROR` means the stage itself failed (a build, a deadline, a missing log), not the candidate. Fix the cause and re-run; never fill the gate table from a run that printed it.

- [ ] **Step 5: Run the gate, image half (production layout, captions, the OTA clips)**

The image and the Linux test binary are built from each candidate's branch:

```bash
cd "$(git rev-parse --show-toplevel)" && task=$(git branch --show-current)
clips=<folder with the chosen clips>          # this session: …/scratchpad/ota/ts, or the cut pieces
signals=<folder with ch<N>.signal.tsv>        # this session: …/scratchpad/ota/log
faultclip=<one clip with captions, no dips>   # file name inside $clips
for v in base A A2 C; do
  git switch -q "scratch/gate-$v" || gate_error "$v" switch $?
  dl 1800 docker build -q -f deploy/Dockerfile -t "bowtie:gate-$v" . >/dev/null; rc=$?
  [ $rc -eq 0 ] || gate_error "$v" docker-build $rc
  arch=$(docker image inspect "bowtie:gate-$v" --format '{{.Architecture}}') || gate_error "$v" inspect $?
  (cd server && GOOS=linux GOARCH="$arch" CGO_ENABLED=0 dl 600 go test -c -tags ffmpeg -o "${TMPDIR%/}/e2e-gate-$v.test" ./internal/e2e/); rc=$?
  [ $rc -eq 0 ] || gate_error "$v" compile $rc
  L="${TMPDIR%/}/gate-$v"; rm -rf "$L/img"; mkdir -p "$L/img"
  docker rm -f "gate-$v-run" >/dev/null 2>&1
  dl 18300 docker run --rm --name "gate-$v-run" --entrypoint /work/e2e.test \
    -v "${TMPDIR%/}/e2e-gate-$v.test:/work/e2e.test:ro" -v "$PWD/server/internal/e2e:/src/e2e:ro" -w /src/e2e \
    -v "$PWD/server/scripts/ffmpeg-soak:/usr/local/bin/ffmpeg-soak:ro" \
    -v "$clips:/clips:ro" -v "$signals:/signals:ro" -v "$L/img:/logs" \
    -e BOWTIE_FFMPEG_PATH=/usr/local/bin/ffmpeg-soak -e BOWTIE_FFPROBE_PATH=/usr/bin/ffprobe -e HOME=/tmp \
    -e FF51_LOGDIR=/logs/ff -e BOWTIE_OTA_CLIPS=/clips -e BOWTIE_OTA_SIGNAL_DIR=/signals -e BOWTIE_OTA_FOR=60m \
    -e BOWTIE_OTA_FAULT_CLIP="/clips/$faultclip" \
    "bowtie:gate-$v" -test.run 'TestScenarios/(rebase-control|production-baseline|corrupt-audio|faults-production)$|TestOTAClips' \
    -test.v -test.parallel 1 -test.timeout 300m > "$L/img.log" 2>&1; rc=$?
  docker rm -f "gate-$v-run" >/dev/null 2>&1   # the container outlives a killed client
  test_ran $rc "$L/img.log" || gate_error "$v" image-run $rc
  echo "== $v (image)"
  grep -E -- '--- (PASS|FAIL)|observed:|fetchErrors=|coverage:|ff: |rebase at|false re-base|consistency:|cues=' "$L/img.log"
  argv_proof "$v" "$L/img/ff" || gate_error "$v" image-argv-proof 1   # lines written by ffmpeg-soak in the container
done
git switch -q "$task"
```

The image loop applies Step 4's argv proof to `"$L"/img/ff/*.log`, the `argv:` lines that `ffmpeg-soak` writes inside the container, and exits 1 on a mismatch: an image that didn't run its candidate's argv stops the gate. `TestOTAClips` itself fails a candidate on any false re-base; its log names each re-base with its verdict.

Choosing the clips: pick them at run time from what was captured.
- `TestOTAClips`: include the clips with logged signal dips (2.2 around 21:54:16–26 local CDT; 4.1 at 22:13:27 and 22:14:58) and the parts of 9.1 that cover ad breaks.
- Clips over 1 GiB must be cut first, because `tsloop.Load` reads a clip into memory. Name each piece after its start offset in seconds so the signal log still lines up:

```bash
dl() { perl -e 'alarm shift @ARGV; exec @ARGV or die "exec $ARGV[0]: $!\n"' "$@"; }
dl 600 ffmpeg -y -ss <start> -t <len> -i ch9.1.ts -map 0 -c copy "<scratch>/ch9.1@<start>.ts"
```

- `BOWTIE_OTA_FAULT_CLIP` must have captions throughout and no dips in its signal log. `ffprobe` shows `Closed Captions` on its video stream. A news segment is a good choice.
  - Check the base candidate's `window i … cues` log lines: base fails the timeline rows, but if it shows a window with no cues at all (timed or not), the clip's captions have gaps. Pick another clip.

Fill in the gate table for the task report, one row per candidate:
- excess, audio holes (per rendition) and skew for each scenario;
- `coverage:` lines;
- `ff:` counts (re-bases, duplicated frames, max burst, warnings), including `splice`'s re-bases, `rebase-control`'s re-bases and the base candidate's `ptsjump` duplicates (the two positive controls);
- production-layout results;
- caption row: cues, timed cues, mistimed or unmatched cues, windows without timed cues;
- per clip: true and false re-bases;
- argv proof;
- pass/fail.

Apply the choosing rule above.

Afterwards delete the scratch branches (`git branch -D scratch/gate-base scratch/gate-A scratch/gate-A2 scratch/gate-C`) and images (`docker rmi bowtie:gate-base bowtie:gate-A bowtie:gate-A2 bowtie:gate-C`).

- [ ] **Step 6: Write the failing test for the chosen option**

For **A′** (or A), add to `TestBuildArgsLiveInputHardening` right after the first `mustContainSeq`:

```go
	mustContainSeq(t, live, "-dts_delta_threshold", "2.6", "-thread_queue_size", "1024") // "1" for A
```

and add `a == "-dts_delta_threshold" ||` to the URL/VOD loop's live-only condition. Update the goldens:

```bash
cd server && perl -i -pe 's/"-thread_queue_size", "1024", "-probesize", "20M",/"-dts_delta_threshold", "2.6", "-thread_queue_size", "1024", "-probesize", "20M",/' internal/transcode/ffmpeg_test.go
```

For **C**, add to `ffmpeg_test.go`:

```go
const (
	clampV = `setpts='if(eq(N\,0)\,PTS\,PREV_OUTPTS+clip(PTS-PREV_INPTS\,1\,0.5/TB))'`
	clampA = `asetpts='if(eq(N\,0)\,PTS\,PREV_OUTPTS+clip(PTS-PREV_INPTS\,0\,0.5/TB))'`
)

// Candidate C clamps every live output: both video layouts, the caption
// input, AAC, and AC-3 (re-encoded, since a copy can't be filtered).
func TestBuildArgsLiveClampEveryLayout(t *testing.T) {
	d := transcode.Decision{VideoCodec: "h264", VideoEncoder: "libx264", Backend: transcode.BackendSoftware,
		Profile: transcode.Profile{Name: "original", Height: 1080, VideoKbps: 8000, AudioKbps: 160}}
	ladder := transcode.JobSpec{Stdin: nonNilStdin(), OutDir: "/tmp/out", D: d, Layout: transcode.Layout{
		Rungs:     transcode.Ladder(720),
		Audio:     []transcode.AudioTrack{{Lang: "eng", AC3: true}},
		AudioKbps: 128, Captions: true, AC3Copy: true, VideoCodec: "h264",
	}}
	one := transcode.JobSpec{Stdin: nonNilStdin(), OutDir: "/tmp/out", D: d, Layout: transcode.Layout{
		Audio: []transcode.AudioTrack{{Lang: "eng"}}, Captions: true, VideoCodec: "h264",
	}}
	for name, s := range map[string]transcode.JobSpec{"ladder": ladder, "one rung": one} {
		got := transcode.BuildArgs(s)
		if g := flagValue(got, "-filter_complex"); !strings.HasPrefix(g, "[0:v]"+clampV+",") {
			t.Errorf("%s: video graph %q does not start with the clamp", name, g)
		}
		mustContainSeq(t, got, "-data_field", "first", "-f", "lavfi", "-i", "movie='pipe\\:3',"+clampV+"[out0+subcc]")
		mustContainSeq(t, got, "-fps_mode:v", "vfr")
		mustContainSeq(t, got, "-filter:a:0", clampA+","+liveAudio)
	}
	mustContainSeq(t, transcode.BuildArgs(ladder), "-c:a:1", "ac3", "-b:a:1", "384k", "-filter:a:1", clampA+","+liveAudio)
	vod := ladder
	vod.Stdin, vod.VOD, vod.InputURL = nil, true, "/tmp/list.txt"
	if strings.Contains(strings.Join(transcode.BuildArgs(vod), " "), "PREV_OUTPTS") {
		t.Error("VOD input got the live clamp")
	}
}
```

Then update the goldens and the existing captions test:
- In each `TestBuildArgsStdinPipeAcrossBackends` golden:
  - write the `-filter_complex` value as `"[0:v]"+clampV+","+<the rest of the old value after [0:v]>`;
  - put `"-fps_mode:v", "vfr",` right after `"-profile:v", "high",`;
  - change the `-filter:a:0` value to `clampA+","+liveAudio`.
- In `TestBuildArgsLadderQSVWithAudioAndCaptions`:
  - the caption input becomes `"movie='pipe\\:3',"+clampV+"[out0+subcc]"`;
  - the graph's first part becomes `"[0:v]"+clampV+",vpp_qsv=deinterlace=2,split=3…"`;
  - `"-c:a:2", "copy", "-c:a:3", "copy"` becomes the AC-3 re-encode with `clampA+","+liveAudio`. If Task 5 Step 6 already re-encodes AC-3, only the filter value changes.

Run: `cd server && go test ./internal/transcode/ -run TestBuildArgs -v`
Expected: FAIL (option missing).

- [ ] **Step 7: Implement the chosen option**

For **A′** (A: the same with `"1"`):

```go
// liveInputArgs harden the live pipe (dirty ATSC transport streams).
// dts_delta_threshold 2.6: a timestamp step over 2.6 s (a corrupt PES,
// a dropout) is re-based instead of filled with duplicate frames, while ad
// splices (steps up to 2.5 s) are left alone; chosen on FFmpeg 5.1 by the
// ptsjump/splice/dropout scenarios, captions and real OTA clips (Task 6
// gate). genpts fills missing timestamps, ignore_err keeps decoding past
// damaged frames, the larger probe still finds every PMT stream when the
// first seconds are damaged, and the thread queue absorbs the burst after
// a stall.
func liveInputArgs() []string {
	return []string{
		"-dts_delta_threshold", "2.6",
		"-thread_queue_size", "1024", "-probesize", "20M", "-analyzeduration", "10M",
		"-fflags", "+discardcorrupt+genpts", "-err_detect", "ignore_err",
	}
}
```

For **C**, leave `liveInputArgs` as it is and change `ffmpeg.go`:

1. Add the constants:

```go
// liveVideoClamp and liveAudioClamp close timestamp steps over 0.5 s (a
// corrupt PES, a splice, a dropout) frame by frame, so the output neither
// duplicates frames nor keeps a hole. Every live output gets them at the
// same point (both see the same step): the video graph, the caption
// decoder's graph, AAC and the AC-3 re-encode. Chosen on FFmpeg 5.1 by the
// Task 6 gate. setpts only rewrites timestamps, so it runs on hardware
// frames too.
const (
	liveVideoClamp = "setpts='if(eq(N\\,0)\\,PTS\\,PREV_OUTPTS+clip(PTS-PREV_INPTS\\,1\\,0.5/TB))'"
	liveAudioClamp = "asetpts='if(eq(N\\,0)\\,PTS\\,PREV_OUTPTS+clip(PTS-PREV_INPTS\\,0\\,0.5/TB))'"
)
```

2. `videoGraph` takes the clamp and puts it first in the `[0:v]` chain, for one rung and the ladder alike:

```go
// videoGraph decodes/deinterlaces once and scales each rung; labels [v0]….
// One rung keeps the single filter chain proven in production. pre, when
// set, is the first filter on [0:v] (the live timestamp clamp).
func videoGraph(b Backend, rungs []Rung, pre string) string {
	deint, scale := filterParts(b)
	head := "[0:v]"
	if pre != "" {
		head += pre + ","
	}
	if len(rungs) == 1 {
		return fmt.Sprintf("%s%s[v0]", head, singleChain(b, deint, scale(rungs[0].Height)))
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s%s,split=%d", head, deint, len(rungs))
	for i := range rungs {
		fmt.Fprintf(&sb, "[s%d]", i)
	}
	for i, r := range rungs {
		fmt.Fprintf(&sb, ";[s%d]%s[v%d]", i, scale(r.Height), i)
	}
	return sb.String()
}
```

3. In `BuildArgs`:
   - Captions:

     ```go
     	if l.Captions {
     		// Same TS again on fd 3, decoded in software only for CEA-608.
     		// Field 1 carries CC1; "auto" can lock onto field 2 (XDS/program info).
     		// Live: the same clamp as the video, so cue times follow it.
     		cc := "movie='pipe\\:3'"
     		if s.Stdin != nil {
     			cc += "," + liveVideoClamp
     		}
     		args = append(args, "-data_field", "first", "-f", "lavfi", "-i", cc+"[out0+subcc]")
     	}
     ```

   - The graph: `pre := ""; if s.Stdin != nil { pre = liveVideoClamp }`, then `videoGraph(s.D.Backend, rungs, pre)`.
   - Right after the `-profile:v` block: `if s.Stdin != nil { args = append(args, "-fps_mode:v", "vfr") }`.
   - Each live AAC output's filter becomes `liveAudioClamp + "," + liveAudioFilter`.
   - Live AC-3 outputs are re-encoded with the same filter: `-c:a:n ac3 -b:a:n 384k -filter:a:n liveAudioClamp+","+liveAudioFilter`. URL and VOD input keep `copy`. If Task 5 Step 6 already re-encodes AC-3, only prefix its filter.

Run: `cd server && go test ./internal/transcode/ && go test -tags ffmpeg ./internal/transcode/`
Expected: PASS.

- [ ] **Step 8: Remove the fixed xfails and add guards**

Delete the `xfail:`/`xfailOn:` lines from `ptsjump.yaml`, `ptsjump-ptsonly.yaml`, `splice.yaml`, `brief-dropout.yaml`, `threshold-dropout.yaml` and `faults-production.yaml`. Add to `baseline.yaml`'s `expect:` block:

```yaml
  maxTimelineExcessSeconds: 2
  minTimelineExcessSeconds: -2
  maxAVStartSkewMs: 100
  pairedAfterEachFault: true
```

Run: `cd server && go test -count=1 -tags ffmpeg -p 1 -parallel 2 -timeout 30m -run TestScenarios ./internal/e2e/`
Expected: PASS (host FFmpeg; the production-layout scenarios run there too, with the host FFmpeg's captions).

Run: `cd server && BOWTIE_FFMPEG_PATH="$PWD/scripts/ffmpeg51" go test -count=1 -tags ffmpeg -p 1 -parallel 2 -timeout 60m -run 'TestScenarios/(rebase-control|ptsjump|ptsjump-ptsonly|splice|brief-dropout|threshold-dropout|baseline|transcoder-restart)$' ./internal/e2e/`
Expected: PASS (FFmpeg 5.1).

Re-run Step 5's command once from the task branch (no scratch branch, image `bowtie:gate-final`). Expect `production-baseline`, `corrupt-audio`, `faults-production`, `rebase-control`, `TestOTAClips` and `TestOTAClipsWithFaults` to PASS. `stuck-recovers` and `long-outage-restart` keep their xfails until Tasks 7 and 10.

- [ ] **Step 9: Document the 5.1 runs**

Add to `docs/dev/fake-hdhomerun.md`, in the section on running the scenario suite:

```markdown
### On FFmpeg 5.1 (the production image's FFmpeg)

`server/scripts/ffmpeg51` runs Debian bookworm's FFmpeg 5.1 in Docker as a
drop-in for `BOWTIE_FFMPEG_PATH` (software encode):

    cd server
    BOWTIE_FFMPEG_PATH=$PWD/scripts/ffmpeg51 go test -tags ffmpeg -p 1 -parallel 2 -timeout 60m -run TestScenarios ./internal/e2e/

`FF51_LOGLEVEL` changes the log level; `FF51_LOGDIR` collects each run's
argv and its re-base and duplicate lines with timestamps (scenarios then log
`ff: rebases=… dupFrames=…` and `rebase at +…`). To compare FFmpeg options,
commit each to its own scratch branch and run from it: the wrapper never
injects options, and the logged argv shows what ran. Captions (fd 3) can't
cross into the container, so scenarios with `bowtie: {layout: production}`
run inside the production image instead: build `deploy/Dockerfile`,
cross-compile the e2e test binary (`go test -c -tags ffmpeg`) and run it in
the image with `BOWTIE_FFMPEG_PATH` pointing at `scripts/ffmpeg-soak`
mounted into the container (see the stream resilience plan, Task 6).

### On real over-the-air clips

`TestOTAClips` plays captured `.ts` files through the production layout and
fails on a timestamp re-base that isn't near a signal dip in the capture's
tuner log. The clips stay out of the repo: point `BOWTIE_OTA_CLIPS` at their
folder and `BOWTIE_OTA_SIGNAL_DIR` at the `ch<N>.signal.tsv` logs
(optionally `BOWTIE_OTA_CLIP_FILES=ch2.2.ts,ch9.1@1200.ts` and
`BOWTIE_OTA_FOR=60m`). Clips are loaded into memory: cut clips over 1 GiB,
naming each piece `ch<N>@<start seconds>.ts`. `TestOTAClipsWithFaults`
injects timestamp faults into `BOWTIE_OTA_FAULT_CLIP` (a clip with captions)
and checks that the cues stay on the video.
```

- [ ] **Step 10: Commit**

```bash
git add server/internal/transcode server/internal/e2e/testdata/scenarios docs/dev/fake-hdhomerun.md
git commit -m "fix(transcode): <chosen option> on the live pipe, gated on FFmpeg 5.1

Gate (ptsjump PTS+DTS and PTS-only at +3/+6/+9 s, splice with zero
re-bases, duplicate bursts, dropouts, production layout with AC-3 and
captions, real OTA clips judged against their signal logs):
<candidate x check table from Steps 4 and 5>

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

Fill in the subject's `<chosen option>` and the table from Steps 4 and 5 before committing. These are run-time results, not placeholders in the plan; a subject might read `re-base timestamp steps over 2.6 s instead of duplicating frames`.

---

### Task 7: Stuck transcoder: drain first, kill only if no bytes reach it

**Files:**
- Modify: `server/internal/stream/subqueue.go`
- Modify: `server/internal/stream/ingest.go`
- Test: `server/internal/stream/subqueue_test.go`, `server/internal/stream/ingest_test.go`
- Modify: `server/internal/e2e/testdata/scenarios/stuck-recovers.yaml`

**Interfaces:**
- Consumes: `channelIngest.joinSnapshotLocked()`. It returns PAT, PMT and the stream since the join point; Task 8 moves the join point to the last complete video key and can make the snapshot up to 4 MiB.
- Produces:
  - **Progress is bytes actually written to FFmpeg's pipe**, not queue pops. `drain` writes each chunk in slices of `pipeWriteSlice = 348 * 188` bytes (65,424 bytes, whole TS packets) and reports every completed slice with `subQueue.wrote(n)`. That moves the stuck clock. A reader that keeps consuming a 4 MiB join chunk makes progress every slice and is never "stuck".
  - `subQueue`:
    - `pop` moves the chunk's bytes into `inFlight` and doesn't touch the stuck clock.
    - `wrote(n int)` moves `n` bytes from in flight to written and stamps the stuck clock.
    - `writtenBytes() uint64` is the total written.
    - `idleFor` counts from the last write while anything is queued **or in flight**.
    - `size() int` is the queued bytes.
    - `reset(head []byte) int` replaces the queued backlog and returns the bytes dropped. It never touches the stuck clock, `inFlight` or the written count, so a drain is never progress.
    - `resetGen() uint64` changes at every reset.
  - After a reset, `drain` abandons the rest of the chunk it was writing at the next slice boundary, so the fresh headers follow at most one slice of old data.
  - **Ruling on unblocking the writer after a reset:** not done. The pipe is an `io.Pipe`, which has no write deadline. A slice already handed to `pw.Write` stays blocked until FFmpeg reads it (≤ 64 KiB) or the pipe is closed by the kill. A reader that never reads is exactly the hung case: it must stay blocked until the grace ends and `forceClose` closes the pipe. FFmpeg can't reopen its stdin, so reopening the pipe isn't possible either. Bounding the leftover to one slice is what matters, and the slice boundary does that.
  - `const ingestSubKillGrace = 30 * time.Second`, and `IngestSub.drainedAt`/`writtenAtDrain`, guarded by `channelIngest.mu`.
  - The rule:
    1. At 30 s with no bytes written while data waits, drain once.
    2. After a drain, kill only if the written count hasn't moved for `ingestSubKillGrace`. Any written byte clears the drain.
    3. A kill goes through `forceClose`, whose hook (`proc.Stop`, SIGKILL) the manager registered. Supervise then sees the exit and restarts with append, as today. `manager.go` needs no change.
  - Log lines:
    - drain: `ingest: channel N: transcoder read nothing for 30s; dropped its K KiB backlog and resent the stream headers`
    - recovery: `ingest: channel N: transcoder reading again Xs after its backlog was dropped`
    - kill: `ingest: channel N: transcoder stuck for 1m0s, closing its input` (the release note watches "transcoder stuck").
  - FFmpeg runs with `-nostats`, so `speed`/`dup` aren't available to log. The drain line carries the idle time and the bytes dropped instead.

- [ ] **Step 1: Write the failing tests**

Append to `server/internal/stream/subqueue_test.go`:

```go
// Progress is bytes written to FFmpeg, not pops: a 4 MiB chunk popped once
// and then written slowly keeps the stuck clock fresh with every slice; a
// reset (drain) never counts as progress.
func TestSubQueueProgressCountsWrittenBytesNotPops(t *testing.T) {
	now := time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC)
	q := newSubQueue(8<<20, func() time.Time { return now })
	q.push(make([]byte, 4<<20), nil)
	if _, ok := q.pop(); !ok {
		t.Fatal("pop")
	}
	now = now.Add(20 * time.Second)
	if got := q.idleFor(now); got != 20*time.Second {
		t.Fatalf("idleFor=%v with 4 MiB in flight and nothing written, want 20s", got)
	}
	q.wrote(pipeWriteSlice)
	now = now.Add(5 * time.Second)
	if got := q.idleFor(now); got != 5*time.Second {
		t.Fatalf("idleFor=%v 5 s after a slice was written, want 5s", got)
	}
	q.push([]byte("aaaa"), nil)
	gen := q.resetGen()
	if n := q.reset([]byte("HEAD")); n != 4 {
		t.Fatalf("reset dropped %d bytes, want 4 (the in-flight chunk isn't queued)", n)
	}
	if q.resetGen() == gen || q.size() != 4 {
		t.Fatalf("resetGen unchanged or size=%d after reset", q.size())
	}
	now = now.Add(10 * time.Second)
	if got := q.idleFor(now); got != 15*time.Second {
		t.Fatalf("idleFor=%v after reset, want 15s (a reset is not progress)", got)
	}
	if q.writtenBytes() != pipeWriteSlice {
		t.Fatalf("writtenBytes=%d", q.writtenBytes())
	}
}

// With nothing queued and nothing in flight the reader isn't stuck (a quiet input).
func TestSubQueueIdleIsZeroWhenNothingWaits(t *testing.T) {
	now := time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC)
	q := newSubQueue(1<<20, func() time.Time { return now })
	q.push([]byte("abcd"), nil)
	c, _ := q.pop()
	q.wrote(len(c))
	now = now.Add(time.Hour)
	if got := q.idleFor(now); got != 0 {
		t.Fatalf("idleFor=%v with nothing waiting", got)
	}
}
```

In `server/internal/stream/ingest_test.go`, replace `TestStuckSubClosedAfter30s` with:

```go
// A reader that takes nothing is drained at the stuck timeout and closed only
// after the grace period passes without a single byte written to it.
func TestStuckSubDrainedThenClosedAfterGrace(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 10, 2, 20, 0, 0, 0, time.UTC))
	pb := newPipeBody()
	im, _ := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) { return pb, 200, nil }, clock)
	stuck, _ := im.Attach(context.Background(), 1, "u") // never reads
	chunk := make([]byte, 188*10)
	feedAndStep(pb, clock, chunk, int(ingestSubStuckTimeout/time.Second)+2)
	if stuck.isClosed() {
		t.Fatal("closed at the stuck timeout; want a drain first")
	}
	if got := stuck.q.size(); got > 3*len(chunk) {
		t.Fatalf("backlog %d bytes after the drain, want ≤ %d", got, 3*len(chunk))
	}
	feedAndStep(pb, clock, chunk, int(ingestSubKillGrace/time.Second)-4)
	if stuck.isClosed() {
		t.Fatal("closed before the grace period ended")
	}
	feedAndStep(pb, clock, chunk, 6)
	waitFor(t, 2*time.Second, stuck.isClosed)
}

// A transcoder that reads again after the drain was slow, not hung: it stays.
func TestStuckSubThatReadsAgainIsNotClosed(t *testing.T) {
	clock := newIngestClock(time.Date(2026, 10, 2, 21, 0, 0, 0, time.UTC))
	pb := newPipeBody()
	im, _ := newTestIngest(t, func(ctx context.Context, url string) (io.ReadCloser, int, error) { return pb, 200, nil }, clock)
	sub, _ := im.Attach(context.Background(), 1, "u")
	chunk := make([]byte, 188*10)
	feedAndStep(pb, clock, chunk, int(ingestSubStuckTimeout/time.Second)+2) // drained
	go func() { _, _ = io.Copy(io.Discard, sub.R) }()
	feedAndStep(pb, clock, chunk, int((ingestSubStuckTimeout+ingestSubKillGrace)/time.Second)+5)
	if sub.isClosed() {
		t.Fatal("a transcoder that read again was closed")
	}
	_ = sub.Close()
}

// A slow reader working through a 4 MiB join chunk (32 KiB per simulated
// second: two minutes for the chunk) is making progress the whole time:
// never drained, never closed.
func TestSlowReaderOfLargeJoinIsNotStuck(t *testing.T) {
	now := time.Date(2026, 10, 11, 21, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	pr, pw := io.Pipe()
	s := &IngestSub{R: pr, pr: pr, pw: pw, q: newSubQueue(8<<20, clock)}
	join := make([]byte, 4<<20)
	go s.drain(join)
	buf := make([]byte, 32<<10)
	for sec := 0; sec < 70; sec++ {
		if _, err := io.ReadFull(pr, buf); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond) // let drain record the slice it finished
		mu.Lock()
		now = now.Add(time.Second)
		mu.Unlock()
		if idle := s.q.idleFor(clock()); idle >= 4*time.Second {
			t.Fatalf("at %ds idleFor=%v while the reader keeps reading", sec, idle)
		}
	}
	_ = s.Close()
}
```

Add `"sync"` to `ingest_test.go`'s imports if it isn't there. In `TestStuckSubRunsForceCloseHook`, change the step count to `int((ingestSubStuckTimeout+ingestSubKillGrace)/time.Second)+2`. In `TestConcurrentCloseVsForceClose`, change the first `feedAndStep` count to `int((ingestSubStuckTimeout+ingestSubKillGrace)/time.Second)-1`, and its comment to `// Bring the sub to the edge of the kill (drained, grace nearly over).`

`IngestSub.Close` calls `s.ci.removeSub` only when `s.ci != nil`, so the bare sub in `TestSlowReaderOfLargeJoinIsNotStuck` closes cleanly. Closing the pipe reader also ends `drain`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd server && go test ./internal/stream/ -run 'TestSubQueue|TestStuckSub|TestSlowReader|TestConcurrentCloseVsForceClose' -v`
Expected: FAIL to compile (`q.wrote undefined`, `undefined: pipeWriteSlice`, `undefined: ingestSubKillGrace`).

- [ ] **Step 3: Implement**

`subqueue.go`: rename `lastPop` to `lastProgress` everywhere in the file, and add fields after it:

```go
	inFlight int    // popped bytes not yet written to the pipe
	written  uint64 // bytes written to the pipe
	gen      uint64 // bumped by reset
```

In `newSubQueue`, initialize `lastProgress: now()`. In `push`, the "data starts waiting now" stamp applies only when nothing is queued **and** nothing is in flight:

```go
	if len(q.chunks) == 0 && q.inFlight == 0 {
		// Data starts waiting now: a quiet input (no signal) is not a stuck
		// reader, so the stuck clock must not count the empty time.
		q.lastProgress = q.now()
	}
```

In `pop`, replace `q.lastPop = q.now()` with `q.inFlight += len(c)`. Replace `idleFor` and add the new methods:

```go
// idleFor is how long data has waited (queued or in flight) without a byte
// reaching the reader; 0 when nothing waits.
func (q *subQueue) idleFor(now time.Time) time.Duration {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.bytes == 0 && q.inFlight == 0 {
		return 0
	}
	return now.Sub(q.lastProgress)
}

// wrote records n bytes written to the reader's pipe: the only progress.
func (q *subQueue) wrote(n int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.inFlight -= n
	if q.inFlight < 0 {
		q.inFlight = 0
	}
	q.written += uint64(n)
	q.lastProgress = q.now()
}

// abandon drops the unwritten rest of the current chunk (after a reset).
func (q *subQueue) abandon(n int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.inFlight -= n
	if q.inFlight < 0 {
		q.inFlight = 0
	}
}

func (q *subQueue) writtenBytes() uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.written
}

func (q *subQueue) resetGen() uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.gen
}

// size is the queued byte count (not counting the chunk in flight).
func (q *subQueue) size() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.bytes
}

// reset replaces the queued backlog with head and returns the bytes dropped.
// The stuck clock, the in-flight count and the written count are left
// alone: a drain is not progress.
func (q *subQueue) reset(head []byte) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return 0
	}
	dropped := q.bytes
	for i := range q.chunks {
		q.chunks[i] = nil
	}
	q.chunks, q.bytes = q.chunks[:0], 0
	q.gen++
	if len(head) > 0 {
		q.chunks = append(q.chunks, head)
		q.bytes = len(head)
		q.cond.Signal()
	}
	return dropped
}
```

`ingest.go`: add the slice constant and replace `drain`:

```go
// pipeWriteSlice is how much drain hands FFmpeg's pipe at once (whole TS
// packets): every completed slice counts as progress, so a slow reader of
// a large join chunk is never mistaken for a stuck one.
const pipeWriteSlice = 348 * tsPacketSize

func (s *IngestSub) drain(join []byte) {
	defer func() {
		_ = s.pw.Close()
	}()
	if len(join) > 0 {
		s.q.addInFlight(len(join))
		if !s.writeChunk(join) {
			return
		}
	}
	for {
		chunk, ok := s.q.pop()
		if !ok {
			return
		}
		if !s.writeChunk(chunk) {
			return
		}
	}
}

// writeChunk writes chunk in slices, recording each as progress. After a
// reset (drain) it abandons the rest of the chunk at the next slice
// boundary, so the fresh headers follow at most one slice of old data.
// False when the pipe is closed.
func (s *IngestSub) writeChunk(chunk []byte) bool {
	gen := s.q.resetGen()
	for off := 0; off < len(chunk); {
		if s.q.resetGen() != gen {
			s.q.abandon(len(chunk) - off)
			return true
		}
		end := off + pipeWriteSlice
		if end > len(chunk) {
			end = len(chunk)
		}
		n, err := s.pw.Write(chunk[off:end])
		if n > 0 {
			s.q.wrote(n)
		}
		if err != nil {
			return false
		}
		off = end
	}
	return true
}
```

and in `subqueue.go`:

```go
// addInFlight counts bytes handed to the writer outside pop (the join).
func (q *subQueue) addInFlight(n int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.bytes == 0 && q.inFlight == 0 {
		q.lastProgress = q.now()
	}
	q.inFlight += n
}
```

Replace the stuck constant's comment and add the grace:

```go
	// ingestSubStuckTimeout: a transcoder that takes no bytes for this long
	// while data waits is drained: its backlog is dropped and it gets the
	// stream headers again.
	ingestSubStuckTimeout = 30 * time.Second
	// ingestSubKillGrace: a drained transcoder that still takes no bytes for
	// this long is hung (a wedged encoder); its input is closed and the
	// manager kills and restarts it.
	ingestSubKillGrace = 30 * time.Second
```

Add to `IngestSub` (after `lastDropLog`):

```go
	// drainedAt is when ingest dropped this stuck sub's backlog (zero: not
	// drained); writtenAtDrain is the bytes written to its pipe then. Under channelIngest.mu.
	drainedAt      time.Time
	writtenAtDrain uint64
```

Replace `ingestChunk` and add `checkStuckLocked`:

```go
func (c *channelIngest) ingestChunk(chunk []byte) {
	c.mu.Lock()
	c.updateJoinLocked(chunk)
	tables := c.tablesLocked()
	now := c.im.now()
	var stuck []*IngestSub
	for s := range c.subs {
		if s.isClosed() {
			continue
		}
		if n := s.q.push(chunk, tables); n > 0 && now.Sub(s.lastDropLog) >= 10*time.Second {
			s.lastDropLog = now
			log.Printf("ingest: channel %d: transcoder behind, dropped %d KiB of old data", c.channelID, n>>10)
		}
		if c.checkStuckLocked(s, now) {
			stuck = append(stuck, s)
		}
	}
	c.mu.Unlock()
	for _, s := range stuck {
		log.Printf("ingest: channel %d: transcoder stuck for %v, closing its input", c.channelID, ingestSubStuckTimeout+ingestSubKillGrace)
		s.forceClose()
	}
}

// checkStuckLocked applies the stuck rule to one sub and reports whether its
// input must be closed. A transcoder that takes no bytes for
// ingestSubStuckTimeout is drained first: its backlog is replaced by PAT,
// PMT and the stream since the join point, as after an overflow. It is
// closed only if no byte reaches it for ingestSubKillGrace after that; one
// that reads again was just slow and is left alone.
func (c *channelIngest) checkStuckLocked(s *IngestSub, now time.Time) bool {
	if !s.drainedAt.IsZero() {
		if s.q.writtenBytes() != s.writtenAtDrain {
			log.Printf("ingest: channel %d: transcoder reading again %v after its backlog was dropped",
				c.channelID, now.Sub(s.drainedAt).Round(time.Second))
			s.drainedAt = time.Time{}
			return false
		}
		return now.Sub(s.drainedAt) >= ingestSubKillGrace
	}
	if idle := s.q.idleFor(now); idle >= ingestSubStuckTimeout {
		dropped := s.q.reset(c.joinSnapshotLocked())
		s.drainedAt, s.writtenAtDrain = now, s.q.writtenBytes()
		log.Printf("ingest: channel %d: transcoder read nothing for %v; dropped its %d KiB backlog and resent the stream headers",
			c.channelID, idle.Round(time.Second), dropped>>10)
	}
	return false
}
```

Then search the package for other `lastPop` users (`grep -n lastPop server/internal/stream/*.go`) and rename them. Existing tests that read `lastPop` keep their meaning under `lastProgress`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd server && go test ./internal/stream/ -race -count=1`
Expected: PASS, including the existing overflow and slow-consumer tests.

- [ ] **Step 5: Remove the `stuck-recovers` xfail**

Run: `cd server && go test -count=1 -tags ffmpeg -p 1 -parallel 2 -timeout 20m -run 'TestScenarios/(stuck-recovers|stuck-hung|slow-consumer-drops)$' -v ./internal/e2e/ 2>&1 | grep -E -- '--- |xfail \(|known gap|observed:'`
Expected: `stuck-recovers` fails with `known gap fixed`. Delete its `xfail:`/`xfailOn:` lines and re-run: all three PASS. `stuck-hung` shows `FfmpegRestarts:1` or more.

- [ ] **Step 6: Commit**

```bash
git add server/internal/stream/subqueue.go server/internal/stream/subqueue_test.go server/internal/stream/ingest.go server/internal/stream/ingest_test.go server/internal/e2e/testdata/scenarios/stuck-recovers.yaml
git commit -m "fix(ingest): measure transcoder progress in bytes written; drain first, kill only if none arrive

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: New subscribers join at the last complete video key

**Files:**
- Modify: `server/internal/stream/probe.go` (`ProgramInfo.VideoStreamType`)
- Modify: `server/internal/stream/ingest.go`
- Test: `server/internal/stream/join_test.go`. This file **already exists** and holds the SharePlay `Join` tests. Append to it and extend its import block; don't replace it.

**Interfaces:**
- Produces:
  - `ProgramInfo.VideoStreamType byte`, the PMT stream_type of the first video PID.
  - `type nalFlags struct { seq, sps, pps, idr bool }` and `func scanVideoKey(b []byte, streamType byte) nalFlags`. They scan PES payload bytes (without the PES header) for an MPEG-2 sequence header (`00 00 01 B3`, stream types 0x01/0x02) and H.264 SPS (7), PPS (8) and IDR (5) NAL units (stream type 0x1B).
  - `tsPayload(pkt []byte) []byte` (after any adaptation field) and `pesPayload(payload []byte) []byte` (after the PES header).
  - Key detection works **per PES, across TS packets**. A PES is classified when its scan buffer fills or the next PES starts, so the join moves to a key at most one PES late. The buffer keeps every packet, so the join still starts exactly at the key. Ingest collects up to `keyScanBytes = 16 * 184` payload bytes of each video PES. It classifies the PES when that buffer is full or the next PES starts, so a start code split across TS packets is found.
  - **A complete key** is either of:
    - an MPEG-2 PES with a sequence header;
    - an H.264 PES with an IDR whose SPS and PPS were seen, either in the same PES or in an earlier PES since the last key. In the earlier-PES case, the join starts at the PES that carried the SPS, so the parameter sets are **retained**.

    An IDR without parameter sets doesn't move the join.
  - `channelIngest.sinceJoin` (renamed from `sincePAT`) holds the packets since the join point. `joinValid` reports whether that point is a complete key:
    - While `joinValid`, a PAT doesn't restart the buffer.
    - When trimming at the 4 MiB cap removes the key, the join is **invalidated**: `joinValid = false`. The next PAT restarts the buffer, as today.
    - Without a valid key (start-up, after a trim, HEVC and other codecs), joins fall back to today's behavior: PAT, PMT and the packets since the last PAT.
  - `ingestJoinBufMax` rises from 1 MiB to 4 MiB, enough for a GOP of 19 Mbps MPEG-2.
  - Every new sub uses the join: a restart's sub, the caption tap and the Task 7 drain.

- [ ] **Step 1: Write the failing tests**

Append to `server/internal/stream/join_test.go`. Add `"bytes"` to its imports; it already imports `"context"`, `"errors"` and `"testing"`.

```go
// pesTS builds one video TS packet (no pointer field) whose payload ends
// exactly at the packet end (adaptation-field stuffing), so a payload can
// be split across packets at a chosen byte.
func pesTS(pid uint16, pusi bool, payload []byte) []byte {
	if len(payload) > 184 {
		panic("payload too long")
	}
	pkt := make([]byte, 0, tsPacketSize)
	b1 := byte(pid>>8) & 0x1F
	if pusi {
		b1 |= 0x40
	}
	pkt = append(pkt, tsSyncByte, b1, byte(pid))
	if len(payload) == 184 {
		pkt = append(pkt, 0x10)
	} else {
		af := 183 - len(payload) // adaptation_field_length
		pkt = append(pkt, 0x30, byte(af))
		if af > 0 {
			pkt = append(pkt, 0x00) // flags
			for i := 1; i < af; i++ {
				pkt = append(pkt, 0xFF)
			}
		}
	}
	return append(pkt, payload...)
}

// pesHead is a video PES header with PTS only.
var pesHead = []byte{0, 0, 1, 0xE0, 0, 0, 0x80, 0x80, 0x05, 0x21, 0, 1, 0, 1}

// nal is an H.264 NAL unit with a 4-byte start code and some body bytes.
func nal(typ byte) []byte { return []byte{0, 0, 0, 1, typ, 0x42, 0x00, 0x1F} }

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

// h264Ingest is a channelIngest that has seen PAT and an H.264 PMT.
func h264Ingest(t *testing.T) (*channelIngest, *tsBuilder, []byte) {
	t.Helper()
	c := newChannelIngest(nil, 1)
	b := newTSBuilder() // PMT: H.264 (0x1B) on mediaPID
	pat, pmt := b.PAT(), b.PMT()
	c.handleTSPacketLocked(pat)
	c.handleTSPacketLocked(pmt)
	return c, b, cat(pat, pmt)
}

func feed(c *channelIngest, pkts ...[]byte) {
	for _, p := range pkts {
		c.handleTSPacketLocked(p)
	}
}

func TestKeyJoinH264SplitAcrossPackets(t *testing.T) {
	c, b, tables := h264Ingest(t)
	pid := b.mediaPID
	before := pesTS(pid, true, cat(pesHead, nal(0x41))) // non-IDR slice
	// SPS, PPS, then the IDR whose start code is split across two packets.
	au := cat(pesHead, nal(0x67), nal(0x68), make([]byte, 160), []byte{0, 0})
	p1 := pesTS(pid, true, au[:184])
	p2 := pesTS(pid, false, cat(au[184:], []byte{0, 1, 0x65, 0x88}))
	next := pesTS(pid, true, cat(pesHead, nal(0x41)))
	feed(c, before, p1, p2, next)
	if got, want := c.joinSnapshotLocked(), cat(tables, p1, p2, next); !bytes.Equal(got, want) {
		t.Fatalf("join does not start at the SPS/PPS/IDR access unit (%d bytes, want %d)", len(got), len(want))
	}
}

func TestKeyJoinKeepsParameterSetsFromEarlierPES(t *testing.T) {
	c, b, tables := h264Ingest(t)
	pid := b.mediaPID
	params := pesTS(pid, true, cat(pesHead, nal(0x67), nal(0x68), nal(0x06))) // SPS, PPS, SEI
	idr := pesTS(pid, true, cat(pesHead, nal(0x65)))
	tail := pesTS(pid, true, cat(pesHead, nal(0x41)))
	feed(c, pesTS(pid, true, cat(pesHead, nal(0x41))), params, idr, tail)
	if got, want := c.joinSnapshotLocked(), cat(tables, params, idr, tail); !bytes.Equal(got, want) {
		t.Fatal("join dropped the SPS/PPS that precede the IDR")
	}
}

func TestKeyJoinIgnoresIDRWithoutParameterSets(t *testing.T) {
	c, b, _ := h264Ingest(t)
	pid := b.mediaPID
	// The next PES start makes ingest classify the IDR-only PES.
	feed(c, pesTS(pid, true, cat(pesHead, nal(0x65))), pesTS(pid, true, cat(pesHead, nal(0x41))))
	if c.joinValid {
		t.Fatal("an IDR without SPS/PPS made a valid join")
	}
}

func TestKeyJoinPATDoesNotRestartAValidJoin(t *testing.T) {
	c, b, _ := h264Ingest(t)
	pid := b.mediaPID
	key := pesTS(pid, true, cat(pesHead, nal(0x67), nal(0x68), nal(0x65)))
	m := pesTS(pid, true, cat(pesHead, nal(0x41))) // the next PES: the key is classified here
	pat2 := b.PAT()
	m2 := pesTS(pid, false, []byte{4, 5, 6})
	feed(c, key, m, pat2, m2)
	if got, want := c.joinSnapshotLocked(), cat(pat2, b.PMT(), key, m, m2); !bytes.Equal(got[2*tsPacketSize:], want[2*tsPacketSize:]) || !bytes.Equal(got[:tsPacketSize], pat2) {
		t.Fatal("a mid-GOP PAT restarted the key join")
	}
}

func TestKeyJoinInvalidatedWhenTrimmed(t *testing.T) {
	c, b, _ := h264Ingest(t)
	pid := b.mediaPID
	feed(c, pesTS(pid, true, cat(pesHead, nal(0x67), nal(0x68), nal(0x65))))
	filler := pesTS(pid, false, make([]byte, 184))
	for i := 0; i < ingestJoinBufMax/tsPacketSize+10; i++ {
		c.handleTSPacketLocked(filler)
	}
	if c.joinValid {
		t.Fatal("join still claims a key after the key was trimmed away")
	}
	pat := b.PAT()
	c.handleTSPacketLocked(pat)
	if got := c.joinSnapshotLocked(); len(got) != 2*tsPacketSize {
		t.Fatalf("after an invalidated join, a PAT must restart the buffer (got %d bytes)", len(got))
	}
}

func TestKeyJoinMPEG2SequenceHeader(t *testing.T) {
	c := newChannelIngest(nil, 1)
	b := newTSBuilder()
	b.pmtPID = 0x30
	pat, pmt := b.PAT(), pmtPacket() // MPEG-2 video on 0x31
	feed(c, pat, pmt)
	gop1 := pesTS(0x31, true, cat(pesHead, []byte{0, 0, 1, 0xB3, 0x50, 0x02, 0xD0, 0x37}))
	mid := pesTS(0x31, true, cat(pesHead, []byte{0, 0, 1, 0x00, 1}))
	gop2 := pesTS(0x31, true, cat(pesHead, []byte{0, 0, 1, 0xB3, 0x50, 0x02, 0xD0, 0x37}))
	tail := pesTS(0x31, false, []byte{4, 5, 6})
	next := pesTS(0x31, true, cat(pesHead, []byte{0, 0, 1, 0x00, 2})) // classifies gop2
	feed(c, gop1, mid, gop2, tail, next)
	if got, want := c.joinSnapshotLocked(), cat(pat, pmt, gop2, tail, next); !bytes.Equal(got, want) {
		t.Fatal("join is not PAT+PMT+last sequence header and what follows")
	}
}

func TestScanVideoKey(t *testing.T) {
	cases := []struct {
		name string
		b    []byte
		st   byte
		want nalFlags
	}{
		{"h264 sps pps idr", cat(nal(0x67), nal(0x68), nal(0x65)), 0x1B, nalFlags{sps: true, pps: true, idr: true}},
		{"h264 3-byte start code idr", []byte{0, 0, 1, 0x65, 0x88}, 0x1B, nalFlags{idr: true}},
		{"h264 non-idr slice", nal(0x41), 0x1B, nalFlags{}},
		{"mpeg2 sequence header", []byte{0, 0, 1, 0xB3, 1}, 0x02, nalFlags{seq: true}},
		{"mpeg2 picture only", []byte{0, 0, 1, 0x00, 1}, 0x02, nalFlags{}},
		{"hevc not scanned", []byte{0, 0, 1, 0x40, 1}, 0x24, nalFlags{}},
	}
	for _, c := range cases {
		if got := scanVideoKey(c.b, c.st); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd server && go test ./internal/stream/ -run 'TestKeyJoin|TestScanVideoKey' -v`
Expected: FAIL to compile (`undefined: scanVideoKey`, `c.joinValid undefined`).

- [ ] **Step 3: Implement**

`probe.go`: add to `ProgramInfo`:

```go
	VideoStreamType byte // PMT stream_type of VideoPID (0x02 MPEG-2, 0x1B H.264, …)
```

and in `parsePMT`'s video case set it with the PID:

```go
			if info.VideoPID == 0 {
				info.VideoPID = pid
				info.VideoMPEG2 = st == 0x01 || st == 0x02
				info.VideoStreamType = st
			}
```

`ingest.go`:
- Change `ingestJoinBufMax = 1 * 1024 * 1024` to:

```go
	// ingestJoinBufMax bounds the join buffer (tables + the stream since the
	// join point): a 19 Mbps MPEG-2 GOP fits.
	ingestJoinBufMax = 4 << 20
	// keyScanBytes is how much of each video PES payload is scanned for a
	// key (SEI before the IDR can be a few hundred bytes).
	keyScanBytes = 16 * 184
```

- In `channelIngest`, rename `sincePAT` to `sinceJoin` everywhere in the file (the struct, `joinSnapshotLocked`, `handleTSPacketLocked`, `capJoinLocked`), and add next to it:

```go
	videoStreamType byte
	sinceJoin       []byte // since the join point: a complete key if joinValid, else the last PAT
	joinValid       bool   // sinceJoin starts at a complete video key
	pesOff          int    // offset in sinceJoin of the current video PES (-1: before the buffer)
	pesScan         []byte // its first keyScanBytes of payload
	pesDone         bool   // the current PES was classified
	paramOff        int    // offset of the last PES with an H.264 SPS since the join (-1: none)
	ppsSeen         bool   // a PPS was seen at or after paramOff
```

  Initialize `pesOff: -1, paramOff: -1` in `newChannelIngest`.

- Replace `handleTSPacketLocked`, `capJoinLocked`, and add the key helpers:

```go
func (c *channelIngest) handleTSPacketLocked(pkt []byte) {
	pid := tsPID(pkt)
	switch {
	case pid == tsPIDPAT:
		c.lastPAT = pkt
		c.pmtPIDs = parsePATPMTPIDs(pkt)
		if !c.joinValid {
			c.restartJoinLocked()
		}
		c.capJoinLocked()
	case c.isPMTPID(pid):
		c.lastPMT = pkt
		info := parsePMT(pkt)
		if info.VideoPID != c.videoPID || info.VideoStreamType != c.videoStreamType {
			c.videoPID, c.videoStreamType = info.VideoPID, info.VideoStreamType
			c.joinValid = false // a different video stream: no key known yet
		}
		// Tables are served from lastPAT/lastPMT; do not duplicate into sinceJoin.
		c.capJoinLocked()
	default:
		if c.videoPID != 0 && pid == c.videoPID {
			if c.sourceHeight == 0 {
				// A header split across packets only delays detection.
				c.sourceHeight = sequenceHeaderHeight(pkt[4:])
			}
			c.videoPacketLocked(pkt)
		} else {
			c.sinceJoin = append(c.sinceJoin, pkt...)
		}
		c.capJoinLocked()
	}
}

// restartJoinLocked empties the buffer (today's join: since the last PAT).
func (c *channelIngest) restartJoinLocked() {
	c.sinceJoin = c.sinceJoin[:0]
	c.pesOff, c.paramOff, c.ppsSeen = -1, -1, false
	c.joinValid = false
}

// keyCodec reports whether joins can start at a video key for this stream.
func (c *channelIngest) keyCodec() bool {
	switch c.videoStreamType {
	case 0x01, 0x02, 0x1B:
		return true
	}
	return false
}

// videoPacketLocked appends a video packet, scanning each PES (across TS
// packets) for a key; a complete key moves the join point to it.
func (c *channelIngest) videoPacketLocked(pkt []byte) {
	payload := tsPayload(pkt)
	if pkt[1]&0x40 != 0 { // a new PES starts
		c.classifyPESLocked()
		c.pesOff, c.pesDone = len(c.sinceJoin), false
		c.pesScan = append(c.pesScan[:0], pesPayload(payload)...)
	} else if !c.pesDone && c.pesOff >= 0 && len(c.pesScan) < keyScanBytes {
		c.pesScan = append(c.pesScan, payload...)
	}
	c.sinceJoin = append(c.sinceJoin, pkt...)
	if !c.pesDone && len(c.pesScan) >= keyScanBytes {
		c.classifyPESLocked()
	}
}

// classifyPESLocked looks at the current PES once and moves the join point
// when it completes a key.
func (c *channelIngest) classifyPESLocked() {
	if c.pesDone || c.pesOff < 0 || !c.keyCodec() {
		c.pesDone = true
		return
	}
	c.pesDone = true
	f := scanVideoKey(c.pesScan, c.videoStreamType)
	switch c.videoStreamType {
	case 0x01, 0x02:
		if f.seq {
			c.joinAtLocked(c.pesOff)
		}
	case 0x1B:
		if f.sps {
			c.paramOff, c.ppsSeen = c.pesOff, f.pps
		} else if f.pps && c.paramOff >= 0 {
			c.ppsSeen = true
		}
		switch {
		case f.idr && f.sps && f.pps:
			c.joinAtLocked(c.pesOff)
		case f.idr && c.paramOff >= 0 && c.ppsSeen:
			c.joinAtLocked(c.paramOff) // keep the parameter sets ahead of the IDR
		}
		// An IDR without parameter sets is not a decodable join: keep the old one.
	}
}

// joinAtLocked makes sinceJoin start at off (a complete key).
func (c *channelIngest) joinAtLocked(off int) {
	c.sinceJoin = append(c.sinceJoin[:0], c.sinceJoin[off:]...)
	c.pesOff -= off
	if c.paramOff >= off {
		c.paramOff -= off
	} else {
		c.paramOff, c.ppsSeen = -1, false
	}
	c.joinValid = true
}

func (c *channelIngest) capJoinLocked() {
	trimmed := 0
	for len(c.lastPAT)+len(c.lastPMT)+len(c.sinceJoin)-trimmed > ingestJoinBufMax && len(c.sinceJoin)-trimmed >= tsPacketSize {
		trimmed += tsPacketSize
	}
	if trimmed == 0 {
		return
	}
	c.sinceJoin = append(c.sinceJoin[:0], c.sinceJoin[trimmed:]...)
	// The key at the head is gone: not a decodable join any more.
	c.joinValid = false
	c.pesOff -= trimmed
	if c.pesOff < 0 {
		c.pesOff, c.pesDone = -1, true
	}
	c.paramOff -= trimmed
	if c.paramOff < 0 {
		c.paramOff, c.ppsSeen = -1, false
	}
}
```

- Add near `tsPID`:

```go
// tsPayload is the packet's payload after any adaptation field (nil if none).
func tsPayload(pkt []byte) []byte {
	if len(pkt) < tsPacketSize {
		return nil
	}
	off := 4
	switch (pkt[3] >> 4) & 0x3 {
	case 0, 2:
		return nil
	case 3:
		off += 1 + int(pkt[4])
	}
	if off >= len(pkt) {
		return nil
	}
	return pkt[off:]
}

// pesPayload strips a PES header from the first packet's payload.
func pesPayload(p []byte) []byte {
	if len(p) >= 9 && p[0] == 0 && p[1] == 0 && p[2] == 1 {
		if h := 9 + int(p[8]); h <= len(p) {
			return p[h:]
		}
		return nil
	}
	return p
}

// nalFlags is what scanVideoKey found in a PES's first bytes.
type nalFlags struct{ seq, sps, pps, idr bool }

// scanVideoKey finds an MPEG-2 sequence header (stream types 0x01/0x02) or
// H.264 SPS, PPS and IDR NAL units (0x1B). Other codecs report nothing.
func scanVideoKey(b []byte, streamType byte) nalFlags {
	var f nalFlags
	for i := 0; i+3 < len(b); i++ {
		if b[i] != 0 || b[i+1] != 0 || b[i+2] != 1 {
			continue
		}
		code := b[i+3]
		switch streamType {
		case 0x01, 0x02:
			if code == 0xB3 {
				f.seq = true
			}
		case 0x1B:
			switch code & 0x1F {
			case 7:
				f.sps = true
			case 8:
				f.pps = true
			case 5:
				f.idr = true
			}
		}
	}
	return f
}
```

`joinSnapshotLocked` keeps its shape (tables plus `sinceJoin`). With `joinValid` it is a key join; without, it is today's since-PAT join.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd server && go test ./internal/stream/ -race -count=1`
Expected: PASS, including every existing join, probe and multitrack test. `TestJoinBufferGivesLateSubTables` still gets PAT, PMT and media: its stream has no key, so the join stays since-PAT.

Run: `cd server && go test -count=1 -tags ffmpeg -p 1 -parallel 2 -timeout 30m -run TestScenarios ./internal/e2e/`
Expected: PASS (only `long-outage-restart` is still xfail). For `transcoder-restart` and `stuck-hung`, the restarted FFmpeg's stderr no longer shows an `Invalid frame dimensions 0x0` storm. Grep the `-v` output for it and record the count before and after in the commit message.

- [ ] **Step 5: Commit**

```bash
git add server/internal/stream/probe.go server/internal/stream/ingest.go server/internal/stream/join_test.go
git commit -m "fix(ingest): new transcoders join at the last complete video key

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: Bowtie-owned timeline: a server-side observer with per-run publication gating (pure core)

**Files:**
- Create: `server/internal/stream/hlsplaylist.go`
- Create: `server/internal/stream/timeline.go`
- Test: `server/internal/stream/timeline_test.go`

**Invariants (what a client may rely on):**
1. Every PTS reset a client sees carries `#EXT-X-DISCONTINUITY`. A PTS reset happens only when FFmpeg restarts. Each rendition's first served segment of a restarted run is tagged, and nothing from a run that never opened is ever served. A run opens in every required rendition at once, or not at all.
2. Every rendition computes the same discontinuity number for the same broadcast moment. That holds while older segments are still in the window, and while the window head is a tagged segment.
3. Within a rendition, PDT never goes backwards and segments never overlap. Across renditions, the same PTS maps to the same wall-clock time, whenever each playlist happens to be written or read.
4. Each served playlist's `#EXT-X-MEDIA-SEQUENCE` never goes backwards, captions included.
5. A session never sits in a gate or a stall forever. A run in which a required rendition publishes nothing for `timelineGateTimeout` of **fed time** is expired and restarted. Every failed run counts toward a budget of `timelineMaxStalls` (3): expired held runs, expired open runs, and crashes. The budget resets only after every required rendition has published continuously for 60 s (none silent for `timelineHealthyGap`, 8 s), and a spent budget ends the session.
6. Nothing is ever stamped from incomplete gap history: if a required rendition still needs an anchor that the anchor cap would drop, the run is expired instead.

**Design:**
- **An observer, not request-driven stamping.** A per-session observer reads every producer playlist in `Layout.Renditions()` on its own clock: every 500 ms (`Run`), at session start, and right before an append restart (`MarkRestart`). Serving (`Retime`, `Window`) only reads its snapshot. It never stamps, and never reads or stats a file.
- **Runs and publication gating.** Each FFmpeg process is a run.
  - **The required cohort** is the reference (`Renditions()[0]`, the top rung) plus every rendition that has ever published a segment. It only grows: a discarded or expired run never shrinks it. A rendition that appears later at startup joins epoch 0 untagged, as today, and is required from then on.
  - **A run is held until every required rendition has a publishable segment staged.** That means:
    - its first segment in the run has its first PTS actually read from the file (see below);
    - for a sibling, that first segment's **whole span** lies within the media end of the reference's placed segments, so it is published in the same pass. "Seen" is not enough: a video run covering media [0,4) with audio starting at 8 stays held, and so does audio [2,6) against video [0,4).
  - **Opening and publishing are one locked pass.** The run's base, start and epoch are fixed, and every required rendition's staged segments are stamped, before the lock is released. No served playlist can see the run with a required rendition missing.
  - Run 0 follows the same rule, with only the reference required.
  - **A run that is still held when FFmpeg dies is discarded** (`MarkRestart`). Its segments are never served, and `Retime` skips them in FFmpeg's playlist.
  - **Liveness (`ExpireStalledRun(elapsed, fresh)`).** The manager calls it on every `maintain` tick (5 s) with the time since its last tick and whether **fresh source bytes** reached this FFmpeg's input in that tick (Task 10). Bytes from the cached join snapshot or a reset head are not fresh.
    - **Fed time** accrues only on fresh ticks. A quiet tick pauses it and never erases what accrued, so sparse feeding (bytes every 10 s) still adds up.
    - A **held** run is stalled once its fed time reaches `timelineGateTimeout` (missing lists the blocking renditions, including "no segment at all").
    - An **open** run is stalled when any required rendition has published nothing for `timelineGateTimeout` of fed time, whether or not it has a pending segment: a reference that stopped, a sibling stuck past it, or a segment whose PTS can't be read. Each required rendition has its own counter, reset on every tick in which it published.
    - A run whose required renditions still need gap history beyond the anchor cap is stalled at once (see "Per-segment state").
  - Expiry is atomic: under the timeline lock the run is marked expired, and an expired run never opens or publishes again. `publishLocked` returns at once for an expired run, so neither the observer nor `MarkRestart`'s final observation stamps a segment that becomes publishable after the expiry (the reference resuming before the kill lands). Nothing can open between the decision and the manager stopping FFmpeg. The restart's `MarkRestart` discards it.
  - **Only a process that ran ends a run.** A run belongs to a process: run 0 to the first, and each later run once the manager calls `ProcessStarted` after the restart's FFmpeg started. `MarkRestart` on a run without a process (a restart attempt whose attach failed, during an outage) is a no-op: it ends nothing, counts nothing, creates no run and returns the budget used. So any number of failed attach attempts cost nothing, and only the process that actually died used budget.
  - **The stall budget.** `ExpireStalledRun` and `MarkRestart` return the count of failed runs. Every expiry counts, and so does a run that ended without being expired (a crash that `supervise` restarts before the gate fires; `MarkRestart` counts it). Opening a run does **not** reset the count. It resets only after `timelineHealthyAfter` (60 s, `healthyResetAfter`, the same as backoff's healthy reset) of ticks in which the run was open and no required rendition had gone `timelineHealthyGap` (2 × `hlsSegmentDuration`, 8 s) without publishing. Segments are 4–6 s and ticks 5 s, so a tick without a new segment is normal; two in a row are not, and restart the 60 s. An outage publishes nothing, so it never counts as healthy. The manager ends the session at `timelineMaxStalls` (3) (Task 10). Renditions are never dropped part-way.
  - A process fed nothing is in an outage, not a stall: ingest owns that case, and a restart during a long outage must survive it (`long-outage-restart`).
  - There is no partial-run merging and no `maxMergedRuns`.
- **Where a segment sits in its run.**
  - Its first PTS is read from the segment file (`segmentFirstPTS`, the first PES with a PTS, in the first 64 KiB). Its place is that PTS relative to the run's **base**: the earliest first PTS of any rendition in the run.
  - The base is fixed when the run opens, from the real first PTS of every rendition's first segment. All renditions are muxed from FFmpeg's one output clock, so this offset doesn't depend on when a playlist or file was written.
  - PTS differences are unwrapped across the 33-bit wrap, choosing the period nearest the rendition's chained position.
  - A failed PTS read is retried on every observation: the file may not be complete or readable yet.
    - A rendition's **first** segment in a run never falls back to EXTINF. Until its PTS is read, the rendition isn't ready (it blocks the gate, or, joining an open run, waits).
    - A later segment chains on the rendition's own EXTINFs only after `timelinePTSReads` (6) failed reads.
- **Wall clock.**
  - A run's wall-clock start is the earliest `mtime − EXTINF − media` across the first segments of all its renditions. It is never earlier than the latest published end, so PDT never goes backwards across a restart.
  - A segment's PDT is `start + media + shift(media)`, clamped to its rendition's previous end and to the run floor.
- **Gaps that FFmpeg closed.**
  - When the chosen timestamp option re-bases (Task 6), the PTS stays contiguous over an outage. The outage only shows in the segment files' mtimes.
  - Gaps are detected on the **reference rendition only**. A reference segment that starts more than `timelineGapThreshold` (2 s) later than predicted adds an anchor `{media, shift}`. The threshold is measured beyond the reference's own steady lag at run open, so encoder delay is not a gap.
  - **The reference publishes in order, and only placed segments.** Its publication loop stops at the first segment without an assigned media position (PTS not read yet, fewer than `timelinePTSReads` failures), the same as the sibling loop. An anchor's media point is always a real assigned position, never a provisional one.
  - **A sibling segment is stamped only once the reference covers its whole span** (`media + duration ≤` the reference's published media end). It takes the shift of the reference interval it overlaps most (`shiftFor`), so a sibling cut a few ms before or after a gap anchor gets the same shift as the reference segment it belongs with. That way a gap shifts every rendition at the same moment, the same moment carries the same discontinuity number in every rendition, and publishing late can never create or move an anchor.
  - A PTS hole that FFmpeg kept is already in the media offset, and needs no anchor.
- **Bowtie's media sequence.** Discarded segments leave holes in FFmpeg's numbering. Served `#EXT-X-MEDIA-SEQUENCE` is therefore Bowtie's own per-rendition counter, assigned at publication, monotonic and gap-free. `Window` reports it too (Task 11).
- **Per-segment state, compacted.**
  - Each published segment keeps its Bowtie sequence, start, duration, epoch and tag flag for as long as it is in FFmpeg's window. The window head therefore keeps its tag, and `DISCONTINUITY-SEQUENCE = epoch(head) − tag(head)`.
  - Skip marks also live only for the window.
  - Only the current run is kept. Its anchors are kept from the last one at or before the lowest **arrival watermark**: for each sibling, the media point from which its segments are still to come. A sibling with nothing placed in the run yet (its playlist absent or late) has watermark 0, so when it arrives every gap still applies to it.
  - Bounds: at most `timelineMaxPending` (64) waiting segments per rendition (older ones, and ones that slid out of FFmpeg's window unpublished, are never served) and `timelineMaxAnchors` (64) anchors, both logged when hit.
  - **The anchor cap never stamps from incomplete history.** When more than 64 anchors would remain:
    - if a **required** rendition's watermark is below an anchor the cap would drop, nothing is dropped and the run is marked to expire (`missing` is those renditions). The next `ExpireStalledRun` expires it through the normal kill path, and it counts toward the stall budget;
    - otherwise only non-required renditions lag. The oldest anchors are dropped, the shift before the first kept one is kept as the run's head shift, and a non-required sibling's segments from before the first kept anchor (`histFloor`) are logged and never served.
  - Earlier runs survive only as the required cohort.
- **Captions.** `Retime("<ref>_vtt", captionPlaylist)` serves the caption playlist (`Layout.CaptionPlaylist()`, the top rung's) with the reference's stamps, tags, skips and Bowtie sequence numbers, by FFmpeg sequence number. `repairCaptionPlaylist` keeps them equal. Caption URIs, `.` included, pass through. No `.vtt` file is read.
  - The observer reads the caption playlist's `MEDIA-SEQUENCE` (and nothing else from it). The reference's stamps are kept down to that number, at most `timelineCaptionKeep` (64) segments below the video window, so a lagging caption playlist still finds its stamps after the video window slides.
- **MEDIA-SEQUENCE floor.** `Retime` remembers, per served playlist name, the `MEDIA-SEQUENCE` it last served. A later (staler) read drops head segments numbered below it. An empty result is numbered with the lowest stamp still to come from that read's first sequence on, never below the floor. So a caption playlist can't serve 100 and then 80.
- **Cost of the gate.**
  - Up to one segment of extra latency for siblings, which wait until the reference covers their whole span.
  - After a restart, the run's segments are served only once every required rendition has a publishable one. That is normally the first observation pass after the slowest rendition's first segment, or one reference segment later when a sibling starts ahead of the reference.
  - The reference's last segments before a crash are served. Sibling segments past the reference's last are dropped, which is at most one segment.

**Interfaces:**
- Produces (package `stream`):
  - `const WallClockLayout = "2006-01-02T15:04:05.000Z07:00"`.
  - `const hlsSegmentDuration = 4 * time.Second`, the live `-hls_time`. Task 12 reuses it.
  - `timelineGateTimeout = playlistTimeout`, `timelineMaxStalls = 3`, `timelineHealthyAfter = healthyResetAfter`, `timelineHealthyGap = 2 * hlsSegmentDuration`, `timelinePTSReads = 6`, `timelineMaxPending = 64`, `timelineMaxAnchors = 64`, `timelineCaptionKeep = 64`.
  - `type MediaPlaylist struct { Header []string; MediaSequence int64; Segments []PlaylistSegment; Trailer []string }` and `type PlaylistSegment struct { Duration float64; Tags []string; URI string }`.
  - `func ParseMediaPlaylist(raw []byte) MediaPlaylist` drops `#EXT-X-PROGRAM-DATE-TIME`, `#EXT-X-DISCONTINUITY` and `#EXT-X-DISCONTINUITY-SEQUENCE`.
  - `func NewTimeline(dir string, renditions []string, now func() time.Time) *Timeline`.
    - `renditions` are the media playlists to observe, reference first, never captions. The reference's caption playlist `<reference>_vtt.m3u8` is read for its `MEDIA-SEQUENCE` only.
    - `now` must be the real clock, because stamps are compared with segment file mtimes.
  - `(*Timeline).Observe()`, `Run(ctx)`.
  - `(*Timeline).MarkRestart() (stalls int)`: ends the current run, counts it as failed if it ended without being expired, and returns the failed-run count. Call it at the start of every append restart attempt, before the new process exists. Idempotent until `ProcessStarted`.
  - `(*Timeline).ProcessStarted()`: the restart's FFmpeg is running; the current run now belongs to it, and the next `MarkRestart` ends it.
  - `(*Timeline).ExpireStalledRun(elapsed time.Duration, fresh bool) (missing []string, stalls int, expired bool)`: one maintain tick; `elapsed` is the time since the last tick, `fresh` whether fresh source bytes reached FFmpeg in it.
  - `(*Timeline).Retime(playlist string, raw []byte) []byte`. `playlist` is the served name without `.m3u8`: a rendition, or `<reference>_vtt`.
  - `(*Timeline).Window(rendition string) WindowTimeline`, with `WindowTimeline{MediaSequence int64; Segments []TimelineSegment}`, `TimelineSegment{Start time.Time; Duration float64}`, and `WindowStart()`/`LiveEdge()`.
  - `func segmentFirstPTS(path string) (int64, bool)`. `stream/probe.go` has no PTS parser, and the fake's `tsloop.PESTimestamps` lives in a test-only package tree, so this is a 30-line reader of its own.

- [ ] **Step 1: Write the failing tests**

Create `server/internal/stream/timeline_test.go`:

```go
package stream

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

var tl0 = time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC)

func sec(f float64) time.Time { return tl0.Add(time.Duration(f * float64(time.Second))) }

// seg is one segment: EXTINF, the file's mtime (seconds after tl0) and the
// first PTS inside it (seconds; negative: the file carries no PTS).
type seg struct{ dur, end, pts float64 }

// tsWithPTS is one TS packet starting a video PES with the given PTS.
func tsWithPTS(pts float64) []byte {
	v := int64(math.Round(pts*90000)) & (int64(1)<<33 - 1)
	p := make([]byte, 188)
	for i := range p {
		p[i] = 0xFF
	}
	copy(p, []byte{0x47, 0x41, 0x00, 0x10, 0, 0, 1, 0xE0, 0, 0, 0x80, 0x80, 5})
	p[13] = byte(0x21 | (v>>29)&0x0E)
	p[14] = byte(v >> 22)
	p[15] = byte((v>>14)&0xFE | 1)
	p[16] = byte(v >> 7)
	p[17] = byte((v<<1)&0xFE | 1)
	return p
}

// writeRun writes name's playlist and segment files (PTS, mtimes) and
// returns the playlist bytes.
func writeRun(t *testing.T, dir, name string, mseq int64, segs ...seg) []byte {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:%d\n", mseq)
	for i, s := range segs {
		uri := fmt.Sprintf("%s_%05d.ts", name, mseq+int64(i))
		path := filepath.Join(dir, uri)
		body := []byte("x")
		if s.pts >= 0 {
			body = tsWithPTS(s.pts)
		}
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, sec(s.end), sec(s.end)); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "#EXTINF:%.6f,\n%s\n", s.dur, uri)
	}
	raw := []byte(b.String())
	if err := os.WriteFile(filepath.Join(dir, name+".m3u8"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return raw
}

// chain is n segments of dur seconds ending at end0+dur, end0+2·dur, …,
// with PTS pts0, pts0+dur, …
func chain(end0, dur float64, n int, pts0 float64) []seg {
	out := make([]seg, n)
	for i := range out {
		out[i] = seg{dur, end0 + dur*float64(i+1), pts0 + dur*float64(i)}
	}
	return out
}

func cat(parts ...[]seg) []seg {
	var out []seg
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// newTestTimeline returns a timeline whose clock is *clock (start: 1000 s).
func newTestTimeline(dir string, renditions ...string) (*Timeline, *[]string, *time.Time) {
	var logs []string
	clock := sec(1000)
	tl := NewTimeline(dir, renditions, func() time.Time { return clock })
	tl.logf = func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	return tl, &logs, &clock
}

type servedSeg struct {
	pdt  time.Time
	disc bool
	uri  string
}

func parseServed(t *testing.T, out []byte) (mseq, dseq int64, segs []servedSeg) {
	t.Helper()
	var cur servedSeg
	for _, line := range strings.Split(string(out), "\n") {
		switch {
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			mseq, _ = strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64)
		case strings.HasPrefix(line, "#EXT-X-DISCONTINUITY-SEQUENCE:"):
			dseq, _ = strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-DISCONTINUITY-SEQUENCE:"), 10, 64)
		case line == "#EXT-X-DISCONTINUITY":
			cur.disc = true
		case strings.HasPrefix(line, "#EXT-X-PROGRAM-DATE-TIME:"):
			v, err := time.Parse(time.RFC3339Nano, strings.TrimPrefix(line, "#EXT-X-PROGRAM-DATE-TIME:"))
			if err != nil {
				t.Fatalf("bad PDT line %q", line)
			}
			cur.pdt = v
		case line != "" && !strings.HasPrefix(line, "#"):
			cur.uri = line
			segs = append(segs, cur)
			cur = servedSeg{}
		}
	}
	return mseq, dseq, segs
}

func served(t *testing.T, tl *Timeline, rendition string, raw []byte) (int64, int64, []servedSeg) {
	t.Helper()
	return parseServed(t, tl.Retime(rendition, raw))
}

// dnum is the discontinuity number a client computes for segs[i].
func dnum(dseq int64, segs []servedSeg, i int) int64 {
	n := dseq
	for _, s := range segs[:i+1] {
		if s.disc {
			n++
		}
	}
	return n
}

// near allows for millisecond PDT formatting.
func near(a, b time.Time) bool {
	d := a.Sub(b)
	return d >= -2*time.Millisecond && d <= 2*time.Millisecond
}

func TestTimelineStampsFromPTSAndMtime(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480")
	raw := writeRun(t, dir, "v480", 0, seg{4, 4, 10}, seg{4, 8, 14}, seg{4, 12.3, 18})
	tl.Observe()
	_, _, segs := served(t, tl, "v480", raw)
	for i, want := range []time.Time{sec(0), sec(4), sec(8)} {
		if !near(segs[i].pdt, want) || segs[i].disc {
			t.Fatalf("segment %d: %+v, want PDT %v and no discontinuity", i, segs[i], want)
		}
	}
}

// FFmpeg closed a 5 s outage (contiguous PTS): the file mtimes show it.
func TestTimelineJumpsForwardOverAClosedGap(t *testing.T) {
	dir := t.TempDir()
	tl, logs, _ := newTestTimeline(dir, "v480")
	writeRun(t, dir, "v480", 0, chain(0, 4, 2, 1.4)...)
	tl.Observe()
	raw := writeRun(t, dir, "v480", 0, cat(chain(0, 4, 2, 1.4), []seg{{4, 17, 9.4}})...)
	tl.Observe()
	_, _, segs := served(t, tl, "v480", raw)
	if !near(segs[2].pdt, sec(13)) || segs[2].disc {
		t.Fatalf("segment 2: %+v, want PDT %v without a discontinuity", segs[2], sec(13))
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "gap") {
		t.Fatalf("logs %v, want one gap line", *logs)
	}
}

// FFmpeg kept the hole in the PTS: the media offset already carries it.
func TestTimelinePTSHoleNeedsNoAnchor(t *testing.T) {
	dir := t.TempDir()
	tl, logs, _ := newTestTimeline(dir, "v480")
	raw := writeRun(t, dir, "v480", 0, seg{4, 4, 1.4}, seg{4, 8, 5.4}, seg{4, 17, 14.4})
	tl.Observe()
	_, _, segs := served(t, tl, "v480", raw)
	if !near(segs[2].pdt, sec(13)) || len(*logs) != 0 {
		t.Fatalf("segment 2 at %v (want %v), logs %v", segs[2].pdt, sec(13), *logs)
	}
}

// Audio (4.032 s) and video (4.004 s) are cut at different points: each
// chains on its own segments (no overlap), and the same PTS gets the same
// wall-clock time in both. An audio segment waits until video covers its
// whole span, so the tenth audio segment needs an eleventh video one.
func TestTimelineUnequalDurationsNeverOverlap(t *testing.T) {
	dir := t.TempDir()
	tl, logs, _ := newTestTimeline(dir, "v480", "aac0")
	rv := writeRun(t, dir, "v480", 0, chain(0, 4.004, 10, 1.4)...)
	ra := writeRun(t, dir, "aac0", 0, chain(0, 4.032, 10, 1.4)...)
	tl.Observe()
	if _, _, as := served(t, tl, "aac0", ra); len(as) != 9 {
		t.Fatalf("served %d audio segments, want 9 (the tenth ends after the video)", len(as))
	}
	rv = writeRun(t, dir, "v480", 0, chain(0, 4.004, 11, 1.4)...)
	tl.Observe()
	_, _, vs := served(t, tl, "v480", rv)
	_, _, as := served(t, tl, "aac0", ra)
	if len(vs) != 11 || len(as) != 10 || len(*logs) != 0 {
		t.Fatalf("served %d video, %d audio; logs %v", len(vs), len(as), *logs)
	}
	for i := 1; i < 10; i++ {
		if end := as[i-1].pdt.Add(4032 * time.Millisecond); as[i].pdt.Before(end.Add(-time.Millisecond)) {
			t.Fatalf("audio segment %d starts %v before segment %d ends", i, end.Sub(as[i].pdt), i-1)
		}
		if want := vs[i].pdt.Add(time.Duration(i) * 28 * time.Millisecond); !near(as[i].pdt, want) {
			t.Fatalf("audio segment %d at %v, want %v (its PTS is %d ms after video's)", i, as[i].pdt, want, 28*i)
		}
	}
}

// A sibling playlist that shows up 5 s late, with files written late, gets
// the same wall clock for the same PTS; the reference isn't disturbed.
func TestTimelineStaggeredPublicationKeepsAlignment(t *testing.T) {
	dir := t.TempDir()
	tl, logs, _ := newTestTimeline(dir, "v480", "aac0")
	writeRun(t, dir, "v480", 0, chain(0, 4, 3, 1.4)...)
	tl.Observe()
	ra := writeRun(t, dir, "aac0", 0, chain(5, 4, 3, 1.42)...) // every file 5 s late
	rv := writeRun(t, dir, "v480", 0, chain(0, 4, 5, 1.4)...)
	tl.Observe()
	_, _, vs := served(t, tl, "v480", rv)
	_, _, as := served(t, tl, "aac0", ra)
	if len(as) != 3 || len(vs) != 5 || len(*logs) != 0 {
		t.Fatalf("served %d audio, %d video; logs %v", len(as), len(vs), *logs)
	}
	for i := range as {
		if !near(as[i].pdt, vs[i].pdt.Add(20*time.Millisecond)) {
			t.Fatalf("audio %d at %v, video %d at %v: want 20 ms apart (their PTS)", i, as[i].pdt, i, vs[i].pdt)
		}
	}
	if !near(vs[4].pdt, sec(16)) {
		t.Fatalf("video segment 4 at %v, want %v", vs[4].pdt, sec(16))
	}
}

// The reference's files land 3 s after the audio's (encoder delay): a
// steady lag, not a gap. A real 5 s gap later shifts both renditions at
// the same media point.
func TestTimelineEncoderDelayIsNotAGap(t *testing.T) {
	dir := t.TempDir()
	tl, logs, _ := newTestTimeline(dir, "v480", "aac0")
	writeRun(t, dir, "v480", 0, chain(3, 4, 5, 1.4)...)
	writeRun(t, dir, "aac0", 0, chain(0, 4, 5, 1.4)...)
	tl.Observe()
	if len(*logs) != 0 {
		t.Fatalf("encoder delay logged as a gap: %v", *logs)
	}
	rv := writeRun(t, dir, "v480", 0, cat(chain(3, 4, 5, 1.4), []seg{{4, 3 + 24 + 5, 21.4}})...)
	ra := writeRun(t, dir, "aac0", 0, cat(chain(0, 4, 5, 1.4), []seg{{4, 24 + 5, 21.4}})...)
	tl.Observe()
	_, _, vs := served(t, tl, "v480", rv)
	_, _, as := served(t, tl, "aac0", ra)
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "gap") {
		t.Fatalf("logs %v, want one gap", *logs)
	}
	if !near(vs[5].pdt, sec(25)) || !near(as[5].pdt, sec(25)) || !near(vs[0].pdt, sec(0)) {
		t.Fatalf("after the gap: video %v audio %v, want %v", vs[5].pdt, as[5].pdt, sec(25))
	}
}

// Stamps don't depend on when the observer looked or the order playlists
// are served.
func TestTimelineObservationTimingDoesNotMatter(t *testing.T) {
	dir := t.TempDir()
	t1, _, _ := newTestTimeline(dir, "v480", "aac0")
	t2, _, _ := newTestTimeline(dir, "v480", "aac0")
	writeRun(t, dir, "v480", 0, chain(0.3, 4.004, 2, 1.4)...)
	writeRun(t, dir, "aac0", 0, chain(0.1, 4.032, 2, 1.41)...)
	t1.Observe()
	rv := writeRun(t, dir, "v480", 0, chain(0.3, 4.004, 3, 1.4)...)
	ra := writeRun(t, dir, "aac0", 0, chain(0.1, 4.032, 3, 1.41)...)
	t1.Observe()
	t2.Observe()
	a1, v1 := string(t1.Retime("aac0", ra)), string(t1.Retime("v480", rv))
	v2, a2 := string(t2.Retime("v480", rv)), string(t2.Retime("aac0", ra))
	if a1 != a2 || v1 != v2 {
		t.Fatalf("observation timing changed the stamps:\n%s\n%s\n%s\n%s", a1, a2, v1, v2)
	}
}

// Two crashes; the middle run wrote audio only. It is never served; the
// next run opens as discontinuity 1 in both renditions, which carry
// Bowtie's own media sequence numbers. The trailing audio segment the
// reference never covered isn't served either.
func TestTimelinePartialRunIsNeverServed(t *testing.T) {
	dir := t.TempDir()
	tl, logs, _ := newTestTimeline(dir, "v480", "aac0")
	writeRun(t, dir, "v480", 0, chain(0, 4, 3, 1.4)...)
	writeRun(t, dir, "aac0", 0, chain(0, 4, 4, 1.4)...) // audio one segment ahead
	restarted(tl)
	held := writeRun(t, dir, "aac0", 0, cat(chain(0, 4, 4, 1.4), []seg{{4, 40, 1.4}})...) // run 1: audio only
	tl.Observe()
	if _, _, as := served(t, tl, "aac0", held); len(as) != 3 {
		t.Fatalf("served %d audio segments while run 1 is held, want 3", len(as))
	}
	restarted(tl)
	// Run 2: audio's first segment ends 20 ms after video's, so video's
	// second segment is what lets the run open.
	rv := writeRun(t, dir, "v480", 0, cat(chain(0, 4, 3, 1.4), []seg{{4, 64, 1.4}, {4, 68, 5.4}})...)
	ra := writeRun(t, dir, "aac0", 0, cat(chain(0, 4, 4, 1.4), []seg{{4, 40, 1.4}, {4, 64, 1.42}})...)
	tl.Observe()
	mv, dv, vs := served(t, tl, "v480", rv)
	ma, da, as := served(t, tl, "aac0", ra)
	if len(vs) != 5 || len(as) != 4 || as[3].uri != "aac0_00005.ts" {
		t.Fatalf("video %+v\naudio %+v", vs, as)
	}
	if !vs[3].disc || !as[3].disc || mv != 0 || ma != 0 || dv != 0 || da != 0 {
		t.Fatalf("video mseq %d dseq %d %+v\naudio mseq %d dseq %d %+v", mv, dv, vs, ma, da, as)
	}
	if dnum(dv, vs, 3) != 1 || dnum(da, as, 3) != 1 {
		t.Fatal("run 2 is not discontinuity 1 in both renditions")
	}
	if !near(as[3].pdt, vs[3].pdt.Add(20*time.Millisecond)) {
		t.Fatalf("run 2 starts at %v (video) and %v (audio), want 20 ms apart", vs[3].pdt, as[3].pdt)
	}
	// The windows slide: the tagged segment is the head, then gone.
	rv = writeRun(t, dir, "v480", 3, seg{4, 64, 1.4}, seg{4, 68, 5.4})
	ra = writeRun(t, dir, "aac0", 5, seg{4, 64, 1.42}, seg{4, 68, 5.42})
	tl.Observe()
	mv, dv, vs = served(t, tl, "v480", rv)
	ma, da, as = served(t, tl, "aac0", ra)
	if mv != 3 || ma != 3 || !vs[0].disc || !as[0].disc || dnum(dv, vs, 0) != 1 || dnum(da, as, 0) != 1 {
		t.Fatalf("head: video mseq %d dseq %d %+v; audio mseq %d dseq %d %+v", mv, dv, vs, ma, da, as)
	}
	rv = writeRun(t, dir, "v480", 4, seg{4, 68, 5.4}, seg{4, 72, 9.4})
	ra = writeRun(t, dir, "aac0", 6, seg{4, 68, 5.42}, seg{4, 72, 9.42})
	tl.Observe()
	mv, dv, vs = served(t, tl, "v480", rv)
	ma, da, as = served(t, tl, "aac0", ra)
	if mv != 4 || ma != 4 || dv != 1 || da != 1 || vs[0].disc || as[0].disc {
		t.Fatalf("after the slide: video mseq %d dseq %d; audio mseq %d dseq %d", mv, dv, ma, da)
	}
	if !strings.Contains(strings.Join(*logs, "\n"), "discarded") {
		t.Fatalf("discard not logged: %v", *logs)
	}
}

// A rendition misses two restarted runs (each held, then restarted after
// the gate timeout). When it returns, every rendition opens the run as
// discontinuity 1 while the old segments are still served.
func TestTimelineRenditionMissingTwoRunsReturnsAligned(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480", "aac0", "ac30")
	base := chain(0, 4, 3, 1.4)
	for _, n := range []string{"v480", "aac0", "ac30"} {
		writeRun(t, dir, n, 0, base...)
	}
	tl.Observe()
	extra := []seg{}
	for _, end := range []float64{30, 50} { // runs 1 and 2: no ac30
		restarted(tl)
		extra = append(extra, seg{4, end, 1.4})
		writeRun(t, dir, "v480", 0, cat(base, extra)...)
		writeRun(t, dir, "aac0", 0, cat(base, extra)...)
		tl.Observe()
		if _, _, expired := tl.ExpireStalledRun(10*time.Second, true); expired {
			t.Fatal("expired before the timeout")
		}
		// The first restart (a crash) counted; each expiry counts.
		missing, stalls, expired := tl.ExpireStalledRun(10*time.Second, true)
		if !expired || strings.Join(missing, ",") != "ac30" || stalls != 1+len(extra) {
			t.Fatalf("ExpireStalledRun = %v, %d, %v; want [ac30], %d, true", missing, stalls, expired, 1+len(extra))
		}
	}
	restarted(tl) // the kill path restarted FFmpeg again
	extra = append(extra, seg{4, 70, 1.4})
	raw := map[string][]byte{
		"v480": writeRun(t, dir, "v480", 0, cat(base, extra)...),
		"aac0": writeRun(t, dir, "aac0", 0, cat(base, extra)...),
		"ac30": writeRun(t, dir, "ac30", 0, cat(base, []seg{{4, 70, 1.4}})...),
	}
	tl.Observe()
	// Opening doesn't reset the stall budget (only a healthy stretch does),
	// and the run that just published every rendition isn't stalled.
	if _, _, expired := tl.ExpireStalledRun(timelineGateTimeout, true); expired || tl.stalls != 3 {
		t.Fatalf("expired %v, %d stalls after the run opened; want false, 3", expired, tl.stalls)
	}
	for name, body := range raw {
		_, dseq, segs := served(t, tl, name, body)
		last := len(segs) - 1
		if len(segs) != 4 || !segs[last].disc || dnum(dseq, segs, last) != 1 || dnum(dseq, segs, 0) != 0 {
			t.Fatalf("%s: dseq %d %+v", name, dseq, segs)
		}
	}
}

// At startup a rendition that shows up late joins run 0 untagged.
func TestTimelineLateRenditionAtStartupJoinsEpochZero(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480", "aac0")
	writeRun(t, dir, "v480", 0, chain(0, 4, 2, 1.4)...)
	tl.Observe()
	rv := writeRun(t, dir, "v480", 0, chain(0, 4, 3, 1.4)...)
	ra := writeRun(t, dir, "aac0", 0, chain(0, 4, 2, 1.38)...)
	tl.Observe()
	_, dv, vs := served(t, tl, "v480", rv)
	_, da, as := served(t, tl, "aac0", ra)
	if len(as) != 2 || as[0].disc || da != 0 || dv != 0 || vs[0].disc {
		t.Fatalf("video dseq %d %+v; audio dseq %d %+v", dv, vs, da, as)
	}
	if !near(as[0].pdt, vs[0].pdt.Add(-20*time.Millisecond)) {
		t.Fatalf("late audio at %v, video at %v", as[0].pdt, vs[0].pdt)
	}
}

func TestTimelineNeverGoesBackwardsAtRestart(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480")
	writeRun(t, dir, "v480", 0, chain(0, 4, 3, 1.4)...)
	restarted(tl)
	// The new run's first segment claims to end at 10 s, before the old run's 12 s end.
	raw := writeRun(t, dir, "v480", 0, cat(chain(0, 4, 3, 1.4), []seg{{4, 10, 1.4}})...)
	tl.Observe()
	_, _, segs := served(t, tl, "v480", raw)
	if !segs[3].disc || !near(segs[3].pdt, sec(12)) {
		t.Fatalf("restart segment %+v, want DISCONTINUITY at %v", segs[3], sec(12))
	}
}

func TestTimelineRestartWithoutSegmentsCountsOnce(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480")
	writeRun(t, dir, "v480", 0, chain(0, 4, 2, 1.4)...)
	restarted(tl) // this run dies before writing a segment
	restarted(tl)
	raw := writeRun(t, dir, "v480", 0, cat(chain(0, 4, 2, 1.4), []seg{{4, 30, 1.4}})...)
	tl.Observe()
	_, _, segs := served(t, tl, "v480", raw)
	if !segs[2].disc || segs[1].disc {
		t.Fatalf("segments %+v, want one DISCONTINUITY on segment 2", segs)
	}
	raw = writeRun(t, dir, "v480", 3, seg{4, 34, 5.4})
	tl.Observe()
	if _, dseq, _ := served(t, tl, "v480", raw); dseq != 1 {
		t.Fatalf("DISCONTINUITY-SEQUENCE %d, want 1", dseq)
	}
}

// Serving reads the snapshot only: newer segments wait for the observer,
// segments that slid out are dropped from the head; nothing is restamped.
func TestTimelineServesOnlyPublishedSegments(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480")
	writeRun(t, dir, "v480", 0, chain(0, 4, 2, 1.4)...)
	tl.Observe()
	newer := writeRun(t, dir, "v480", 0, chain(0, 4, 3, 1.4)...) // not observed yet
	if _, _, segs := served(t, tl, "v480", newer); len(segs) != 2 {
		t.Fatalf("served %d segments, want the 2 observed", len(segs))
	}
	writeRun(t, dir, "v480", 2, seg{4, 12, 9.4}, seg{4, 16, 13.4})
	tl.Observe()
	mseq, _, segs := served(t, tl, "v480", newer) // stale: still starts at 0
	if mseq != 2 || len(segs) != 1 || !near(segs[0].pdt, sec(8)) {
		t.Fatalf("stale playlist served from %d: %+v", mseq, segs)
	}
}

// Caption playlists take the rung's stamps and tags by sequence number;
// their URIs (FFmpeg 5.1's "." included) pass through, and no .vtt is read.
func TestTimelineCaptionsUseRungStamps(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v720")
	writeRun(t, dir, "v720", 0, chain(0, 4, 2, 1.4)...)
	restarted(tl)
	rv := writeRun(t, dir, "v720", 0, cat(chain(0, 4, 2, 1.4), []seg{{4, 30, 1.4}})...)
	tl.Observe()
	caps := []byte("#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:4.000,\n.\n" +
		"#EXTINF:4.000,\nv7201.vtt\n#EXT-X-DISCONTINUITY\n#EXTINF:4.000,\nv7202.vtt\n")
	_, _, cs := served(t, tl, "v720", caps)
	_, _, vs := served(t, tl, "v720", rv)
	if len(cs) != 3 || cs[0].uri != "." || cs[2].uri != "v7202.vtt" {
		t.Fatalf("caption segments %+v", cs)
	}
	for i := range cs {
		if !cs[i].pdt.Equal(vs[i].pdt) || cs[i].disc != vs[i].disc {
			t.Fatalf("caption %d %+v, video %+v", i, cs[i], vs[i])
		}
	}
}

func TestTimelineDropsFFmpegTimelineTags(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v720")
	raw := []byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-DISCONTINUITY\n" +
		"#EXT-X-PROGRAM-DATE-TIME:2026-10-11T19:00:00.000+0000\n#EXTINF:4.004000,\nv720_00000.ts\n" +
		"#EXT-X-DISCONTINUITY\n#EXT-X-PROGRAM-DATE-TIME:2026-10-11T18:00:00.000+0000\n#EXTINF:4.004000,\nv720_00001.ts\n")
	if err := os.WriteFile(filepath.Join(dir, "v720.m3u8"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	for i, pts := range []float64{1.4, 5.404} { // a run opens only once its first PTS is read
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("v720_%05d.ts", i)), tsWithPTS(pts), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tl.Observe()
	out := string(tl.Retime("v720", raw))
	if strings.Contains(out, "#EXT-X-DISCONTINUITY\n") || strings.Contains(out, "+0000") {
		t.Fatalf("FFmpeg's timeline tags survived:\n%s", out)
	}
	if !strings.HasPrefix(out, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-DISCONTINUITY-SEQUENCE:0\n") {
		t.Fatalf("header:\n%s", out)
	}
	if strings.Count(out, "#EXT-X-PROGRAM-DATE-TIME:") != 2 || !strings.Contains(out, "#EXTINF:4.004000,\nv720_00001.ts\n") {
		t.Fatalf("segments:\n%s", out)
	}
}

// Long sessions keep bounded state: stamps and skips for the window only,
// one current run, anchors compacted.
func TestTimelineStateStaysBounded(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480", "aac0")
	end, pts := 0.0, 1.4
	var window []seg
	for i := 0; i < 60; i++ {
		if i%6 == 5 {
			end += 5 // an outage FFmpeg closed
		}
		if i%15 == 14 {
			restarted(tl) // and now and then a restart
			pts = 1.4
		}
		end += 4
		window = append(window, seg{4, end, pts})
		pts += 4
		if len(window) > 5 {
			window = window[1:]
		}
		writeRun(t, dir, "v480", int64(i+1-len(window)), window...)
		writeRun(t, dir, "aac0", int64(i+1-len(window)), window...)
		tl.Observe()
	}
	for _, n := range []string{"v480", "aac0"} {
		r := tl.rends[n]
		if len(r.segs) > 5 || len(r.skip) > 5 {
			t.Fatalf("%s keeps %d stamps and %d skips for a 5-segment window", n, len(r.segs), len(r.skip))
		}
	}
	if len(tl.cur.anchors) > 1 {
		t.Fatalf("%d anchors kept", len(tl.cur.anchors))
	}
	// Audio runs far ahead of a reference that stopped: its waiting
	// segments are bounded too.
	writeRun(t, dir, "aac0", 60, chain(end, 4, 2*timelineMaxPending, pts)...)
	tl.Observe()
	if n := len(tl.rends["aac0"].pending); n > timelineMaxPending {
		t.Fatalf("%d audio segments waiting", n)
	}
}

func TestTimelineUnwrapsPTS(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480")
	raw := writeRun(t, dir, "v480", 0, seg{4, 4, ptsPeriod - 6}, seg{4, 8, ptsPeriod - 2}, seg{4, 12, 2})
	tl.Observe()
	_, _, segs := served(t, tl, "v480", raw)
	if !near(segs[2].pdt, sec(8)) {
		t.Fatalf("segment after the PTS wrap at %v, want %v", segs[2].pdt, sec(8))
	}
}

func TestSegmentFirstPTSSkipsTables(t *testing.T) {
	pat := make([]byte, 188)
	for i := range pat {
		pat[i] = 0xFF
	}
	copy(pat, []byte{0x47, 0x40, 0x00, 0x10, 0x00, 0x00, 0xB0, 0x0D})
	path := filepath.Join(t.TempDir(), "s.ts")
	if err := os.WriteFile(path, append(pat, tsWithPTS(12.5)...), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, ok := segmentFirstPTS(path); !ok || v != 12*90000+45000 {
		t.Fatalf("segmentFirstPTS = %d, %v", v, ok)
	}
}

func TestTimelineWindow(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480")
	writeRun(t, dir, "v480", 5, seg{4, 4, 1.4}, seg{4, 8, 5.4})
	tl.Observe()
	w := tl.Window("v480")
	if w.MediaSequence != 0 || len(w.Segments) != 2 || !near(w.WindowStart(), sec(0)) || !near(w.LiveEdge(), sec(8)) {
		t.Fatalf("window %+v start %v edge %v", w, w.WindowStart(), w.LiveEdge())
	}
	if (WindowTimeline{}).LiveEdge() != (time.Time{}) || len(tl.Window("nope").Segments) != 0 {
		t.Fatal("empty window has segments or a live edge")
	}
}

func TestParseMediaPlaylist(t *testing.T) {
	p := ParseMediaPlaylist([]byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-MEDIA-SEQUENCE:9\n#EXT-X-DISCONTINUITY\n" +
		"#EXTINF:4.004000,\nv_00009.ts\n#EXT-X-ENDLIST\n"))
	if p.MediaSequence != 9 || len(p.Header) != 2 || len(p.Segments) != 1 || len(p.Trailer) != 1 {
		t.Fatalf("%+v", p)
	}
	if s := p.Segments[0]; s.Duration != 4.004 || s.URI != "v_00009.ts" || len(s.Tags) != 1 {
		t.Fatalf("segment %+v", s)
	}
}

// Codex's storm: a restarted run's video covers media [0,4) and its audio
// starts at 8. Seen is not publishable: the run stays held instead of
// opening with video alone, and the crash that follows discards it. The
// next run opens as discontinuity 1 in both renditions, and audio is still
// required afterwards.
func TestTimelineGateWaitsForAPublishableSegmentInEveryRendition(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480", "aac0")
	base := chain(0, 4, 3, 1.4)
	writeRun(t, dir, "v480", 0, base...)
	writeRun(t, dir, "aac0", 0, base...)
	tl.Observe()
	restarted(tl)
	rv := writeRun(t, dir, "v480", 0, cat(base, []seg{{4, 30, 1.4}})...) // run 1: video [0,4)
	ra := writeRun(t, dir, "aac0", 0, cat(base, []seg{{4, 38, 9.4}})...) // audio from media 8
	tl.Observe()
	if _, _, vs := served(t, tl, "v480", rv); len(vs) != 3 {
		t.Fatalf("run 1 opened with video alone: %d video segments served", len(vs))
	}
	if missing := tl.blockingLocked(tl.cur); strings.Join(missing, ",") != "aac0" {
		t.Fatalf("blocking %v, want [aac0]", missing)
	}
	restarted(tl) // the crash: run 1 is discarded
	rv = writeRun(t, dir, "v480", 0, cat(base, []seg{{4, 30, 1.4}, {4, 60, 1.4}, {4, 64, 5.4}, {4, 68, 9.4}})...)
	ra = writeRun(t, dir, "aac0", 0, cat(base, []seg{{4, 38, 9.4}, {4, 60, 1.42}, {4, 64, 5.42}})...)
	tl.Observe()
	_, dv, vs := served(t, tl, "v480", rv)
	_, da, as := served(t, tl, "aac0", ra)
	if len(vs) != 6 || len(as) != 5 || !vs[3].disc || !as[3].disc || vs[3].uri != "v480_00004.ts" || as[3].uri != "aac0_00004.ts" {
		t.Fatalf("run 2:\nvideo %+v\naudio %+v", vs, as)
	}
	for i := 3; i < 5; i++ {
		if dnum(dv, vs, i) != 1 || dnum(da, as, i) != 1 {
			t.Fatalf("segment %d: video discontinuity %d, audio %d; want 1 and 1", i, dnum(dv, vs, i), dnum(da, as, i))
		}
	}
	// The required cohort survived the discarded run: a run without audio
	// is held, then expired.
	restarted(tl)
	writeRun(t, dir, "v480", 0, cat(base, []seg{{4, 30, 1.4}, {4, 60, 1.4}, {4, 64, 5.4}, {4, 68, 9.4}, {4, 90, 1.4}})...)
	tl.Observe()
	if missing, _, expired := tl.ExpireStalledRun(timelineGateTimeout, true); !expired || strings.Join(missing, ",") != "aac0" {
		t.Fatalf("ExpireStalledRun = %v, %v; want [aac0], true", missing, expired)
	}
}

// Gap anchors at media 8 and 16 are both kept while the audio playlist is
// missing; when it shows up, every audio segment gets the shift the video
// segment at the same media point got.
func TestTimelineDelayedRenditionGetsEveryGap(t *testing.T) {
	dir := t.TempDir()
	tl, logs, _ := newTestTimeline(dir, "v480", "aac0")
	video := []seg{{4, 4, 1.4}, {4, 8, 5.4}, {4, 17, 9.4}, {4, 21, 13.4}, {4, 30, 17.4}, {4, 34, 21.4}, {4, 38, 25.4}}
	writeRun(t, dir, "v480", 0, video[:3]...)
	tl.Observe()
	rv := writeRun(t, dir, "v480", 0, video...)
	tl.Observe()
	tl.Observe()
	if n := strings.Count(strings.Join(*logs, "\n"), " gap before "); n != 2 {
		t.Fatalf("%d gaps logged, want 2: %v", n, *logs)
	}
	ra := writeRun(t, dir, "aac0", 0, chain(40, 4, 6, 1.42)...) // late playlist, late files
	tl.Observe()
	_, _, vs := served(t, tl, "v480", rv)
	_, _, as := served(t, tl, "aac0", ra)
	if len(vs) != 7 || len(as) != 6 {
		t.Fatalf("served %d video, %d audio", len(vs), len(as))
	}
	for i := range as {
		if !near(as[i].pdt, vs[i].pdt.Add(20*time.Millisecond)) {
			t.Fatalf("audio %d at %v, video %d at %v: want 20 ms apart", i, as[i].pdt, i, vs[i].pdt)
		}
	}
}

// A first segment whose PTS can't be read yet (file not complete) keeps the
// run held; once it reads, the segment lands where its PTS says (6 s after
// the audio's), not where EXTINF chaining would have put it.
func TestTimelineWaitsForFirstSegmentPTS(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480", "aac0", "ac30")
	writeRun(t, dir, "v480", 0, seg{4, 4, -1}, seg{4, 8, 11.4}) // the first file has no PTS yet
	ra := writeRun(t, dir, "aac0", 0, chain(0, 4, 4, 1.4)...)
	for i := 0; i < timelinePTSReads+2; i++ {
		tl.Observe()
	}
	if _, _, as := served(t, tl, "aac0", ra); len(as) != 0 {
		t.Fatalf("run opened before the reference's first PTS was read: %d audio segments served", len(as))
	}
	if missing, _, expired := tl.ExpireStalledRun(time.Hour, false); expired {
		t.Fatalf("expired with no input fed (%v)", missing)
	}
	rv := writeRun(t, dir, "v480", 0, seg{4, 10, 7.4}, seg{4, 14, 11.4}) // now complete
	tl.Observe()
	_, _, vs := served(t, tl, "v480", rv)
	_, _, as := served(t, tl, "aac0", ra)
	if len(vs) != 2 || len(as) != 3 { // audio [12,16) waits for video past 16
		t.Fatalf("served %d video, %d audio", len(vs), len(as))
	}
	if !near(vs[0].pdt, as[0].pdt.Add(6*time.Second)) {
		t.Fatalf("video starts at %v, audio at %v: want 6 s apart (their PTS)", vs[0].pdt, as[0].pdt)
	}
	// A rendition that joins the open run late (not required yet) waits for
	// its first PTS too, rather than chaining from media 0.
	writeRun(t, dir, "ac30", 0, seg{4, 12, -1}, seg{4, 16, 13.4})
	for i := 0; i < timelinePTSReads+2; i++ {
		tl.Observe()
	}
	if n := len(tl.Window("ac30").Segments); n != 0 {
		t.Fatalf("%d AC-3 segments stamped before the first PTS was read", n)
	}
	writeRun(t, dir, "ac30", 0, seg{4, 12, 9.4}, seg{4, 16, 13.4})
	tl.Observe()
	// [8,12) is covered by the video's [6,14); [12,16) waits for more video.
	if w := tl.Window("ac30"); len(w.Segments) != 1 || !near(w.Segments[0].Start, as[0].pdt.Add(8*time.Second)) {
		t.Fatalf("AC-3 window %+v, want 1 segment from %v", w, as[0].pdt.Add(8*time.Second))
	}
}

// The caption playlist lags the reference across a window slide: the
// reference's stamps stay while the caption playlist still lists them,
// and its MEDIA-SEQUENCE never goes back, even for a staler read.
func TestTimelineCaptionSequenceNeverGoesBack(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v720")
	caps := func(mseq int64, n int) []byte {
		var b strings.Builder
		fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:%d\n", mseq)
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "#EXTINF:4.000,\nv720%d.vtt\n", mseq+int64(i))
		}
		if err := os.WriteFile(filepath.Join(dir, "v720_vtt.m3u8"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return []byte(b.String())
	}
	writeRun(t, dir, "v720", 0, chain(0, 4, 3, 1.4)...)
	c0 := caps(0, 3)
	tl.Observe()
	// The video window slides to 4..6; the caption playlist is still at 0.
	writeRun(t, dir, "v720", 4, seg{4, 20, 17.4}, seg{4, 24, 21.4}, seg{4, 28, 25.4})
	tl.Observe() // (3 slid out unobserved: no stamp, never served)
	mseq, _, cs := served(t, tl, "v720_vtt", c0)
	if mseq != 0 || len(cs) != 3 || !near(cs[0].pdt, sec(0)) {
		t.Fatalf("lagging captions: mseq %d %+v", mseq, cs)
	}
	c2 := caps(2, 5)
	tl.Observe()
	if mseq, _, cs = served(t, tl, "v720_vtt", c2); mseq != 2 || len(cs) != 1 {
		t.Fatalf("captions from 2: mseq %d %+v (2 is stamped; 3 never was)", mseq, cs)
	}
	if mseq, _, _ = served(t, tl, "v720_vtt", c0); mseq < 2 {
		t.Fatalf("a staler caption read served MEDIA-SEQUENCE %d after 2", mseq)
	}
	// A read listing only the never-stamped 3 is empty, numbered for what
	// follows it (4 is Bowtie's 3), not for the end of the window.
	if mseq, _, cs = served(t, tl, "v720_vtt", caps(3, 1)); mseq != 3 || len(cs) != 0 {
		t.Fatalf("captions listing only 3: mseq %d %+v", mseq, cs)
	}
	if mseq, _, cs = served(t, tl, "v720_vtt", c2); mseq != 3 || len(cs) != 0 {
		t.Fatalf("captions went back after an empty read: mseq %d %+v", mseq, cs)
	}
	if mseq, _, cs = served(t, tl, "v720_vtt", caps(4, 3)); mseq != 3 || len(cs) != 3 {
		t.Fatalf("captions from 4: mseq %d %+v", mseq, cs)
	}
	// 2's stamp is still kept (the observer last saw the captions at 2),
	// but this playlist was already served from 3.
	if mseq, _, cs = served(t, tl, "v720_vtt", c2); mseq != 3 || len(cs) != 0 {
		t.Fatalf("captions went back: mseq %d %+v", mseq, cs)
	}
	if mseq, _, vs := served(t, tl, "v720", writeRun(t, dir, "v720", 4, seg{4, 20, 17.4}, seg{4, 24, 21.4}, seg{4, 28, 25.4})); mseq != 3 || len(vs) != 3 {
		t.Fatalf("video after the slide: mseq %d, %d segments", mseq, len(vs))
	}
	caps(6, 1)
	tl.Observe()
	if len(tl.rends["v720"].segs) != 3 {
		t.Fatalf("%d stamps kept once the captions caught up, want the 3 in the window", len(tl.rends["v720"].segs))
	}
}

// Liveness: a run is charged fed time from its process start; quiet
// ticks (no fresh bytes) pause the clock and never erase it; an expired run
// never opens; an open run in which a required rendition stops publishing
// expires too.
func TestTimelineStalledRunsExpireAtomically(t *testing.T) {
	dir := t.TempDir()
	tl, logs, _ := newTestTimeline(dir, "v480", "aac0")
	for i := 0; i < 100; i++ { // an outage: never charged
		if _, _, expired := tl.ExpireStalledRun(5*time.Second, false); expired {
			t.Fatal("expired while not fed")
		}
	}
	for _, fresh := range []bool{true, false, true, false} { // 10 s fed, interleaved with quiet ticks
		if _, _, expired := tl.ExpireStalledRun(5*time.Second, fresh); expired {
			t.Fatal("expired after 10 s fed")
		}
	}
	if missing, stalls, expired := tl.ExpireStalledRun(5*time.Second, true); !expired || stalls != 1 || strings.Join(missing, ",") != "v480" {
		t.Fatalf("a run with no segment after 15 s fed: %v, %d, %v", missing, stalls, expired)
	}
	writeRun(t, dir, "v480", 0, chain(0, 4, 2, 1.4)...)
	tl.Observe()
	if tl.cur.state != runHeld {
		t.Fatal("an expired run opened")
	}
	if stalls := restarted(tl); stalls != 1 {
		t.Fatalf("MarkRestart after an expiry: %d stalls, want 1 (counted once)", stalls)
	}
	if !strings.Contains(strings.Join(*logs, "\n"), "discarded") {
		t.Fatalf("expired run not discarded: %v", *logs)
	}
	rv := writeRun(t, dir, "v480", 0, cat(chain(0, 4, 2, 1.4), chain(40, 4, 2, 1.4))...)
	writeRun(t, dir, "aac0", 0, chain(40, 4, 2, 1.4)...)
	tl.Observe()
	if _, _, vs := served(t, tl, "v480", rv); len(vs) != 2 || tl.stalls != 1 {
		t.Fatalf("run 1: %d segments served, %d stalls (opening must not reset them)", len(vs), tl.stalls)
	}
	// The reference stops; audio goes on (and waits for it).
	writeRun(t, dir, "aac0", 0, chain(40, 4, 6, 1.4)...)
	tl.Observe()
	tl.ExpireStalledRun(5*time.Second, true) // publication at open counts for this tick
	for i := 0; i < 2; i++ {
		if _, _, expired := tl.ExpireStalledRun(5*time.Second, true); expired {
			t.Fatalf("expired after %d s of fed time without publishing", 5*(i+1))
		}
	}
	missing, stalls, expired := tl.ExpireStalledRun(5*time.Second, true)
	if !expired || stalls != 2 || !strings.HasPrefix(strings.Join(missing, ","), "v480") {
		t.Fatalf("an open run whose reference stopped: %v, %d, %v", missing, stalls, expired)
	}
	if _, _, expired := tl.ExpireStalledRun(time.Hour, true); expired {
		t.Fatal("the same run expired twice")
	}
	// An expired open run never publishes again: the reference resumes
	// before the kill lands, and neither the observer nor MarkRestart's
	// final observation stamps or serves any of it.
	ra := writeRun(t, dir, "aac0", 0, chain(40, 4, 6, 1.4)...)
	_, _, before := served(t, tl, "aac0", ra)
	vWin, aWin := len(tl.Window("v480").Segments), len(tl.Window("aac0").Segments)
	rv = writeRun(t, dir, "v480", 0, cat(chain(0, 4, 2, 1.4), chain(40, 4, 6, 1.4))...)
	check := func(when string) {
		t.Helper()
		_, _, vs := served(t, tl, "v480", rv)
		_, _, as := served(t, tl, "aac0", ra)
		if len(vs) != 2 || len(as) != len(before) ||
			len(tl.Window("v480").Segments) != vWin || len(tl.Window("aac0").Segments) != aWin {
			t.Fatalf("%s: an expired run published: video %d (want 2), audio %d (want %d)", when, len(vs), len(as), len(before))
		}
	}
	tl.Observe()
	check("after Observe")
	if stalls := tl.MarkRestart(); stalls != 2 {
		t.Fatalf("MarkRestart after an open-run expiry: %d stalls, want 2 (counted once)", stalls)
	}
	check("after MarkRestart")
	// Attach attempts that never start FFmpeg (an outage) end nothing and
	// count nothing: MarkRestart is idempotent until ProcessStarted.
	cur, runs := tl.cur, tl.runs
	for i := 0; i < 5; i++ {
		if stalls := tl.MarkRestart(); stalls != 2 || tl.cur != cur || tl.runs != runs {
			t.Fatalf("attempt %d without a process: %d stalls, new run %v", i, stalls, tl.cur != cur)
		}
	}
	tl.ProcessStarted()
	if stalls := tl.MarkRestart(); stalls != 3 {
		t.Fatalf("a started process that crashed: %d stalls, want 3", stalls)
	}
}

// grower appends segments to renditions over time (a window of 5), with
// PTS and mtimes advancing together.
type grower struct {
	t     *testing.T
	dir   string
	segs  map[string][]seg
	first map[string]int64 // FFmpeg sequence of segs[name][0]
}

func newGrower(t *testing.T, dir string) *grower {
	return &grower{t: t, dir: dir, segs: map[string][]seg{}, first: map[string]int64{}}
}

// add appends one segment to name and rewrites its playlist.
func (g *grower) add(name string, s seg) []byte {
	g.segs[name] = append(g.segs[name], s)
	if len(g.segs[name]) > 5 {
		g.segs[name] = g.segs[name][1:]
		g.first[name]++
	}
	return writeRun(g.t, g.dir, name, g.first[name], g.segs[name]...)
}

// Ruling: opening doesn't reset the stall budget; three
// open-then-reference-stopped runs use it up; only 60 s of every required
// rendition publishing (fed) resets it.
func TestTimelineStallBudgetOnlyResetsAfterHealthyOutput(t *testing.T) {
	dir := t.TempDir()
	tl, _, _ := newTestTimeline(dir, "v480", "aac0")
	g := newGrower(t, dir)
	end := 0.0
	for cycle := 1; cycle <= timelineMaxStalls; cycle++ {
		if cycle > 1 {
			if stalls := restarted(tl); stalls != cycle-1 {
				t.Fatalf("cycle %d: MarkRestart after an expiry = %d", cycle, stalls)
			}
		}
		for i, pts := range []float64{1.4, 5.4} { // a new run that opens
			end += 4
			g.add("v480", seg{4, end, pts})
			g.add("aac0", seg{4, end, pts})
			_ = i
		}
		tl.Observe()
		if tl.cur.state != runOpen {
			t.Fatalf("cycle %d: run didn't open", cycle)
		}
		var stalls int
		expired := false
		for i := 0; i < 5 && !expired; i++ { // the reference stops; FFmpeg is still fed
			_, stalls, expired = tl.ExpireStalledRun(5*time.Second, true)
		}
		if !expired || stalls != cycle {
			t.Fatalf("cycle %d: expired %v, stalls %d", cycle, expired, stalls)
		}
	}
	// A healthy run at broadcast cadence: video skips one tick in three
	// (segments a little over 5 s) and audio trails it by a tick, so each
	// rendition sometimes has a tick without a new segment. That is still
	// continuous; two silent ticks in a row are not, and restart the 60 s.
	restarted(tl)
	pts, vEnd := 1.4, end
	var lastVideo *seg
	tick := func(video bool) {
		t.Helper()
		if lastVideo != nil { // audio: the segment video added a tick ago
			g.add("aac0", *lastVideo)
			lastVideo = nil
		}
		if video {
			vEnd += 4
			s := seg{4, vEnd, pts}
			g.add("v480", s)
			lastVideo = &s
			pts += 4
		}
		tl.Observe()
		if _, _, expired := tl.ExpireStalledRun(5*time.Second, true); expired {
			t.Fatal("a healthy run expired")
		}
	}
	for i := 0; i < 10; i++ { // opens at tick 1; 45 s healthy
		tick(i%3 != 2)
	}
	if tl.cur.state != runOpen || tl.stalls != timelineMaxStalls {
		t.Fatalf("state %v, budget %d before the gap", tl.cur.state, tl.stalls)
	}
	tick(false) // 50 s
	tick(false) // video silent for two ticks: not continuous
	for i := 0; i < 16; i++ {
		tick(i%3 != 2)
		if i < 11 && tl.stalls != timelineMaxStalls {
			t.Fatalf("budget reset %d s after a two-tick silence", 5*(i+1))
		}
	}
	if tl.stalls != 0 {
		t.Fatalf("budget %d after 75 s of output at broadcast cadence, want 0", tl.stalls)
	}
}

// A later reference segment whose PTS can't be read yet isn't published:
// nothing is stamped (or anchored) from a guess. When it reads, 6 s past
// where EXTINF would put it (a hole FFmpeg kept), both renditions land on
// its PTS.
func TestTimelineWaitsForEveryReferencePTS(t *testing.T) {
	dir := t.TempDir()
	tl, logs, _ := newTestTimeline(dir, "v480", "aac0")
	writeRun(t, dir, "v480", 0, seg{4, 4, 1.4})
	writeRun(t, dir, "aac0", 0, seg{4, 4, 1.4})
	tl.Observe()
	rv := writeRun(t, dir, "v480", 0, seg{4, 4, 1.4}, seg{4, 14, -1}) // not readable yet
	ra := writeRun(t, dir, "aac0", 0, seg{4, 4, 1.4}, seg{4, 14, 11.4})
	for i := 0; i < timelinePTSReads-1; i++ {
		tl.Observe()
	}
	_, _, vs := served(t, tl, "v480", rv)
	_, _, as := served(t, tl, "aac0", ra)
	if len(vs) != 1 || len(as) != 1 {
		t.Fatalf("served %d video, %d audio while the video PTS is unread; want 1 and 1", len(vs), len(as))
	}
	writeRun(t, dir, "v480", 0, seg{4, 4, 1.4}, seg{4, 14, 11.4})
	tl.Observe()
	_, _, vs = served(t, tl, "v480", rv)
	_, _, as = served(t, tl, "aac0", ra)
	if len(vs) != 2 || len(as) != 2 || !near(vs[1].pdt, sec(10)) || !near(as[1].pdt, sec(10)) {
		t.Fatalf("video %+v\naudio %+v\nwant both second segments at %v", vs, as, sec(10))
	}
	if strings.Contains(strings.Join(*logs, "\n"), "gap") {
		t.Fatalf("a gap was anchored: %v", *logs)
	}
}

// Audio cut 20 ms before or after the video around a closed 5 s gap gets
// the video's shift (the reference interval it overlaps most), whichever
// playlist is published first: the same moment carries the same PDT
// within one segment, and the same discontinuity number.
func TestTimelineNearAlignedCutsShareTheGapShift(t *testing.T) {
	for _, off := range []float64{-0.02, 0.02} {
		for _, audioFirst := range []bool{false, true} {
			name := fmt.Sprintf("audio %+.0f ms, audio first %v", off*1000, audioFirst)
			dir := t.TempDir()
			tl, _, _ := newTestTimeline(dir, "v480", "aac0")
			ends := []float64{4, 8, 17, 21, 25} // the gap is before the third segment
			var video, audio []seg
			for i, e := range ends {
				video = append(video, seg{4, e, 1.4 + 4*float64(i)})
				audio = append(audio, seg{4, e + off, 1.4 + off + 4*float64(i)})
			}
			writeRun(t, dir, "v480", 0, video[:2]...)
			if audioFirst {
				writeRun(t, dir, "aac0", 0, audio...)
			}
			tl.Observe()
			rv := writeRun(t, dir, "v480", 0, video...)
			ra := writeRun(t, dir, "aac0", 0, audio...)
			tl.Observe()
			_, dv, vs := served(t, tl, "v480", rv)
			_, da, as := served(t, tl, "aac0", ra)
			if len(vs) != 5 || len(as) < 4 {
				t.Fatalf("%s: served %d video, %d audio", name, len(vs), len(as))
			}
			for i := 0; i < 4; i++ {
				if d := as[i].pdt.Sub(vs[i].pdt); d < -30*time.Millisecond || d > 30*time.Millisecond {
					t.Fatalf("%s: segment %d: audio %v, video %v (%v apart)", name, i, as[i].pdt, vs[i].pdt, d)
				}
				if dnum(dv, vs, i) != dnum(da, as, i) {
					t.Fatalf("%s: segment %d: discontinuity %d vs %d", name, i, dnum(dv, vs, i), dnum(da, as, i))
				}
			}
		}
	}
}

// More than timelineMaxAnchors gaps while a required rendition lags: the
// anchors it needs are never dropped; the run stops publishing and
// expires (counted), and nothing is stamped from partial history. A
// rendition that has never published only loses its segments from before
// the kept history.
func TestTimelineAnchorCapNeverStampsFromIncompleteHistory(t *testing.T) {
	dir := t.TempDir()
	tl, logs, _ := newTestTimeline(dir, "v480", "aac0")
	g := newGrower(t, dir)
	g.add("v480", seg{4, 4, 1.4})
	g.add("aac0", seg{4, 4, 1.4})
	tl.Observe()
	end, pts := 4.0, 1.4
	for i := 0; i < timelineMaxAnchors+3; i++ { // a 3 s closed gap before every segment
		end, pts = end+7, pts+4
		g.add("v480", seg{4, end, pts})
		tl.Observe()
	}
	if !strings.Contains(strings.Join(*logs, "\n"), "stopping the run") {
		t.Fatalf("anchor cap not hit: %d anchors", len(tl.cur.anchors))
	}
	if a := tl.cur.anchors; len(a) == 0 || a[0].media > 4+1e-9 {
		t.Fatalf("an anchor aac0 needs was dropped: first kept at media %v", a[0].media)
	}
	missing, stalls, expired := tl.ExpireStalledRun(0, false) // no fed time needed
	if !expired || stalls != 1 || strings.Join(missing, ",") != "aac0" {
		t.Fatalf("ExpireStalledRun = %v, %d, %v; want [aac0], 1, true", missing, stalls, expired)
	}
	g.add("aac0", seg{4, 11, 5.4}) // the lagging rendition catches up: still nothing stamped
	tl.Observe()
	if n := len(tl.Window("aac0").Segments); n != 1 {
		t.Fatalf("%d audio segments stamped after the run stopped, want 1", n)
	}
	if stalls := restarted(tl); stalls != 1 {
		t.Fatalf("MarkRestart = %d stalls, want 1 (the expiry already counted)", stalls)
	}

	// ac30 has never published: history only it needs is dropped, and its
	// segments from before the kept history are never served.
	dir2 := t.TempDir()
	tl2, logs2, _ := newTestTimeline(dir2, "v480", "ac30")
	g2 := newGrower(t, dir2)
	end, pts = 4.0, 1.4
	g2.add("v480", seg{4, end, pts})
	tl2.Observe()
	for i := 0; i < timelineMaxAnchors+3; i++ {
		end, pts = end+7, pts+4
		g2.add("v480", seg{4, end, pts})
		tl2.Observe()
	}
	if !strings.Contains(strings.Join(*logs2, "\n"), "never served") || len(tl2.cur.anchors) > timelineMaxAnchors {
		t.Fatalf("%d anchors kept; logs %v", len(tl2.cur.anchors), (*logs2)[len(*logs2)-1])
	}
	vw := tl2.Window("v480")
	last := vw.Segments[len(vw.Segments)-2]
	writeRun(t, dir2, "ac30", 0, seg{4, 4, 1.4}, seg{4, end - 4, pts - 4}) // media 0, then the video's last-but-one
	tl2.Observe()
	aw := tl2.Window("ac30")
	if len(aw.Segments) != 1 || !near(aw.Segments[0].Start, last.Start) {
		t.Fatalf("ac30 window %+v, want one segment at %v", aw, last.Start)
	}
}

// restarted is an append restart whose FFmpeg started.
func restarted(tl *Timeline) int {
	stalls := tl.MarkRestart()
	tl.ProcessStarted()
	return stalls
}
```

The tests that pin the invariants:
- `TestTimelinePartialRunIsNeverServed`: the flipped audio-only middle run is never served, and the next run opens as discontinuity 1 in both renditions, with Bowtie's sequence numbers.
- `TestTimelineRenditionMissingTwoRunsReturnsAligned`: a rendition misses two held runs and returns at its siblings' number while old segments are still served.
- `TestTimelineStaggeredPublicationKeepsAlignment` and `TestTimelineEncoderDelayIsNotAGap`: staggered publication and late first segments, kept separate from the reception gaps in `TestTimelineJumpsForwardOverAClosedGap`.
- `TestTimelinePartialRunIsNeverServed` (after the slide): the window head keeps its tag.
- `TestTimelineStateStaysBounded`: compaction, and bounded waiting segments.
- `TestTimelineGateWaitsForAPublishableSegmentInEveryRendition`: Codex's storm (video [0,4), audio from 8, then a crash). The run stays held, the crash discards it, the next run is discontinuity 1 everywhere, and audio stays required.
- `TestTimelineDelayedRenditionGetsEveryGap`: two gaps while the audio playlist is absent; every late audio segment gets its video segment's shift.
- `TestTimelineWaitsForFirstSegmentPTS`: an unreadable first segment keeps the run held, and is placed by its real PTS (6 s later) once read. A late-joining rendition waits for its first PTS too. Real segment files, no EXTINF guess.
- `TestTimelineCaptionSequenceNeverGoesBack`: a caption playlist lagging across a video window slide keeps its stamps; no read, staler or empty, makes its `MEDIA-SEQUENCE` go back.
- `TestTimelineStalledRunsExpireAtomically`: fed time only (a run with no segment at all expires after 15 s of it; an outage of any length doesn't, and quiet ticks pause the clock without erasing it), an expired run never opens, a crash counts in `MarkRestart`, and an open run whose reference stops expires and counts. After that expiry the reference resumes: neither `Observe` nor `MarkRestart` stamps or serves anything more. Repeated `MarkRestart` without `ProcessStarted` counts nothing and makes no run; after `ProcessStarted` the next one counts. The tests' `restarted(tl)` helper is `MarkRestart` plus `ProcessStarted`, a restart whose FFmpeg started.
- `TestTimelineStallBudgetOnlyResetsAfterHealthyOutput`: opening a run doesn't reset the budget. 60 s of output at broadcast cadence does: video skips one tick in three, and audio trails it by a tick. A rendition silent for two ticks in a row restarts that 60 s.
- `TestTimelineWaitsForEveryReferencePTS`: a later reference segment whose PTS can't be read for several observations, then reads 6 s ahead, holds both renditions; both land on that PTS, and the gap anchor sits at the real position.
- `TestTimelineNearAlignedCutsShareTheGapShift`: audio cut 20 ms before and 20 ms after a gap anchor, in both publication orders. The two renditions' PDTs agree within one segment, and the same moment carries the same discontinuity number.
- `TestTimelineAnchorCapNeverStampsFromIncompleteHistory`: more than 64 gaps while a required rendition lags. The run expires (counted), and nothing is stamped. A lagging non-required rendition instead has its too-old segments skipped, never stamped wrong.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd server && go test ./internal/stream/ -run 'TestTimeline|TestParseMediaPlaylist|TestSegmentFirstPTS' -v`
Expected: FAIL to compile (`undefined: NewTimeline`).

- [ ] **Step 3: Implement**

Create `server/internal/stream/hlsplaylist.go`:

```go
package stream

import (
	"strconv"
	"strings"
)

// MediaPlaylist is an HLS media playlist as FFmpeg writes it, minus the
// tags Bowtie owns (see bowtieOwnedTag).
type MediaPlaylist struct {
	Header        []string // tags before the first segment (EXTM3U, VERSION, TARGETDURATION, …)
	MediaSequence int64
	Segments      []PlaylistSegment
	Trailer       []string // tags after the last segment (#EXT-X-ENDLIST)
}

// PlaylistSegment is one segment: its tags in order (EXTINF included) and URI.
type PlaylistSegment struct {
	Duration float64
	Tags     []string
	URI      string
}

// bowtieOwnedTag reports the timeline tags Bowtie writes itself. FFmpeg's
// are dropped: its PDT jumps at restarts, and 5.1 writes a spurious
// DISCONTINUITY at the top of a continued playlist and no DISCONTINUITY-SEQUENCE.
func bowtieOwnedTag(line string) bool {
	return line == "#EXT-X-DISCONTINUITY" ||
		strings.HasPrefix(line, "#EXT-X-PROGRAM-DATE-TIME:") ||
		strings.HasPrefix(line, "#EXT-X-DISCONTINUITY-SEQUENCE:")
}

// ParseMediaPlaylist parses raw, dropping the tags Bowtie owns.
func ParseMediaPlaylist(raw []byte) MediaPlaylist {
	var p MediaPlaylist
	var tags []string
	dur := 0.0
	inHeader := true
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
		case strings.HasPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"):
			p.MediaSequence, _ = strconv.ParseInt(strings.TrimPrefix(line, "#EXT-X-MEDIA-SEQUENCE:"), 10, 64)
		case bowtieOwnedTag(line):
		case line == "#EXT-X-ENDLIST":
			p.Trailer = append(p.Trailer, line)
		case strings.HasPrefix(line, "#EXTINF:"):
			inHeader = false
			v := strings.TrimPrefix(line, "#EXTINF:")
			if i := strings.IndexByte(v, ','); i >= 0 {
				v = v[:i]
			}
			dur, _ = strconv.ParseFloat(v, 64)
			tags = append(tags, line)
		case strings.HasPrefix(line, "#"):
			if inHeader {
				p.Header = append(p.Header, line)
			} else {
				tags = append(tags, line)
			}
		default:
			p.Segments = append(p.Segments, PlaylistSegment{Duration: dur, Tags: tags, URI: line})
			tags, dur = nil, 0
		}
	}
	return p
}
```

Create `server/internal/stream/timeline.go`. `playlistTimeout` is `manager.go`'s existing constant.

```go
package stream

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// WallClockLayout is how Bowtie writes wall-clock times (playlists and the
// timeline API): RFC 3339 with milliseconds.
const WallClockLayout = "2006-01-02T15:04:05.000Z07:00"

const (
	// hlsSegmentDuration is FFmpeg's -hls_time for live sessions.
	hlsSegmentDuration = 4 * time.Second
	// timelineGapThreshold: a reference segment whose file says it started
	// this much later than the timeline predicts (beyond the encoder's own
	// steady lag) follows a real gap that FFmpeg closed; the timeline jumps
	// forward instead of absorbing it.
	timelineGapThreshold = 2 * time.Second
	// timelineObserveEvery is how often the observer reads the playlists.
	timelineObserveEvery = 500 * time.Millisecond
	// timelineGateTimeout bounds fed time without publishing: a held run
	// that hasn't opened after this much fed time since its process start,
	// or an open run in which a required rendition published nothing for
	// this much fed time. Fed time accrues only on the manager's ticks
	// where fresh source bytes reached FFmpeg (see ExpireStalledRun). It is
	// the bound a new session gets for its first playlist.
	timelineGateTimeout = playlistTimeout
	// timelineMaxStalls failed runs (expired, crashed, or stopped for the
	// anchor cap) end the session. Opening a run doesn't reset the count;
	// timelineHealthyAfter of every required rendition publishing
	// continuously does, which matches the crash backoff's healthy reset.
	// Continuously means no rendition goes timelineHealthyGap without
	// publishing: segments are 4-6 s and liveness ticks 5 s, so a tick
	// without a new segment is normal, two in a row are not.
	timelineMaxStalls    = 3
	timelineHealthyAfter = healthyResetAfter
	timelineHealthyGap   = 2 * hlsSegmentDuration
	// timelinePTSReads is how many observations may fail to read a
	// segment's first PTS before a segment that isn't its rendition's first
	// in the run chains on EXTINF instead. A first segment never does: its
	// rendition just isn't ready.
	timelinePTSReads = 6
	// timelineMaxPending, timelineMaxAnchors and timelineCaptionKeep bound
	// the per-run state: segments waiting per rendition, gap anchors, and
	// stamps kept below the reference's window for a lagging caption
	// playlist.
	timelineMaxPending  = 64
	timelineMaxAnchors  = 64
	timelineCaptionKeep = 64
	// ptsPeriod is the 33-bit 90 kHz PTS period in seconds.
	ptsPeriod = float64(int64(1)<<33) / 90000
)

// Timeline is a session's wall-clock record of its segments. An observer
// reads every rendition playlist on its own clock (never on client
// requests). Each FFmpeg process is a run. A run is held out of the served
// playlists until every required rendition (the reference, and every
// rendition ever published) has a publishable segment in it, with its
// first PTS read; then it opens and publishes as one discontinuity epoch
// for all renditions at once. A run that never opens is never served.
//
// Within a run, a segment's place on the broadcast clock is its first PTS
// relative to the run's earliest first PTS (all renditions share FFmpeg's
// output clock), so publication timing can't move it. The run's wall-clock
// start comes from the segment files' mtimes. Gaps FFmpeg closed are found
// on the reference rendition (the top rung) only, and a sibling segment is
// stamped only once the reference has passed it, so a gap shifts every
// rendition at the same media point.
type Timeline struct {
	dir        string
	renditions []string
	ref        string
	caption    string // the reference's caption playlist (Layout.CaptionPlaylist without .m3u8)
	now        func() time.Time
	logf       func(format string, args ...any)

	obsMu sync.Mutex // serializes Observe and MarkRestart (file reads included)

	mu       sync.Mutex
	cur      *tlRun
	runs     int             // runs started so far (run ids, for logs)
	required map[string]bool // every rendition ever published: a run opens only with all of them
	epoch    int64           // epoch of the last opened run (-1: none)
	stalls   int             // failed runs since the last healthy stretch (the stall budget)
	healthy  time.Duration   // how long every required rendition has kept publishing
	maxEnd   time.Time       // latest end of any published segment
	capMseq  int64           // FFmpeg's MEDIA-SEQUENCE of the caption playlist (-1: none seen)
	floors   map[string]int64
	rends    map[string]*tlRend
}

type tlRunState int

const (
	runHeld tlRunState = iota // segments collected, none served
	runOpen                   // published as one epoch
)

type tlRun struct {
	id          int
	state       tlRunState
	expired     bool          // ExpireStalledRun gave up on it: it never opens or publishes again (and is counted)
	started     bool          // a process is writing it (run 0, or after ProcessStarted)
	fed         time.Duration // fed time since the process started
	overflow    []string      // required renditions the anchor cap would cut off: stop and expire
	headShift   time.Duration // shift before anchors[0] (of the anchors compacted away)
	histFloor   float64       // non-required siblings' segments below this lost their gap history
	epoch       int64
	hasBase     bool
	base        int64     // earliest first PTS of any rendition in the run (90 kHz)
	start       time.Time // wall clock of media 0
	floor       time.Time // nothing in this run starts earlier (the previous end)
	refLag      time.Duration
	refMediaEnd float64 // media end of the reference's last stamped segment
	anchors     []tlAnchor
	seen        map[string]bool // renditions with a segment in this run
	pub         map[string]bool // renditions with a published segment in this run
}

type tlAnchor struct {
	media float64       // seconds into the run's media timeline
	shift time.Duration // total forward jump from here on
}

// shiftAt is the shift of the last anchor at or before media (the
// reference's own segments start on anchors). Anchors are added in media
// order with growing shifts.
func (r *tlRun) shiftAt(media float64) time.Duration {
	for i := len(r.anchors) - 1; i >= 0; i-- {
		if r.anchors[i].media <= media+1e-9 {
			return r.anchors[i].shift
		}
	}
	return r.headShift
}

// shiftFor is the shift of the anchor interval that [m, e) overlaps most:
// a sibling segment takes the shift of the reference segment(s) it shares
// most of its span with, so cuts a few milliseconds apart around a gap get
// the same shift.
func (r *tlRun) shiftFor(m, e float64) time.Duration {
	overlap := func(a, b float64) float64 { return math.Min(e, b) - math.Max(m, a) }
	best, bestOv := r.headShift, overlap(math.Inf(-1), r.anchors0())
	for i, a := range r.anchors {
		end := math.Inf(1)
		if i+1 < len(r.anchors) {
			end = r.anchors[i+1].media
		}
		if ov := overlap(a.media, end); ov > bestOv+1e-9 {
			best, bestOv = a.shift, ov
		}
	}
	return best
}

func (r *tlRun) anchors0() float64 {
	if len(r.anchors) == 0 {
		return math.Inf(1)
	}
	return r.anchors[0].media
}

type tlRend struct {
	ffMseq      int64           // FFmpeg's MEDIA-SEQUENCE last observed
	lastSeq     int64           // highest FFmpeg sequence seen (-1: none)
	pending     []*tlItem       // seen in the current run, not yet published
	segs        map[int64]tlSeg // FFmpeg sequence → published stamp (window, and captions' lag)
	skip        map[int64]bool  // FFmpeg sequences never served (window only)
	nextB       int64           // Bowtie's next media sequence number
	prevEnd     time.Time       // end of the last published segment
	everPub     bool            // published at least one segment
	runMediaEnd float64         // media end of the last segment assigned in the run
	asgRun      int             // run of the last assigned segment (-1: none)
	pubTick     bool            // stamped a segment since the last liveness tick
	fedNoPub    time.Duration   // fed time since it last published (open run)
	sincePub    time.Duration   // tick time since it last published (open run; healthy check)
}

type tlItem struct {
	seq      int64
	dur      float64
	path     string
	obs      time.Time // file mtime − EXTINF (or now − EXTINF)
	file     bool      // obs came from the segment file
	raw      int64     // first PTS (90 kHz)
	hasPTS   bool
	reads    int     // PTS read attempts
	media    float64 // seconds from the run's base
	hasMedia bool
	first    bool // the rendition's first segment in its run
}

// placeable: the item's PTS was read, or (not a first segment) the reads
// ran out and it may chain on EXTINF.
func (it *tlItem) placeable() bool {
	return it.hasPTS || (!it.first && it.reads >= timelinePTSReads)
}

// tlSeg is a published segment: what serving needs, kept while the
// segment is in FFmpeg's window (or the caption playlist's).
type tlSeg struct {
	bseq  int64 // Bowtie's media sequence number
	start time.Time
	dur   float64
	epoch int64
	tag   bool // first segment of a new epoch (#EXT-X-DISCONTINUITY)
}

func seconds(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }

// NewTimeline returns the timeline of the session in dir. renditions are
// the media playlists (without .m3u8) to observe, reference (top rung)
// first; the reference's caption playlist, <reference>_vtt.m3u8, is read
// for its MEDIA-SEQUENCE only. now must be the real clock: stamps are
// compared with segment file mtimes. Run 0's process start is now.
func NewTimeline(dir string, renditions []string, now func() time.Time) *Timeline {
	t := &Timeline{
		dir: dir, renditions: renditions, now: now, logf: log.Printf,
		epoch: -1, capMseq: -1, required: map[string]bool{}, floors: map[string]int64{}, rends: map[string]*tlRend{},
	}
	if len(renditions) > 0 {
		t.ref = renditions[0]
		t.caption = t.ref + "_vtt"
	}
	t.cur = t.newRun()
	t.cur.started = true // run 0 is the first process's
	return t
}

func (t *Timeline) newRun() *tlRun {
	r := &tlRun{id: t.runs, state: runHeld, histFloor: math.Inf(-1), seen: map[string]bool{}, pub: map[string]bool{}}
	t.runs++
	return r
}

// Run observes every timelineObserveEvery until ctx ends.
func (t *Timeline) Run(ctx context.Context) {
	tick := time.NewTicker(timelineObserveEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			t.Observe()
		}
	}
}

// Observe reads every rendition playlist, records new segments and
// publishes what can be published.
func (t *Timeline) Observe() {
	t.obsMu.Lock()
	defer t.obsMu.Unlock()
	t.observe()
}

// MarkRestart ends the current run (after observing everything the dead
// process wrote) and starts the next one, held until it is complete. Call
// it at the start of every append restart attempt, before anything else.
// A held run is discarded: its segments are never served. A run that
// ended without ExpireStalledRun expiring it (a crash supervise saw first,
// or a stuck kill) counts toward the stall budget too. It returns the
// budget used; at timelineMaxStalls the caller ends the session instead
// of starting FFmpeg.
//
// It is idempotent across attempts that never start FFmpeg (an attach
// that fails during an outage): until ProcessStarted, the current run has
// no process, so a further MarkRestart ends nothing, counts nothing and
// creates no other run.
func (t *Timeline) MarkRestart() (stalls int) {
	t.obsMu.Lock()
	defer t.obsMu.Unlock()
	t.mu.Lock()
	started, stalls := t.cur.started, t.stalls
	t.mu.Unlock()
	if !started {
		return stalls // no process since the last MarkRestart: nothing ended
	}
	t.observe()
	t.mu.Lock()
	defer t.mu.Unlock()
	run := t.cur
	dropped := 0
	for _, name := range t.renditions {
		r := t.rends[name]
		if r == nil {
			continue
		}
		for _, it := range r.pending {
			r.skip[it.seq] = true
			dropped++
		}
		r.pending = nil
	}
	switch {
	case run.state == runHeld && len(run.seen) > 0:
		t.logf("stream: timeline %s: run %d discarded before it was complete (no %s); its %d segments are never served",
			filepath.Base(t.dir), run.id, strings.Join(t.blockingLocked(run), ","), dropped)
	case dropped > 0:
		t.logf("stream: timeline %s: run %d: %d segments past the reference's last are never served",
			filepath.Base(t.dir), run.id, dropped)
	}
	if !run.expired {
		t.stalls++
		t.logf("stream: timeline %s: run %d ended without being expired (%d failed runs in a row)", filepath.Base(t.dir), run.id, t.stalls)
	}
	t.healthy = 0
	t.cur = t.newRun()
	return t.stalls
}

// ProcessStarted records that the restart's FFmpeg is running: the
// current run (made by MarkRestart) now belongs to a process, and the next
// MarkRestart ends it.
func (t *Timeline) ProcessStarted() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cur.started = true
}

// ExpireStalledRun is the manager's liveness tick. elapsed is the
// manager's time since its previous tick for this process (or since the
// process started); fresh says whether fresh source bytes reached FFmpeg
// in that time (not the cached join or reset snapshot). Fed time accrues
// elapsed on fresh ticks only; a quiet tick pauses it and never erases it.
//
// The current run is expired, atomically, when:
//   - it is held and its fed time since the process start reached
//     timelineGateTimeout;
//   - it is open and a required rendition published nothing for that much
//     fed time (the reference stopped, a sibling's PTS can't be read, or
//     FFmpeg is fed and writes nothing);
//   - the anchor cap would cut a required rendition off its gap history.
//
// An expired run never opens, so nothing can open between this call and
// the caller stopping FFmpeg; the restart's MarkRestart discards it.
// Every expiry counts toward the stall budget, which only
// timelineHealthyAfter of every required rendition publishing
// continuously (none silent for timelineHealthyGap) resets;
// stalls is the budget used, and at timelineMaxStalls the caller ends the
// session.
func (t *Timeline) ExpireStalledRun(elapsed time.Duration, fresh bool) (missing []string, stalls int, expired bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	run := t.cur
	var fed time.Duration
	if fresh {
		fed = elapsed
	}
	run.fed += fed
	healthy := run.state == runOpen && !run.expired
	for _, name := range t.requiredLocked() {
		r := t.rends[name]
		switch {
		case r == nil:
			healthy = false
		case r.pubTick:
			r.fedNoPub, r.sincePub = 0, 0
		default:
			r.fedNoPub += fed
			r.sincePub += elapsed
			if r.sincePub >= timelineHealthyGap {
				healthy = false
			}
		}
	}
	for _, r := range t.rends {
		r.pubTick = false
	}
	if healthy {
		t.healthy += elapsed
		if t.healthy >= timelineHealthyAfter && t.stalls > 0 {
			t.logf("stream: timeline %s: every rendition published for %v; stall budget reset", filepath.Base(t.dir), t.healthy.Round(time.Second))
			t.stalls = 0
		}
	} else {
		t.healthy = 0
	}
	if run.expired {
		return nil, t.stalls, false
	}
	switch {
	case len(run.overflow) > 0:
		missing = run.overflow
	case run.state == runHeld && run.fed >= timelineGateTimeout:
		missing = t.blockingLocked(run)
	case run.state == runOpen:
		for _, name := range t.requiredLocked() {
			if r := t.rends[name]; r != nil && r.fedNoPub >= timelineGateTimeout {
				missing = append(missing, name)
			}
		}
	}
	if len(missing) == 0 {
		return nil, t.stalls, false
	}
	run.expired = true
	t.stalls++
	t.logf("stream: timeline %s: run %d expired: no %s after %v fed (%d failed runs in a row)",
		filepath.Base(t.dir), run.id, strings.Join(missing, ","), run.fed.Round(time.Second), t.stalls)
	return missing, t.stalls, true
}

// requiredLocked: the reference and every rendition ever published, in
// rendition order.
func (t *Timeline) requiredLocked() []string {
	var out []string
	for _, n := range t.renditions {
		if n == t.ref || t.required[n] {
			out = append(out, n)
		}
	}
	return out
}

// blockingLocked lists the required renditions that keep run from opening:
// no segment in it, a first PTS not read yet, or (siblings) a first
// segment whose whole span the reference's staged segments don't cover,
// which could not be published together with them.
func (t *Timeline) blockingLocked(run *tlRun) []string {
	base, hasBase := t.stagedBaseLocked()
	refEnd := 0.0
	if ref := t.rends[t.ref]; hasBase && ref != nil {
		refEnd = stagedEnd(ref.pending, base)
	}
	var out []string
	for _, n := range t.requiredLocked() {
		r := t.rends[n]
		if r == nil || len(r.pending) == 0 || !r.pending[0].first || !r.pending[0].hasPTS || !hasBase {
			out = append(out, n)
			continue
		}
		if n != t.ref && unwrapMedia(r.pending[0].raw-base, 0)+r.pending[0].dur > refEnd+1e-6 {
			out = append(out, n)
		}
	}
	return out
}

// stagedBaseLocked is the earliest first PTS read among the renditions'
// first segments in the current run.
func (t *Timeline) stagedBaseLocked() (int64, bool) {
	var base int64
	has := false
	for _, name := range t.renditions {
		r := t.rends[name]
		if r == nil || len(r.pending) == 0 {
			continue
		}
		if it := r.pending[0]; it.first && it.hasPTS && (!has || ptsBefore(it.raw, base)) {
			base, has = it.raw, true
		}
	}
	return base, has
}

// stagedEnd is the media end the staged items would reach from base,
// stopping at the first one that can't be placed yet (assignMedia's rule).
func stagedEnd(items []*tlItem, base int64) float64 {
	end := 0.0
	for _, it := range items {
		if !it.placeable() {
			break
		}
		m := end
		switch {
		case it.first:
			m = unwrapMedia(it.raw-base, 0)
		case it.hasPTS:
			m = unwrapMedia(it.raw-base, end)
		}
		end = m + it.dur
	}
	return end
}

func (t *Timeline) rendLocked(name string) *tlRend {
	r := t.rends[name]
	if r == nil {
		r = &tlRend{segs: map[int64]tlSeg{}, skip: map[int64]bool{}, lastSeq: -1, asgRun: -1}
		t.rends[name] = r
	}
	return r
}

func (t *Timeline) observe() {
	lists := map[string]MediaPlaylist{}
	for _, name := range t.renditions {
		raw, err := os.ReadFile(filepath.Join(t.dir, name+".m3u8"))
		if err != nil {
			continue // not written yet
		}
		lists[name] = ParseMediaPlaylist(raw)
	}
	capMseq := int64(-1)
	if t.caption != "" {
		if raw, err := os.ReadFile(filepath.Join(t.dir, t.caption+".m3u8")); err == nil {
			capMseq = ParseMediaPlaylist(raw).MediaSequence
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	run := t.cur
	if capMseq > t.capMseq {
		t.capMseq = capMseq
	}
	for _, name := range t.renditions {
		r := t.rends[name]
		if r != nil {
			// A failed PTS read is retried: the file may not have been
			// complete or readable yet.
			for _, it := range r.pending {
				if !it.hasPTS && (it.first || it.reads < timelinePTSReads) {
					it.raw, it.hasPTS = segmentFirstPTS(it.path)
					it.reads++
				}
			}
		}
		p, ok := lists[name]
		if !ok {
			continue
		}
		r = t.rendLocked(name)
		if p.MediaSequence < r.ffMseq {
			continue // a stale read: never go back
		}
		for i, sg := range p.Segments {
			seq := p.MediaSequence + int64(i)
			if seq <= r.lastSeq {
				continue
			}
			if r.lastSeq >= 0 && seq > r.lastSeq+1 {
				t.logf("stream: timeline %s: %s: %d segments slid out unobserved", filepath.Base(t.dir), name, seq-r.lastSeq-1)
			}
			r.lastSeq = seq
			path := filepath.Join(t.dir, sg.URI)
			it := &tlItem{seq: seq, dur: sg.Duration, path: path, first: !run.seen[name], reads: 1}
			it.obs, it.file = t.observedStart(path, sg.Duration, now)
			it.raw, it.hasPTS = segmentFirstPTS(path)
			run.seen[name] = true
			r.pending = append(r.pending, it)
		}
		r.ffMseq = p.MediaSequence
		t.trimPendingLocked(name, r)
		t.pruneLocked(name, r)
	}
	if run.state == runHeld && !run.expired && len(t.blockingLocked(run)) == 0 {
		t.openLocked(run) // and publish below, in the same pass
	}
	if run.state == runOpen {
		for _, name := range t.renditions {
			if r := t.rends[name]; r != nil {
				assignPending(run, r)
			}
		}
		t.publishLocked(run)
	}
}

// trimPendingLocked bounds r's waiting segments: those that slid out of
// FFmpeg's window unpublished, and the oldest beyond timelineMaxPending,
// are never served.
func (t *Timeline) trimPendingLocked(name string, r *tlRend) {
	n := 0
	for n < len(r.pending) && (r.pending[n].seq < r.ffMseq || len(r.pending)-n > timelineMaxPending) {
		r.skip[r.pending[n].seq] = true
		n++
	}
	if n == 0 {
		return
	}
	if r.pending[n-1].first && n < len(r.pending) {
		r.pending[n].first = true // the run's first segment for this rendition is now the next one
	}
	r.pending = r.pending[n:]
	t.logf("stream: timeline %s: %s: %d segments waited too long and are never served", filepath.Base(t.dir), name, n)
}

// openLocked fixes run's base PTS, wall-clock start and epoch from the
// first segment of every rendition, then assigns media offsets. The
// caller publishes in the same locked pass, so every required rendition
// publishes its staged segment as the run opens.
func (t *Timeline) openLocked(run *tlRun) {
	run.base, run.hasBase = t.stagedBaseLocked()
	first := true
	for _, name := range t.renditions {
		r := t.rends[name]
		if r == nil {
			continue
		}
		assignPending(run, r)
		for _, it := range r.pending {
			if it.first && it.hasMedia {
				if s := it.obs.Add(-seconds(it.media)); first || s.Before(run.start) {
					run.start, first = s, false
				}
			}
		}
	}
	run.floor = t.maxEnd
	if run.start.Before(run.floor) {
		run.start = run.floor // a run never starts before what came before it ended
	}
	if ref := t.rends[t.ref]; ref != nil && len(ref.pending) > 0 {
		it := ref.pending[0]
		if lag := it.obs.Sub(run.start.Add(seconds(it.media))); lag > 0 {
			run.refLag = lag
		}
	}
	run.epoch = t.epoch + 1
	t.epoch = run.epoch
	run.state = runOpen // the stall budget is not reset: only a healthy stretch does that
	for _, r := range t.rends {
		r.fedNoPub, r.sincePub = 0, 0
	}
	if run.epoch > 0 {
		t.logf("stream: timeline %s: run %d opened as discontinuity %d", filepath.Base(t.dir), run.id, run.epoch)
	}
}

// assignPending places r's waiting segments in order, up to the first one
// whose PTS isn't read yet.
func assignPending(run *tlRun, r *tlRend) {
	for _, it := range r.pending {
		if it.hasMedia {
			continue
		}
		if !it.placeable() {
			return
		}
		assignMedia(run, r, it)
	}
}

// assignMedia places it on run's media timeline: its first PTS relative to
// the run's base (unwrapped next to where the rendition's chain expects
// it), or the chain itself when a later segment's PTS couldn't be read.
func assignMedia(run *tlRun, r *tlRend, it *tlItem) {
	if it.first {
		r.runMediaEnd = 0
	}
	m := r.runMediaEnd
	if it.hasPTS && run.hasBase {
		m = unwrapMedia(it.raw-run.base, r.runMediaEnd)
	}
	it.media, it.hasMedia = m, true
	r.runMediaEnd = m + it.dur
	r.asgRun = run.id
}

// unwrapMedia turns a 90 kHz PTS difference into seconds, choosing the
// 33-bit period nearest to expected.
func unwrapMedia(d int64, expected float64) float64 {
	v := float64(d&(int64(1)<<33-1)) / 90000
	return v + math.Round((expected-v)/ptsPeriod)*ptsPeriod
}

// ptsBefore reports whether a is earlier than b on the 33-bit clock.
func ptsBefore(a, b int64) bool {
	return (a-b)&(int64(1)<<33-1) >= int64(1)<<32
}

// publishLocked stamps the reference's placed segments in order (finding
// gaps), stopping at the first one whose PTS isn't read yet, then every
// sibling segment whose whole span the reference covers, with the shift
// of the reference interval it overlaps most.
func (t *Timeline) publishLocked(run *tlRun) {
	if run.expired || len(run.overflow) > 0 {
		return // expired (or being expired for the anchor cap): never publishes again
	}
	if ref := t.rends[t.ref]; ref != nil {
		for len(ref.pending) > 0 && ref.pending[0].hasMedia {
			it := ref.pending[0]
			ref.pending = ref.pending[1:]
			shift := run.shiftAt(it.media)
			start := run.start.Add(seconds(it.media) + shift)
			if !it.first && it.file {
				if gap := it.obs.Sub(start) - run.refLag; gap > timelineGapThreshold {
					run.anchors = append(run.anchors, tlAnchor{media: it.media, shift: shift + gap})
					t.logf("stream: timeline %s: %v gap before %s segment %d",
						filepath.Base(t.dir), gap.Round(100*time.Millisecond), t.ref, it.seq)
					start = start.Add(gap)
				}
			}
			t.stampLocked(run, t.ref, ref, it, start)
			if e := it.media + it.dur; e > run.refMediaEnd {
				run.refMediaEnd = e
			}
		}
	}
	for _, name := range t.renditions[1:] {
		r := t.rends[name]
		if r == nil {
			continue
		}
		for len(r.pending) > 0 && r.pending[0].hasMedia && r.pending[0].media+r.pending[0].dur <= run.refMediaEnd+1e-6 {
			it := r.pending[0]
			r.pending = r.pending[1:]
			if !t.required[name] && it.media < run.histFloor-1e-9 {
				r.skip[it.seq] = true // its gap history was dropped: never stamp from it
				t.logf("stream: timeline %s: %s segment %d predates the kept gap history; never served", filepath.Base(t.dir), name, it.seq)
				continue
			}
			t.stampLocked(run, name, r, it, run.start.Add(seconds(it.media)+run.shiftFor(it.media, it.media+it.dur)))
		}
	}
	t.compactAnchorsLocked(run)
}

// compactAnchorsLocked keeps the last anchor at or before the lowest
// arrival watermark and every anchor after it. A rendition's watermark is
// where its next segment in this run can start: 0 while it has none placed
// in the run (its playlist may simply be late), so its later segments
// still find every gap. Past timelineMaxAnchors, history only a
// non-required rendition needs is dropped (its older segments are then
// never served); history a required rendition needs never is: the run
// stops publishing and ExpireStalledRun expires it.
func (t *Timeline) compactAnchorsLocked(run *tlRun) {
	wmAll, wmReq := run.refMediaEnd, run.refMediaEnd
	for _, name := range t.renditions[1:] {
		w := t.watermarkLocked(run, name)
		wmAll = math.Min(wmAll, w)
		if t.required[name] {
			wmReq = math.Min(wmReq, w)
		}
	}
	lastAtOrBefore := func(wm float64) int {
		k := 0
		for i, a := range run.anchors {
			if a.media <= wm+1e-9 {
				k = i
			}
		}
		return k
	}
	keep := lastAtOrBefore(wmAll)
	if len(run.anchors)-keep > timelineMaxAnchors {
		if len(run.anchors)-lastAtOrBefore(wmReq) > timelineMaxAnchors {
			cut := run.anchors[len(run.anchors)-timelineMaxAnchors].media
			for _, name := range t.renditions[1:] {
				if t.required[name] && t.watermarkLocked(run, name) < cut {
					run.overflow = append(run.overflow, name)
				}
			}
			t.logf("stream: timeline %s: run %d: %s still need(s) more than %d gaps of history; stopping the run",
				filepath.Base(t.dir), run.id, strings.Join(run.overflow, ","), timelineMaxAnchors)
			return
		}
		keep = len(run.anchors) - timelineMaxAnchors
		run.histFloor = run.anchors[keep].media
		t.logf("stream: timeline %s: run %d: more than %d gaps of history; segments of renditions not published yet from before media %.1fs are never served",
			filepath.Base(t.dir), run.id, timelineMaxAnchors, run.histFloor)
	}
	if keep > 0 {
		run.headShift = run.anchors[keep-1].shift
	}
	run.anchors = run.anchors[keep:]
}

// watermarkLocked is the media point from which name's segments in run
// are still to come.
func (t *Timeline) watermarkLocked(run *tlRun, name string) float64 {
	r := t.rends[name]
	if r == nil || r.asgRun != run.id {
		return 0
	}
	if len(r.pending) > 0 && r.pending[0].hasMedia {
		return r.pending[0].media
	}
	return r.runMediaEnd
}

func (t *Timeline) stampLocked(run *tlRun, name string, r *tlRend, it *tlItem, start time.Time) {
	if start.Before(run.floor) {
		start = run.floor
	}
	if start.Before(r.prevEnd) {
		start = r.prevEnd // never overlap the rendition's previous segment
	}
	r.segs[it.seq] = tlSeg{bseq: r.nextB, start: start, dur: it.dur, epoch: run.epoch, tag: it.first && r.everPub}
	r.nextB++
	r.everPub = true
	r.pubTick = true
	run.pub[name] = true
	t.required[name] = true // a published rendition is required from now on, crashes or not
	end := start.Add(seconds(it.dur))
	r.prevEnd = end
	if end.After(t.maxEnd) {
		t.maxEnd = end
	}
}

// observedStart is when the segment's file says it started: its mtime (the
// end) minus EXTINF. Without a usable file it is now minus EXTINF.
func (t *Timeline) observedStart(path string, dur float64, now time.Time) (time.Time, bool) {
	d := seconds(dur)
	if fi, err := os.Stat(path); err == nil && !fi.ModTime().After(now) {
		return fi.ModTime().Add(-d), true
	}
	return now.Add(-d), false
}

// segmentFirstPTS is the PTS of the first PES in a TS segment file that
// carries one (90 kHz). It reads at most the first 64 KiB.
func segmentFirstPTS(path string) (int64, bool) {
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 64*1024)
	n, _ := io.ReadFull(f, buf)
	buf = buf[:n]
	for off := 0; off+188 <= len(buf); off += 188 {
		p := buf[off : off+188]
		if p[0] != 0x47 || p[1]&0x40 == 0 || p[3]&0x10 == 0 {
			continue // not a payload unit start
		}
		i := 4
		if p[3]&0x20 != 0 {
			i += 1 + int(p[4])
		}
		if i+14 > len(p) {
			continue
		}
		pes := p[i:]
		if pes[0] != 0 || pes[1] != 0 || pes[2] != 1 || pes[7]&0x80 == 0 {
			continue // a table section, or a PES without PTS
		}
		b := pes[9:14]
		return int64(b[0]>>1&0x07)<<30 | int64(b[1])<<22 | int64(b[2]>>1)<<15 | int64(b[3])<<7 | int64(b[4]>>1), true
	}
	return 0, false
}

// pruneLocked keeps r's skips for its own window, and its stamps for its
// window or, for the reference, as far back as the caption playlist still
// lists (at most timelineCaptionKeep segments further).
func (t *Timeline) pruneLocked(name string, r *tlRend) {
	keep := r.ffMseq
	if name == t.ref && t.capMseq >= 0 && t.capMseq < keep {
		keep = max(t.capMseq, r.ffMseq-timelineCaptionKeep)
	}
	for seq := range r.segs {
		if seq < keep {
			delete(r.segs, seq)
		}
	}
	for seq := range r.skip {
		if seq < r.ffMseq {
			delete(r.skip, seq)
		}
	}
}

// Retime rewrites one served playlist (FFmpeg's bytes, URIs still bare)
// from the observer's snapshot: Bowtie's media sequence numbers,
// #EXT-X-PROGRAM-DATE-TIME on every segment, #EXT-X-DISCONTINUITY on each
// rendition's first segment of a new epoch and #EXT-X-DISCONTINUITY-SEQUENCE.
// playlist is the served name without .m3u8: a rendition, or the caption
// playlist, which is retimed with the reference's stamps by FFmpeg
// sequence number (repairCaptionPlaylist keeps the numbers equal; its URIs
// pass through). Segments of discarded runs are left out; segments that
// slid out of the window are dropped from the head; segments not published
// yet end the playlist. Each playlist's MEDIA-SEQUENCE never goes below
// what it was last served with: head segments under that floor are left
// out (a stale read).
func (t *Timeline) Retime(playlist string, raw []byte) []byte {
	p := ParseMediaPlaylist(raw)
	t.mu.Lock()
	defer t.mu.Unlock()
	name, caption := playlist, playlist == t.caption && t.caption != ""
	if caption {
		name = t.ref
	}
	r := t.rends[name]
	type out struct {
		PlaylistSegment
		st tlSeg
	}
	var segs []out
	complete := r != nil
	for i, sg := range p.Segments {
		if r == nil {
			break
		}
		seq := p.MediaSequence + int64(i)
		if r.skip[seq] {
			continue // a discarded run, or past a run's reference
		}
		if !caption && seq < r.ffMseq {
			continue // slid out (a stamp may be kept for the captions only)
		}
		st, ok := r.segs[seq]
		if !ok {
			if len(segs) == 0 && seq < r.ffMseq {
				continue // slid out since this playlist was read
			}
			complete = false
			break // not published yet
		}
		segs = append(segs, out{sg, st})
	}
	floor, hasFloor := t.floors[playlist]
	for len(segs) > 0 && hasFloor && segs[0].st.bseq < floor {
		segs = segs[1:] // already behind what this playlist served
	}
	mseq, dseq := int64(0), t.epoch
	if r != nil {
		// Empty: the lowest number still to be served from this read's
		// first sequence on (never below the floor), so a later read that
		// lists those segments starts there rather than going back.
		mseq = r.nextB
		for seq, st := range r.segs {
			if seq >= p.MediaSequence && st.bseq < mseq && (!hasFloor || st.bseq >= floor) {
				mseq = st.bseq
			}
		}
		if hasFloor && floor > mseq {
			mseq = floor
		}
	}
	if dseq < 0 {
		dseq = 0
	}
	if len(segs) > 0 {
		mseq, dseq = segs[0].st.bseq, segs[0].st.epoch
		if segs[0].st.tag {
			dseq--
		}
	}
	if r != nil {
		t.floors[playlist] = mseq // r != nil: a known playlist, so the map stays bounded
	}
	var b bytes.Buffer
	for _, h := range p.Header {
		b.WriteString(h)
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "#EXT-X-MEDIA-SEQUENCE:%d\n#EXT-X-DISCONTINUITY-SEQUENCE:%d\n", mseq, dseq)
	for _, s := range segs {
		if s.st.tag {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		b.WriteString("#EXT-X-PROGRAM-DATE-TIME:" + s.st.start.UTC().Format(WallClockLayout) + "\n")
		for _, tag := range s.Tags {
			b.WriteString(tag)
			b.WriteByte('\n')
		}
		b.WriteString(s.URI)
		b.WriteByte('\n')
	}
	if complete {
		for _, tr := range p.Trailer {
			b.WriteString(tr)
			b.WriteByte('\n')
		}
	}
	return b.Bytes()
}

// TimelineSegment is one window segment on the wall clock.
type TimelineSegment struct {
	Start    time.Time
	Duration float64
}

// WindowTimeline maps a rendition's current window to the wall clock.
// MediaSequence is Bowtie's number of the first segment.
type WindowTimeline struct {
	MediaSequence int64
	Segments      []TimelineSegment
}

// WindowStart is the first segment's start (zero when the window is empty).
func (w WindowTimeline) WindowStart() time.Time {
	if len(w.Segments) == 0 {
		return time.Time{}
	}
	return w.Segments[0].Start
}

// LiveEdge is the last segment's end (zero when the window is empty).
func (w WindowTimeline) LiveEdge() time.Time {
	if len(w.Segments) == 0 {
		return time.Time{}
	}
	last := w.Segments[len(w.Segments)-1]
	return last.Start.Add(seconds(last.Duration))
}

// Window is the published window of rendition on the wall clock.
func (t *Timeline) Window(rendition string) WindowTimeline {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.rends[rendition]
	if r == nil {
		return WindowTimeline{}
	}
	w := WindowTimeline{MediaSequence: r.nextB}
	var seqs []int64
	for seq := range r.segs {
		if seq >= r.ffMseq { // stamps kept for the captions aren't in the window
			seqs = append(seqs, seq)
		}
	}
	sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
	for i, seq := range seqs {
		st := r.segs[seq]
		if i == 0 {
			w.MediaSequence = st.bseq
		}
		w.Segments = append(w.Segments, TimelineSegment{Start: st.start, Duration: st.dur})
	}
	return w
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd server && go test ./internal/stream/ -run 'TestTimeline|TestParseMediaPlaylist|TestSegmentFirstPTS' -v -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/stream/hlsplaylist.go server/internal/stream/timeline.go server/internal/stream/timeline_test.go
git commit -m "feat(stream): timeline observer with per-run publication gating

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 10: Run the observer per session, restart stalled runs, and serve every media playlist from its snapshot

**Files:**
- Modify: `server/internal/transcode/layout.go` (`Renditions`)
- Test: `server/internal/transcode/layout_test.go`
- Modify: `server/internal/stream/subqueue.go`, `server/internal/stream/ingest.go` (fresh-byte accounting, `IngestSub.FreshBytes`)
- Test: `server/internal/stream/ingest_test.go`
- Modify: `server/internal/stream/session.go`, `server/internal/stream/manager.go`
- Test: `server/internal/stream/manager_test.go`
- Modify: `server/internal/api/stream_handlers.go` (`StreamController`, `serveMediaPlaylist`)
- Test: `server/internal/api/stream_handlers_test.go`
- Modify: `server/internal/e2e/testdata/scenarios/{long-outage-restart,baseline,transcoder-restart}.yaml`

**Interfaces:**
- Consumes:
  - Task 9: `NewTimeline`, `(*Timeline).Observe`, `Run`, `MarkRestart`, `ProcessStarted`, `ExpireStalledRun`, `Retime`, `timelineMaxStalls`, `timelineGateTimeout`, and the test helper `tsWithPTS`.
  - Task 7: `subQueue.wrote`, `addInFlight`, `reset`, `IngestSub.drain`/`writeChunk` (slices of `pipeWriteSlice`).
- Produces:
  - `func (l Layout) Renditions() []string`: the media playlist names FFmpeg writes, without `.m3u8` (rungs, `aacN`, `ac3N`). Caption playlists aren't renditions: they're served through their rung.
  - **Fresh-byte accounting** (on top of Task 7):
    - `subQueue.fresh uint64` counts written bytes that came from the live source. `subQueue.cachedFront int` counts the cached copies at the front of the queue. Cached copies only ever enter at the front: a reset head (`reset`) and the PAT+PMT put back after an overflow (`push`). The join snapshot is written by `drain` before the queue.
    - `popChunk() (c []byte, cached, ok bool)`; `pop` wraps it. `wroteFrom(n int, cached bool)` is `wrote(n)` plus, unless cached, `fresh += n`. `freshBytes() uint64`.
    - `drain` writes the join with `cached = true` and each popped chunk with its own flag.
    - `func (s *IngestSub) FreshBytes() uint64`: live source bytes written to this FFmpeg's input so far.
  - `session.timeline *Timeline` and `session.timelineCancel func()`.
    - The timeline is created in `startAttempt` right after `waitPlaylist`, with `time.Now`, and observed once synchronously, so the first playlist request already has stamps.
    - `go tl.Run(ctx)` starts only once the session is registered. The duplicate-key abandon path never starts it.
    - `teardownSessionLocked` cancels it.
  - `session.lastFresh uint64` and `session.lastTick time.Time`: the gate's tick state. `startAttempt` sets `lastTick` to the process start; `restartSession`'s commit resets both (`0`, the new process start), since the new sub's counters start at 0.
  - `maintain` makes one gate tick per live session that isn't crashed or restarting:
    - `fresh := sess.sub.FreshBytes()`; `moved := fresh != sess.lastFresh`; `elapsed := now − sess.lastTick`; then `sess.timeline.ExpireStalledRun(elapsed, moved)`.
    - **Fed time accrues only on ticks where fresh source bytes reached FFmpeg.** The join snapshot a restart writes at attach is cached, so a restart during an outage (even one whose FFmpeg drains a large join slowly over many ticks) is never charged. A quiet tick pauses fed time and never erases it, so a source that feeds every 10 s still accrues it.
    - `maintain` runs every `reaperInterval` (5 s), so a stall is detected 15–20 s of fed time after it starts.
    - The decision and the expiry are one call under the timeline's lock. An expired run never opens, so no run can open between it and `Stop`.
    - On an expiry with the budget not spent, it logs `stream: session <id>: ffmpeg published no <renditions> for 15s of fed time; restarting it` and stops the process: `sess.proc.Stop()`, the existing kill path. `supervise` then marks the session crashed, and the next `maintain` restarts it with the usual backoff. The restart's `MarkRestart` discards an expired held run, so a half-aligned run is never published.
    - On the expiry that spends the budget (`timelineMaxStalls` failed runs without 60 s of continuous output from every required rendition between), it logs `stream: session <id>: 3 failed runs in a row (last: no <renditions>); ending the session` and calls `teardownSessionLocked`. That stops FFmpeg, closes the ingest subscription (the tuner is released after ingest's 5 s tail) and removes the viewers. Their next request gets the existing `404 session not found`, the same as an admin `Terminate`, and clients reconnect into a fresh session. Renditions are never dropped part-way.
    - The timeline never takes `m.mu`, so calling it under `m.mu` is safe.
  - `restartSession` calls `sess.timeline.MarkRestart()` **first**, before `Attach` and `m.runner.Start`, outside `m.mu` as `restartSession` already runs. `MarkRestart` takes only the timeline's own locks. Its commit block calls `sess.timeline.ProcessStarted()` once the new FFmpeg is running.
    - **Failed attaches cost nothing.** During a long outage ingest closes the device connection, and every restart attempt's `Attach` fails with `ErrNoSignal` (or busy), so FFmpeg never starts. `MarkRestart` is a no-op until `ProcessStarted`, so those attempts end no run, count nothing and create no held run. Only the process that actually died used budget, and a plain outage never ends a session.
    - It discards a run that never opened, and holds the next run until every rendition the last opened run published has a segment in it.
    - A run that ended without being expired (FFmpeg crashed, and `supervise` restarts it before the gate fires) counts toward the budget. `restartSession` is reached only from `maintain` for a crashed session, so a planned restart never counts as a failure. When the budget is spent, `restartSession` logs `stream: session <id>: 3 failed runs in a row; ending the session` and tears the session down instead of attaching or starting FFmpeg. So no restart path, gate or crash, ever starts a fourth FFmpeg.
  - `func (m *Manager) RetimePlaylist(viewerID, playlist string, raw []byte) ([]byte, bool)` reads the snapshot only. `playlist` is the served name without `.m3u8`, captions included, so each playlist keeps its own `MEDIA-SEQUENCE` floor.
  - `func (m *Manager) timelineOf(viewerID string) (tl *Timeline, top string, owner int64, ok bool)`; Task 11 uses it.
  - `api.StreamController` gains `RetimePlaylist(viewerID, playlist string, raw []byte) ([]byte, bool)`.
  - `serveMediaPlaylist` retimes after the caption repair and before URL rewriting, under the playlist's own name (`v720_vtt` for the captions). The timeline maps the caption playlist to the reference's stamps.
- Start latency:
  - A playlist request that arrives before the observer has stamped anything gets a playlist with no segments, and the player polls again. Run 0 opens once the reference's first segment's PTS is read.
  - The synchronous `Observe` after `waitPlaylist` makes that rare. `maxStartSeconds` on `baseline` and `production-baseline` (Task 4) catches a regression.

- [ ] **Step 1: Write the failing tests**

Append to `server/internal/transcode/layout_test.go`:

```go
func TestLayoutRenditions(t *testing.T) {
	l := transcode.Layout{
		Rungs:   transcode.Ladder(720),
		Audio:   []transcode.AudioTrack{{Lang: "eng", AC3: true}, {Lang: "spa", AC3: true}},
		AC3Copy: true, Captions: true,
	}
	if got := strings.Join(l.Renditions(), ","); got != "v720,v480,v360,aac0,aac1,ac30,ac31" {
		t.Fatalf("Renditions = %s", got)
	}
	if got := strings.Join((transcode.Layout{Rungs: []transcode.Rung{{Height: 480}}}).Renditions(), ","); got != "v480,aac0" {
		t.Fatalf("single rung Renditions = %s", got)
	}
}
```

Append to `server/internal/stream/ingest_test.go` (`bytes`, `io` and `time` are already imported there):

```go
// The gate's clock counts only live source bytes reaching FFmpeg. A large
// join snapshot drained slowly (during an outage, nothing else arrives),
// a reset head and the PAT+PMT put back after an overflow are cached
// copies: written, never fresh. Live chunks are fresh.
func TestCachedBytesAreNotFresh(t *testing.T) {
	now := time.Date(2026, 10, 11, 21, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	pr, pw := io.Pipe()
	s := &IngestSub{R: pr, pr: pr, pw: pw, q: newSubQueue(1<<20, clock)}
	defer s.Close()
	go s.drain(make([]byte, 1<<20))
	read := func(n int) {
		t.Helper()
		if _, err := io.ReadFull(pr, make([]byte, n)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond) // let drain record the slice
	}
	for i := 0; i < 16; i++ { // the 1 MiB join, 64 KiB at a time
		read(64 << 10)
		if f := s.FreshBytes(); f != 0 {
			t.Fatalf("after %d KiB of the join, fresh = %d", (i+1)*64, f)
		}
	}
	if w := s.q.writtenBytes(); w != 1<<20 {
		t.Fatalf("written = %d, want the whole join", w)
	}
	live := bytes.Repeat([]byte{0x47}, 10*tsPacketSize)
	s.q.push(live, nil)
	read(len(live))
	if f := s.FreshBytes(); f != uint64(len(live)) {
		t.Fatalf("fresh = %d after a live chunk, want %d", f, len(live))
	}
	s.q.reset(bytes.Repeat([]byte{0x47}, 4*tsPacketSize)) // a reset head
	read(4 * tsPacketSize)
	if f := s.FreshBytes(); f != uint64(len(live)) {
		t.Fatalf("a reset head counted as fresh: %d", f)
	}
	// Overflow on a queue nothing reads: the oldest chunks go (first an
	// unread reset head) and PAT+PMT is put back at the head, cached; the
	// live chunks after it are fresh.
	q := newSubQueue(1000*tsPacketSize, clock)
	tables := bytes.Repeat([]byte{0x47}, 2*tsPacketSize)
	big := bytes.Repeat([]byte{0x47}, 400*tsPacketSize)
	q.reset(tables)
	for q.push(big, tables) == 0 {
	}
	if c, cached, _ := q.popChunk(); len(c) != len(tables) || !cached {
		t.Fatalf("after an overflow popped %d bytes, cached %v; want the PAT+PMT, cached", len(c), cached)
	}
	for q.size() > 0 {
		if _, cached, _ := q.popChunk(); cached {
			t.Fatal("a live chunk after an overflow popped as cached")
		}
	}
}
```

Append to `server/internal/stream/manager_test.go`:

```go
// The session's observer starts with it. An append restart holds the new
// run until every rendition has a publishable segment in it. A run that
// still misses one after the gate timeout of fed time is expired and
// restarted through the kill path, and never served; time without fresh
// input isn't charged. Teardown stops the observer. Segment files are real
// TS with PTS: a run opens only once its first PTS is read.
func TestSessionTimelineGatesRestartedRuns(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	m, _, _ := newTestManagerWithDial(st, cfg, clock, runner, nil)
	h, err := m.Start(context.Background(), user, chID, clientCaps(""))
	if err != nil {
		t.Fatal(err)
	}
	media, _ := m.SessionMediaOf(h.ViewerID)
	top := media.Layout.TopName()
	if got := strings.Join(media.Layout.Renditions(), ","); got != top+",aac0" {
		t.Fatalf("renditions %s", got)
	}
	tl, _, _, ok := m.timelineOf(h.ViewerID)
	if !ok {
		t.Fatal("no timeline for the viewer")
	}
	g := newGateHarness(t, m, clock, runner, h)
	g.write(top, 1.4, 5.4)
	g.write("aac0", 1.4, 5.4)
	tl.Observe()

	runner.LastProc().Crash(errors.New("boom"))
	g.restartTo(2)
	raw := g.write(top, 1.4, 5.4, 1.4) // run 1: video only so far
	tl.Observe()
	if out, _ := m.RetimePlaylist(h.ViewerID, top, raw); strings.Contains(string(out), top+"_00002.ts") {
		t.Fatalf("served a segment of a held run:\n%s", out)
	}
	for i := 0; i < 10; i++ { // 50 s with no fresh input: an outage, not a stall
		g.tick(0, 0)
	}
	g.tick(64<<10, 0)
	g.tick(64<<10, 0)
	if runner.LastProc().Stopped() {
		t.Fatal("stopped after 10 s of fed time")
	}
	g.tick(64<<10, 0)
	if !runner.LastProc().Stopped() {
		t.Fatal("a run missing aac0 after 15 s of fed time was not restarted")
	}
	g.restartTo(3)
	raw = g.write(top, 1.4, 5.4, 1.4, 1.4, 5.4)
	g.write("aac0", 1.4, 5.4, 1.42)
	tl.Observe()
	out, ok := m.RetimePlaylist(h.ViewerID, top, raw)
	s := string(out)
	if !ok || strings.Contains(s, top+"_00002.ts") || !strings.Contains(s, "#EXT-X-DISCONTINUITY\n#EXT-X-PROGRAM-DATE-TIME:") ||
		strings.Count(s, "#EXT-X-DISCONTINUITY\n") != 1 || strings.Count(s, "#EXT-X-PROGRAM-DATE-TIME:") != 4 ||
		!strings.Contains(s, "#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-DISCONTINUITY-SEQUENCE:0\n") {
		t.Fatalf("retimed playlist:\n%s", s)
	}
	if _, ok := m.RetimePlaylist("no-such-viewer", top, raw); ok {
		t.Fatal("unknown viewer retimed")
	}
	m.mu.Lock()
	m.teardownSessionLocked(m.sessions[m.viewers[h.ViewerID].SessionID])
	m.mu.Unlock()
	if _, _, _, ok := m.timelineOf(h.ViewerID); ok {
		t.Fatal("timeline outlived the session")
	}
}

// Fed sparsely (fresh bytes every 10 s), runs that publish nothing still
// expire, and the third failed run ends the session: no further restart,
// the viewer's session is gone (404 session not found, so the client
// starts a fresh one), and the tuner is released after ingest's tail.
func TestThirdGateStallEndsSessionAndFreesTuner(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	m, im, _ := newTestManagerWithDial(st, cfg, clock, runner, nil)
	h, err := m.Start(context.Background(), user, chID, clientCaps(""))
	if err != nil {
		t.Fatal(err)
	}
	media, _ := m.SessionMediaOf(h.ViewerID)
	top := media.Layout.TopName()
	tl, _, _, _ := m.timelineOf(h.ViewerID)
	g := newGateHarness(t, m, clock, runner, h)
	g.write(top, 1.4, 5.4)
	g.write("aac0", 1.4, 5.4)
	tl.Observe() // run 0 opens, then FFmpeg writes nothing more
	for i := 0; i < 60 && !g.sessionGone(); i++ {
		fresh := 0
		if i%2 == 0 {
			fresh = 64 << 10
		}
		g.tick(fresh, 0)
	}
	if !g.sessionGone() {
		t.Fatalf("session still up after 5 min fed sparsely; %d starts", runner.Starts())
	}
	if runner.Starts() != timelineMaxStalls {
		t.Fatalf("starts = %d, want %d", runner.Starts(), timelineMaxStalls)
	}
	for i := 0; i < 10; i++ {
		g.tick(0, 0)
	}
	if runner.Starts() != timelineMaxStalls {
		t.Fatalf("starts = %d after the session ended", runner.Starts())
	}
	g.waitTunerFreed(im)
}

// Ruling: opening doesn't reset the budget. Three runs that open and then
// stop publishing the reference (audio goes on, FFmpeg is fed) end the
// session; no fourth FFmpeg starts.
func TestOpenThenStalledRunsUseUpTheBudget(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	m, im, _ := newTestManagerWithDial(st, cfg, clock, runner, nil)
	h, err := m.Start(context.Background(), user, chID, clientCaps(""))
	if err != nil {
		t.Fatal(err)
	}
	media, _ := m.SessionMediaOf(h.ViewerID)
	top := media.Layout.TopName()
	tl, _, _, _ := m.timelineOf(h.ViewerID)
	g := newGateHarness(t, m, clock, runner, h)
	var video, audio []float64
	for cycle := 1; cycle <= timelineMaxStalls; cycle++ {
		if cycle > 1 {
			g.restartTo(cycle)
		}
		video = append(video, 1.4, 5.4) // a new run opens
		audio = append(audio, 1.4, 5.4)
		g.write(top, video...)
		g.write("aac0", audio...)
		tl.Observe()
		tl.mu.Lock()
		open := tl.cur.state == runOpen
		tl.mu.Unlock()
		if !open {
			t.Fatalf("cycle %d: the run didn't open", cycle)
		}
		for i := 0; i < 8 && !runner.LastProc().Stopped() && !g.sessionGone(); i++ {
			audio = append(audio, audio[len(audio)-1]+4) // the reference stopped; audio goes on
			g.write("aac0", audio...)
			tl.Observe()
			g.tick(64<<10, 0)
		}
		if cycle < timelineMaxStalls && (!runner.LastProc().Stopped() || g.sessionGone()) {
			t.Fatalf("cycle %d: stopped %v, session gone %v", cycle, runner.LastProc().Stopped(), g.sessionGone())
		}
	}
	if !g.sessionGone() {
		t.Fatal("three open-then-stalled runs did not end the session")
	}
	for i := 0; i < 10; i++ {
		g.tick(0, 0)
	}
	if runner.Starts() != timelineMaxStalls {
		t.Fatalf("starts = %d, want %d (no fourth FFmpeg)", runner.Starts(), timelineMaxStalls)
	}
	g.waitTunerFreed(im)
}

// An outage never tears the session down: not with no input at all, and
// not while a restarted FFmpeg slowly drains the cached join (written
// bytes move, fresh ones don't).
func TestOutageAndCachedJoinNeverExpire(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	m, _, _ := newTestManagerWithDial(st, cfg, clock, runner, nil)
	h, err := m.Start(context.Background(), user, chID, clientCaps(""))
	if err != nil {
		t.Fatal(err)
	}
	media, _ := m.SessionMediaOf(h.ViewerID)
	top := media.Layout.TopName()
	tl, _, _, _ := m.timelineOf(h.ViewerID)
	g := newGateHarness(t, m, clock, runner, h)
	g.write(top, 1.4, 5.4)
	g.write("aac0", 1.4, 5.4)
	tl.Observe()
	for i := 0; i < 60; i++ { // 5 min of outage on the open run
		g.tick(0, 0)
	}
	if runner.LastProc().Stopped() || g.sessionGone() {
		t.Fatal("an outage stopped FFmpeg or ended the session")
	}
	runner.LastProc().Crash(errors.New("killed during the outage"))
	g.restartTo(2)
	for i := 0; i < 60; i++ { // the restart drains a big cached join, 64 KiB per tick
		g.tick(0, 64<<10)
	}
	if runner.LastProc().Stopped() || g.sessionGone() || runner.Starts() != 2 {
		t.Fatalf("a cached join during an outage counted as feeding: stopped %v, gone %v, starts %d",
			runner.LastProc().Stopped(), g.sessionGone(), runner.Starts())
	}
}

// Continuous feeding with every rendition publishing keeps the session
// and, after 60 s, resets the stall budget a crash used.
func TestContinuousFeedingResetsTheBudget(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	m, _, _ := newTestManagerWithDial(st, cfg, clock, runner, nil)
	h, err := m.Start(context.Background(), user, chID, clientCaps(""))
	if err != nil {
		t.Fatal(err)
	}
	media, _ := m.SessionMediaOf(h.ViewerID)
	top := media.Layout.TopName()
	tl, _, _, _ := m.timelineOf(h.ViewerID)
	g := newGateHarness(t, m, clock, runner, h)
	pts := []float64{1.4, 5.4}
	g.write(top, pts...)
	g.write("aac0", pts...)
	tl.Observe()
	runner.LastProc().Crash(errors.New("boom"))
	g.restartTo(2)
	tl.mu.Lock()
	used := tl.stalls
	tl.mu.Unlock()
	if used != 1 {
		t.Fatalf("stall budget used %d after a crash, want 1", used)
	}
	next := 1.4
	for i := 0; i < 14; i++ { // 70 s: a segment per rendition per tick
		pts = append(pts, next)
		next += 4
		g.write(top, pts...)
		g.write("aac0", pts...)
		tl.Observe()
		g.tick(64<<10, 0)
		if runner.LastProc().Stopped() {
			t.Fatalf("a healthy run was stopped at tick %d", i)
		}
	}
	tl.mu.Lock()
	used = tl.stalls
	tl.mu.Unlock()
	if used != 0 || runner.Starts() != 2 {
		t.Fatalf("stall budget %d, starts %d after 70 s of healthy output; want 0, 2", used, runner.Starts())
	}
}

// A plain outage never ends a session, even when FFmpeg dies in it: the
// restart's attaches fail with ErrNoSignal again and again (ingest closed
// the device connection), and none of those attempts starts FFmpeg, so
// none of them uses the stall budget or makes another run. Only the
// process that actually died counts. When the signal returns, the
// restart goes through and the viewer is still there.
func TestNoSignalAttachRetriesDontUseTheBudget(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	var noSignal atomic.Bool
	dial := func(ctx context.Context, url string) (io.ReadCloser, int, error) {
		if noSignal.Load() {
			return nil, 503, &DeviceError{Status: 503, Reason: "807 No Video Data"}
		}
		return newHangBody(), 200, nil
	}
	m, im, _ := newTestManagerWithDial(st, cfg, clock, runner, dial)
	h, err := m.Start(context.Background(), user, chID, clientCaps(""))
	if err != nil {
		t.Fatal(err)
	}
	media, _ := m.SessionMediaOf(h.ViewerID)
	top := media.Layout.TopName()
	tl, _, _, _ := m.timelineOf(h.ViewerID)
	g := newGateHarness(t, m, clock, runner, h)
	g.write(top, 1.4, 5.4)
	g.write("aac0", 1.4, 5.4)
	tl.Observe() // run 0 opens

	noSignal.Store(true)
	runner.LastProc().Crash(errors.New("input ended: the antenna lost the signal"))
	attaches := im.AttachCalls()
	for i := 0; i < 60; i++ { // 5 min of outage
		g.tick(0, 0)
	}
	if n := im.AttachCalls() - attaches; n <= int64(timelineMaxStalls) {
		t.Fatalf("only %d restart attaches during the outage; the test needs more than %d", n, timelineMaxStalls)
	}
	tl.mu.Lock()
	used, runs := tl.stalls, tl.runs
	tl.mu.Unlock()
	if g.sessionGone() || runner.Starts() != 1 || used != 1 || runs != 2 {
		t.Fatalf("after failed attaches: session gone %v, starts %d, budget used %d (want 1), runs %d (want 2)",
			g.sessionGone(), runner.Starts(), used, runs)
	}

	noSignal.Store(false) // the signal returns
	g.restartTo(2)
	g.write(top, 1.4, 5.4, 1.4, 5.4)
	g.write("aac0", 1.4, 5.4, 1.4, 5.4)
	tl.Observe()
	tl.mu.Lock()
	used, open := tl.stalls, tl.cur.state == runOpen
	tl.mu.Unlock()
	if g.sessionGone() || !open || used != 1 {
		t.Fatalf("after the signal returned: session gone %v, run open %v, budget used %d (want 1)", g.sessionGone(), open, used)
	}
	if _, ok := m.RetimePlaylist(h.ViewerID, top, g.write(top, 1.4, 5.4, 1.4, 5.4)); !ok {
		t.Fatal("the viewer lost its session")
	}
}

// gateHarness drives a session's FFmpeg runs for the gate tests: real
// segment files, byte counters, and real 5 s maintain ticks.
type gateHarness struct {
	t      *testing.T
	m      *Manager
	clock  *fakeClock
	runner *stubRunner
	h      ViewerHandle
}

func newGateHarness(t *testing.T, m *Manager, clock *fakeClock, runner *stubRunner, h ViewerHandle) *gateHarness {
	return &gateHarness{t: t, m: m, clock: clock, runner: runner, h: h}
}

// write replaces name's playlist with one segment per PTS (seconds), as
// real TS files.
func (g *gateHarness) write(name string, pts ...float64) []byte {
	g.t.Helper()
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n")
	for i, p := range pts {
		uri := fmt.Sprintf("%s_%05d.ts", name, i)
		if err := os.WriteFile(filepath.Join(g.h.SessionDir, uri), tsWithPTS(p), 0o644); err != nil {
			g.t.Fatal(err)
		}
		fmt.Fprintf(&b, "#EXTINF:4.0,\n%s\n", uri)
	}
	if err := os.WriteFile(filepath.Join(g.h.SessionDir, name+".m3u8"), []byte(b.String()), 0o644); err != nil {
		g.t.Fatal(err)
	}
	return []byte(b.String())
}

// tick is one maintain tick: fresh source bytes and cached (join or reset)
// bytes reach the current FFmpeg's input, the manager's clock moves 5 s,
// and the viewer is still polling. (The stub process never reads its
// input, so the bytes are recorded on its ingest queue directly.)
func (g *gateHarness) tick(fresh, cached int) {
	g.m.mu.Lock()
	var sub *IngestSub
	if v, ok := g.m.viewers[g.h.ViewerID]; ok {
		sub = g.m.sessions[v.SessionID].sub
	}
	g.m.mu.Unlock()
	if sub != nil {
		if cached > 0 {
			sub.q.wroteFrom(cached, true)
		}
		if fresh > 0 {
			sub.q.wroteFrom(fresh, false)
		}
	}
	g.clock.Advance(reaperInterval)
	g.m.Touch(g.h.ViewerID)
	g.m.maintain()
	time.Sleep(10 * time.Millisecond) // let supervise see a stopped process
}

func (g *gateHarness) sessionGone() bool {
	_, ok := g.m.SessionDirOf(g.h.ViewerID)
	return !ok
}

// restartTo ticks (unfed) until the runner has started FFmpeg starts times.
func (g *gateHarness) restartTo(starts int) {
	g.t.Helper()
	for i := 0; i < 40 && g.runner.Starts() < starts; i++ {
		g.tick(0, 0)
	}
	if g.runner.Starts() != starts {
		g.t.Fatalf("starts = %d, want %d", g.runner.Starts(), starts)
	}
}

// waitTunerFreed advances the clock past ingest's tail until the device
// connection is gone.
func (g *gateHarness) waitTunerFreed(im *IngestManager) {
	g.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for len(im.ActiveChannels()) != 0 && time.Now().Before(deadline) {
		g.clock.Advance(ingestTailDuration + time.Second)
		time.Sleep(10 * time.Millisecond)
	}
	if got := im.ActiveChannels(); len(got) != 0 {
		g.t.Fatalf("tuner still held for channels %v", got)
	}
}
```

The segment files are real TS packets with PTS (`tsWithPTS` from `timeline_test.go`, same package), because a run opens only once its first PTS is read. Their mtimes are the write time; Task 9 covers the PTS and mtime arithmetic.

The manager tests change nothing but the byte counters and the manager's clock. Each `tick` records fresh and/or cached bytes on the current sub's queue with `wroteFrom` (the stub process never reads its input, so nothing else would move them), advances the fake clock by `reaperInterval`, touches the viewer and runs `maintain`. There is no feed seam and no backdating: fed time comes only from real 5 s ticks with fresh bytes.
- `TestSessionTimelineGatesRestartedRuns`: a plain outage on a held run (10 ticks, 50 s) doesn't stop it; fed ticks expire it at 15 s; the next run opens with one DISCONTINUITY.
- `TestThirdGateStallEndsSessionAndFreesTuner`: sparse feeding (fresh bytes every other tick, every 10 s) still expires runs; the third ends the session, no fourth FFmpeg starts, and the tuner is freed after the tail.
- `TestOpenThenStalledRunsUseUpTheBudget`: three runs that open and then stop publishing the reference (audio goes on, fed) end the session; opening never resets the budget; no fourth FFmpeg.
- `TestOutageAndCachedJoinNeverExpire`: 5 minutes of outage on an open run, then a crash restart whose FFmpeg drains a cached join slowly (cached bytes every tick for 5 minutes): never stopped, never ended.
- `TestContinuousFeedingResetsTheBudget`: a crash uses one failed run (`MarkRestart`); 70 s of every rendition publishing resets the budget to 0 and stops nothing.
- `TestNoSignalAttachRetriesDontUseTheBudget`: FFmpeg dies in an outage and the restart's attaches fail with `ErrNoSignal` for 5 minutes (well over three attempts). The session survives, FFmpeg isn't started, the budget used is 1 and there are 2 runs. When the signal returns the restart goes through, the new run opens, the budget is still 1, and the viewer still has its session.
- `TestCachedBytesAreNotFresh` pins the ingest side of the slow join: a 1 MiB join drained in 64 KiB reads is written but never fresh, nor a reset head or the PAT+PMT put back after an overflow; live chunks are fresh.

In `server/internal/api/stream_handlers_test.go`, add a field to `stubStreams`:

```go
	retime func(viewerID, rendition string, raw []byte) ([]byte, bool)
```

and the method:

```go
func (s *stubStreams) RetimePlaylist(viewerID, rendition string, raw []byte) ([]byte, bool) {
	if s.retime == nil {
		return nil, false
	}
	return s.retime(viewerID, rendition, raw)
}
```

Append the test:

```go
// Rendition and caption playlists go through the session timeline before
// segment URLs are signed, each under its own name (the timeline maps the
// captions to their rung's stamps and keeps a MEDIA-SEQUENCE floor each).
func TestMediaPlaylistServedThroughTimeline(t *testing.T) {
	ss := newStubStreams()
	var renditions []string
	ss.retime = func(viewerID, rendition string, raw []byte) ([]byte, bool) {
		renditions = append(renditions, rendition)
		return append([]byte("#EXT-X-BOWTIE-TIMELINE\n"), raw...), true
	}
	h, st, _ := testAPIWithStreams(t, ss)
	seedUser(t, st, "alice", "pass", "viewer")
	dir := filepath.Join(t.TempDir(), "sess1")
	writeFixtureSession(t, dir)
	ss.register("viewer-a", dir)
	tok := stream.SignStreamToken([]byte(streamSecret), "viewer-a", time.Now().UTC().Add(time.Hour))
	for _, name := range []string{"v720.m3u8", "aac0.m3u8", "v720_vtt.m3u8"} {
		rr := doJSON(t, h, "GET", "/api/v1/stream/viewer-a/"+name+"?token="+tok, nil, nil)
		body := rr.Body.String()
		if rr.Code != http.StatusOK || !strings.HasPrefix(body, "#EXT-X-BOWTIE-TIMELINE\n") ||
			!strings.Contains(body, "/api/v1/stream/viewer-a/") {
			t.Fatalf("%s: status %d body:\n%s", name, rr.Code, body)
		}
	}
	if got := strings.Join(renditions, ","); got != "v720,aac0,v720_vtt" {
		t.Fatalf("retimed playlists %s, want v720,aac0,v720_vtt", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd server && go test ./internal/transcode/ ./internal/stream/ ./internal/api/ -run 'TestLayoutRenditions|TestCachedBytesAreNotFresh|TestSessionTimelineGatesRestartedRuns|TestThirdGateStallEndsSessionAndFreesTuner|TestOpenThenStalledRunsUseUpTheBudget|TestOutageAndCachedJoinNeverExpire|TestContinuousFeedingResetsTheBudget|TestNoSignalAttachRetriesDontUseTheBudget|TestMediaPlaylistServedThroughTimeline' -v`
Expected: FAIL to compile (`media.Layout.Renditions undefined`, `s.FreshBytes undefined`, `q.popChunk undefined`, `m.timelineOf undefined`).

- [ ] **Step 3: Implement**

In `transcode/layout.go`, after `CaptionPlaylist`:

```go
// Renditions are the media playlist names (without .m3u8) FFmpeg writes for
// l: every rung, then the AAC and AC-3 renditions (VarStreamMap's names).
// Caption playlists are not renditions; they share their rung's sequence.
func (l Layout) Renditions() []string {
	var out []string
	for _, r := range l.rungs() {
		out = append(out, rungName(r))
	}
	for i := range l.AACTracks() {
		out = append(out, fmt.Sprintf("aac%d", i))
	}
	for i := range l.AC3Tracks() {
		out = append(out, fmt.Sprintf("ac3%d", i))
	}
	return out
}
```

These are the same loops `VarStreamMap` uses to name the variants. `AC3Tracks()` is empty unless `AC3Copy` is set.

In `stream/session.go`, add to `session` after `hlsListSize`:

```go
	timeline       *Timeline // server-side observer: wall-clock stamps, discontinuity epochs
	timelineCancel func()    // stops the observer (teardown)
	// Gate liveness (maintain): the fresh source bytes this process's input
	// had at the last tick, and when that tick was (the process start
	// before the first).
	lastFresh uint64
	lastTick  time.Time
```

In `stream/subqueue.go` (as Task 7 left it), add after `gen`:

```go
	// fresh counts written bytes that came from the live source. Cached
	// copies (the join snapshot, a reset head, the PAT+PMT put back after
	// an overflow) are written but not fresh. They only ever enter at the
	// front of the queue, so cachedFront counts them there.
	fresh       uint64
	cachedFront int
```

- In `push`'s overflow drop loop, after `dropped += len(q.chunks[0])`:

```go
			if q.cachedFront > 0 {
				q.cachedFront-- // the dropped front chunk was a cached copy
			}
```

  and after the PAT+PMT is put back (`q.bytes += len(tables)`), add `q.cachedFront++`.

- Replace `pop` with:

```go
// pop blocks until a chunk is available; ok is false once the queue is closed.
func (q *subQueue) pop() ([]byte, bool) {
	c, _, ok := q.popChunk()
	return c, ok
}

// popChunk is pop, and whether the chunk is a cached copy (a reset head or
// PAT+PMT put back after an overflow) rather than live source bytes.
func (q *subQueue) popChunk() (c []byte, cached, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.chunks) == 0 && !q.closed {
		q.cond.Wait()
	}
	if len(q.chunks) == 0 {
		return nil, false, false
	}
	c = q.chunks[0]
	q.chunks[0] = nil
	q.chunks = q.chunks[1:]
	q.bytes -= len(c)
	q.inFlight += len(c)
	if q.cachedFront > 0 {
		q.cachedFront--
		cached = true
	}
	return c, cached, true
}
```

- In `reset`, set `q.cachedFront = 0` where the backlog is cleared, and `q.cachedFront = 1` where `head` is queued (a cached copy). In `close`, set `q.cachedFront = 0`.

- Add after `wrote`:

```go
// wroteFrom is wrote, also counting the bytes as fresh unless they came
// from a cached copy.
func (q *subQueue) wroteFrom(n int, cached bool) {
	q.wrote(n)
	if !cached {
		q.mu.Lock()
		q.fresh += uint64(n)
		q.mu.Unlock()
	}
}

// freshBytes is the live source bytes written to the reader's pipe.
func (q *subQueue) freshBytes() uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.fresh
}
```

In `stream/ingest.go`, give `writeChunk` a `cached bool` parameter and record progress with `s.q.wroteFrom(n, cached)` instead of `s.q.wrote(n)`. In `drain`, write the join with `s.writeChunk(join, true)` (the join is a cached copy), and pop with `chunk, cached, ok := s.q.popChunk()` and `s.writeChunk(chunk, cached)`. Add after `OnForceClose`:

```go
// FreshBytes is how many live source bytes have reached this subscriber's
// transcoder (Task 10's gate clock). The join snapshot, a reset head and
// PAT+PMT put back after an overflow are cached copies and don't count.
func (s *IngestSub) FreshBytes() uint64 { return s.q.freshBytes() }
```

In `stream/manager.go`:

- In `startAttempt`, right after the `waitPlaylist` error block and before `now := m.now()`:

```go
	// Real clock: stamps are compared with segment file mtimes. One
	// synchronous pass so the first playlist request already has stamps.
	timeline := NewTimeline(dir, layout.Renditions(), time.Now)
	timeline.Observe()
	tlCtx, tlCancel := context.WithCancel(context.Background())
```

  Add `timeline: timeline, timelineCancel: tlCancel, lastTick: now,` to the `sess := &session{…}` literal.

  On the duplicate-key abandon path (`if existing, ok := m.byKey[key]; …`), add `tlCancel()` next to `procCancel()`.

  Right before `m.wg.Add(1)` / `go m.supervise(sess)`:

```go
	go timeline.Run(tlCtx)
```

  If the `addViewerLocked` error path already tore the session down, `tlCtx` is cancelled, and `Run` returns at once.

- In `teardownSessionLocked`, after `sess.procCancel()`:

```go
	if sess.timelineCancel != nil {
		sess.timelineCancel()
	}
```

- In `maintain`, inside the `for _, sess := range sessions` loop, after the `emptySince` teardown check and before the crashed/restart check:

```go
		// Gate liveness: a run that publishes nothing for the gate timeout of
		// fed time is expired and FFmpeg restarted; the third failed run
		// without a healthy stretch ends the session.
		if !sess.crashed && !sess.restarting && sess.proc != nil && sess.timeline != nil {
			var fresh uint64
			if sess.sub != nil {
				fresh = sess.sub.FreshBytes()
			}
			moved := fresh != sess.lastFresh
			elapsed := now.Sub(sess.lastTick)
			sess.lastFresh, sess.lastTick = fresh, now
			if missing, stalls, expired := sess.timeline.ExpireStalledRun(elapsed, moved); expired {
				if stalls >= timelineMaxStalls {
					log.Printf("stream: session %s: %d failed runs in a row (last: no %s); ending the session",
						sess.id, stalls, strings.Join(missing, ","))
					m.teardownSessionLocked(sess)
					continue
				}
				log.Printf("stream: session %s: ffmpeg published no %s for %v of fed time; restarting it",
					sess.id, strings.Join(missing, ","), timelineGateTimeout)
				sess.proc.Stop()
			}
		}
```

  (`supervise` sees the exit, marks the session crashed, and a later `maintain` restarts it.) `teardownSessionLocked` is the admin `Terminate` path: FFmpeg stopped, ingest subscriptions closed (the device goes after ingest's tail), viewers removed. It returns at once for a session already terminated. Add `"strings"` to `manager.go`'s imports, which don't have it yet.

- At the very top of `restartSession`, before `m.ingest.Attach`:

```go
	// The old process is dead: record everything it wrote (a run that never
	// opened is discarded) and hold the next run until it is complete. A
	// run that ended without being expired (a crash) counts toward the
	// stall budget; a spent budget ends the session instead of restarting.
	if sess.timeline != nil {
		if stalls := sess.timeline.MarkRestart(); stalls >= timelineMaxStalls {
			log.Printf("stream: session %s: %d failed runs in a row; ending the session", sess.id, stalls)
			m.mu.Lock()
			sess.restarting = false
			m.teardownSessionLocked(sess)
			m.mu.Unlock()
			return
		}
	}
```

  In its commit block (after the `sess.terminated` check), next to `sess.procStart = m.now()`, add:

```go
	sess.lastFresh, sess.lastTick = 0, sess.procStart // a new sub: its counters start at 0
	if sess.timeline != nil {
		sess.timeline.ProcessStarted() // the next MarkRestart ends this process's run
	}
```

  An attempt that fails at `Attach` or `Start` never reaches the commit, so its `MarkRestart` was a no-op for the next attempt.

  The run's fed time starts at `MarkRestart` with nothing accrued. The time `Attach` and `Start` take is never charged: `maintain` skips a restarting session, and the first tick after the commit measures from the new process start.

- Add after `SessionMediaOf`:

```go
// timelineOf returns the viewer's session timeline, its top rendition and
// the viewer's user.
func (m *Manager) timelineOf(viewerID string) (tl *Timeline, top string, owner int64, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.viewers[viewerID]
	if !ok {
		return nil, "", 0, false
	}
	sess, ok := m.sessions[v.SessionID]
	if !ok || sess.terminated || sess.timeline == nil {
		return nil, "", 0, false
	}
	return sess.timeline, sess.layout.TopName(), v.UserID, true
}

// RetimePlaylist rewrites one rendition or caption playlist (FFmpeg's
// bytes, segment names still bare; playlist is its name without .m3u8)
// from the session observer's snapshot: Bowtie's PDT on every segment,
// DISCONTINUITY where a new epoch starts, DISCONTINUITY-SEQUENCE, and a
// MEDIA-SEQUENCE that never goes back. It never reads files. False when
// the viewer has no session.
func (m *Manager) RetimePlaylist(viewerID, playlist string, raw []byte) ([]byte, bool) {
	tl, _, _, ok := m.timelineOf(viewerID)
	if !ok {
		return nil, false
	}
	return tl.Retime(playlist, raw), true
}
```

In `api/stream_handlers.go`:

- Add to `StreamController`:

```go
	// RetimePlaylist rewrites a rendition or caption playlist (name without
	// .m3u8) from the session timeline (PDT, discontinuities); false when
	// the viewer has no session.
	RetimePlaylist(viewerID, playlist string, raw []byte) ([]byte, bool)
```

- In `serveMediaPlaylist`, after the caption-repair `if` block and before `rewritten := …`:

```go
	// Captions keep their own name: the timeline gives them the rung's
	// stamps and their own MEDIA-SEQUENCE floor.
	if retimed, ok := s.deps.Streams.RetimePlaylist(viewerID, strings.TrimSuffix(name, ".m3u8"), raw); ok {
		raw = retimed
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd server && go test ./internal/transcode/ ./internal/stream/ ./internal/api/ -race -count=1`
Expected: PASS. Existing manager tests that count goroutines or wait on `m.wg` must still pass. `Run` isn't in `m.wg`, and teardown and `shutdownAll` cancel it.

- [ ] **Step 5: Remove the `long-outage-restart` xfail and add guards**

Delete the `xfail:` and `xfailOn:` lines from `long-outage-restart.yaml`, and add to its `expect:` block:

```yaml
  discontinuitiesAligned: true
  pdtMonotonic: true
  pdtAligned: true
  maxAVStartSkewMs: 100
  pairedAfterEachFault: true
```

Add to `baseline.yaml`'s `expect:` block:

```yaml
  renditionsMonotonic: true
  discontinuitySequenceOK: true
  discontinuitiesAligned: true
  pdtMonotonic: true
  pdtAligned: true
```

Add to `transcoder-restart.yaml`'s `expect:` block:

```yaml
  discontinuitySequenceOK: true
  discontinuitiesAligned: true
  pdtMonotonic: true
  pdtAligned: true
  maxAVStartSkewMs: 100
  pairedAfterEachFault: true
```

Add to `production-baseline.yaml`'s `expect:` block:

```yaml
  discontinuitySequenceOK: true
  discontinuitiesAligned: true
  pdtMonotonic: true
  pdtAligned: true
```

A/V skew after a restart can be checked now. Task 3 pairs audio and video segments by PDT overlap within the same discontinuity number, and both are Bowtie's now. `pairedAfterEachFault` makes it fail if no pair was compared in a post-fault window.

Run: `cd server && go test -count=1 -tags ffmpeg -p 1 -parallel 2 -timeout 30m -run TestScenarios -v ./internal/e2e/ 2>&1 | grep -E -- '--- |xfail \(|known gap|consistency:'`
Expected: every scenario PASS, no `xfail (` lines left, no `consistency:` lines on passing scenarios.

Run: `cd server && BOWTIE_FFMPEG_PATH="$PWD/scripts/ffmpeg51" go test -count=1 -tags ffmpeg -p 1 -parallel 2 -timeout 60m -run 'TestScenarios/(long-outage-restart|transcoder-restart|baseline|production-baseline)$' -v ./internal/e2e/ 2>&1 | grep -E -- '--- |consistency:|timeline .*(discarded|never served|unobserved|expired)|restarting it|ending the session'`
Expected:
- PASS on FFmpeg 5.1.
- 5.1's spurious top DISCONTINUITY doesn't show up as a boundary.
- Explain any `discarded` / `never served` / `unobserved` / `expired` / `restarting it` / `ending the session` log line in the commit message. For `long-outage-restart`, `discarded` is possible: the kill lands during the outage. It must show no `expired` line: the restarted FFmpeg is fed only the cached join until the signal returns, which isn't fed time. Expect none of these lines in the other scenarios.

- [ ] **Step 6: Commit**

```bash
git add server/internal/transcode/layout.go server/internal/transcode/layout_test.go server/internal/stream/subqueue.go server/internal/stream/session.go server/internal/stream/manager.go server/internal/stream/ingest.go server/internal/stream/ingest_test.go server/internal/stream/manager_test.go server/internal/api/stream_handlers.go server/internal/api/stream_handlers_test.go server/internal/e2e/testdata/scenarios
git commit -m "feat(stream): per-session timeline observer with gated restarts; serve Bowtie's timeline

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 11: Timeline endpoint for clients without PDT

**Files:**
- Modify: `server/internal/stream/manager.go` (`TimelineOf`)
- Test: `server/internal/stream/manager_test.go`
- Modify: `server/internal/api/stream_handlers.go` (interface, handler), `server/internal/api/server.go` (route)
- Test: `server/internal/api/stream_handlers_test.go`
- Modify: `docs/api/openapi.yaml`

**Interfaces:**
- Consumes: `(*Manager).timelineOf` (Task 10), `(*Timeline).Window`, `WindowTimeline`, `TimelineSegment`, `WallClockLayout` (Task 9).
- Produces:
  - `func (m *Manager) ViewerOwner(viewerID string) (int64, bool)` is the viewer's user ID. It reports false when the viewer has no live session. It's cheap, and authorization runs on it **before** any timeline is built.
  - `func (m *Manager) TimelineOf(viewerID string) (WindowTimeline, bool)` is the observer's snapshot of the session's top rendition window. It never reads a file.
  - `api.StreamController` gains both.
  - `GET /api/v1/sessions/{viewerId}/timeline` returns 200 with `{"mediaSequence": n, "windowStart": "<RFC 3339 ms>"|null, "liveEdge": "<RFC 3339 ms>"|null, "segments": [["<start>", <durationSec>], …]}`.
  - Access is **owner Bearer only** (spec:125). The route sits behind `auth.RequireUser`, so a request without a valid Bearer token gets that middleware's 401 before any viewer lookup. A stream token (`?token=`) doesn't qualify.
  - A signed-in user who doesn't own the viewer gets **404** (`session not found`), whether or not they're an admin. That's the same answer as a missing viewer, so viewer IDs can't be probed.
  - It doesn't touch the viewer; playlist polls keep it alive.
  - Roku's API client already sends Bearer on its signed-in calls, recordings for example (`roku/test/client-recordings.test.mjs`). Its heartbeat sends only the stream token. The Part 2 Roku client must call this endpoint with Bearer.

- [ ] **Step 1: Write the failing tests**

Append to `server/internal/stream/manager_test.go`:

```go
func TestTimelineOfReportsOwnerAndObservedWindow(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	m := newTestManager(st, cfg, clock, runner)
	h, err := m.Start(context.Background(), user, chID, clientCaps(""))
	if err != nil {
		t.Fatal(err)
	}
	if owner, ok := m.ViewerOwner(h.ViewerID); !ok || owner != user.ID {
		t.Fatalf("ViewerOwner = %d, %v", owner, ok)
	}
	media, _ := m.SessionMediaOf(h.ViewerID)
	top := media.Layout.TopName()
	pl := "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:3\n#EXTINF:4.0,\n" + top + "_00003.ts\n#EXTINF:4.0,\n" + top + "_00004.ts\n"
	if err := os.WriteFile(filepath.Join(h.SessionDir, top+".m3u8"), []byte(pl), 0o644); err != nil {
		t.Fatal(err)
	}
	// Real TS files: run 0 opens only once its first PTS is read (Task 9).
	for i, pts := range []float64{1.4, 5.4} {
		if err := os.WriteFile(filepath.Join(h.SessionDir, fmt.Sprintf("%s_%05d.ts", top, 3+i)), tsWithPTS(pts), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tl, _, _, _ := m.timelineOf(h.ViewerID)
	tl.Observe()
	win, ok := m.TimelineOf(h.ViewerID)
	// MediaSequence is Bowtie's own number (the served playlists'), not FFmpeg's 3.
	if !ok || win.MediaSequence != 0 || len(win.Segments) != 2 {
		t.Fatalf("TimelineOf = %+v ok=%v", win, ok)
	}
	if !win.LiveEdge().Equal(win.WindowStart().Add(8 * time.Second)) {
		t.Fatalf("window %v → %v, want 8 s", win.WindowStart(), win.LiveEdge())
	}
	if _, ok := m.TimelineOf("no-such-viewer"); ok {
		t.Fatal("unknown viewer has a timeline")
	}
	if _, ok := m.ViewerOwner("no-such-viewer"); ok {
		t.Fatal("unknown viewer has an owner")
	}
}
```

In `server/internal/api/stream_handlers_test.go`, add to `stubStreams`:

```go
	timelines     map[string]timelineStub // viewerID → window + owner
	timelineBuilt []string                // viewers whose timeline was built
```

and:

```go
type timelineStub struct {
	owner int64
	win   stream.WindowTimeline
}

func (s *stubStreams) ViewerOwner(viewerID string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tl, ok := s.timelines[viewerID]
	return tl.owner, ok
}

// TimelineOf records that a timeline was built, so the test can check that
// authorization ran first.
func (s *stubStreams) TimelineOf(viewerID string) (stream.WindowTimeline, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.timelineBuilt = append(s.timelineBuilt, viewerID)
	tl, ok := s.timelines[viewerID]
	return tl.win, ok
}
```

Append the test:

```go
// Only the owner's Bearer token sees the timeline. Another user (even an
// admin) gets the same 404 as a missing viewer; no Bearer (a stream token
// included) gets 401. No timeline is built for a refused request.
func TestSessionTimelineOwnerOnly(t *testing.T) {
	ss := newStubStreams()
	h, st, _ := testAPIWithStreams(t, ss)
	alice := seedUser(t, st, "alice", "pass-a", "viewer")
	seedUser(t, st, "bob", "pass-b", "admin")
	start := time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC)
	ss.timelines = map[string]timelineStub{"viewer-a": {owner: alice.ID, win: stream.WindowTimeline{
		MediaSequence: 7,
		Segments: []stream.TimelineSegment{
			{Start: start, Duration: 4.004},
			{Start: start.Add(4004 * time.Millisecond), Duration: 4.004},
		},
	}}}
	login := func(user, pass string) string {
		rr := doJSON(t, h, "POST", "/api/v1/auth/login", map[string]string{"username": user, "password": pass}, nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("login %s: %d", user, rr.Code)
		}
		return decodeLogin(t, rr).AccessToken
	}
	aliceTok, bobTok := login("alice", "pass-a"), login("bob", "pass-b")
	const path = "/api/v1/sessions/viewer-a/timeline"
	viewerTok := stream.SignStreamToken([]byte(streamSecret), "viewer-a", time.Now().UTC().Add(time.Hour))

	for name, req := range map[string]struct {
		path string
		hdr  map[string]string
		want int
	}{
		"another user's bearer (admin)": {path, map[string]string{"Authorization": "Bearer " + bobTok}, http.StatusNotFound},
		"owner, unknown viewer":         {"/api/v1/sessions/viewer-x/timeline", map[string]string{"Authorization": "Bearer " + aliceTok}, http.StatusNotFound},
		"viewer's own stream token":     {path + "?token=" + viewerTok, nil, http.StatusUnauthorized},
		"no auth":                       {path, nil, http.StatusUnauthorized},
		"garbage bearer":                {path, map[string]string{"Authorization": "Bearer nope"}, http.StatusUnauthorized},
	} {
		if rr := doJSON(t, h, "GET", req.path, nil, req.hdr); rr.Code != req.want {
			t.Errorf("%s: status %d, want %d", name, rr.Code, req.want)
		}
	}
	if len(ss.timelineBuilt) != 0 {
		t.Fatalf("timelines built for refused requests: %v", ss.timelineBuilt)
	}

	rr := doJSON(t, h, "GET", path, nil, map[string]string{"Authorization": "Bearer " + aliceTok})
	if rr.Code != http.StatusOK {
		t.Fatalf("owner: status %d body %s", rr.Code, rr.Body.String())
	}
	var body struct {
		MediaSequence int64   `json:"mediaSequence"`
		WindowStart   string  `json:"windowStart"`
		LiveEdge      string  `json:"liveEdge"`
		Segments      [][]any `json:"segments"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.MediaSequence != 7 || body.WindowStart != "2026-10-11T20:00:00.000Z" || body.LiveEdge != "2026-10-11T20:00:08.008Z" ||
		len(body.Segments) != 2 || body.Segments[1][0] != "2026-10-11T20:00:04.004Z" || body.Segments[1][1] != 4.004 {
		t.Fatalf("body %+v", body)
	}
}
```

Add `"encoding/json"` to the test file's imports if it isn't there.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd server && go test ./internal/stream/ ./internal/api/ -run 'TestTimelineOf|TestSessionTimelineOwnerOnly' -v`
Expected: FAIL to compile (`m.ViewerOwner undefined`).

- [ ] **Step 3: Implement**

In `stream/manager.go`, after `RetimePlaylist`:

```go
// ViewerOwner is the viewer's user ID; false when the viewer has no live
// session. Callers authorize on it before asking for anything else.
func (m *Manager) ViewerOwner(viewerID string) (int64, bool) {
	_, _, owner, ok := m.timelineOf(viewerID)
	return owner, ok
}

// TimelineOf is the observer's snapshot of the viewer's window (top
// rendition) on the wall clock. It reads no files. False when the viewer
// has no session.
func (m *Manager) TimelineOf(viewerID string) (WindowTimeline, bool) {
	tl, top, _, ok := m.timelineOf(viewerID)
	if !ok {
		return WindowTimeline{}, false
	}
	return tl.Window(top), true
}
```

In `api/stream_handlers.go`, add to `StreamController`:

```go
	// ViewerOwner is the viewer's user ID; false when the viewer has no session.
	ViewerOwner(viewerID string) (int64, bool)
	// TimelineOf is the viewer's window on the wall clock (the observer's
	// snapshot); false when the viewer has no session.
	TimelineOf(viewerID string) (stream.WindowTimeline, bool)
```

Add the handler after `handleHeartbeat`:

```go
// handleSessionTimeline maps the viewer's current window to wall-clock time
// for clients that can't read EXT-X-PROGRAM-DATE-TIME (Roku). The route
// requires a Bearer token (auth.RequireUser); only the user who owns the
// viewer sees it, and anyone else gets the same 404 as a missing viewer.
// Authorization runs before any timeline is built.
func (s *Server) handleSessionTimeline(w http.ResponseWriter, r *http.Request) {
	viewerID := r.PathValue("viewerId")
	if s.deps.Streams == nil {
		writeError(w, http.StatusServiceUnavailable, "streaming not available")
		return
	}
	claims, ok := auth.ClaimsFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	owner, ok := s.deps.Streams.ViewerOwner(viewerID)
	if !ok || owner != claims.UserID {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	win, ok := s.deps.Streams.TimelineOf(viewerID)
	if !ok {
		writeError(w, http.StatusNotFound, "session not found")
		return
	}
	segs := make([][2]any, 0, len(win.Segments))
	for _, sg := range win.Segments {
		segs = append(segs, [2]any{sg.Start.UTC().Format(stream.WallClockLayout), sg.Duration})
	}
	body := map[string]any{"mediaSequence": win.MediaSequence, "windowStart": nil, "liveEdge": nil, "segments": segs}
	if len(win.Segments) > 0 {
		body["windowStart"] = win.WindowStart().UTC().Format(stream.WallClockLayout)
		body["liveEdge"] = win.LiveEdge().UTC().Format(stream.WallClockLayout)
	}
	writeJSON(w, http.StatusOK, body)
}
```

In `api/server.go`, after the heartbeat route:

```go
	handle("GET /api/v1/sessions/{viewerId}/timeline", auth.RequireUser(s.deps.Auth)(http.HandlerFunc(s.handleSessionTimeline)))
```

`docs/api/openapi.yaml`: insert this path item right after the `/api/v1/sessions/{viewerId}/heartbeat:` path item (before the next top-level path key):

```yaml
  /api/v1/sessions/{viewerId}/timeline:
    parameters:
      - name: viewerId
        in: path
        required: true
        schema:
          type: string
    get:
      summary: Wall-clock map of the viewer's current window
      description: |
        Maps the session's playlist window to wall-clock time for clients
        that can't read EXT-X-PROGRAM-DATE-TIME (Roku): a client turns a
        broadcast moment T into an offset into the window. Times are RFC 3339
        with milliseconds (UTC), the same values the playlists' PDT carry.
        Requires the Bearer token of the user who owns the viewer (a
        stream token does not qualify). Any other signed-in user, admins
        included, gets the same 404 as a missing viewer.
      operationId: getSessionTimeline
      tags: [stream]
      security:
        - bearerAuth: []
      responses:
        '200':
          description: The current window
          content:
            application/json:
              schema:
                type: object
                required: [mediaSequence, windowStart, liveEdge, segments]
                properties:
                  mediaSequence:
                    type: integer
                    format: int64
                    description: Media sequence number of the first segment, as the served playlists number it (Bowtie's numbering, not the transcoder's).
                  windowStart:
                    type: string
                    format: date-time
                    nullable: true
                    description: Wall-clock start of the first segment (null while the window is empty).
                  liveEdge:
                    type: string
                    format: date-time
                    nullable: true
                    description: Wall-clock end of the last segment (null while the window is empty).
                  segments:
                    type: array
                    description: |
                      One `[startWallClock, durationSec]` pair per segment in
                      playlist order. An outage shows as a start later than
                      the previous segment's end.
                    items:
                      type: array
                      minItems: 2
                      maxItems: 2
                      items: {}
        '401':
          description: Missing or invalid Bearer token.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Error'
        '404':
          description: No such viewer, or the caller doesn't own it.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Error'
        '503':
          description: Streaming not available.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Error'
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd server && go test ./internal/stream/ ./internal/api/ -race -count=1`
Expected: PASS, including `TestOpenAPICoversRoutes`.

- [ ] **Step 5: Commit**

```bash
git add server/internal/stream/manager.go server/internal/stream/manager_test.go server/internal/api docs/api/openapi.yaml
git commit -m "feat(api): owner-only session timeline endpoint for clients without PDT

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 12: Caption `.vtt` sweeper

**Files:**
- Create: `server/internal/stream/captions_sweep.go`
- Test: `server/internal/stream/captions_sweep_test.go`
- Modify: `server/internal/stream/manager.go` (`maintain`)

**Interfaces:**
- Consumes: `ParseMediaPlaylist` and `hlsSegmentDuration` (Task 9; don't redefine it), `transcode.Layout.CaptionPlaylist()`/`TopName()`, `transcode.DefaultHLSListSize`.
- Produces: `func sweepCaptions(dir string, l transcode.Layout, listSize int, now time.Time) int`. It deletes `.vtt` files that the caption playlist doesn't list and that are older than (window + 1 segment) × 4 s, and returns how many it removed. `maintain` runs it outside `m.mu`, with the real clock, for every live session with captions.
- Listed names are derived from MEDIA-SEQUENCE (`<top><seq>.vtt`). 5.1's `.` entries after an append restart still protect the right files.

- [ ] **Step 1: Write the failing tests**

Create `server/internal/stream/captions_sweep_test.go`:

```go
package stream

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

func TestSweepCaptionsRemovesOldUnlistedVTT(t *testing.T) {
	dir := t.TempDir()
	l := transcode.Layout{Rungs: []transcode.Rung{{Height: 480, VideoKbps: 1500}}, Captions: true}
	now := time.Now()
	write := func(name, body string, age time.Duration) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	// FFmpeg 5.1 after an append restart: "." entries; names come from the sequence.
	write("v480_vtt.m3u8", "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:5\n#EXTINF:4.0,\n.\n#EXTINF:4.0,\nv4806.vtt\n", 0)
	write("v4800.vtt", "WEBVTT\n", time.Minute)       // old, unlisted: removed
	write("v4805.vtt", "WEBVTT\n", time.Minute)       // old but listed (as "."): kept
	write("v4807.vtt", "WEBVTT\n", 2*time.Second)     // new, not listed yet: kept
	write("v480_00000.ts", "TS", time.Minute)          // not a caption: kept
	if n := sweepCaptions(dir, l, 3, now); n != 1 {
		t.Fatalf("removed %d files, want 1", n)
	}
	for name, want := range map[string]bool{"v4800.vtt": false, "v4805.vtt": true, "v4807.vtt": true, "v480_00000.ts": true} {
		if _, err := os.Stat(filepath.Join(dir, name)); (err == nil) != want {
			t.Errorf("%s exists=%v, want %v", name, err == nil, want)
		}
	}
	l.Captions = false
	write("v4801.vtt", "WEBVTT\n", time.Minute)
	if n := sweepCaptions(dir, l, 3, now); n != 0 {
		t.Fatalf("swept %d files of a session without captions", n)
	}
}

func TestMaintainSweepsStaleCaptions(t *testing.T) {
	m, _, _, chID := newMultitrackManager(t, false)
	h, err := m.Start(context.Background(), viewer("alice", ""), chID, clientCaps(""))
	if err != nil {
		t.Fatal(err)
	}
	media, _ := m.SessionMediaOf(h.ViewerID)
	if !media.Layout.Captions {
		t.Fatal("multitrack session has no captions")
	}
	stale := filepath.Join(h.SessionDir, media.Layout.TopName()+"0.vtt")
	if err := os.WriteFile(stale, []byte("WEBVTT\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	m.maintain()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale caption segment still there (err=%v)", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd server && go test ./internal/stream/ -run 'TestSweepCaptions|TestMaintainSweepsStaleCaptions' -v`
Expected: FAIL to compile (`undefined: sweepCaptions`).

- [ ] **Step 3: Implement**

Create `server/internal/stream/captions_sweep.go`:

```go
package stream

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// sweepCaptions deletes caption segments FFmpeg left behind: .vtt files the
// caption playlist no longer lists that are older than the window plus one
// segment. (FFmpeg 5.1 loses track of them after an append restart: its
// playlist lists "." and delete_segments then fails on the directory.)
// It returns how many files it removed.
func sweepCaptions(dir string, l transcode.Layout, listSize int, now time.Time) int {
	if !l.Captions {
		return 0
	}
	if listSize <= 0 {
		listSize = transcode.DefaultHLSListSize
	}
	keep := map[string]bool{}
	if raw, err := os.ReadFile(filepath.Join(dir, l.CaptionPlaylist())); err == nil {
		p := ParseMediaPlaylist(raw)
		for i, sg := range p.Segments {
			keep[fmt.Sprintf("%s%d.vtt", l.TopName(), p.MediaSequence+int64(i))] = true
			keep[sg.URI] = true
		}
	}
	maxAge := time.Duration(listSize+1) * hlsSegmentDuration
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".vtt") || keep[name] {
			continue
		}
		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) <= maxAge {
			continue
		}
		if os.Remove(filepath.Join(dir, name)) == nil {
			n++
		}
	}
	return n
}
```

`stream/manager.go` `maintain`:
- Before the `for _, sess := range sessions {` loop, declare:

```go
	type captionSweep struct {
		dir      string
		layout   transcode.Layout
		listSize int
	}
	var sweeps []captionSweep
```

- In that loop, right after the empty-session teardown `if` block (after its closing brace), add:

```go
		if sess.layout.Captions {
			sweeps = append(sweeps, captionSweep{sess.dir, sess.layout, sess.hlsListSize})
		}
```

- After the `for _, sess := range due { m.restartSession(sess) }` loop at the end:

```go
	// File work runs without m.mu. Real clock: compared with file mtimes.
	for _, s := range sweeps {
		if n := sweepCaptions(s.dir, s.layout, s.listSize, time.Now()); n > 0 {
			log.Printf("stream: removed %d stale caption segments in %s", n, filepath.Base(s.dir))
		}
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd server && go test ./internal/stream/ -race -count=1`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/stream/captions_sweep.go server/internal/stream/captions_sweep_test.go server/internal/stream/manager.go
git commit -m "fix(stream): sweep caption segments FFmpeg 5.1 leaves behind after a restart

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 13: One-hour real-broadcast soak (confirms the Task 6 gate)

**Files:**
- Create: `server/internal/e2e/realsoak_test.go`
- Uses: `server/scripts/ffmpeg-soak` (Task 6), `rebasesSince` (Task 6)

**Interfaces:**
- Consumes:
  - testplayer: `AllRenditions`, `Probe`, `Report`, `Consistency`, `OnSegment`/`SegmentEvent`, `Mark`, `FetchErrors`, `TailGap` (Task 3); `UnpairedAfterFaults` (Task 4).
  - `FFprobe` and `ffprobePath` (Task 4).
  - `rebasesSince` and the `FF51_LOGDIR` log format (Task 6).
- Produces `TestRealSoak` (build tags `ffmpeg && realsoak`). It watches a running Bowtie and reads everything from the environment, so nothing in the code names a host:
  - `BOWTIE_SOAK_URL`, `BOWTIE_SOAK_USER`, `BOWTIE_SOAK_PASS`, `BOWTIE_SOAK_CHANNEL`;
  - `BOWTIE_SOAK_FOR`, default `1h`;
  - `BOWTIE_SOAK_DEVICE`, optional; it gets `$HDHR_ADDR`;
  - `BOWTIE_SOAK_SERVER_LOG`, the server's log as `docker logs -t` writes it;
  - `FF51_LOGDIR`, the host side of the container's re-base log directory.
- Pass criteria, over a clean hour that includes ad breaks. The test asserts each one:
  - zero `restarting ffmpeg` and zero `transcoder stuck` lines in the server log during the soak;
  - |timeline excess| ≤ 2 s;
  - max audio hole ≤ 50 ms, in every audio rendition;
  - max A/V start skew ≤ 100 ms;
  - every consistency flag true;
  - no backward jumps and no session loss;
  - `FetchErrors == 0`: every playlist, segment and probe failure in any rendition counts;
  - every listed rendition delivered segments, and each one's `TailGap` is ≤ 12 s at the end. This is progress through the end of observation, per rendition;
  - A/V alignment was measured throughout. The test calls `Mark` at the start and every 5 minutes, and `UnpairedAfterFaults` must be empty: in every 5-minute window, every audio and video rendition, and every discontinuity number it delivered there, has PDT-paired segments (`AVPairs > 0` overall);
  - zero **false re-bases**. A "timestamp discontinuity" line is false when, within the 60 s after it, a probed segment shows any of:
    - an audio hole over 50 ms;
    - an A/V skew over 100 ms;
    - a timeline excess more than 1 s away from its value at the re-base.

    Lines with none of these are true splice re-bases. They're counted and kept.
- It also measures, and logs without asserting:
  - re-bases per minute;
  - each re-base's offset from the start, with its verdict.
- On failure the timestamp option goes back to Task 6. Task 13 never changes the option itself.

- [ ] **Step 1: Write the soak test**

Create `server/internal/e2e/realsoak_test.go`:

```go
//go:build ffmpeg && realsoak

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/testplayer"
)

// TestRealSoak watches one real channel on a running Bowtie with the virtual
// player following every rendition, and fails on what a viewer would notice
// and on any restart, stuck kill or false timestamp re-base.
//
//	BOWTIE_SOAK_URL         Bowtie base URL
//	BOWTIE_SOAK_USER        admin username
//	BOWTIE_SOAK_PASS        admin password
//	BOWTIE_SOAK_CHANNEL     guide number
//	BOWTIE_SOAK_FOR         duration (default 1h)
//	BOWTIE_SOAK_DEVICE      HDHomeRun address to add first (optional)
//	BOWTIE_SOAK_SERVER_LOG  file that `docker logs -t -f` is writing
//	FF51_LOGDIR             re-base logs written by scripts/ffmpeg-soak
func TestRealSoak(t *testing.T) {
	need := func(k string) string {
		v := os.Getenv(k)
		if v == "" {
			t.Skipf("%s not set", k)
		}
		return v
	}
	base, user, pass, guide := need("BOWTIE_SOAK_URL"), need("BOWTIE_SOAK_USER"), need("BOWTIE_SOAK_PASS"), need("BOWTIE_SOAK_CHANNEL")
	serverLog := need("BOWTIE_SOAK_SERVER_LOG")
	need("FF51_LOGDIR")
	dur := time.Hour
	if v := os.Getenv("BOWTIE_SOAK_FOR"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatal(err)
		}
		dur = d
	}

	c := &soakClient{t: t, base: base}
	var login struct {
		AccessToken string `json:"accessToken"`
	}
	c.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"username": user, "password": pass}, &login, false)
	c.token = login.AccessToken
	if dev := os.Getenv("BOWTIE_SOAK_DEVICE"); dev != "" {
		c.do(http.MethodPost, "/api/v1/admin/devices", map[string]string{"ip": dev}, nil, true) // may already exist
	}
	var chans []struct {
		ID          int64  `json:"id"`
		GuideNumber string `json:"guideNumber"`
	}
	c.do(http.MethodGet, "/api/v1/admin/channels", nil, &chans, false)
	var id int64
	for _, ch := range chans {
		if ch.GuideNumber == guide {
			id = ch.ID
		}
	}
	if id == 0 {
		t.Fatalf("channel %s not in the lineup", guide)
	}
	c.do(http.MethodPatch, fmt.Sprintf("/api/v1/admin/channels/%d", id), map[string]bool{"enabled": true}, nil, false)

	var mu sync.Mutex
	var events []testplayer.SegmentEvent
	start := time.Now()
	p, err := testplayer.Start(context.Background(), testplayer.Config{
		BaseURL: base, Token: c.token, ChannelID: id, AllRenditions: true, Probe: FFprobe(ffprobePath()),
		// AC-3 too, so the copy rendition (when the channel has AC-3) is soaked.
		Caps: map[string]any{"videoCodecs": []string{"h264"}, "audioCodecs": []string{"aac", "ac3"}, "maxHeight": 1080},
		OnSegment: func(e testplayer.SegmentEvent) {
			mu.Lock()
			events = append(events, e)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Window 0 is empty: coverage is checked from the first 5-minute window on.
	p.Mark()
	ctx, cancel := context.WithTimeout(context.Background(), dur)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // progress every 5 minutes, wall clock to line up with the logs
		defer wg.Done()
		tick := time.NewTicker(5 * time.Minute)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-tick.C:
				if dl, _ := ctx.Deadline(); time.Until(dl) > time.Minute {
					p.Mark() // each 5-minute window must show A/V-paired coverage; no stub window at the end
				}
				r := p.Report()
				t.Logf("%s segments=%d excess=%.2fs audioHole=%v avSkew=%v (%d pairs) backward=%d fetchErrors=%d tailGap=%v",
					now.UTC().Format(time.RFC3339), r.SegmentsFetched, r.TimelineExcess, r.MaxAudioHole, r.MaxAVStartSkew, r.AVPairs,
					r.BackwardJumps, r.FetchErrors, r.TailGap)
			}
		}
	}()
	p.Run(ctx)
	wg.Wait()
	r := p.Report()
	p.Stop(context.Background())
	end := time.Now()

	t.Logf("renditions=%v segments=%d fetchErrors=%d (segments %d, playlists %d, probes %d) excess=%.2fs audioHoleBy=%v avSkew=%v (%d pairs) probed=%d/%d maxGap=%v tailGap=%v cues=%d (%d mistimed)",
		r.Renditions, r.SegmentsFetched, r.FetchErrors, r.SegmentErrors, r.PlaylistErrors, r.ProbeErrors, r.TimelineExcess, r.AudioHoleBy,
		r.MaxAVStartSkew, r.AVPairs, r.ProbedAudio, r.ProbedVideo, r.MaxGap, r.TailGap, r.Cues, r.CueErrors)
	for _, pr := range r.Consistency.Problems {
		t.Logf("consistency: %s", pr)
	}
	for _, e := range r.Errors {
		t.Logf("player error: %s", e)
	}
	cs := r.Consistency
	switch {
	case r.SessionGone || r.EndList:
		t.Error("session ended during the soak")
	case r.BackwardJumps > 0 || !cs.RenditionsMonotonic:
		t.Error("media sequence went backwards")
	}
	if !cs.PDTMonotonic || !cs.PDTAligned || !cs.DiscontinuitiesAligned || !cs.DiscontinuitySequenceOK {
		t.Errorf("timeline consistency: %+v", cs)
	}
	if r.TimelineExcess > 2 || r.TimelineExcess < -2 {
		t.Errorf("timeline excess %.2fs, want within ±2s", r.TimelineExcess)
	}
	if r.ProbedAudio == 0 || r.MaxAudioHole > 50*time.Millisecond {
		t.Errorf("audio hole %v (by rendition %v, probed %d), want ≤ 50ms", r.MaxAudioHole, r.AudioHoleBy, r.ProbedAudio)
	}
	if r.AVPairs == 0 || r.MaxAVStartSkew > 100*time.Millisecond {
		t.Errorf("A/V start skew %v over %d pairs, want ≤ 100ms over some", r.MaxAVStartSkew, r.AVPairs)
	}
	if r.FetchErrors > 0 {
		t.Errorf("fetch errors %d: playlists %d, segments %d, probes %d", r.FetchErrors, r.PlaylistErrors, r.SegmentErrors, r.ProbeErrors)
	}
	for _, name := range r.Renditions {
		if r.RenditionSegments[name] == 0 {
			t.Errorf("rendition %s delivered no segments", name)
		}
		if g := r.TailGap[name]; g > 12*time.Second {
			t.Errorf("rendition %s stopped %v before the end of the soak", name, g.Round(time.Second))
		}
	}
	for _, u := range r.UnpairedAfterFaults() {
		t.Errorf("coverage: %s", u)
	}

	for _, needle := range []string{"restarting ffmpeg", "transcoder stuck"} {
		for _, line := range serverLogLines(t, serverLog, start, end, needle) {
			t.Errorf("server log: %s", line)
		}
	}

	rebases := rebasesSince(t, start)
	t.Logf("re-bases: %d (%.2f per minute)", len(rebases), float64(len(rebases))/dur.Minutes())
	mu.Lock()
	defer mu.Unlock()
	for _, rb := range rebases {
		if why := falseRebase(start.Add(rb.At), events); why != "" {
			t.Errorf("false re-base at +%v (%s): %s", rb.At.Round(time.Second), why, rb.Line)
		} else {
			t.Logf("true re-base at +%v: %s", rb.At.Round(time.Second), rb.Line)
		}
	}
}

// falseRebase says why a re-base at `at` was harmful ("" when it wasn't):
// within the next 60 s, a probed segment with an audio hole over 50 ms, an
// A/V skew over 100 ms, or a timeline excess more than 1 s from its value
// at the re-base.
func falseRebase(at time.Time, events []testplayer.SegmentEvent) string {
	excessAt, haveExcess := 0.0, false
	for _, e := range events {
		if e.At.After(at) {
			break
		}
		excessAt, haveExcess = e.TimelineExcess, true
	}
	for _, e := range events {
		if e.At.Before(at) || e.At.After(at.Add(time.Minute)) {
			continue
		}
		switch {
		case e.AudioHole > 50*time.Millisecond:
			return fmt.Sprintf("%s segment %d: audio hole %v", e.Rendition, e.Seq, e.AudioHole)
		case e.AVSkew > 100*time.Millisecond:
			return fmt.Sprintf("%s segment %d: A/V skew %v", e.Rendition, e.Seq, e.AVSkew)
		case haveExcess && (e.TimelineExcess-excessAt > 1 || excessAt-e.TimelineExcess > 1):
			return fmt.Sprintf("timeline excess %.2fs → %.2fs", excessAt, e.TimelineExcess)
		}
	}
	return ""
}

// serverLogLines returns the lines of a `docker logs -t` file between start
// and end that contain needle (the first field is an RFC 3339 timestamp).
func serverLogLines(t *testing.T, path string, start, end time.Time, needle string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("server log: %v", err)
	}
	defer func() { _ = f.Close() }()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		ts, rest, ok := strings.Cut(sc.Text(), " ")
		at, err := time.Parse(time.RFC3339Nano, ts)
		if !ok || err != nil || at.Before(start) || at.After(end) || !strings.Contains(rest, needle) {
			continue
		}
		out = append(out, sc.Text())
	}
	return out
}

func TestFalseRebase(t *testing.T) {
	t0 := time.Unix(1_760_000_000, 0)
	ev := func(s float64, hole, skew time.Duration, excess float64) testplayer.SegmentEvent {
		return testplayer.SegmentEvent{At: t0.Add(time.Duration(s * float64(time.Second))), Rendition: "aac0", AudioHole: hole, AVSkew: skew, TimelineExcess: excess}
	}
	clean := []testplayer.SegmentEvent{ev(-4, 0, 0, 0.2), ev(4, 10*time.Millisecond, 20*time.Millisecond, 0.4), ev(50, 0, 0, 0.9)}
	if why := falseRebase(t0, clean); why != "" {
		t.Fatalf("clean splice judged false: %s", why)
	}
	for name, events := range map[string][]testplayer.SegmentEvent{
		"hole":   {ev(-4, 0, 0, 0), ev(8, 80*time.Millisecond, 0, 0)},
		"skew":   {ev(-4, 0, 0, 0), ev(8, 0, 150*time.Millisecond, 0)},
		"excess": {ev(-4, 0, 0, 0), ev(30, 0, 0, 1.6)},
	} {
		if falseRebase(t0, events) == "" {
			t.Errorf("%s: not judged false", name)
		}
	}
	if why := falseRebase(t0, []testplayer.SegmentEvent{ev(-4, 0, 0, 0), ev(70, 80*time.Millisecond, 0, 3)}); why != "" {
		t.Fatalf("an event after 60 s counted: %s", why)
	}
}

type soakClient struct {
	t     *testing.T
	base  string
	token string
}

func (c *soakClient) do(method, path string, body, out any, allowFail bool) {
	c.t.Helper()
	var rd bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = *bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, &rd)
	if err != nil {
		c.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode/100 != 2 {
		if allowFail {
			c.t.Logf("%s %s: HTTP %d (ignored)", method, path, resp.StatusCode)
			return
		}
		c.t.Fatalf("%s %s: HTTP %d", method, path, resp.StatusCode)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			c.t.Fatalf("%s %s: decode: %v", method, path, err)
		}
	}
}
```

`TestFalseRebase` sits in the same build-tagged file because it tests the soak's own rule.

Run: `cd server && go vet -tags ffmpeg,realsoak ./internal/e2e/ && go test -tags ffmpeg,realsoak -run 'TestRealSoak|TestFalseRebase' -v ./internal/e2e/`
Expected: vet is clean, `TestFalseRebase` PASSes, and `TestRealSoak` SKIPs (`BOWTIE_SOAK_URL not set`).

Commit:

```bash
git add server/internal/e2e/realsoak_test.go
git commit -m "test(e2e): real-broadcast soak with restart, stuck and false re-base checks

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

- [ ] **Step 2: Build and start the soak server (production image, FFmpeg 5.1, software encode)**

This runs on the dev Mac, never on the production server, and uses one tuner of the household HDHomeRun.
- Set `HDHR_ADDR` to its LAN address in your shell first. The address is passed only through env vars, never written into code.
- Guide download is off.

```bash
: "${HDHR_ADDR:?set HDHR_ADDR to the HDHomeRun's LAN address}"
cd "$(git rev-parse --show-toplevel)"
docker build -f deploy/Dockerfile -t bowtie:soak .
soak="${TMPDIR%/}/bowtie-soak"; rm -rf "$soak"; mkdir -p "$soak/data" "$soak/ff"
docker run -d --name bowtie-soak -p 8410:8400 -v "$soak/data:/data" -v "$soak/ff:/logs/ff" \
  -v "$PWD/server/scripts/ffmpeg-soak:/usr/local/bin/ffmpeg-soak:ro" \
  -e BOWTIE_HDHOMERUN_GUIDE=off -e BOWTIE_DEVICES="$HDHR_ADDR" \
  -e BOWTIE_FFMPEG_PATH=/usr/local/bin/ffmpeg-soak -e FF51_LOGDIR=/logs/ff bowtie:soak
docker logs -t -f bowtie-soak > "$soak/server.log" 2>&1 &
sleep 5; grep 'first run: created admin user' "$soak/server.log"
docker exec bowtie-soak /usr/bin/ffmpeg -version | head -1
```

Expected:
- The admin line prints the generated password.
- FFmpeg reports `5.1.9-0+deb12u1`.
- Before starting, make sure no one in the household needs more than the remaining tuners for the next hour.

The container and the Mac share one clock (Docker Desktop keeps the VM in sync), so the Unix-ms prefixes in `$soak/ff` line up with the test's start time.

- [ ] **Step 3: Run the soak (one hour, channel 9.1, one tuner)**

```bash
soak="${TMPDIR%/}/bowtie-soak"
cd server && \
BOWTIE_SOAK_URL=http://127.0.0.1:8410 BOWTIE_SOAK_USER=admin BOWTIE_SOAK_PASS='<password from Step 2>' \
BOWTIE_SOAK_CHANNEL=9.1 BOWTIE_SOAK_FOR=1h BOWTIE_SOAK_SERVER_LOG="$soak/server.log" FF51_LOGDIR="$soak/ff" \
go test -count=1 -tags ffmpeg,realsoak -run TestRealSoak -v -timeout 70m ./internal/e2e/ 2>&1 | tee "$soak/test.log"
```

If the container can't keep up (`transcoder behind` lines, or a growing max gap):
- turn off Streaming → Adaptive in the soak server's admin UI, which leaves one rung;
- run the soak again;
- say so in the report.

- [ ] **Step 4: Record the measurements**

```bash
soak="${TMPDIR%/}/bowtie-soak"; L="$soak/server.log"
grep -E -- '--- (PASS|FAIL)|re-bases:|(true|false) re-base at|renditions=|consistency:|server log:' "$soak/test.log"
echo "frames duplicated:        $(cat "$soak"/ff/*.log 2>/dev/null | grep -c 'frames duplicated')"
echo "dup! bursts (frames, max): $(cat "$soak"/ff/*.log 2>/dev/null | perl -ne '$n++, $s += $1, $m = $1 > $m ? $1 : $m if /\*\*\* (\d+) dup!/; END { printf "%d bursts, %d frames, max %d", $n, $s, $m }')"
echo "Invalid frame dimensions: $(cat "$soak"/ff/*.log 2>/dev/null | grep -c 'Invalid frame dimensions')"
echo "transcoder behind:        $(grep -c 'transcoder behind' "$L")"
echo "drained (read nothing):   $(grep -c 'transcoder read nothing' "$L")"
echo "timeline gaps:            $(grep -c 'stream: timeline .* gap before' "$L")"
echo "runs held/discarded:      $(grep -cE 'stream: timeline .*(discarded|never served|unobserved|expired)|restarting it|ending the session' "$L")"
echo "vtt files now:            $(docker exec bowtie-soak sh -c 'ls /data/segments/*/*.vtt 2>/dev/null | wc -l')"
```

The segment directory is `/data/segments` unless `BOWTIE_SEGMENT_DIR` says otherwise. Check with `docker exec bowtie-soak ls /data`.

- [ ] **Step 5: Decide**

- **Pass**: the test passed. That means zero false re-bases, zero restarts and zero `transcoder stuck` lines. Keep the Task 6 option, and record the re-bases per minute and the Step 4 counts in the commit below.
- **Any failure**: stop. Don't change the timestamp option or loosen a threshold here.
  - Report the failing lines, `$soak/test.log` and the `$soak/ff` logs.
  - The option goes back to Task 6's gate, with this hour's false re-bases added as evidence (their offsets and signal context).

Clean up: `kill %1 2>/dev/null; docker rm -f bowtie-soak`.

Record the result (no code change when it passed):

```bash
git commit --allow-empty -m "test: real-broadcast soak passed (channel 9.1, 1 h, FFmpeg 5.1)

<paste the Step 4 output>

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 14: Release notes and docs

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `docs/deploy/truenas.md` (GPU notes)

**Interfaces:**
- Consumes:
  - the timestamp option chosen by Task 6's gate and confirmed by Task 13;
  - Task 5's AC-3 outcome;
  - the log lines from Tasks 7 and 9.

- [ ] **Step 1: Write the changelog entry**

Add under the intro, above `## [0.23.0]`:

```markdown
## [Unreleased]

### Fixed

- **Live TV rides out bad reception without stutter or drift.** A damaged
  timestamp, a short signal dropout or a broadcaster's splice no longer
  freezes the picture on repeated frames or pulls the sound out of sync;
  missing audio is filled with silence so it stays in step with the video.
  The Dolby Digital (AC-3) track no longer drops out either.
  The Dolby Digital (AC-3) track is now re-encoded (384 kb/s) for live TV
  instead of copied, so its dropouts are filled too.
- **A slow transcoder is no longer restarted.** When FFmpeg stops reading
  for 30 seconds, Bowtie first drops its backlog and lets it catch up; it
  restarts FFmpeg only if it still reads nothing 30 seconds later.
- **Restarts pick up faster and cleaner.** A restarted transcoder (and the
  captions reader) starts at the last full video frame instead of mid-picture.
- **Players keep their place across a restart.** Every playlist carries
  Bowtie's own clock (program date-time) and marks a restart at the same
  moment in every quality and audio track, so players and SharePlay stay
  in step. A restarted transcoder's output is held back until every quality
  and audio track has started, so no player ever sees half a restart; if a
  track doesn't start within 15 seconds, Bowtie restarts the transcoder again.
- **Old caption files are cleaned up** after a transcoder restart.

### Added

- `GET /api/v1/sessions/{viewerId}/timeline` maps the current live window
  to wall-clock time, for players that can't read program date-time. It
  needs the signed-in owner's Bearer token; anyone else gets 404.

### Notes for Intel Quick Sync (QSV) servers

After updating, please watch the server log around ad breaks for:
- `transcoder stuck` (should no longer appear on a healthy encoder);
- `transcoder read nothing … dropped its … backlog` followed by
  `transcoder reading again` (a slow moment that recovered — expected);
- `stream: timeline … gap before` outside real reception problems;
- `restarting it` or `discarded before it was complete` (a restart
  that didn't produce every track in time, or a track that stopped
  while FFmpeg was being fed), and `ending the session` (three failed
  runs without a minute of healthy output between them: viewers
  reconnect to a fresh session).
FFmpeg 5.1 logs "timestamp discontinuity" only at debug level, so it
won't appear at Bowtie's normal log level.
```

Of the two AC-3 sentences, keep exactly one:
- the first if Task 5 skipped its Step 6, because input hardening alone kept the copy's holes at or under 50 ms;
- the second if Task 5 re-encoded AC-3, or if Task 6 chose candidate C (which re-encodes AC-3 to clamp it).

- [ ] **Step 2: Write the QSV note**

Append to the `## GPU notes (Intel iGPU)` section of `docs/deploy/truenas.md`:

```markdown
### What to watch after updating (stream resilience)

Bowtie now drains a transcoder that stops reading before it restarts it.
On QSV, check the app's log around ad breaks for a while after updating:

- `transcoder stuck for 1m0s, closing its input` means the encoder read
  nothing for a full minute and was restarted. Occasional lines point at
  the iGPU; frequent ones are worth an issue with the surrounding log.
- `transcoder read nothing for 30s; dropped its … backlog` followed by
  `transcoder reading again` is a slow moment that recovered on its own.
- FFmpeg 5.1 (the image's FFmpeg) logs "timestamp discontinuity" only at
  debug level, so you won't see those lines at the normal log level.
```

- [ ] **Step 3: Commit**

```bash
git add CHANGELOG.md docs/deploy/truenas.md
git commit -m "docs: stream resilience release notes and QSV watch list

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 15: Full verification and the restart discriminator

**Files:**
- Create: `server/internal/e2e/discriminator_test.go` (build tags `ffmpeg && discriminator`)
- Otherwise none. Fix anything that fails in the task that owns it, then re-run.

**Interfaces:**
- Consumes:
  - the harness (`New`, `Options.Production`, `Options.HLSListSize`, `adminUser`/`adminPass`), `CountingRunner`, `resolveSource`, `ffmpegPath`;
  - `scenario.Load` and `long-outage-restart.yaml`.
- Produces `TestRestartDiscriminator`, the spec's restart discriminator (spec:231).
  - It starts the e2e stack on the production layout with `long-outage-restart`'s channel, behind a same-origin proxy. The proxy also serves an hls.js page at `/discriminator`.
  - It prints the URL and the harness admin credentials, then loops the scenario's faults: the 65 s outage plus the kill at +30 s, once per cycle.
  - The page plays the channel and posts its position to the test about every 2 s and on every `waiting`/`playing`/`seeking`/`seeked` event. The test logs each report:
    - `currentTime`;
    - the seekable window start and end;
    - the program date-time being played.
  - The questions for each restart:
    - Once playback resumes, does the position land at the **window start**, at the **live end**, or near where it was?
    - Is the jump already gone?
  - The result is recorded either way. Part 2 is built regardless, because stalls without restarts still need it.

- [ ] **Step 1: Unit tests and vet (CI's first job)**

Run: `cd server && go test ./... && go vet ./...`
Expected: PASS, vet clean.

- [ ] **Step 2: Race run of the touched packages**

Run: `cd server && go test -race -count=1 ./internal/stream/ ./internal/api/ ./internal/testplayer/ ./internal/hdhr/... ./internal/transcode/`
Expected: PASS.

- [ ] **Step 3: e2e suite, as CI runs it (host FFmpeg)**

Run: `cd server && go test -tags ffmpeg -p 1 -parallel 2 -timeout 15m ./...`
Expected: PASS.
- Every scenario passes, with no `xfail` left: `grep -l xfail server/internal/e2e/testdata/scenarios/*.yaml` prints nothing.
- If 15 m is too short now, raise it in `.github/workflows/ci.yml` to the measured time plus 50%, and include that in the commit.

- [ ] **Step 4: e2e suite on FFmpeg 5.1 (wrapper)**

Run: `cd server && L="${TMPDIR%/}/verify-ff51" && rm -rf "$L" && FF51_LOGDIR="$L/ff" FF51_LOGLEVEL=level+debug BOWTIE_FFMPEG_PATH="$PWD/scripts/ffmpeg51" go test -count=1 -tags ffmpeg -p 1 -parallel 1 -timeout 90m -run TestScenarios -v ./internal/e2e/ 2>&1 | tee "$L.log" | grep -E -- '--- (PASS|FAIL)|rebase at'`
Expected:
- PASS.
- `rebase at` lines appear only in scenarios whose fault is a real step over the chosen threshold (`ptsjump*`, the dropouts), and **never** in `splice`, `baseline` or `production-baseline`.

- [ ] **Step 5: e2e on FFmpeg 5.1 inside the production image (production layout and OTA clips)**

Re-run Task 6 Step 4's image command once, for HEAD only:
- no scratch branch;
- `v=final`;
- the same `-test.run 'TestScenarios/(production-baseline|corrupt-audio)$|TestOTAClips'`;
- `BOWTIE_OTA_CLIPS` set to the clips chosen in Task 6.

Expected:
- every test PASSes;
- `audioHoleBy=` at or under 50 ms for every audio rendition, `ac3N` included;
- no false re-base under Task 6's clip rule.

If the clips aren't available on this machine anymore, run without them (`TestOTAClips` SKIPs) and say so in the report.

- [ ] **Step 6: Lint**

Run: `cd server && golangci-lint run ./... && golangci-lint run --build-tags ffmpeg,discriminator,realsoak ./internal/e2e/`
Expected: no findings. Also run `shellcheck server/scripts/ffmpeg51 server/scripts/ffmpeg-soak` if shellcheck is installed; don't install it.

- [ ] **Step 7: Write the restart discriminator**

Create `server/internal/e2e/discriminator_test.go`:

```go
//go:build ffmpeg && discriminator

package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake"
	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake/scenario"
	"github.com/ajthom90/bowtie/server/internal/hdhr/hdhrfake/tsloop"
	"github.com/ajthom90/bowtie/server/internal/stream"
)

// TestRestartDiscriminator serves the e2e stack (production layout) for a
// real player and loops long-outage-restart's faults: watch where the
// player's position lands after each FFmpeg restart.
//
//	BOWTIE_DISCRIMINATOR_CYCLES  fault cycles (default 3)
func TestRestartDiscriminator(t *testing.T) {
	sc, err := scenario.Load("testdata/scenarios/long-outage-restart.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cycles := 3
	if v := os.Getenv("BOWTIE_DISCRIMINATOR_CYCLES"); v != "" {
		if cycles, err = strconv.Atoi(v); err != nil {
			t.Fatal(err)
		}
	}
	count := &CountingRunner{Inner: &stream.FFmpegRunner{Path: ffmpegPath()}}
	h := New(t, Options{
		Fake: hdhrfake.Options{
			TunerCount: 2,
			Channels:   []hdhrfake.Channel{{GuideNumber: sc.Channel, Name: "FAKE " + sc.Name, Source: resolveSource(t, sc.Source)}},
		},
		Runner: count, FFmpegPath: ffmpegPath(), Production: true, HLSListSize: sc.WindowSegments,
	})

	target, err := url.Parse(h.Server.URL)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /discriminator", func(w http.ResponseWriter, r *http.Request) {
		page := strings.NewReplacer("{{USER}}", adminUser, "{{PASS}}", adminPass,
			"{{CHANNEL}}", strconv.FormatInt(h.Channels[sc.Channel], 10)).Replace(discriminatorPage)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, page)
	})
	mux.HandleFunc("POST /discriminator/report", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 4096))
		t.Logf("%s player: %s", time.Now().UTC().Format("15:04:05.000"), b)
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/", httputil.NewSingleHostReverseProxy(target))
	srv := httptest.NewServer(mux) // 127.0.0.1: the iOS Simulator shares the Mac's network
	defer srv.Close()
	t.Logf("Chrome (hls.js):             %s/discriminator?engine=hlsjs", srv.URL)
	t.Logf("iOS Simulator (AVFoundation): %s/discriminator?engine=native", srv.URL)
	t.Logf("Bowtie iOS app: server %s, user %s, password %s, channel %s", srv.URL, adminUser, adminPass, sc.Channel)

	deadline := time.Now().Add(5 * time.Minute)
	for len(h.Sessions(t)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no player started a session within 5 minutes")
		}
		time.Sleep(time.Second)
	}
	time.Sleep(20 * time.Second) // let the player settle at the live edge
	for i := 1; i <= cycles; i++ {
		start := time.Now().UTC()
		t.Logf("cycle %d at %s: outage at +10s for 65s, transcoder killed at +%v", i, start.Format("15:04:05.000"), sc.Bowtie.KillTranscoderAt)
		ctx, cancel := context.WithTimeout(context.Background(), sc.Duration)
		go func() {
			select {
			case <-ctx.Done():
			case <-time.After(sc.Bowtie.KillTranscoderAt):
				count.KillCurrent()
				t.Logf("cycle %d: transcoder killed at %s", i, time.Now().UTC().Format("15:04:05.000"))
			}
		}()
		if err := sc.Run(ctx, h.Fake, tsloop.RealClock()); err != nil && ctx.Err() == nil {
			t.Errorf("cycle %d: %v", i, err)
		}
		<-ctx.Done()
		cancel()
	}
	t.Logf("ffmpeg starts: %d", count.Starts())
}

// discriminatorPage plays the channel with hls.js (or the browser's own HLS
// for engine=native) and reports where playback sits in the window.
const discriminatorPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Restart discriminator</title>
<style>body{font:14px system-ui,sans-serif;margin:16px;background:#fff;color:#111}video{width:100%;max-width:640px;background:#000}pre{white-space:pre-wrap;font-size:12px}</style>
<script src="https://cdn.jsdelivr.net/npm/hls.js@1.5.15/dist/hls.min.js" integrity="sha384-oJR5PTkR0gvqJ/UnX1IkFQgxg6BByX7jPWmgSSY5dz0+Yc7O3mloo717zZpF/xpb" crossorigin="anonymous"></script>
</head><body>
<video id="v" controls playsinline muted autoplay></video>
<pre id="log"></pre>
<script>
const engine = new URLSearchParams(location.search).get("engine") === "native" ? "native" : "hlsjs";
const v = document.getElementById("v");
let hls = null;
function report(ev) {
  const s = v.seekable, n = s.length;
  const start = n ? s.start(0) : null, end = n ? s.end(n - 1) : null;
  let pdt = null;
  if (hls && hls.playingDate) pdt = hls.playingDate.toISOString();
  else if (v.getStartDate && !isNaN(v.getStartDate().getTime())) pdt = new Date(v.getStartDate().getTime() + v.currentTime * 1000).toISOString();
  const o = {engine, ev, cur: +v.currentTime.toFixed(2), start, end,
    fromStart: n ? +(v.currentTime - start).toFixed(2) : null, fromEnd: n ? +(end - v.currentTime).toFixed(2) : null, pdt};
  document.getElementById("log").textContent = JSON.stringify(o) + "\n" + document.getElementById("log").textContent.slice(0, 4000);
  fetch("/discriminator/report", {method: "POST", body: JSON.stringify(o)}).catch(() => {});
}
(async () => {
  const login = await (await fetch("/api/v1/auth/login", {method: "POST", headers: {"Content-Type": "application/json"},
    body: JSON.stringify({username: "{{USER}}", password: "{{PASS}}"})})).json();
  const sess = await (await fetch("/api/v1/sessions", {method: "POST",
    headers: {"Content-Type": "application/json", Authorization: "Bearer " + login.accessToken},
    body: JSON.stringify({channelId: {{CHANNEL}}, caps: {videoCodecs: ["h264"], audioCodecs: ["aac"], maxHeight: 1080}})})).json();
  if (engine === "hlsjs") {
    hls = new Hls();
    hls.on(Hls.Events.ERROR, (_, d) => report("error:" + d.details + (d.fatal ? ":fatal" : "")));
    hls.loadSource(sess.playlistUrl);
    hls.attachMedia(v);
  } else {
    v.src = sess.playlistUrl;
  }
  for (const ev of ["waiting", "playing", "seeking", "seeked", "stalled"]) v.addEventListener(ev, () => report(ev));
  setInterval(() => report("tick"), 2000);
})();
</script></body></html>`
```

The `integrity` value is the pinned file's SHA-384, computed while writing this plan. If the page refuses to load the script, recompute it: `echo "sha384-$(curl -fsSL https://cdn.jsdelivr.net/npm/hls.js@1.5.15/dist/hls.min.js | openssl dgst -sha384 -binary | base64)"`.

Run: `cd server && go vet -tags ffmpeg,discriminator ./internal/e2e/`
Expected: clean. The test isn't part of any CI run: it waits for a human-driven or Playwright-driven player.

- [ ] **Step 8: Run the discriminator (Chrome/hls.js and AVPlayer)**

Start it. It waits up to 5 minutes for a player:

```bash
cd server && go test -count=1 -tags ffmpeg,discriminator -run TestRestartDiscriminator -v -timeout 40m ./internal/e2e/ 2>&1 | tee "${TMPDIR%/}/discriminator-chrome.log"
```

Then:
- **Chrome/hls.js:** open the printed `?engine=hlsjs` URL with Playwright (Chromium) and keep the page open until the test ends.
- **AVPlayer:** run the test again, writing the log to `discriminator-ios.log`.
  - Boot the iOS Simulator.
  - Open the printed `?engine=native` URL: `xcrun simctl openurl booted '<url>'`. Safari on iOS plays HLS natively through AVFoundation.
  - Also sign in to the Bowtie iOS app on the same simulator with the printed server and credentials, using the local test recipe, and watch one cycle there. Its player is AVPlayer itself.

For each cycle, read the `player:` lines from the moment playback resumes after the kill and outage, and record:
- `fromStart` and `fromEnd` at the first `playing` after recovery;
- the PDT before the outage and after resuming;
- whether the position landed at the window start (`fromStart` under one segment), at the live end (`fromEnd` under one segment), or near where it was.

Write the result in Part 2's input. Use an empty commit, with the per-player, per-cycle table and the judgement "jump still present" or "jump gone":

```bash
git commit --allow-empty -m "test: restart discriminator (hls.js and AVPlayer) after Part 1

<per-player, per-cycle table: fromStart/fromEnd at resume, PDT before/after, verdict>

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

Commit the test itself first:

```bash
git add server/internal/e2e/discriminator_test.go
git commit -m "test(e2e): restart discriminator server for real players

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

- [ ] **Step 9: Commit any fixes**

```bash
git add -A server .github
git commit -m "chore: stream resilience verification fixes

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

Skip this if nothing changed.
