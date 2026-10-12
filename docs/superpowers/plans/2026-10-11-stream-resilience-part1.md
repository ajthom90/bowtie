# Stream Resilience Part 1 (Server) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Live streams survive dirty broadcasts, stalls and transcoder restarts. That means no duplicate-frame storms, no audio holes, no needless FFmpeg kills and no broken timeline. Every served playlist carries a Bowtie-owned wall-clock timeline that matches across renditions: PDT, discontinuities and the discontinuity sequence. A timeline endpoint serves clients that can't read PDT.

**Architecture:**
- **Test harness first.** It adds:
  - `pts-jump` faults: PTS+DTS and PTS-only;
  - a multi-rendition testplayer with a pure consistency checker, ffprobe audio checks and post-fault progress counts;
  - a "production" e2e layout: a software ladder, every audio track, the AC-3 copy and captions;
  - new fault scenarios marked `xfail`. Each is pinned to the check that must fail until the task that fixes it.
- **FFmpeg changes.** Input and audio hardening come first. Then the timestamp option is chosen by a gate on FFmpeg 5.1 (the production image's FFmpeg). The gate runs the ptsjump variants, the splice scenario and real over-the-air clips, and counts FFmpeg's own "timestamp discontinuity" re-bases.
- **Ingest changes.** A stuck transcoder is drained first. It is killed only if no bytes reach FFmpeg for a further grace period. New subscribers join at a complete video key (sequence header, or SPS/PPS plus IDR).
- **Session timeline.** A per-session `stream.Timeline` observer reads every producer playlist on its own clock, independent of client requests.
  - It stamps each rendition's segments from one shared broadcast clock: the run's wall-clock start plus that rendition's own cumulative durations, with forward jumps over real gaps.
  - It assigns discontinuity epochs session-wide.
  - Serving only reads its snapshot. The snapshot also backs the owner-only `GET /api/v1/sessions/{viewerId}/timeline`.
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
- Timestamp option: chosen by the Task 6 gate on FFmpeg 5.1. The candidates are `-dts_delta_threshold 1`, `-dts_delta_threshold` between the largest splice step and 3 s (2.6), and `-fps_mode vfr` with a constraint that closes the PTS hole. The gate requires all of:
  - the PTS+DTS and PTS-only `ptsjump` variants at +3, +6 and +9 s, and the `splice` scenario, with no timeline inflation and no duplicate storm;
  - zero "timestamp discontinuity" re-bases on splices (FFmpeg 5.1 debug logs);
  - the same results on the real OTA clips.

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
   - Audio and video are cut at different points (4.032 s vs 4.004 s), and clients poll them in any order.
   - A viewer expects PDT never to overlap within a rendition and the same moment to carry the same PDT, within one segment, in every rendition.
   - Pinned by `TestTimelineUnequalDurationsNeverOverlap`, `TestTimelinePollOrderDoesNotMatter` (Task 9) and `pdtAligned` on the production-layout scenarios (Task 10).
3. **Discontinuity numbering diverging after partial runs.**
   - Two quick crashes where the middle run wrote audio only, or a rendition whose playlist appears late, must not leave renditions with different discontinuity numbers for the same run.
   - Pinned by `TestTimelineAudioOnlyMiddleRunKeepsEpochsAligned` and `TestTimelineLateRenditionNeverFallsBackToRunZero` (Task 9).
4. **A slow-but-alive transcoder killed while reading a large join, or a hung one never killed.**
   - Pinned by:
     - `TestSlowReaderOfLargeJoinIsNotStuck` and `TestSubQueueProgressCountsWrittenBytesNotPops` (Task 7);
     - `TestStuckSubDrainedThenClosedAfterGrace` (Task 7);
     - the `stuck-hung` scenario (Task 4).
5. **The timeline endpoint leaking another user's session.** Only the owner's Bearer token sees it. Stream URL tokens, other users and admins get the same 404 as a missing viewer. Pinned by `TestSessionTimelineOwnerOnly` (Task 11).

---

## File map

| File | Responsibility | Tasks |
|---|---|---|
| `server/internal/hdhr/hdhrfake/faults/spec.go`, `engine.go` | `pts-jump` fault (PTS+DTS or PTS-only) | 1 |
| `server/internal/hdhr/hdhrfake/scenario/scenario.go`, `run.go` | pts-jump one-shot; `ptsOnly`; layout; new expectations; pinned xfail; `windowSegments` | 1, 4 |
| `server/internal/testplayer/consistency.go` (new) | pure cross-rendition playlist checks | 2 |
| `server/internal/testplayer/player.go` | follow every rendition, probe segments, epoch-paired A/V skew, progress since a mark, per-segment events | 3 |
| `server/internal/e2e/harness.go`, `scenarios_test.go`, `ffprobe_test.go` (new), `rebases_test.go` (new), `otaclips_test.go` (new) | plumbing, production layout, ffprobe probe, re-base log reader, OTA clip gate | 4, 6 |
| `server/internal/e2e/testdata/scenarios/*.yaml` | new scenarios plus guards on existing ones | 4, 5, 6, 7, 10 |
| `server/internal/stream/manager.go` | `HLSListSize`, `ForceAdaptive`; timeline observer per session; RetimePlaylist; TimelineOf; caption sweep | 4, 10, 11, 12 |
| `server/internal/transcode/ffmpeg.go` | live input hardening, aresample (AAC and, if needed, AC-3), timestamp option | 5, 6 |
| `server/scripts/ffmpeg51` (new) | FFmpeg 5.1 (bookworm) through Docker for e2e on the Mac | 6 |
| `server/internal/stream/subqueue.go`, `ingest.go` | progress by bytes written; drain-then-kill; join at a complete video key | 7, 8 |
| `server/internal/stream/probe.go` | PMT video stream type | 8 |
| `server/internal/stream/hlsplaylist.go` (new), `timeline.go` (new) | playlist parse, server-side timeline observer | 9 |
| `server/internal/transcode/layout.go` | `Layout.Renditions()` | 10 |
| `server/internal/stream/session.go` | `session.timeline`, observer stop | 10 |
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
| `brief-dropout` | `timeline excess`: the 4 s hole is filled with duplicated frames | Task 6 |
| `threshold-dropout` | `timeline excess`: same for the 9 s and 12 s holes | Task 6 |
| `corrupt-audio` | `audio hole`: AAC (and AC-3 copy) holes where TEI packets are discarded | Task 5 |
| `stuck-recovers` | `ffmpeg restarts`: Bowtie kills FFmpeg at 30 s | Task 7 |
| `long-outage-restart` | `discontinuity sequence`: FFmpeg writes no `#EXT-X-DISCONTINUITY-SEQUENCE` | Task 10 |
| `stuck-hung`, `production-baseline` | none (guards) | — |

**Rule when reality differs from the table:** a pre-fix run might pass the pinned check, for example if an input stall already makes FFmpeg skip instead of fill. In that case re-run the scenario once. If it still passes:
- remove `xfail`/`xfailOn`, so the scenario becomes a guard;
- say so in the commit message.

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
  - `type SegmentObs struct { Seq int64; Duration float64; PDT time.Time; Discontinuity bool; URI string }`
  - `type Snapshot struct { Rendition string; At time.Time; MediaSequence, DiscontinuitySequence int64; Segments []SegmentObs }`
  - `func ParseSnapshot(rendition string, body []byte, at time.Time) Snapshot`. It accepts RFC 3339 PDTs and FFmpeg's `+0000` form.
  - `type Consistency struct { RenditionsMonotonic, DiscontinuitySequenceOK, PDTMonotonic, PDTAligned, DiscontinuitiesAligned bool; MaxDiscontinuitySequence int64; Problems []string }`
  - `func CheckConsistency(snaps []Snapshot) Consistency`. `snaps` is in poll order; renditions are grouped by name.
  - Alignment key: (epoch, index). The epoch is `DISCONTINUITY-SEQUENCE` plus the tags seen so far in the snapshot. The index is `seq − first seq of the epoch` (0 for epoch 0). This is the same keying the server uses in Task 9.

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
}

// mk renders a media playlist whose segments are 4 s long.
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
		fmt.Fprintf(&b, "#EXT-X-PROGRAM-DATE-TIME:%s\n#EXTINF:4.000,\n/s/%s_%05d.ts?token=x\n",
			e.pdt.UTC().Format("2006-01-02T15:04:05.000Z07:00"), name, mseq+int64(i))
	}
	return b.String()
}

func snap(name, body string, n int) Snapshot {
	return ParseSnapshot(name, []byte(body), at(float64(n)))
}

// restartRun is two renditions across a restart at 70 s: video seq 0..2 then
// 3.. (new run); audio was one segment ahead when FFmpeg died (0..3 then 4..).
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

func TestConsistencyAlignsByIndexWhenAudioWasAhead(t *testing.T) {
	c := CheckConsistency(restartRun(at(70)))
	if !c.RenditionsMonotonic || !c.DiscontinuitySequenceOK || !c.PDTMonotonic || !c.PDTAligned || !c.DiscontinuitiesAligned {
		t.Fatalf("consistent run flagged: %+v", c)
	}
	if c.MaxDiscontinuitySequence != 1 {
		t.Fatalf("MaxDiscontinuitySequence=%d, want 1", c.MaxDiscontinuitySequence)
	}
}

func TestConsistencyFlagsMisalignedBoundary(t *testing.T) {
	c := CheckConsistency(restartRun(at(74)))
	if c.DiscontinuitiesAligned || c.PDTAligned {
		t.Fatalf("audio boundary 4 s late not flagged: %+v", c)
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
		"#EXT-X-PROGRAM-DATE-TIME:2026-10-11T20:00:04.500+0000\n#EXTINF:4.004000,\nv480_00007.ts\n"), at(0))
	if s.MediaSequence != 7 || s.DiscontinuitySequence != 2 || len(s.Segments) != 1 {
		t.Fatalf("snapshot %+v", s)
	}
	g := s.Segments[0]
	if g.Seq != 7 || g.Duration != 4.004 || !g.PDT.Equal(at(4.5)) || g.URI != "v480_00007.ts" {
		t.Fatalf("segment %+v", g)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd server && go test ./internal/testplayer/ -run 'Consistency|ParseSnapshot' -v`
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

// maxAlign is how far apart two renditions may place the same moment.
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
	URI           string
}

// Snapshot is one poll of one rendition's media playlist.
type Snapshot struct {
	Rendition             string
	At                    time.Time
	MediaSequence         int64
	DiscontinuitySequence int64
	Segments              []SegmentObs
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
	// PDTAligned: the same (run, index) segment has the same PDT (±500 ms) in
	// every rendition.
	PDTAligned bool
	// DiscontinuitiesAligned: restarts sit at the same PDT in every rendition
	// and every rendition ends in the same run.
	DiscontinuitiesAligned bool
	Problems               []string
}

type epochKey struct {
	epoch int64
	index int64
}

type segRec struct {
	epoch int64
	pdt   time.Time
	dur   float64
	disc  bool
}

type rendView struct {
	snaps      []Snapshot
	segs       map[int64]segRec    // seq → first observation
	firstSeq   map[int64]int64     // epoch → seq of its first segment (epoch 0 → 0)
	boundary   map[int64]time.Time // epoch → PDT of its first segment
	byKey      map[epochKey]time.Time
	finalEpoch int64
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
			v = &rendView{segs: map[int64]segRec{}, firstSeq: map[int64]int64{0: 0}, boundary: map[int64]time.Time{}, byKey: map[epochKey]time.Time{}}
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
			epoch := s.DiscontinuitySequence
			for _, g := range s.Segments {
				if g.Discontinuity {
					epoch++
					v.firstSeq[epoch] = g.Seq
					if !g.PDT.IsZero() {
						v.boundary[epoch] = g.PDT
					}
				}
				if g.PDT.IsZero() && !noPDT {
					noPDT = true
					problem("pdt", &c.PDTMonotonic, "%s: segment %d has no PROGRAM-DATE-TIME", name, g.Seq)
				}
				if old, ok := v.segs[g.Seq]; ok {
					if old.disc != g.Discontinuity {
						problem("dseq", &c.DiscontinuitySequenceOK, "%s: DISCONTINUITY on segment %d came and went", name, g.Seq)
					}
					if old.epoch != epoch {
						problem("dseq", &c.DiscontinuitySequenceOK, "%s: segment %d moved from run %d to %d", name, g.Seq, old.epoch, epoch)
					}
					if d := g.PDT.Sub(old.pdt); d > pdtSlack || d < -pdtSlack {
						problem("pdt", &c.PDTMonotonic, "%s: PDT of segment %d changed by %v", name, g.Seq, d)
					}
					continue
				}
				v.segs[g.Seq] = segRec{epoch: epoch, pdt: g.PDT, dur: g.Duration, disc: g.Discontinuity}
			}
			v.finalEpoch = epoch
		}
		seqs := make([]int64, 0, len(v.segs))
		for q := range v.segs {
			seqs = append(seqs, q)
		}
		sort.Slice(seqs, func(i, j int) bool { return seqs[i] < seqs[j] })
		for i, q := range seqs {
			r := v.segs[q]
			if first, ok := v.firstSeq[r.epoch]; ok && !r.pdt.IsZero() {
				v.byKey[epochKey{r.epoch, q - first}] = r.pdt
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
		for j := i + 1; j < len(names); j++ {
			a, b := views[names[i]], views[names[j]]
			for k, ta := range a.byKey {
				if tb, ok := b.byKey[k]; ok {
					if d := ta.Sub(tb); d > maxAlign || d < -maxAlign {
						problem("align", &c.PDTAligned, "%s/%s: run %d segment %d differ by %v", names[i], names[j], k.epoch, k.index, d)
					}
				}
			}
			for e, ta := range a.boundary {
				if tb, ok := b.boundary[e]; ok {
					if d := ta.Sub(tb); d > maxAlign || d < -maxAlign {
						problem("boundary", &c.DiscontinuitiesAligned, "%s/%s: restart %d differs by %v", names[i], names[j], e, d)
					}
				}
			}
			if a.finalEpoch != b.finalEpoch {
				problem("boundary", &c.DiscontinuitiesAligned, "%s ends in run %d, %s in run %d", names[i], a.finalEpoch, names[j], b.finalEpoch)
			}
		}
	}
	return c
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd server && go test ./internal/testplayer/ -v -run 'Consistency|ParseSnapshot'` then `cd server && go test ./internal/testplayer/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/testplayer/consistency.go server/internal/testplayer/consistency_test.go
git commit -m "test(testplayer): cross-rendition playlist consistency checks

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---
### Task 3: testplayer follows every rendition, probes segments, measures timeline excess

**Files:**
- Modify: `server/internal/testplayer/player.go`
- Test: `server/internal/testplayer/player_renditions_test.go` (new)

**Interfaces:**
- Consumes: `ParseSnapshot`, `CheckConsistency`, `Snapshot`, `Consistency` (Task 2).
- Produces:
  - `type Packet struct { PTS, Dur float64 }` (seconds).
  - `type SegmentProbe func(ctx context.Context, seg []byte) ([]Packet, error)` lists the packets of the segment's first stream.
  - `Config.AllRenditions bool` and `Config.Probe SegmentProbe`.
  - New `Report` fields:
    - `Renditions []string` (primary first);
    - `TimelineExcess float64` (seconds);
    - `MaxAudioHole time.Duration`, `AudioHoleBy map[string]time.Duration` (per audio rendition) and `MaxAVStartSkew time.Duration`;
    - `ProbedAudio, ProbedVideo int`;
    - `Consistency Consistency`;
    - `ProbeErrors, PlaylistErrors int`;
    - `RenditionSegments, SegmentsSinceMark map[string]int`: segments fetched OK per rendition, in total and since the last `Mark`.
  - `func (p *Player) Mark()` starts the "since mark" counts. Scenarios call it when the last fault ends, so post-fault progress is measured in every rendition.
  - `Config.OnSegment func(SegmentEvent)`, with `type SegmentEvent struct { At time.Time; Rendition string; Seq int64; AudioHole, AVSkew time.Duration; TimelineExcess float64 }`. It is called after each probed segment (time-local measurements for the soak).
  - Captions: `TYPE=SUBTITLES` renditions (`<rung>_vtt`) are followed too. Their playlists feed the consistency checks, and their segments must be `WEBVTT`. They are never probed.
  - Fetch errors: a secondary playlist that returns 404 **after** it has once returned 200 counts as a `PlaylistErrors` (before that, it isn't written yet). Any probe failure counts in `ProbeErrors`. Task 4's `noFetchErrors` expectation fails on any of these, on segment errors, and on a listed rendition that never delivered a segment.
  - Timeline excess is Σ EXTINF of the primary rendition's segments that first appear after the first poll that listed any, minus the wall time between that poll and the poll that first listed the newest of them.
  - A/V start skew compares the first packet PTS of an audio and a video segment with the same **(epoch, index)** key: the epoch is `DISCONTINUITY-SEQUENCE` plus the tags seen, and the index is the sequence number minus the epoch's first sequence number. This is the same keying as Task 2. After a kill, the audio numbering can run one ahead, but the keys still pair the same moment, so restart scenarios can set `maxAVStartSkewMs` once Task 10 serves Bowtie's discontinuities. Segments whose epoch start was never seen are not paired.
  - Audio holes are measured within a segment, and from the end of the previous segment when the sequence numbers are consecutive and the segment carries no DISCONTINUITY. A rendition is audio when its name starts with `aac` or `ac3`.

- [ ] **Step 1: Write the failing test**

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
	near := func(got, want time.Duration) bool { d := got - want; return d > -time.Millisecond && d < time.Millisecond }
	if !near(r.MaxAudioHole, 200*time.Millisecond) {
		t.Fatalf("MaxAudioHole=%v, want 200ms", r.MaxAudioHole)
	}
	if !near(r.MaxAVStartSkew, 200*time.Millisecond) {
		t.Fatalf("MaxAVStartSkew=%v, want 200ms", r.MaxAVStartSkew)
	}
	c := r.Consistency
	if !c.PDTMonotonic || !c.PDTAligned || !c.RenditionsMonotonic || !c.DiscontinuitySequenceOK {
		t.Fatalf("consistency %+v", c)
	}
	if r.TimelineExcess <= 0 {
		t.Fatalf("TimelineExcess=%v: 16 s of segments appeared in well under a second", r.TimelineExcess)
	}
	if len(r.Errors) != 0 {
		t.Fatalf("errors %v", r.Errors)
	}
}
// After a kill the audio is one segment ahead; pairing by (epoch, index)
// still compares the same moment. Errors and post-mark progress are counted.
func TestPlayerPairsAVByEpochAndCountsSinceMark(t *testing.T) {
	var mu sync.Mutex
	polls := 0
	segRe := regexp.MustCompile(`/s/(v480|aac0)_(\d{5})\.(ts)$|/s/v480_vtt_(\d+)\.vtt$`)
	// Video: 0,1 then restart at 2. Audio: 0,1,2 then restart at 3.
	media := func(name string, n int) string {
		restart := map[string]int{"v480": 2, "aac0": 3, "v480_vtt": 2}[name]
		var b strings.Builder
		b.WriteString("#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-DISCONTINUITY-SEQUENCE:0\n")
		for i := 0; i <= n+restart-2; i++ {
			if i == restart {
				b.WriteString("#EXT-X-DISCONTINUITY\n")
			}
			at := time.Date(2026, 10, 11, 20, 0, 4*i, 0, time.UTC)
			if i >= restart {
				at = at.Add(time.Minute)
			}
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
			fmt.Fprint(w, "WEBVTT\n")
		case segRe.MatchString(r.URL.Path):
			_, _ = w.Write(append([]byte{0x47}, r.URL.Path...))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	// Both renditions restart their PTS at 100 s: seq 2 (video) and 3 (audio)
	// are the same moment. Pairing by sequence number would see 96 s of skew.
	probe := func(_ context.Context, seg []byte) ([]Packet, error) {
		m := segRe.FindStringSubmatch(string(seg[1:]))
		seq, _ := strconv.Atoi(m[2])
		restart := map[string]int{"v480": 2, "aac0": 3}[m[1]]
		start := float64(seq) * 4
		if seq >= restart {
			start = 100 + float64(seq-restart)*4
		}
		return []Packet{{PTS: start, Dur: 4}}, nil
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
	if r.MaxAVStartSkew > time.Millisecond {
		t.Fatalf("MaxAVStartSkew=%v, want 0 (paired by epoch and index)", r.MaxAVStartSkew)
	}
	if r.SegmentsSinceMark["v480"] != 2 || r.SegmentsSinceMark["v480_vtt"] != 2 || r.SegmentsSinceMark["aac0"] != 0 {
		t.Fatalf("since mark %v", r.SegmentsSinceMark)
	}
	if r.PlaylistErrors == 0 {
		t.Fatal("a rendition that went missing was not counted")
	}
	if len(events) == 0 || events[0].Rendition == "" {
		t.Fatalf("events %+v", events)
	}
}

```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd server && go test ./internal/testplayer/ -run 'TestPlayerFollowsEveryRenditionAndProbes|TestPlayerPairsAVByEpochAndCountsSinceMark' -v`
Expected: FAIL to compile with `unknown field AllRenditions` / `undefined: Packet`.

- [ ] **Step 3: Implement**

In `server/internal/testplayer/player.go`:

1. Add `"regexp"` to the imports.

2. Add the probe types after `Config`:

```go
// Packet is one demuxed packet of a probed segment, in seconds.
type Packet struct{ PTS, Dur float64 }

// SegmentProbe lists the packets of the first stream in a TS segment.
type SegmentProbe func(ctx context.Context, seg []byte) ([]Packet, error)
```

3. Add to `Config` (after `Client`):

```go
	// AllRenditions follows every rendition the master lists (all variants and
	// audio renditions), not only the first variant.
	AllRenditions bool
	// Probe, when set, runs on every new segment (audio holes, A/V start).
	Probe SegmentProbe
	// OnSegment, when set, is called (on the Run goroutine) after each
	// probed segment with time-local measurements.
	OnSegment func(SegmentEvent)
```

and after `SegmentProbe`:

```go
// SegmentEvent is one probed segment, for time-local analysis (the soak).
type SegmentEvent struct {
	At             time.Time
	Rendition      string
	Seq            int64
	AudioHole      time.Duration // largest hole in/before this audio segment
	AVSkew         time.Duration // skew against the paired segment of the other kind (0: unpaired)
	TimelineExcess float64       // the primary's running excess at this moment
}
```

4. Add to `Report` (after `EndList`):

```go
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
	// MaxAVStartSkew is the largest first-PTS difference between an audio
	// and a video segment with the same media sequence number.
	MaxAVStartSkew           time.Duration
	ProbedAudio, ProbedVideo int
	// Consistency checks every polled media playlist (CheckConsistency).
	Consistency Consistency
	// ProbeErrors counts failed probes; PlaylistErrors counts secondary
	// playlists that failed after they had been served once.
	ProbeErrors, PlaylistErrors int
	// RenditionSegments counts segments fetched OK per rendition;
	// SegmentsSinceMark counts those fetched after the last Mark.
	RenditionSegments, SegmentsSinceMark map[string]int
```

5. Add the rendition type and helpers at the end of the file:

```go
// rendition is one followed media playlist and its probe state.
type rendition struct {
	name       string
	url        string
	highest    int64
	lastSeq    int64   // last probed audio segment (-2: none)
	lastEnd    float64 // end PTS of that segment
	everOK     bool    // the playlist was served at least once
	fetched    int     // segments fetched OK
	atMark     int     // fetched at the last Mark
	epochFirst map[int64]int64
}

func newRendition(name, url string) *rendition {
	return &rendition{name: name, url: url, highest: -1, lastSeq: -2, epochFirst: map[int64]int64{0: 0}}
}

// keys maps each segment of s to its (epoch, index) key, as Task 2 keys
// alignment. A segment whose epoch start was never seen gets no key.
func (r *rendition) keys(s Snapshot) map[int64]epochKey {
	out := map[int64]epochKey{}
	epoch := s.DiscontinuitySequence
	for _, g := range s.Segments {
		if g.Discontinuity {
			epoch++
			if _, ok := r.epochFirst[epoch]; !ok {
				r.epochFirst[epoch] = g.Seq
			}
		}
		if first, ok := r.epochFirst[epoch]; ok {
			out[g.Seq] = epochKey{epoch, g.Seq - first}
		}
	}
	return out
}

func isSubtitleRendition(name string) bool { return strings.HasSuffix(name, "_vtt") }

// renditionName is a playlist reference's file name without .m3u8 or query.
func renditionName(ref string) string {
	if q := strings.IndexByte(ref, '?'); q >= 0 {
		ref = ref[:q]
	}
	ref = ref[strings.LastIndexByte(ref, '/')+1:]
	return strings.TrimSuffix(ref, ".m3u8")
}

func isAudioRendition(name string) bool {
	return strings.HasPrefix(name, "aac") || strings.HasPrefix(name, "ac3")
}

var mediaURIRe = regexp.MustCompile(`URI="([^"]+)"`)

// renditionRefs lists a master's variants (in order), then its audio and
// subtitle renditions.
func renditionRefs(master []byte) []string {
	var variants, audio []string
	sc := bufio.NewScanner(bytes.NewReader(master))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "#EXT-X-MEDIA:") &&
			(strings.Contains(line, "TYPE=AUDIO") || strings.Contains(line, "TYPE=SUBTITLES")):
			if m := mediaURIRe.FindStringSubmatch(line); m != nil {
				audio = append(audio, m[1])
			}
		case line != "" && !strings.HasPrefix(line, "#"):
			variants = append(variants, line)
		}
	}
	return append(variants, audio...)
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
```

6. Add fields to `Player` (after `prevNews int64`):

```go
	primary  *rendition
	others   []*rendition
	snaps    []Snapshot
	firstPTS map[string]map[epochKey]float64 // rendition → (epoch, index) → first packet PTS

	excessStarted bool
	excessBase    int64 // newest segment at the first poll that listed any
	excessFirst   time.Time
	excessLast    time.Time
	excessSum     float64
```

In `Start`, initialize them in the returned `Player` literal:

```go
		primary:     newRendition(renditionName(out.PlaylistURL), out.PlaylistURL),
		firstPTS:    map[string]map[epochKey]float64{},
```

7. Give `entry` a duration and parse it. Change the struct to:

```go
type entry struct {
	abs           int64
	uri           string
	dur           float64
	discontinuity bool
}
```

In `parsePlaylist`, add a `dur := 0.0` next to `disc := false`, add this case before the `case line == "" …` case:

```go
		case strings.HasPrefix(line, "#EXTINF:"):
			v := strings.TrimPrefix(line, "#EXTINF:")
			if i := strings.IndexByte(v, ','); i >= 0 {
				v = v[:i]
			}
			dur, _ = strconv.ParseFloat(v, 64)
```

and set `dur: dur` in the appended `entry`, resetting `dur = 0` next to `disc = false`.

8. Rewrite `poll`. Keep its existing behavior and add the new work:

```go
// poll fetches the playlist and any new segments; it returns true when the
// session is gone.
func (p *Player) poll(ctx context.Context) bool {
	pollAt := time.Now()
	body, status, err := p.get(ctx, p.playlistURL)
	p.mu.Lock()
	p.report.Polls++
	p.mu.Unlock()
	if err != nil {
		if ctx.Err() == nil {
			p.errorf("playlist: %v", err)
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
		p.errorf("playlist: HTTP %d", status)
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
						p.others = append(p.others, newRendition(n, resolveRef(base, ref)))
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
	keys := p.primary.keys(snap)
	p.mu.Lock()
	p.snaps = append(p.snaps, snap)
	others := append([]*rendition(nil), p.others...)
	p.mu.Unlock()

	entries := parsePlaylist(body)
	if len(entries) > 0 {
		newest := entries[len(entries)-1].abs
		p.mu.Lock()
		p.report.Newest = append(p.report.Newest, newest)
		if p.prevNews >= 0 && newest < p.prevNews {
			p.report.BackwardJumps++
		}
		p.prevNews = newest
		if !p.excessStarted {
			p.excessStarted, p.excessBase, p.excessFirst = true, newest, pollAt
		}
		p.mu.Unlock()
	}

	for _, e := range entries {
		p.mu.Lock()
		isNew := e.abs > p.highest
		p.mu.Unlock()
		if !isNew {
			continue
		}
		seg, segStatus, err := p.get(ctx, e.uri)
		ok := err == nil && segStatus == http.StatusOK && len(seg) > 0 && seg[0] == 0x47
		p.mu.Lock()
		if ok {
			p.report.SegmentsFetched++
		} else {
			p.report.SegmentErrors++
		}
		if e.discontinuity {
			p.report.Discontinuities++
		}
		now := time.Now()
		if gap := now.Sub(p.lastNew); gap > p.report.MaxGap {
			p.report.MaxGap = gap
		}
		p.lastNew = now
		p.highest = e.abs
		if e.abs > p.excessBase {
			p.excessSum += e.dur
			p.excessLast = pollAt
		}
		p.mu.Unlock()
		if ok {
			p.mu.Lock()
			p.primary.fetched++
			p.mu.Unlock()
			k, hasKey := keys[e.abs]
			p.probe(ctx, p.primary, e.abs, k, hasKey, e.discontinuity, seg)
		}
	}
	for _, r := range others {
		p.pollRendition(ctx, r)
	}
	return false
}

// pollRendition polls a non-primary rendition: snapshot, new segments, probes.
// Only the Run goroutine touches r.highest.
func (p *Player) pollRendition(ctx context.Context, r *rendition) {
	body, status, err := p.get(ctx, r.url)
	switch {
	case err != nil:
		if ctx.Err() == nil {
			p.errorf("%s playlist: %v", r.name, err)
		}
		return
	case status == http.StatusNotFound:
		if r.everOK {
			p.mu.Lock()
			p.report.PlaylistErrors++
			p.mu.Unlock()
			p.errorf("%s playlist: HTTP 404 after it was served", r.name)
		}
		return // otherwise not written yet
	case status != http.StatusOK:
		p.errorf("%s playlist: HTTP %d", r.name, status)
		return
	}
	r.everOK = true
	s := ParseSnapshot(r.name, body, time.Now())
	keys := r.keys(s)
	p.mu.Lock()
	p.snaps = append(p.snaps, s)
	p.mu.Unlock()
	for _, g := range s.Segments {
		if g.Seq <= r.highest {
			continue
		}
		r.highest = g.Seq
		seg, st, err := p.get(ctx, g.URI)
		valid := len(seg) > 0 && seg[0] == 0x47
		if isSubtitleRendition(r.name) {
			valid = bytes.HasPrefix(seg, []byte("WEBVTT"))
		}
		if err != nil || st != http.StatusOK || !valid {
			p.mu.Lock()
			p.report.SegmentErrors++
			p.mu.Unlock()
			continue
		}
		p.mu.Lock()
		r.fetched++
		p.mu.Unlock()
		if !isSubtitleRendition(r.name) {
			k, hasKey := keys[g.Seq]
			p.probe(ctx, r, g.Seq, k, hasKey, g.Discontinuity, seg)
		}
	}
}

// probe measures audio holes and the A/V start skew of one fetched segment.
func (p *Player) probe(ctx context.Context, r *rendition, seq int64, key epochKey, hasKey, disc bool, seg []byte) {
	if p.cfg.Probe == nil {
		return
	}
	pk, err := p.cfg.Probe(ctx, seg)
	if err != nil || len(pk) == 0 {
		if ctx.Err() == nil {
			p.mu.Lock()
			p.report.ProbeErrors++
			p.mu.Unlock()
			p.errorf("probe %s segment %d: %v (%d packets)", r.name, seq, err, len(pk))
		}
		return
	}
	ev := SegmentEvent{At: time.Now(), Rendition: r.name, Seq: seq}
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
		if r.lastSeq == seq-1 && !disc {
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
		r.lastSeq, r.lastEnd = seq, last.PTS+last.Dur
	} else {
		p.report.ProbedVideo++
	}
	if !hasKey {
		return
	}
	if p.firstPTS[r.name] == nil {
		p.firstPTS[r.name] = map[epochKey]float64{}
	}
	p.firstPTS[r.name][key] = pk[0].PTS
	for name, m := range p.firstPTS {
		if name == r.name || isAudioRendition(name) == audio {
			continue
		}
		if other, ok := m[key]; ok {
			s := absSeconds(pk[0].PTS - other)
			if s > ev.AVSkew {
				ev.AVSkew = s
			}
			if s > p.report.MaxAVStartSkew {
				p.report.MaxAVStartSkew = s
			}
		}
	}
}
```

9. In `Report()`, before `return r`, add:

```go
	r.Renditions = []string{p.primary.name}
	r.RenditionSegments = map[string]int{p.primary.name: p.primary.fetched}
	r.SegmentsSinceMark = map[string]int{p.primary.name: p.primary.fetched - p.primary.atMark}
	for _, o := range p.others {
		r.Renditions = append(r.Renditions, o.name)
		r.RenditionSegments[o.name] = o.fetched
		r.SegmentsSinceMark[o.name] = o.fetched - o.atMark
	}
	if !p.excessLast.IsZero() {
		r.TimelineExcess = p.excessSum - p.excessLast.Sub(p.excessFirst).Seconds()
	}
	r.Consistency = CheckConsistency(p.snaps)
```

10. Add `Mark`:

```go
// Mark starts the SegmentsSinceMark counts (scenarios call it when the last
// fault ends, to require progress afterwards in every rendition).
func (p *Player) Mark() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.primary.atMark = p.primary.fetched
	for _, o := range p.others {
		o.atMark = o.fetched
	}
}
```

`r.fetched` and `r.atMark` are read and written under `p.mu`. The other `rendition` fields are only touched by the `Run` goroutine.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd server && go test ./internal/testplayer/ -v -race`
Expected: PASS, including every existing `player_test.go` test (their reports gain fields; none of their assertions change).

- [ ] **Step 5: Commit**

```bash
git add server/internal/testplayer
git commit -m "test(testplayer): follow every rendition, probe segments, measure timeline excess

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Scenario expectations, e2e plumbing and the new fault scenarios

**Files:**
- Modify: `server/internal/hdhr/hdhrfake/scenario/scenario.go`, `run.go`
- Test: `server/internal/hdhr/hdhrfake/scenario/scenario_test.go`
- Modify: `server/internal/stream/manager.go` (`ManagerDeps.HLSListSize`)
- Test: `server/internal/stream/manager_test.go`
- Modify: `server/internal/e2e/harness.go`, `server/internal/e2e/scenarios_test.go`
- Create: `server/internal/e2e/ffprobe_test.go`
- Create: `server/internal/e2e/testdata/scenarios/{ptsjump,ptsjump-ptsonly,corrupt-audio,brief-dropout,threshold-dropout,splice,long-outage-restart,stuck-recovers,stuck-hung,production-baseline}.yaml`
- Modify: `server/internal/e2e/testdata/scenarios/transcoder-restart.yaml`

**Interfaces:**
- Consumes: the testplayer `Config.AllRenditions`, `Config.Probe`, `Packet`, `SegmentProbe` and the `Report` fields (Task 3).
- Produces:
  - `scenario.Scenario.WindowSegments int` (`windowSegments`: the session's `-hls_list_size`; 0 = default 30; otherwise ≥ 3).
  - `scenario.Expect` gains `RenditionsMonotonic, DiscontinuitiesAligned, DiscontinuitySequenceOK, PDTMonotonic, PDTAligned *bool`, `MinDiscontinuitySequence *int64`, `MaxTimelineExcessSeconds, MinTimelineExcessSeconds, MaxAudioHoleMs, MaxAVStartSkewMs *float64` and `MinFfmpegRestarts *int`. YAML keys are the lowerCamel field names.
  - `scenario.Observed` gains the matching fields: `RenditionsMonotonic`, `DiscontinuitiesAligned`, `DiscontinuitySequenceOK`, `MaxDiscontinuitySequence`, `PDTMonotonic`, `PDTAligned`, `TimelineExcess`, `MaxAudioHole`, `MaxAVStartSkew`, `ProbedAudio`, `ProbedVideo`.
  - `scenario.Expect` also gains:
    - `XFailOn string` (`xfailOn`, required with `xfail`): the text of the violation that must occur. Any other violation fails the xfail run.
    - `MinSegmentsAfterFault *int` (`minSegmentsAfterFault`): every followed rendition fetched at least this many segments after the last fault ended.
    - `NoFetchErrors *bool` (`noFetchErrors`): no segment, probe or playlist errors, and every listed rendition delivered a segment.
    - `MinRenditions *int` (`minRenditions`).
    - `MaxStartSeconds *float64` (`maxStartSeconds`): from POST /sessions to its answer, which waits for the first playlist.
  - `scenario.Observed` gains `MinSegmentsAfterFault int`, `FetchErrors int`, `RenditionsCovered bool`, `Renditions int` and `StartLatency time.Duration`.
  - `scenario.BowtieSpec.Layout string` (`bowtie: {layout: production}`). The production layout is what a real server serves: multitrack (every audio track as AAC, the AC-3 copy, captions) and a ladder, even with the software encoder. The default stays one rung and one AAC.
  - `func (s *Scenario) LastFaultEnd() time.Duration` returns the latest end of any timeline step (`at` + `for`), slow-consumer window or kill/close time.
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
		ProbedAudio: 3, ProbedVideo: 3, FfmpegRestarts: 1,
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

Add `"strings"` to the test file's imports if it isn't there.

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

Run: `cd server && go test ./internal/hdhr/hdhrfake/scenario/ ./internal/stream/ -run 'TestCheckTimelineExpectations|TestParseWindowSegments|TestXFailNeedsXFailOn|TestHLSListSizeOverride|TestForceAdaptive' -v`
Expected: FAIL to compile (`unknown field RenditionsMonotonic`, `unknown field HLSListSize`).

- [ ] **Step 3: Implement scenario and manager changes**

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
// LastFaultEnd is when the last fault of the run is over: the latest
// timeline step end (at + for) and Bowtie-side window or action.
func (s *Scenario) LastFaultEnd() time.Duration {
	var end time.Duration
	for _, st := range s.Timeline {
		if e := st.At + st.Spec.For; e > end {
			end = e
		}
	}
	if b := s.Bowtie; b != nil {
		if w := b.SlowConsumer; w != nil && w.At+w.For > end {
			end = w.At + w.For
		}
		for _, t := range []time.Duration{b.KillTranscoderAt, b.CloseTranscoderInputAt} {
			if t > end {
				end = t
			}
		}
	}
	return end
}
```

(`Step` embeds `faults.Spec`, so `st.Spec.For` is the step's `for`.)

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
		e.MinSegmentsAfterFault == nil && e.NoFetchErrors == nil && e.MinRenditions == nil && e.MaxStartSeconds == nil
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
	FetchErrors              int // segment + probe + playlist errors
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
		case o.ProbedAudio == 0 || o.ProbedVideo == 0:
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

Run: `cd server && go test ./internal/hdhr/hdhrfake/scenario/ ./internal/stream/ -run 'TestCheck|TestParse|TestHLSListSize' -v`
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

- In the `default:` branch, right after `defer cancel()`, schedule the progress mark:

```go
		markAt := sc.LastFaultEnd()
		go func() {
			select {
			case <-ctx.Done():
			case <-time.After(markAt):
				p.Mark()
				t.Logf("progress mark at %v (last fault over)", markAt)
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
		obs.FetchErrors = r.SegmentErrors + r.ProbeErrors + r.PlaylistErrors
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
		t.Logf("player: since mark %v, total %v, audioHoleBy=%v probeErrors=%d playlistErrors=%d start=%v",
			r.SegmentsSinceMark, r.RenditionSegments, r.AudioHoleBy, r.ProbeErrors, r.PlaylistErrors, obs.StartLatency.Round(100*time.Millisecond))
		for _, pr := range c.Problems {
			t.Logf("consistency: %s", pr)
		}
		t.Logf("player: renditions=%v excess=%.2fs audioHole=%v avSkew=%v probed=%d/%d",
			r.Renditions, r.TimelineExcess, r.MaxAudioHole, r.MaxAVStartSkew, r.ProbedAudio, r.ProbedVideo)
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
  minSegmentsAfterFault: 3
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
  minSegmentsAfterFault: 3
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
  maxAudioHoleMs: 50
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
  noFetchErrors: true
  sessionSurvives: true
  sequenceMonotonic: true
  maxFfmpegRestarts: 0
  maxTimelineExcessSeconds: 1.5
  minTimelineExcessSeconds: -1.5
  maxAudioHoleMs: 50
  maxAVStartSkewMs: 100
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
```

Append to `transcoder-restart.yaml`'s `expect:` block:

```yaml
  renditionsMonotonic: true
  minSegmentsAfterFault: 3
```

Append to `baseline.yaml` and `baseline-1080i.yaml`'s `expect:` blocks (start latency guard for Task 5's larger probe):

```yaml
  maxStartSeconds: 10
```

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
- Create: `server/scripts/ffmpeg51` (executable)
- Create: `server/scripts/ffmpeg-soak` (executable; Task 13 uses it too)
- Create: `server/internal/e2e/otaclips_test.go`
- Create: `server/internal/e2e/rebases_test.go`
- Modify: `server/internal/transcode/ffmpeg.go` (`liveInputArgs`, or the video/audio filters for candidate C)
- Test: `server/internal/transcode/ffmpeg_test.go`
- Modify: `server/internal/e2e/testdata/scenarios/{ptsjump,ptsjump-ptsonly,splice,brief-dropout,threshold-dropout,baseline}.yaml`
- Modify: `docs/dev/fake-hdhomerun.md` (running the suite on FFmpeg 5.1, in the image, and on OTA clips)

**Interfaces:**
- Consumes: `liveInputArgs()` and `liveAudioFilter` (Task 5); the scenarios and `runScenario` (Task 4).
- Produces:
  - `server/scripts/ffmpeg51` is a drop-in `BOWTIE_FFMPEG_PATH` that runs `debian:bookworm-slim`'s FFmpeg 5.1.9 in Docker. Its environment variables:
    - `FF51_PRE` adds args first.
    - `FF51_PRE_OUTPUT` adds args before the final (output) argument.
    - `FF51_LOGLEVEL` replaces the `-loglevel` value.
    - `FF51_LOGDIR`, when set, appends every stderr line matching `timestamp discontinuity|frames duplicated|Invalid frame dimensions|\[(warning|error|fatal)\]` to `$FF51_LOGDIR/ffmpeg-<pid>.log`, each prefixed with the Unix time in milliseconds.

    It refuses the fd-3 caption input. The production-layout scenarios and the clips therefore run on 5.1 inside the production image (Step 4), not through the wrapper.
  - `server/scripts/ffmpeg-soak` is an in-image FFmpeg wrapper for test and soak images only, never production:
    - It raises `-loglevel warning` to `level+debug`, because 5.1 logs "timestamp discontinuity" at debug level.
    - It keeps only the matching stderr lines.
    - When `FF51_LOGDIR` is set, it writes them, Unix-ms prefixed, the same way `ffmpeg51` does. `rebasesSince` reads both.
  - `func rebasesSince(t testing.TB, start time.Time) []rebase` (`rebases_test.go`, build tag `ffmpeg`) returns `type rebase struct { At time.Duration; Line string }`. These are the "timestamp discontinuity" lines in `FF51_LOGDIR` after `start`, offset from it.
  - `TestOTAClips` (build tag `ffmpeg`) plays each real OTA clip from `BOWTIE_OTA_CLIPS` through the production layout. It skips when the variable is unset. The other variables:
    - `BOWTIE_OTA_CLIP_FILES`: an optional comma list of base names;
    - `BOWTIE_OTA_FOR`: the per-clip cap, default `10m`.
  - The chosen option, recorded in `liveInputArgs` (or the filters) and its comment.

**Candidates.** Each is run with Task 5's argv:
- **A**: `-dts_delta_threshold 1`. It is likely to re-base the ±1.5–2.5 s splices, which fails the gate.
- **A′**: `-dts_delta_threshold 2.6`, above every splice step and below the smallest `ptsjump` step (+3 s).
- **C**: `-fps_mode:v vfr` plus a hole-closing clamp:
  - video `setpts='if(eq(N\,0)\,PTS\,PREV_OUTPTS+clip(PTS-PREV_INPTS\,1\,0.5/TB))'`, placed as the first filter of each rung's chain;
  - audio `asetpts='if(eq(N\,0)\,PTS\,PREV_OUTPTS+clip(PTS-PREV_INPTS\,0\,0.5/TB))'`, placed ahead of `aresample`.

  The clamp removes any step over 0.5 s without duplicating frames or logging a re-base, while `aresample` still fills smaller audio holes. A step clamped in one stream is clamped in the other at the same point, because both see the same step.

  C's exact filter-graph placement is written in Step 4 if C wins. A smoke test (Step 1) proves the expressions parse on 5.1 first.

**The gate.** A candidate passes only if every item below holds on FFmpeg 5.1.

| Check | Required |
|---|---|
| `ptsjump`, `ptsjump-ptsonly` (+3/+6/+9 s) | every expectation, including \|excess\| ≤ 2 s (no inflation), ≤ 50 ms audio holes, ≤ 100 ms A/V skew, no restarts, post-fault progress |
| no duplicate storm | zero `frames duplicated` lines in `FF51_LOGDIR` for these runs |
| `splice` (±1.5–2.5 s steps) | every expectation (\|excess\| ≤ 1.5 s, audio holes, skew), **and zero `timestamp discontinuity` lines** during the run |
| `brief-dropout`, `threshold-dropout`, `baseline` | every expectation |
| OTA clips | no restarts, excess ≤ +2 s, audio holes ≤ 50 ms, skew ≤ 100 ms, post-fault progress. Every re-base must fall within ±10 s of a logged signal dip. A re-base anywhere else (an ad break) is a false re-base and fails the candidate |

**Choosing:**
- If several candidates pass, prefer C. It is the `fps_mode` family, which the spec prefers ("less magic").
- Between A and A′, prefer A′ (it doesn't fire on splices).
- If none passes, stop. Report the full table (candidate × check, with values and re-base counts). The splice requirements may conflict: zero re-bases and no inflation on a pure timestamp step. That is the controller's call, not this task's. Don't loosen a threshold, and don't pick a "least bad" option.
- There is no later "raise the threshold" step: the Task 13 soak confirms the choice or sends it back here.

- [ ] **Step 1: Create the wrapper and smoke-test 5.1**

Create `server/scripts/ffmpeg51` and `chmod +x` it:

```bash
#!/usr/bin/env bash
# ffmpeg51: FFmpeg 5.1 from Debian bookworm (the production image's FFmpeg),
# run in Docker as a drop-in BOWTIE_FFMPEG_PATH for e2e tests on a Mac:
#
#   BOWTIE_FFMPEG_PATH=$PWD/scripts/ffmpeg51 go test -tags ffmpeg ./internal/e2e/
#
# Env:
#   FF51_PRE         extra args placed first (global or input options)
#   FF51_PRE_OUTPUT  extra args placed before the final (output) argument
#   FF51_LOGLEVEL    replaces the value after -loglevel (e.g. level+debug)
#   FF51_LOGDIR      append re-base/dup/warning lines, prefixed with Unix ms,
#                    to $FF51_LOGDIR/ffmpeg-<pid>.log (stderr still passes through)
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
if [ -n "${FF51_PRE:-}" ]; then
  # shellcheck disable=SC2206 # word splitting is the point
  args+=($FF51_PRE)
fi
n=$#
i=0
prev=""
for a in "$@"; do
  i=$((i + 1))
  case "$a" in
  *'pipe\:3'*)
    echo "ffmpeg51: the caption input on fd 3 can't run through Docker" >&2
    exit 2
    ;;
  esac
  if [ "$prev" = "-loglevel" ] && [ -n "${FF51_LOGLEVEL:-}" ]; then
    a=$FF51_LOGLEVEL
  fi
  if [ "$i" -eq "$n" ] && [ -n "${FF51_PRE_OUTPUT:-}" ] && [ "$n" -gt 1 ]; then
    # shellcheck disable=SC2206
    args+=($FF51_PRE_OUTPUT)
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
  exec 8> >(perl -MTime::HiRes=time -ne '
    BEGIN { $| = 1; open(L, ">>", shift @ARGV) or die "ffmpeg51: log: $!"; select((select(L), $| = 1)[0]) }
    print STDERR $_;
    printf L "%d %s", time() * 1000, $_ if /timestamp discontinuity|frames duplicated|Invalid frame dimensions|\[(warning|error|fatal)\]/;
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

Smoke tests:

Run: `cd server && scripts/ffmpeg51 -version | head -1`
Expected: `ffmpeg version 5.1.9-0+deb12u1 …` (the first run builds the image).

Run the hardened argv, every candidate's options and the caption `movie` syntax on 5.1:

```bash
cd server && d="${TMPDIR%/}/ff51-flags" && mkdir -p "$d" && \
scripts/ffmpeg51 -hide_banner -loglevel error -f lavfi -i testsrc=d=2:s=320x240 -f lavfi -i sine=d=2 \
  -c:v mpeg2video -c:a ac3 -f mpegts "$d/in.ts" && \
for opt in "-dts_delta_threshold 1" "-dts_delta_threshold 2.6"; do
  scripts/ffmpeg51 -hide_banner -loglevel warning -nostats $opt \
    -thread_queue_size 1024 -probesize 20M -analyzeduration 10M -fflags +discardcorrupt+genpts \
    -err_detect ignore_err -i pipe:0 -map 0:v -map 0:a -c:v libx264 -c:a:0 aac -ac:a:0 2 -b:a:0 96k \
    -filter:a:0 aresample=async=1:min_hard_comp=0.02:first_pts=0 -f null - < "$d/in.ts" || exit 1
done && \
scripts/ffmpeg51 -hide_banner -loglevel warning -nostats -i "$d/in.ts" -map 0:v -map 0:a -fps_mode:v vfr \
  -filter:v "setpts='if(eq(N\,0)\,PTS\,PREV_OUTPTS+clip(PTS-PREV_INPTS\,1\,0.5/TB))'" \
  -filter:a "asetpts='if(eq(N\,0)\,PTS\,PREV_OUTPTS+clip(PTS-PREV_INPTS\,0\,0.5/TB))',aresample=async=1:min_hard_comp=0.02:first_pts=0" \
  -c:v libx264 -c:a aac -f null - && \
scripts/ffmpeg51 -hide_banner -loglevel warning -thread_queue_size 1024 -data_field first -f lavfi \
  -i "movie='$d/in.ts':format_opts='probesize=20M\:analyzeduration=10M'[out0+subcc]" -map 0:v -f null - && echo FLAGS-OK
```

Expected: `FLAGS-OK`, with no `Unrecognized option`, `Option not found` or expression parse errors. If C's expressions don't parse on 5.1, C is out of the gate. Record that and continue with A and A′.

Kill test: the container must die when the wrapper is SIGKILLed.

```bash
cd server && (scripts/ffmpeg51 -hide_banner -f lavfi -re -i testsrc -f null - & pid=$!; sleep 3; kill -9 $pid; sleep 2; docker ps --filter name=ffmpeg51- --format '{{.Names}}' | wc -l)
```

Expected: `0`.

Log test:

```bash
cd server && L="${TMPDIR%/}/ff51-logtest" && rm -rf "$L" && FF51_LOGDIR="$L" scripts/ffmpeg51 -hide_banner -loglevel warning \
  -f lavfi -i "testsrc=d=1" -vf "setpts=PTS+if(gt(N\,10)\,5/TB\,0)" -f null - ; cat "$L"/*.log | head -3
```

Expected: at least one line starting with a 13-digit Unix-ms stamp (a warning about the timestamps); none if FFmpeg prints no warning for this input. In that case check that `"$L"/ffmpeg-*.log` exists and is empty.

- [ ] **Step 2: Write the re-base reader and the OTA clip test**

Create `server/internal/e2e/rebases_test.go`:

```go
//go:build ffmpeg

package e2e

import (
	"bufio"
	"os"
	"path/filepath"
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

// rebasesSince reads FF51_LOGDIR (see scripts/ffmpeg51) for timestamp
// re-bases logged after start. Nil when FF51_LOGDIR is unset.
func rebasesSince(t testing.TB, start time.Time) []rebase {
	t.Helper()
	dir := os.Getenv("FF51_LOGDIR")
	if dir == "" {
		return nil
	}
	files, _ := filepath.Glob(filepath.Join(dir, "ffmpeg-*.log"))
	var out []rebase
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			ms, rest, ok := strings.Cut(sc.Text(), " ")
			v, err := strconv.ParseInt(ms, 10, 64)
			if !ok || err != nil || !strings.Contains(rest, "timestamp discontinuity") {
				continue
			}
			if at := time.UnixMilli(v); !at.Before(start) {
				out = append(out, rebase{At: at.Sub(start), Line: rest})
			}
		}
		_ = fh.Close()
	}
	return out
}

func TestRebasesSince(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FF51_LOGDIR", dir)
	start := time.UnixMilli(1_760_000_000_000)
	body := "1759999999000 [mpegts @ 0x1] timestamp discontinuity (stream id=0): -270000, new offset= 0\n" +
		"1760000002500 [mpegts @ 0x1] timestamp discontinuity (stream id=0): 270000, new offset= -270000\n" +
		"1760000003000 [warning] frames duplicated\n"
	if err := os.WriteFile(filepath.Join(dir, "ffmpeg-1.log"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := rebasesSince(t, start)
	if len(got) != 1 || got[0].At != 2500*time.Millisecond {
		t.Fatalf("rebases %+v", got)
	}
}
```

In `runScenario`, record the start time and log re-bases at the end (scenarios read them in the gate). Add next to the `startAt := time.Now()` line from Task 4:

```go
	defer func() {
		for _, rb := range rebasesSince(t, startAt) {
			t.Logf("rebase at +%v: %s", rb.At.Round(100*time.Millisecond), rb.Line)
		}
	}()
```

Create `server/internal/e2e/otaclips_test.go`:

```go
//go:build ffmpeg

package e2e

import (
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

// TestOTAClips plays real over-the-air captures through the production
// layout. The clips are never committed: BOWTIE_OTA_CLIPS names their
// directory (skipped when unset); BOWTIE_OTA_CLIP_FILES picks some
// (comma-separated base names); BOWTIE_OTA_FOR caps each run (default 10m).
// Re-base lines are logged with their offset into the clip; the gate
// (Task 6) judges them against the capture's signal log.
func TestOTAClips(t *testing.T) {
	dir := os.Getenv("BOWTIE_OTA_CLIPS")
	if dir == "" {
		t.Skip("BOWTIE_OTA_CLIPS not set")
	}
	maxFor := 10 * time.Minute
	if v := os.Getenv("BOWTIE_OTA_FOR"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatal(err)
		}
		maxFor = d
	}
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
	for _, clip := range clips {
		clip := clip
		t.Run(filepath.Base(clip), func(t *testing.T) {
			d := clipDuration(t, clip) - 5*time.Second
			if d > maxFor {
				d = maxFor
			}
			if d < 30*time.Second {
				t.Skipf("clip shorter than 35 s")
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
  minSegmentsAfterFault: 5
  noFetchErrors: true
`, strings.TrimSuffix(filepath.Base(clip), ".ts"), clip, d)))
			if err != nil {
				t.Fatal(err)
			}
			runScenario(t, sc)
		})
	}
}

// clipDuration is the clip's container duration (ffprobe).
func clipDuration(t *testing.T, path string) time.Duration {
	t.Helper()
	out, err := exec.Command(ffprobePath(), "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path).Output()
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

With no timeline steps, `LastFaultEnd` is 0, so `minSegmentsAfterFault` covers the whole run.

`tsloop.Load` reads a whole clip into memory. Before running a clip over 1 GiB (for example `ch9.1.ts`, a one-hour clip with ad breaks), cut it into pieces of 1 GiB or less around the ad breaks, into the scratchpad:

```bash
ffmpeg -ss <start> -t <len> -i ch9.1.ts -map 0 -c copy <scratch>/ch9.1-partN.ts
```

Point `BOWTIE_OTA_CLIPS` at that folder for the run.

Run: `cd server && go vet -tags ffmpeg ./internal/e2e/ && go test -tags ffmpeg -run 'TestRebasesSince|TestOTAClips' -v ./internal/e2e/`
Expected: `TestRebasesSince` PASS; `TestOTAClips` SKIP (`BOWTIE_OTA_CLIPS not set`).

Commit the tools before the experiment:

```bash
git add server/scripts/ffmpeg51 server/internal/e2e/rebases_test.go server/internal/e2e/otaclips_test.go server/internal/e2e/scenarios_test.go
git commit -m "test(e2e): FFmpeg 5.1 wrapper, re-base log readers and OTA clip runs

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

- [ ] **Step 3: Run the gate, scenario half (wrapper, default layout)**

For each candidate, run the scenarios on FFmpeg 5.1 with debug logging and a fresh log directory. Candidate C needs the clamp filters inside Bowtie's filter graph, which `FF51_PRE_OUTPUT` can't add. Run C on a scratch branch (`git switch -c scratch/gate-c`) where `BuildArgs` adds them (the C code in Step 6), then discard the branch.

```bash
cd server
for v in base A A2 C; do
  pre=""
  case $v in
  A) pre="-dts_delta_threshold 1" ;;
  A2) pre="-dts_delta_threshold 2.6" ;;
  C) git switch -q scratch/gate-c ;;
  esac
  L="${TMPDIR%/}/gate-$v"; rm -rf "$L"; mkdir -p "$L"
  FF51_LOGDIR="$L/ff" FF51_LOGLEVEL=level+debug FF51_PRE="$pre" BOWTIE_FFMPEG_PATH="$PWD/scripts/ffmpeg51" \
    go test -count=1 -tags ffmpeg -p 1 -parallel 1 -timeout 120m \
    -run 'TestScenarios/(ptsjump|ptsjump-ptsonly|splice|brief-dropout|threshold-dropout|baseline)$' -v ./internal/e2e/ \
    > "$L/scenarios.log" 2>&1
  [ "$v" = C ] && git switch -q -
  echo "== $v"
  grep -E -- '--- (PASS|FAIL)|observed:|player: renditions|audioHoleBy=|rebase at|xfail \(|known gap|unexpected' "$L/scenarios.log"
  echo "frames duplicated: $(cat "$L"/ff/*.log 2>/dev/null | grep -c 'frames duplicated')"
done
```

`-parallel 1` keeps each scenario's re-base lines apart, because `rebasesSince` reads every log written after the scenario's start. The xfail markers are still in place, so judge the gate's checks from the `observed:` lines. Ignore the xfail verdicts.

- [ ] **Step 4: Run the gate, image half (production layout with captions, and the OTA clips)**

Create `server/scripts/ffmpeg-soak` and `chmod +x` it:

```bash
#!/bin/bash
# ffmpeg-soak: FFmpeg for test and soak images only (never production).
# Raises "-loglevel warning" to level+debug so FFmpeg 5.1's "timestamp
# discontinuity" lines (debug level in 5.1) are logged, and keeps only the
# lines that the gate and the soak count. With FF51_LOGDIR set, also writes
# them prefixed with Unix ms to $FF51_LOGDIR/ffmpeg-<pid>.log (as ffmpeg51).
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
fi
exec /usr/bin/ffmpeg "${args[@]}" 2> >(
  while IFS= read -r line; do
    case "$line" in
    *"timestamp discontinuity"* | *"frames duplicated"* | *"Invalid frame dimensions"* | *"[warning]"* | *"[error]"* | *"[fatal]"*)
      printf '%s\n' "$line" >&2
      if [ -n "$log" ]; then printf '%s %s\n' "$(date +%s%3N)" "$line" >>"$log"; fi
      ;;
    esac
  done
)
```

Commit it:

```bash
git add server/scripts/ffmpeg-soak
git commit -m "test: in-image FFmpeg wrapper that logs 5.1's timestamp re-bases

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

Build the image and a Linux e2e test binary once per candidate. For A, A′ and C, check out a scratch branch whose `liveInputArgs` (or filters) carry that candidate (Step 6's code), build, run, and discard the branch:

```bash
cd "$(git rev-parse --show-toplevel)"
clips=<folder with the chosen clips>   # this session: .../scratchpad/ota/ts (or the cut pieces)
for v in base A A2 C; do
  # git switch -q scratch/gate-$v   (A, A2, C: the candidate's argv committed there)
  docker build -q -f deploy/Dockerfile -t bowtie:e2e51 . >/dev/null
  arch=$(docker image inspect bowtie:e2e51 --format '{{.Architecture}}')
  (cd server && GOOS=linux GOARCH="$arch" CGO_ENABLED=0 go test -c -tags ffmpeg -o "${TMPDIR%/}/e2e51.test" ./internal/e2e/)
  L="${TMPDIR%/}/gate-$v"; mkdir -p "$L/img"
  docker run --rm --entrypoint /work/e2e51.test \
    -v "${TMPDIR%/}/e2e51.test:/work/e2e51.test:ro" -v "$PWD/server/internal/e2e:/src/e2e:ro" -w /src/e2e \
    -v "$PWD/server/scripts/ffmpeg-soak:/usr/local/bin/ffmpeg-soak:ro" -v "$clips:/clips:ro" -v "$L/img:/logs" \
    -e BOWTIE_FFMPEG_PATH=/usr/local/bin/ffmpeg-soak -e BOWTIE_FFPROBE_PATH=/usr/bin/ffprobe -e HOME=/tmp \
    -e FF51_LOGDIR=/logs/ff -e BOWTIE_OTA_CLIPS=/clips -e BOWTIE_OTA_FOR=60m \
    bowtie:e2e51 -test.run 'TestScenarios/(production-baseline|corrupt-audio)$|TestOTAClips' -test.v -test.parallel 1 -test.timeout 300m \
    > "$L/img.log" 2>&1
  # git switch -q -
  echo "== $v"; grep -E -- '--- (PASS|FAIL)|observed:|audioHoleBy=|rebase at|consistency:' "$L/img.log"
done
```

Choosing the clips: pick them at run time from what was captured.
- Include the clips with logged signal dips: 2.2 around 21:54:16–26 local CDT, and 4.1 at 22:13:27 and 22:14:58.
- Include the parts of 9.1 that cover ad breaks.

Judging each clip re-base:
1. Add its `+offset` to the clip's capture start (the `start` line in `ota/log/progress.log`) to get local time.
2. Compare that time with the dips in `ota/log/ch<N>.signal.tsv`.
3. A re-base within ±10 s of a dip is true; any other is false.

Fill in the gate table for the task report, one row per candidate:
- excess, audio holes (per rendition) and skew for each scenario;
- `timestamp discontinuity` lines during `splice`;
- `frames duplicated` lines;
- production-layout results on 5.1;
- per clip: true and false re-bases;
- pass/fail.

Apply the choosing rule above.

- [ ] **Step 5: Write the failing test for the chosen option**

For **A′** (or A), add to `TestBuildArgsLiveInputHardening` right after the first `mustContainSeq`:

```go
	mustContainSeq(t, live, "-dts_delta_threshold", "2.6", "-thread_queue_size", "1024") // "1" for A
```

and add `a == "-dts_delta_threshold" ||` to the URL/VOD loop's live-only condition. Update the goldens:

```bash
cd server && perl -i -pe 's/"-thread_queue_size", "1024", "-probesize", "20M",/"-dts_delta_threshold", "2.6", "-thread_queue_size", "1024", "-probesize", "20M",/' internal/transcode/ffmpeg_test.go
```

For **C**, add instead:

```go
	mustContainSeq(t, live, "-fps_mode:v", "vfr")
	if !strings.Contains(strings.Join(live, " "), "setpts='if(eq(N\\,0)\\,PTS\\,PREV_OUTPTS+clip(PTS-PREV_INPTS\\,1\\,0.5/TB))'") {
		t.Fatalf("video clamp missing: %q", live)
	}
	mustContainSeq(t, live, "-filter:a:0", "asetpts='if(eq(N\\,0)\\,PTS\\,PREV_OUTPTS+clip(PTS-PREV_INPTS\\,0\\,0.5/TB))',"+liveAudio)
```

Then put `"-fps_mode:v", "vfr",` in each `TestBuildArgsStdinPipeAcrossBackends` golden right after `"-profile:v", "high",`, and change each golden's `-filter:a:0` value the same way.

Run: `cd server && go test ./internal/transcode/ -run TestBuildArgs -v`
Expected: FAIL (option missing).

- [ ] **Step 6: Implement the chosen option**

For **A′** (A: the same with `"1"`):

```go
// liveInputArgs harden the live pipe (dirty ATSC transport streams).
// dts_delta_threshold 2.6: a timestamp step over 2.6 s (a corrupt PES,
// a dropout) is re-based instead of filled with duplicate frames, while ad
// splices (steps up to 2.5 s) are left alone; chosen on FFmpeg 5.1 by the
// ptsjump/splice/dropout scenarios and real OTA clips (Task 6 gate).
// genpts fills missing timestamps, ignore_err keeps decoding past damaged
// frames, the larger probe still finds every PMT stream when the first
// seconds are damaged, and the thread queue absorbs the burst after a stall.
func liveInputArgs() []string {
	return []string{
		"-dts_delta_threshold", "2.6",
		"-thread_queue_size", "1024", "-probesize", "20M", "-analyzeduration", "10M",
		"-fflags", "+discardcorrupt+genpts", "-err_detect", "ignore_err",
	}
}
```

For **C**, leave `liveInputArgs` as it is and add to `ffmpeg.go`:

```go
// liveVideoClamp and liveAudioClamp close timestamp steps over 0.5 s (a
// corrupt PES, a splice, a dropout) frame by frame, in both streams at the
// same point, so the output neither duplicates frames nor keeps a hole;
// chosen on FFmpeg 5.1 by the Task 6 gate.
const (
	liveVideoClamp = "setpts='if(eq(N\\,0)\\,PTS\\,PREV_OUTPTS+clip(PTS-PREV_INPTS\\,1\\,0.5/TB))'"
	liveAudioClamp = "asetpts='if(eq(N\\,0)\\,PTS\\,PREV_OUTPTS+clip(PTS-PREV_INPTS\\,0\\,0.5/TB))'"
)
```

Then wire them in:
- `-fps_mode:v vfr` goes after the profile block when `s.Stdin != nil`.
- `liveVideoClamp` is the first filter of the video graph for live input: in the `-filter_complex` split for ladders, or `-vf` for one rung, ahead of the scaler.
- Every live audio filter becomes `liveAudioClamp + "," + liveAudioFilter`. That covers the AC-3 re-encode too, if Task 5 needed it.

Run: `cd server && go test ./internal/transcode/ && go test -tags ffmpeg ./internal/transcode/`
Expected: PASS.

- [ ] **Step 7: Remove the fixed xfails and add guards**

Delete the `xfail:`/`xfailOn:` lines from `ptsjump.yaml`, `ptsjump-ptsonly.yaml`, `splice.yaml`, `brief-dropout.yaml` and `threshold-dropout.yaml`. Add to `baseline.yaml`'s `expect:` block:

```yaml
  maxTimelineExcessSeconds: 2
  minTimelineExcessSeconds: -2
  maxAVStartSkewMs: 100
```

Run: `cd server && go test -count=1 -tags ffmpeg -p 1 -parallel 2 -timeout 30m -run TestScenarios ./internal/e2e/`
Expected: PASS (host FFmpeg).

Run: `cd server && BOWTIE_FFMPEG_PATH="$PWD/scripts/ffmpeg51" go test -count=1 -tags ffmpeg -p 1 -parallel 2 -timeout 60m -run 'TestScenarios/(ptsjump|ptsjump-ptsonly|splice|brief-dropout|threshold-dropout|baseline|transcoder-restart)$' ./internal/e2e/`
Expected: PASS (FFmpeg 5.1). The production-layout scenarios ran in the image in Step 4; re-run that command once with the chosen option (no scratch branch) and expect `production-baseline` and `corrupt-audio` to PASS. `stuck-recovers` and `long-outage-restart` keep their xfails until Tasks 7 and 10.

- [ ] **Step 8: Document the 5.1 runs**

Add to `docs/dev/fake-hdhomerun.md`, in the section on running the scenario suite:

```markdown
### On FFmpeg 5.1 (the production image's FFmpeg)

`server/scripts/ffmpeg51` runs Debian bookworm's FFmpeg 5.1 in Docker as a
drop-in for `BOWTIE_FFMPEG_PATH` (software encode):

    cd server
    BOWTIE_FFMPEG_PATH=$PWD/scripts/ffmpeg51 go test -tags ffmpeg -p 1 -parallel 2 -timeout 60m -run TestScenarios ./internal/e2e/

`FF51_PRE`, `FF51_PRE_OUTPUT` and `FF51_LOGLEVEL` add or change arguments;
`FF51_LOGDIR` collects re-base and duplicate lines with timestamps
(scenarios then log `rebase at +…`). Captions (fd 3) can't cross into the
container, so scenarios with `bowtie: {layout: production}` run inside the
production image instead: build `deploy/Dockerfile`, cross-compile the e2e
test binary (`go test -c -tags ffmpeg`) and run it in the image with
`BOWTIE_FFMPEG_PATH=/usr/bin/ffmpeg` (see the stream resilience plan, Task 6).

### On real over-the-air clips

`TestOTAClips` plays captured `.ts` files through the production layout.
The clips stay out of the repo: point `BOWTIE_OTA_CLIPS` at their folder
(optionally `BOWTIE_OTA_CLIP_FILES=a.ts,b.ts` and `BOWTIE_OTA_FOR=60m`).
Clips are loaded into memory; cut clips over 1 GiB first.
```

- [ ] **Step 9: Commit**

```bash
git add server/internal/transcode server/internal/e2e/testdata/scenarios docs/dev/fake-hdhomerun.md
git commit -m "fix(transcode): <chosen option> on the live pipe, gated on FFmpeg 5.1

Gate (ptsjump PTS+DTS and PTS-only at +3/+6/+9 s, splice with zero
re-bases, dropouts, real OTA clips):
<candidate x check table from Step 3>

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

Fill the subject's `<chosen option>` and the table from Step 3 before committing. These are run-time results, not placeholders in the plan: for example, `re-base timestamp steps over 2.6 s instead of duplicating frames`.

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

### Task 9: Bowtie-owned timeline: a server-side observer (pure core)

**Files:**
- Create: `server/internal/stream/hlsplaylist.go`
- Create: `server/internal/stream/timeline.go`
- Test: `server/internal/stream/timeline_test.go`

**Design:**
- **Stamping isn't driven by client requests.** A per-session observer reads every producer playlist (every name in `Layout.Renditions()`) on its own clock, whether or not anyone asks for it. It reads every 500 ms (`Run`), at session start, and right before an append restart (`MarkRestart`). Serving only reads the observer's snapshot (`Retime`, `Window`). It never stamps, never reads segment files, and never stats `.vtt` files or `.`.
- **Runs** are FFmpeg processes: run 0 at session start, plus one per append restart.
  - `MarkRestart` first observes everything the dead process wrote, then opens the next run.
  - Every segment first observed after that belongs to the new run. A rendition whose playlist was missing or silent never falls back to run 0: its first segment simply belongs to the run that is current when it appears.
- **Per-rendition durations, one shared broadcast clock.** A run has one wall-clock start, `run.start`. A segment's PDT is `run.start + media + shift(media)`:
  - `media` is the sum of **that rendition's own** EXTINF durations before the segment in its run. Within a rendition, PDTs therefore chain on its own durations and can never overlap. Across renditions, the same broadcast moment maps to the same wall-clock time within one segment, because all of them are cut from the same media timeline.
  - `run.start` is set the first time any of its segments is observed. In each observation pass it is the earliest `mtime − EXTINF − media` among the run's new segments, so it doesn't depend on the order renditions are read. It is never earlier than the latest segment end stamped before it, so a restart's PDT never goes backwards.
  - `shift(media)` is the largest anchor at or before `media`. When a newly observed media segment's file says it started more than 2 s after its predicted PDT (an outage FFmpeg skipped), an anchor `{media, shift(media)+gap}` is added and logged. The PDT then jumps forward by the real gap, at the same media point for every rendition stamped from then on.
  - Taking the largest anchor, rather than the sum, means two renditions that see the same outage don't double it.
  - Stamps are permanent once made.
- **Discontinuity epochs are assigned session-wide.**
  - Run 0 is epoch 0. At `MarkRestart`, the ending run is classified:
    - It is **partial** if some rendition that produced output in the previous epoch produced nothing in the current epoch. That covers an audio-only run between two crashes, and a run that wrote nothing.
    - A partial run's epoch absorbs the next run, so the next run shares its epoch number.
    - At most `maxMergedRuns` (3) runs share an epoch, so a permanently dead rendition can't freeze the numbering.
  - `#EXT-X-DISCONTINUITY` goes on a rendition's first segment of each new epoch. `#EXT-X-DISCONTINUITY-SEQUENCE` equals the first served segment's epoch, minus one if that segment carries the tag. So each epoch has the same discontinuity number in every rendition.
  - The one cost: inside a merged epoch, the boundary between the partial run and the next run carries no tag in the renditions that have segments in both. That is at most one segment of audio with no matching video. The merge is logged.
- **Retention:** each rendition keeps the stamps of its own current window, which is everything at or after its playlist's MEDIA-SEQUENCE. Client polls play no part.
- **Stale or newer playlists at serve time:** `Retime` drops head segments that already slid out of the observed window, adjusting MEDIA-SEQUENCE, and ends the playlist before any segment the observer hasn't stamped yet. Nothing is restamped.
- **Captions:** `Retime(rung, captionPlaylist)` serves a caption playlist with the **rung's** stamps and tags, by sequence number. The caption segments share the rung's numbers, as `repairCaptionPlaylist` ensures. Caption URIs, `.` included, pass through untouched.

**Interfaces:**
- Produces (package `stream`):
  - `const WallClockLayout = "2006-01-02T15:04:05.000Z07:00"`
  - `const hlsSegmentDuration = 4 * time.Second` (live `-hls_time`; Task 12 reuses it)
  - `type MediaPlaylist struct { Header []string; MediaSequence int64; Segments []PlaylistSegment; Trailer []string }` and `type PlaylistSegment struct { Duration float64; Tags []string; URI string }`
  - `func ParseMediaPlaylist(raw []byte) MediaPlaylist` drops `#EXT-X-PROGRAM-DATE-TIME`, `#EXT-X-DISCONTINUITY` and `#EXT-X-DISCONTINUITY-SEQUENCE`.
  - `func NewTimeline(dir string, renditions []string, now func() time.Time) *Timeline`. `renditions` are the media playlists to observe (never captions). `now` must be the real clock, because stamps are compared with segment file mtimes.
  - `(*Timeline).Observe()`, `(*Timeline).Run(ctx context.Context)` (observes every `timelineObserveEvery` = 500 ms until ctx ends), and `(*Timeline).MarkRestart()`.
  - `(*Timeline).Retime(rendition string, raw []byte) []byte`, which also serves caption playlists through the rung's name.
  - `(*Timeline).Window(rendition string) WindowTimeline`, with `type WindowTimeline struct { MediaSequence int64; Segments []TimelineSegment }`, `type TimelineSegment struct { Start time.Time; Duration float64 }`, and `WindowStart()`/`LiveEdge() time.Time`.

- [ ] **Step 1: Write the failing tests**

Create `server/internal/stream/timeline_test.go`:

```go
package stream

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

var tl0 = time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC)

func sec(f float64) time.Time { return tl0.Add(time.Duration(f * float64(time.Second))) }

// seg is one segment: EXTINF and the file's mtime (seconds after tl0).
type seg struct{ dur, end float64 }

// writeRun writes name's playlist and segment files (with mtimes) and
// returns the playlist bytes.
func writeRun(t *testing.T, dir, name string, mseq int64, segs ...seg) []byte {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:%d\n", mseq)
	for i, s := range segs {
		uri := fmt.Sprintf("%s_%05d.ts", name, mseq+int64(i))
		path := filepath.Join(dir, uri)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
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

// chain is n segments of dur seconds ending at start+dur, start+2·dur, …
func chain(start, dur float64, n int) []seg {
	out := make([]seg, n)
	for i := range out {
		out[i] = seg{dur, start + dur*float64(i+1)}
	}
	return out
}

func newTestTimeline(dir string, renditions ...string) (*Timeline, *[]string) {
	var logs []string
	tl := NewTimeline(dir, renditions, func() time.Time { return sec(1000) })
	tl.logf = func(f string, a ...any) { logs = append(logs, fmt.Sprintf(f, a...)) }
	return tl, &logs
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

// served observes and returns rendition's served playlist, parsed.
func served(t *testing.T, tl *Timeline, rendition string, raw []byte) (int64, int64, []servedSeg) {
	t.Helper()
	return parseServed(t, tl.Retime(rendition, raw))
}

// dnum is the discontinuity number of the first served segment.
func dnum(dseq int64, segs []servedSeg) int64 {
	if len(segs) > 0 && segs[0].disc {
		return dseq + 1
	}
	return dseq
}

func TestTimelineStampsFromMtimeAndChains(t *testing.T) {
	dir := t.TempDir()
	tl, _ := newTestTimeline(dir, "v480")
	raw := writeRun(t, dir, "v480", 0, seg{4, 4}, seg{4, 8}, seg{4, 12.3})
	tl.Observe()
	_, _, segs := served(t, tl, "v480", raw)
	for i, want := range []time.Time{sec(0), sec(4), sec(8)} {
		if !segs[i].pdt.Equal(want) || segs[i].disc {
			t.Fatalf("segment %d: %+v, want PDT %v and no discontinuity", i, segs[i], want)
		}
	}
}

func TestTimelineJumpsForwardOverAGap(t *testing.T) {
	dir := t.TempDir()
	tl, logs := newTestTimeline(dir, "v480")
	writeRun(t, dir, "v480", 0, seg{4, 4}, seg{4, 8})
	tl.Observe()
	// 5 s without output (a dropout FFmpeg skipped), then segment 2.
	raw := writeRun(t, dir, "v480", 0, seg{4, 4}, seg{4, 8}, seg{4, 17})
	tl.Observe()
	_, _, segs := served(t, tl, "v480", raw)
	if !segs[2].pdt.Equal(sec(13)) || segs[2].disc {
		t.Fatalf("segment 2: %+v, want PDT %v without a discontinuity", segs[2], sec(13))
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "gap") {
		t.Fatalf("logs %v, want one gap line", *logs)
	}
}

// Audio (4.032 s) and video (4.004 s) are cut at different points: each
// chains on its own durations (no overlap), both from one run start.
func TestTimelineUnequalDurationsNeverOverlap(t *testing.T) {
	dir := t.TempDir()
	tl, logs := newTestTimeline(dir, "v480", "aac0")
	rv := writeRun(t, dir, "v480", 0, chain(0, 4.004, 10)...)
	ra := writeRun(t, dir, "aac0", 0, chain(0, 4.032, 10)...)
	tl.Observe()
	_, _, vs := served(t, tl, "v480", rv)
	_, _, as := served(t, tl, "aac0", ra)
	for i := 1; i < 10; i++ {
		if end := as[i-1].pdt.Add(4032 * time.Millisecond); as[i].pdt.Before(end) {
			t.Fatalf("audio segment %d starts %v before segment %d ends", i, end.Sub(as[i].pdt), i-1)
		}
		if d := as[i].pdt.Sub(vs[i].pdt); d < 0 || d > 500*time.Millisecond {
			t.Fatalf("segment %d: audio-video PDT %v, want within one segment's drift", i, d)
		}
	}
	if !as[0].pdt.Equal(vs[0].pdt) || len(*logs) != 0 {
		t.Fatalf("run start audio %v video %v, logs %v", as[0].pdt, vs[0].pdt, *logs)
	}
}

// Stamps don't depend on the order renditions are listed, read or served.
func TestTimelinePollOrderDoesNotMatter(t *testing.T) {
	dir := t.TempDir()
	t1, _ := newTestTimeline(dir, "v480", "aac0")
	t2, _ := newTestTimeline(dir, "aac0", "v480")
	writeRun(t, dir, "v480", 0, seg{4.004, 4.3}, seg{4.004, 8.2})
	writeRun(t, dir, "aac0", 0, seg{4.032, 4.1}, seg{4.032, 8.6})
	t1.Observe()
	t2.Observe()
	rv := writeRun(t, dir, "v480", 0, seg{4.004, 4.3}, seg{4.004, 8.2}, seg{4.004, 12.1})
	ra := writeRun(t, dir, "aac0", 0, seg{4.032, 4.1}, seg{4.032, 8.6}, seg{4.032, 12.2})
	t2.Observe()
	t1.Observe()
	a1, v1 := string(t1.Retime("aac0", ra)), string(t1.Retime("v480", rv))
	v2, a2 := string(t2.Retime("v480", rv)), string(t2.Retime("aac0", ra))
	if a1 != a2 || v1 != v2 {
		t.Fatalf("order changed the stamps:\n%s\n%s\n%s\n%s", a1, a2, v1, v2)
	}
}

// Two crashes; the middle run wrote audio only. Both renditions must give
// the third run the same discontinuity number.
func TestTimelineAudioOnlyMiddleRunKeepsEpochsAligned(t *testing.T) {
	dir := t.TempDir()
	tl, logs := newTestTimeline(dir, "v480", "aac0")
	writeRun(t, dir, "v480", 0, chain(0, 4, 3)...)
	writeRun(t, dir, "aac0", 0, chain(0, 4, 4)...) // audio one segment ahead
	tl.MarkRestart()
	writeRun(t, dir, "aac0", 0, append(chain(0, 4, 4), seg{4, 40})...) // run 1: audio only
	tl.MarkRestart()
	rv := writeRun(t, dir, "v480", 0, append(chain(0, 4, 3), seg{4, 64})...)
	ra := writeRun(t, dir, "aac0", 0, append(chain(0, 4, 4), seg{4, 40}, seg{4, 64})...)
	tl.Observe()
	_, dv, vs := served(t, tl, "v480", rv)
	_, da, as := served(t, tl, "aac0", ra)
	if !vs[3].disc || !as[4].disc || as[5].disc {
		t.Fatalf("tags: video %+v audio %+v", vs, as)
	}
	if dv != 0 || da != 0 {
		t.Fatalf("DISCONTINUITY-SEQUENCE video %d audio %d, want 0", dv, da)
	}
	if !vs[3].pdt.Equal(as[5].pdt) {
		t.Fatalf("run 2 starts at %v (video) and %v (audio)", vs[3].pdt, as[5].pdt)
	}
	// The windows slide to run 2: both say discontinuity 1 for it.
	rv = writeRun(t, dir, "v480", 3, seg{4, 64}, seg{4, 68})
	ra = writeRun(t, dir, "aac0", 5, seg{4, 64}, seg{4, 68})
	tl.Observe()
	_, dv, vs = served(t, tl, "v480", rv)
	_, da, as = served(t, tl, "aac0", ra)
	if dnum(dv, vs) != 1 || dnum(da, as) != 1 {
		t.Fatalf("run 2 discontinuity number: video %d audio %d, want 1 and 1", dnum(dv, vs), dnum(da, as))
	}
	if !strings.Contains(strings.Join(*logs, "\n"), "shares") {
		t.Fatalf("merge not logged: %v", *logs)
	}
}

// A rendition whose playlist first appears after a restart belongs to that
// run, never run 0: its first segment carries the run's epoch.
func TestTimelineLateRenditionNeverFallsBackToRunZero(t *testing.T) {
	dir := t.TempDir()
	tl, _ := newTestTimeline(dir, "v480", "aac0")
	writeRun(t, dir, "v480", 0, chain(0, 4, 2)...)
	tl.MarkRestart()
	rv := writeRun(t, dir, "v480", 0, append(chain(0, 4, 2), seg{4, 50})...)
	ra := writeRun(t, dir, "aac0", 0, seg{4, 50})
	tl.Observe()
	_, dv, vs := served(t, tl, "v480", rv)
	_, da, as := served(t, tl, "aac0", ra)
	if !vs[2].disc || as[0].disc || da != 1 || dv != 0 {
		t.Fatalf("video dseq %d %+v; audio dseq %d %+v", dv, vs, da, as)
	}
	if !as[0].pdt.Equal(vs[2].pdt) || as[0].pdt.Before(sec(8)) {
		t.Fatalf("late audio starts %v, video's run 1 %v", as[0].pdt, vs[2].pdt)
	}
}

func TestTimelineNeverGoesBackwardsAtRestart(t *testing.T) {
	dir := t.TempDir()
	tl, _ := newTestTimeline(dir, "v480")
	writeRun(t, dir, "v480", 0, chain(0, 4, 3)...)
	tl.MarkRestart()
	// The new run's first segment claims to end at 10 s, before the old run's 12 s end.
	raw := writeRun(t, dir, "v480", 0, append(chain(0, 4, 3), seg{4, 10})...)
	tl.Observe()
	_, _, segs := served(t, tl, "v480", raw)
	if !segs[3].disc || !segs[3].pdt.Equal(sec(12)) {
		t.Fatalf("restart segment %+v, want DISCONTINUITY at %v", segs[3], sec(12))
	}
}

func TestTimelineRestartWithoutSegmentsCountsOnce(t *testing.T) {
	dir := t.TempDir()
	tl, _ := newTestTimeline(dir, "v480")
	writeRun(t, dir, "v480", 0, chain(0, 4, 2)...)
	tl.MarkRestart() // this run dies before writing a segment
	tl.MarkRestart()
	raw := writeRun(t, dir, "v480", 0, append(chain(0, 4, 2), seg{4, 30})...)
	tl.Observe()
	_, _, segs := served(t, tl, "v480", raw)
	if !segs[2].disc || segs[1].disc {
		t.Fatalf("segments %+v, want one DISCONTINUITY on segment 2", segs)
	}
	raw = writeRun(t, dir, "v480", 3, seg{4, 34})
	tl.Observe()
	if _, dseq, _ := served(t, tl, "v480", raw); dseq != 1 {
		t.Fatalf("DISCONTINUITY-SEQUENCE %d, want 1", dseq)
	}
}

// Serving reads the snapshot only: newer segments wait for the observer,
// segments that slid out are dropped from the head; nothing is restamped.
func TestTimelineServesOnlyObservedSegments(t *testing.T) {
	dir := t.TempDir()
	tl, _ := newTestTimeline(dir, "v480")
	writeRun(t, dir, "v480", 0, chain(0, 4, 2)...)
	tl.Observe()
	newer := writeRun(t, dir, "v480", 0, chain(0, 4, 3)...) // not observed yet
	if _, _, segs := served(t, tl, "v480", newer); len(segs) != 2 {
		t.Fatalf("served %d segments, want the 2 observed", len(segs))
	}
	writeRun(t, dir, "v480", 2, seg{4, 12}, seg{4, 16})
	tl.Observe()
	mseq, _, segs := served(t, tl, "v480", newer) // stale: still starts at 0
	if mseq != 2 || len(segs) != 1 || !segs[0].pdt.Equal(sec(8)) {
		t.Fatalf("stale playlist served from %d: %+v", mseq, segs)
	}
}

// Caption playlists take the rung's stamps and tags by sequence number;
// their URIs (FFmpeg 5.1's "." included) pass through, and no .vtt is read.
func TestTimelineCaptionsUseRungStamps(t *testing.T) {
	dir := t.TempDir()
	tl, _ := newTestTimeline(dir, "v720")
	writeRun(t, dir, "v720", 0, chain(0, 4, 2)...)
	tl.MarkRestart()
	rv := writeRun(t, dir, "v720", 0, append(chain(0, 4, 2), seg{4, 30})...)
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
	tl, _ := newTestTimeline(dir, "v720")
	raw := []byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-DISCONTINUITY\n" +
		"#EXT-X-PROGRAM-DATE-TIME:2026-10-11T19:00:00.000+0000\n#EXTINF:4.004000,\nv720_00000.ts\n" +
		"#EXT-X-DISCONTINUITY\n#EXT-X-PROGRAM-DATE-TIME:2026-10-11T18:00:00.000+0000\n#EXTINF:4.004000,\nv720_00001.ts\n")
	if err := os.WriteFile(filepath.Join(dir, "v720.m3u8"), raw, 0o644); err != nil {
		t.Fatal(err)
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

// Each rendition keeps its own window's stamps, however fast the others
// slide and whoever polls what.
func TestTimelineRetainsEachRenditionsWindow(t *testing.T) {
	dir := t.TempDir()
	tl, _ := newTestTimeline(dir, "v480", "aac0")
	ra := writeRun(t, dir, "aac0", 0, chain(0, 4, 5)...) // stops updating (abandoned)
	for n := int64(0); n < 40; n++ {
		var segs []seg
		for i := n; i < n+5; i++ {
			segs = append(segs, seg{4, float64(4 * (i + 1))})
		}
		writeRun(t, dir, "v480", n, segs...)
		tl.Observe()
	}
	if _, _, as := served(t, tl, "aac0", ra); len(as) != 5 || !as[0].pdt.Equal(sec(0)) {
		t.Fatalf("audio window lost its stamps: %+v", as)
	}
	if n := len(tl.rends["v480"].segs); n > 5 {
		t.Fatalf("%d video stamps kept for a 5-segment window", n)
	}
}

func TestTimelineWindow(t *testing.T) {
	dir := t.TempDir()
	tl, _ := newTestTimeline(dir, "v480")
	writeRun(t, dir, "v480", 5, seg{4, 4}, seg{4, 8})
	tl.Observe()
	w := tl.Window("v480")
	if w.MediaSequence != 5 || len(w.Segments) != 2 || !w.WindowStart().Equal(sec(0)) || !w.LiveEdge().Equal(sec(8)) {
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd server && go test ./internal/stream/ -run 'TestTimeline|TestParseMediaPlaylist' -v`
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

Create `server/internal/stream/timeline.go`:

```go
package stream

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// WallClockLayout is how Bowtie writes wall-clock times (playlists and the
// timeline API): RFC 3339 with milliseconds.
const WallClockLayout = "2006-01-02T15:04:05.000Z07:00"

const (
	// hlsSegmentDuration is FFmpeg's -hls_time for live sessions.
	hlsSegmentDuration = 4 * time.Second
	// timelineGapThreshold: a segment whose file says it started this much
	// later than the timeline predicts follows a real gap (an outage FFmpeg
	// skipped); the timeline jumps forward instead of absorbing it.
	timelineGapThreshold = 2 * time.Second
	// timelineObserveEvery is how often the observer reads the playlists.
	timelineObserveEvery = 500 * time.Millisecond
	// maxMergedRuns bounds how many runs share one discontinuity epoch.
	maxMergedRuns = 3
)

// Timeline is a session's wall-clock record of its segments, built by an
// observer that reads every rendition playlist on its own clock (never on
// client requests). Each FFmpeg process is a run with one wall-clock start;
// a segment's PDT is that start plus its rendition's own media offset in
// the run (plus any forward jump over an outage), so renditions cut at
// different points never overlap and agree on the broadcast moment.
// Discontinuity epochs are session-wide.
type Timeline struct {
	dir        string
	renditions []string
	now        func() time.Time
	logf       func(format string, args ...any)

	obsMu sync.Mutex // serializes Observe and MarkRestart (file reads included)

	mu     sync.Mutex
	runs   []*tlRun
	rends  map[string]*tlRend
	maxEnd time.Time // latest end of any stamped segment
}

type tlRun struct {
	epoch    int64
	start    time.Time
	anchors  []tlAnchor
	produced map[string]bool
}

type tlAnchor struct {
	media float64       // seconds into the run's media timeline
	shift time.Duration // total forward jump from here on
}

// shiftAt is the largest anchor at or before media: renditions that see the
// same outage at slightly different points don't double it.
func (r *tlRun) shiftAt(media float64) time.Duration {
	var s time.Duration
	for _, a := range r.anchors {
		if a.media <= media+1e-9 && a.shift > s {
			s = a.shift
		}
	}
	return s
}

type tlRend struct {
	mseq     int64           // window start (MEDIA-SEQUENCE last observed)
	segs     map[int64]tlSeg // seq → stamp, the window only
	lastSeq  int64           // highest seq stamped (-1: none)
	lastRun  int             // run of lastSeq (-1: none)
	mediaEnd float64         // media offset at the end of lastSeq in lastRun
	bounds   []tlBound       // first segment of each run in this rendition
}

type tlSeg struct {
	run   int
	start time.Time
	dur   float64
}

type tlBound struct {
	run      int
	firstSeq int64
}

func seconds(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }

// NewTimeline returns the timeline of the session in dir. renditions are
// the media playlists (without .m3u8) to observe; now must be the real
// clock: stamps are compared with segment file mtimes.
func NewTimeline(dir string, renditions []string, now func() time.Time) *Timeline {
	return &Timeline{
		dir: dir, renditions: renditions, now: now, logf: log.Printf,
		runs:  []*tlRun{{produced: map[string]bool{}}},
		rends: map[string]*tlRend{},
	}
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

// Observe reads every rendition playlist and stamps the new segments.
func (t *Timeline) Observe() {
	t.obsMu.Lock()
	defer t.obsMu.Unlock()
	t.observe()
}

// MarkRestart closes the current run (after observing everything the dead
// process wrote) and opens the next one. Call it before an append restart
// starts FFmpeg.
func (t *Timeline) MarkRestart() {
	t.obsMu.Lock()
	defer t.obsMu.Unlock()
	t.observe()
	t.mu.Lock()
	defer t.mu.Unlock()
	cur := len(t.runs) - 1
	epoch := t.runs[cur].epoch + 1
	if missing := t.partialLocked(cur); missing != "" {
		epoch = t.runs[cur].epoch
		t.logf("stream: timeline %s: run %d produced no %s; the next run shares its discontinuity %d",
			filepath.Base(t.dir), cur, missing, epoch)
	} else if dead := t.deadLocked(cur); dead != "" {
		// The merge cap was hit: a rendition that stopped producing will
		// skip this epoch, so its numbering can differ from its siblings'.
		t.logf("stream: timeline %s: %s skipped discontinuity %d; numbering may differ across renditions",
			filepath.Base(t.dir), dead, t.runs[cur].epoch)
	}
	t.runs = append(t.runs, &tlRun{epoch: epoch, produced: map[string]bool{}})
}

// partialLocked names a rendition that produced output in the previous
// epoch but none in the current one ("" when the run is complete, run 0,
// or the epoch already spans maxMergedRuns runs).
func (t *Timeline) partialLocked(cur int) string {
	e := t.runs[cur].epoch
	if e == 0 {
		return ""
	}
	need, have := map[string]bool{}, map[string]bool{}
	size := 0
	for _, r := range t.runs {
		switch r.epoch {
		case e - 1:
			for n := range r.produced {
				need[n] = true
			}
		case e:
			size++
			for n := range r.produced {
				have[n] = true
			}
		}
	}
	if size >= maxMergedRuns {
		return ""
	}
	for _, n := range t.renditions {
		if need[n] && !have[n] {
			return n
		}
	}
	return ""
}

// deadLocked names a rendition that produced in the previous epoch but not
// in the current one, regardless of the merge cap ("" for run 0).
func (t *Timeline) deadLocked(cur int) string {
	e := t.runs[cur].epoch
	if e == 0 {
		return ""
	}
	need, have := map[string]bool{}, map[string]bool{}
	for _, r := range t.runs {
		for n := range r.produced {
			if r.epoch == e-1 {
				need[n] = true
			} else if r.epoch == e {
				have[n] = true
			}
		}
	}
	for _, n := range t.renditions {
		if need[n] && !have[n] {
			return n
		}
	}
	return ""
}

func (t *Timeline) rendLocked(name string) *tlRend {
	r := t.rends[name]
	if r == nil {
		r = &tlRend{segs: map[int64]tlSeg{}, lastSeq: -1, lastRun: -1}
		t.rends[name] = r
	}
	return r
}

type tlNew struct {
	rend  string
	seq   int64
	media float64
	dur   float64
	obs   time.Time // observed start (file mtime − EXTINF, or now − EXTINF)
	file  bool      // obs came from the segment file
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
	t.mu.Lock()
	defer t.mu.Unlock()
	t.applyLocked(lists)
}

func (t *Timeline) applyLocked(lists map[string]MediaPlaylist) {
	runIdx := len(t.runs) - 1
	run := t.runs[runIdx]
	now := t.now()
	var items []tlNew
	for _, name := range t.renditions {
		p, ok := lists[name]
		if !ok {
			continue
		}
		r := t.rendLocked(name)
		if p.MediaSequence < r.mseq {
			continue // a stale read: never go back
		}
		for i, sg := range p.Segments {
			seq := p.MediaSequence + int64(i)
			if seq <= r.lastSeq {
				continue
			}
			if r.lastRun != runIdx {
				r.bounds = append(r.bounds, tlBound{run: runIdx, firstSeq: seq})
				r.lastRun, r.mediaEnd = runIdx, 0
			} else if seq > r.lastSeq+1 {
				missed := seq - r.lastSeq - 1
				t.logf("stream: timeline %s: %s: %d segments slid out unobserved", filepath.Base(t.dir), name, missed)
				r.mediaEnd += float64(missed) * hlsSegmentDuration.Seconds()
			}
			it := tlNew{rend: name, seq: seq, media: r.mediaEnd, dur: sg.Duration}
			it.obs, it.file = t.observedStart(sg, now)
			items = append(items, it)
			r.mediaEnd += sg.Duration
			r.lastSeq = seq
			run.produced[name] = true
		}
		r.mseq = p.MediaSequence
		t.pruneLocked(r)
	}
	if len(items) == 0 {
		return
	}
	// Same order whatever the rendition order: by media time, then name.
	sort.Slice(items, func(i, j int) bool {
		if items[i].media != items[j].media {
			return items[i].media < items[j].media
		}
		return items[i].rend < items[j].rend
	})
	if run.start.IsZero() {
		start := items[0].obs.Add(-seconds(items[0].media))
		for _, it := range items[1:] {
			if s := it.obs.Add(-seconds(it.media)); s.Before(start) {
				start = s
			}
		}
		if start.Before(t.maxEnd) {
			start = t.maxEnd // a restart never starts before what came before it ended
		}
		run.start = start
	}
	for _, it := range items {
		shift := run.shiftAt(it.media)
		start := run.start.Add(seconds(it.media) + shift)
		if gap := it.obs.Sub(start); it.file && gap > timelineGapThreshold {
			run.anchors = append(run.anchors, tlAnchor{media: it.media, shift: shift + gap})
			t.logf("stream: timeline %s: %v gap before %s segment %d",
				filepath.Base(t.dir), gap.Round(100*time.Millisecond), it.rend, it.seq)
			start = it.obs
		}
		t.rends[it.rend].segs[it.seq] = tlSeg{run: runIdx, start: start, dur: it.dur}
		if e := start.Add(seconds(it.dur)); e.After(t.maxEnd) {
			t.maxEnd = e
		}
	}
}

// observedStart is when the segment's file says it started: its mtime (the
// end) minus EXTINF. Without a usable file it is now minus EXTINF.
func (t *Timeline) observedStart(sg PlaylistSegment, now time.Time) (time.Time, bool) {
	d := seconds(sg.Duration)
	if fi, err := os.Stat(filepath.Join(t.dir, sg.URI)); err == nil && !fi.ModTime().After(now) {
		return fi.ModTime().Add(-d), true
	}
	return now.Add(-d), false
}

// pruneLocked keeps r's stamps for its own window, and the run boundary
// just before it (tags need the previous run).
func (t *Timeline) pruneLocked(r *tlRend) {
	for seq := range r.segs {
		if seq < r.mseq {
			delete(r.segs, seq)
		}
	}
	keep := 0
	for i, b := range r.bounds {
		if b.firstSeq <= r.mseq {
			keep = i
		}
	}
	r.bounds = r.bounds[keep:]
}

// taggedLocked reports whether seq starts a new epoch in r.
func (t *Timeline) taggedLocked(r *tlRend, seq int64) bool {
	for i, b := range r.bounds {
		if b.firstSeq == seq {
			if i == 0 {
				return false // this rendition's first segment ever (or the oldest kept)
			}
			return t.runs[b.run].epoch > t.runs[r.bounds[i-1].run].epoch
		}
	}
	return false
}

// Retime rewrites one playlist (FFmpeg's bytes, URIs still bare) from the
// observer's snapshot of rendition: Bowtie's #EXT-X-PROGRAM-DATE-TIME on
// every segment, #EXT-X-DISCONTINUITY where a new epoch starts and
// #EXT-X-DISCONTINUITY-SEQUENCE. Segments that slid out of the observed
// window are dropped from the head; segments not observed yet end the
// playlist. Caption playlists are retimed with their rung's name (they
// share its sequence numbers); their URIs pass through.
func (t *Timeline) Retime(rendition string, raw []byte) []byte {
	p := ParseMediaPlaylist(raw)
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.rends[rendition]
	type out struct {
		PlaylistSegment
		seq int64
		st  tlSeg
	}
	var segs []out
	complete := true
	for i, sg := range p.Segments {
		seq := p.MediaSequence + int64(i)
		var st tlSeg
		ok := false
		if r != nil {
			st, ok = r.segs[seq]
		}
		if !ok {
			if r != nil && len(segs) == 0 && seq < r.mseq {
				continue // slid out since this playlist was read
			}
			complete = false
			break // not observed yet
		}
		segs = append(segs, out{sg, seq, st})
	}
	mseq, dseq := p.MediaSequence, t.runs[len(t.runs)-1].epoch
	if len(segs) > 0 {
		mseq, dseq = segs[0].seq, t.runs[segs[0].st.run].epoch
		if t.taggedLocked(r, segs[0].seq) {
			dseq--
		}
	}
	var b bytes.Buffer
	for _, h := range p.Header {
		b.WriteString(h)
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "#EXT-X-MEDIA-SEQUENCE:%d\n#EXT-X-DISCONTINUITY-SEQUENCE:%d\n", mseq, dseq)
	for _, s := range segs {
		if t.taggedLocked(r, s.seq) {
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

// Window is the observed window of rendition on the wall clock.
func (t *Timeline) Window(rendition string) WindowTimeline {
	t.mu.Lock()
	defer t.mu.Unlock()
	r := t.rends[rendition]
	if r == nil {
		return WindowTimeline{}
	}
	w := WindowTimeline{MediaSequence: r.mseq}
	for seq := r.mseq; ; seq++ {
		st, ok := r.segs[seq]
		if !ok {
			break
		}
		w.Segments = append(w.Segments, TimelineSegment{Start: st.start, Duration: st.dur})
	}
	return w
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd server && go test ./internal/stream/ -run 'TestTimeline|TestParseMediaPlaylist' -v -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add server/internal/stream/hlsplaylist.go server/internal/stream/timeline.go server/internal/stream/timeline_test.go
git commit -m "feat(stream): server-side timeline observer (shared broadcast clock, session-wide epochs)

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 10: Run the observer per session and serve every media playlist from its snapshot

**Files:**
- Modify: `server/internal/transcode/layout.go` (`Renditions`)
- Test: `server/internal/transcode/layout_test.go`
- Modify: `server/internal/stream/session.go`, `server/internal/stream/manager.go`
- Test: `server/internal/stream/manager_test.go`
- Modify: `server/internal/api/stream_handlers.go` (`StreamController`, `serveMediaPlaylist`)
- Test: `server/internal/api/stream_handlers_test.go`
- Modify: `server/internal/e2e/testdata/scenarios/{long-outage-restart,baseline,transcoder-restart}.yaml`

**Interfaces:**
- Consumes (Task 9): `NewTimeline`, `(*Timeline).Observe`, `Run`, `MarkRestart`, `Retime`.
- Produces:
  - `func (l Layout) Renditions() []string`: the media playlist names FFmpeg writes, without `.m3u8` (rungs, `aacN`, `ac3N`). Caption playlists aren't renditions: they're served through their rung.
  - `session.timeline *Timeline` and `session.timelineCancel func()`.
    - The timeline is created in `startAttempt` right after `waitPlaylist`, with `time.Now`, and observed once synchronously, so the first playlist request already has stamps.
    - `go tl.Run(ctx)` starts only once the session is registered. The duplicate-key abandon path never starts it.
    - `teardownSessionLocked` cancels it.
  - `restartSession` calls `sess.timeline.MarkRestart()` right before `m.runner.Start`. That runs outside `m.mu`, as `restartSession` already does. `MarkRestart` takes only the timeline's own locks.
  - `func (m *Manager) RetimePlaylist(viewerID, rendition string, raw []byte) ([]byte, bool)` reads the snapshot only.
  - `func (m *Manager) timelineOf(viewerID string) (tl *Timeline, top string, owner int64, ok bool)`; Task 11 uses it.
  - `api.StreamController` gains `RetimePlaylist(viewerID, rendition string, raw []byte) ([]byte, bool)`.
  - `serveMediaPlaylist` retimes after the caption repair and before URL rewriting. A caption playlist `<rung>_vtt.m3u8` is retimed as `<rung>`.
- Start latency:
  - A playlist request that arrives before the observer has stamped anything gets a playlist with no segments, and the player polls again.
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

Append to `server/internal/stream/manager_test.go`:

```go
// The session's observer starts with it, an append restart opens a new
// run (DISCONTINUITY on its first segment), serving reads the snapshot,
// and teardown stops the observer.
func TestSessionTimelineObservesAndMarksRestarts(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	m, _, _ := newTestManagerWithDial(st, cfg, clock, runner, nil)
	h, err := m.Start(context.Background(), user, chID, clientCaps(""))
	if err != nil {
		t.Fatal(err)
	}
	media, _ := m.SessionMediaOf(h.ViewerID)
	top := media.Layout.TopName()
	write := func(n int) []byte {
		var b strings.Builder
		b.WriteString("#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n")
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "#EXTINF:4.0,\n%s_%05d.ts\n", top, i)
		}
		if err := os.WriteFile(filepath.Join(h.SessionDir, top+".m3u8"), []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return []byte(b.String())
	}
	tl, _, _, ok := m.timelineOf(h.ViewerID)
	if !ok {
		t.Fatal("no timeline for the viewer")
	}
	write(2)
	tl.Observe()
	runner.LastProc().Crash(errors.New("boom"))
	deadline := time.Now().Add(3 * time.Second)
	for runner.Starts() < 2 && time.Now().Before(deadline) {
		clock.Advance(time.Second)
		m.maintain()
		time.Sleep(10 * time.Millisecond)
	}
	if runner.Starts() != 2 {
		t.Fatalf("starts = %d, want 2", runner.Starts())
	}
	raw := write(3)
	tl.Observe() // the background observer may already have; Observe is idempotent
	out, ok := m.RetimePlaylist(h.ViewerID, top, raw)
	s := string(out)
	i := strings.Index(s, "#EXT-X-DISCONTINUITY\n#EXT-X-PROGRAM-DATE-TIME:")
	if !ok || i < 0 || strings.Index(s, top+"_00001.ts") > i || !strings.Contains(s[i:], top+"_00002.ts") ||
		strings.Count(s, "#EXT-X-DISCONTINUITY\n") != 1 || strings.Count(s, "#EXT-X-PROGRAM-DATE-TIME:") != 3 {
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
```

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
// segment URLs are signed; captions use their rung's name.
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
	if got := strings.Join(renditions, ","); got != "v720,aac0,v720" {
		t.Fatalf("retimed renditions %s, want v720,aac0,v720", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd server && go test ./internal/transcode/ ./internal/stream/ ./internal/api/ -run 'TestLayoutRenditions|TestSessionTimelineObservesAndMarksRestarts|TestMediaPlaylistServedThroughTimeline' -v`
Expected: FAIL to compile (`l.Renditions undefined`, `m.timelineOf undefined`).

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

  Add `timeline: timeline, timelineCancel: tlCancel,` to the `sess := &session{…}` literal.

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

- In `restartSession`, right before `log.Printf("stream: session %s: restarting ffmpeg (append to playlist)", sess.id)`:

```go
	// The old process is dead: record everything it wrote, then open a new
	// timeline run (session-wide discontinuity epoch, PDT never going back).
	if sess.timeline != nil {
		sess.timeline.MarkRestart()
	}
```

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
// bytes, segment names still bare) from the session observer's snapshot:
// Bowtie's PDT on every segment, DISCONTINUITY where a new epoch starts,
// DISCONTINUITY-SEQUENCE. It never reads files. False when the viewer has
// no session.
func (m *Manager) RetimePlaylist(viewerID, rendition string, raw []byte) ([]byte, bool) {
	tl, _, _, ok := m.timelineOf(viewerID)
	if !ok {
		return nil, false
	}
	return tl.Retime(rendition, raw), true
}
```

In `api/stream_handlers.go`:

- Add to `StreamController`:

```go
	// RetimePlaylist rewrites a rendition playlist from the session
	// timeline (PDT, discontinuities); false when the viewer has no session.
	RetimePlaylist(viewerID, rendition string, raw []byte) ([]byte, bool)
```

- In `serveMediaPlaylist`, after the caption-repair `if` block and before `rewritten := …`:

```go
	rendition := strings.TrimSuffix(name, ".m3u8")
	if rung, ok := strings.CutSuffix(rendition, "_vtt"); ok {
		rendition = rung // caption segments share the rung's sequence numbers and stamps
	}
	if retimed, ok := s.deps.Streams.RetimePlaylist(viewerID, rendition, raw); ok {
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
```

Add to `production-baseline.yaml`'s `expect:` block:

```yaml
  discontinuitySequenceOK: true
  discontinuitiesAligned: true
  pdtMonotonic: true
  pdtAligned: true
```

A/V skew after a restart can be paired now: Task 3 pairs by epoch, and the epochs are Bowtie's.

Run: `cd server && go test -count=1 -tags ffmpeg -p 1 -parallel 2 -timeout 30m -run TestScenarios -v ./internal/e2e/ 2>&1 | grep -E -- '--- |xfail \(|known gap|consistency:'`
Expected: every scenario PASS, no `xfail (` lines left, no `consistency:` lines on passing scenarios.

Run: `cd server && BOWTIE_FFMPEG_PATH="$PWD/scripts/ffmpeg51" go test -count=1 -tags ffmpeg -p 1 -parallel 2 -timeout 60m -run 'TestScenarios/(long-outage-restart|transcoder-restart|baseline|production-baseline)$' -v ./internal/e2e/ 2>&1 | grep -E -- '--- |consistency:|timeline .*(shares|skipped|unobserved)'`
Expected:
- PASS on FFmpeg 5.1.
- 5.1's spurious top DISCONTINUITY doesn't show up as a boundary.
- Any `shares` / `skipped` / `unobserved` log line is explained in the commit message. On these scenarios, expect none.

- [ ] **Step 6: Commit**

```bash
git add server/internal/transcode/layout.go server/internal/transcode/layout_test.go server/internal/stream/session.go server/internal/stream/manager.go server/internal/stream/manager_test.go server/internal/api/stream_handlers.go server/internal/api/stream_handlers_test.go server/internal/e2e/testdata/scenarios
git commit -m "feat(stream): per-session timeline observer; serve Bowtie's PDT and epochs in every rendition

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
	tl, _, _, _ := m.timelineOf(h.ViewerID)
	tl.Observe()
	win, ok := m.TimelineOf(h.ViewerID)
	if !ok || win.MediaSequence != 3 || len(win.Segments) != 2 {
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
                    description: Media sequence number of the first segment.
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
  - testplayer: `AllRenditions`, `Probe`, `Report`, `Consistency`, `OnSegment`/`SegmentEvent` (Task 3).
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
  - no backward jumps, no segment, playlist or probe errors, every listed rendition delivered segments, and no session loss;
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
				r := p.Report()
				t.Logf("%s segments=%d excess=%.2fs audioHole=%v avSkew=%v backward=%d errors=%d",
					now.UTC().Format(time.RFC3339), r.SegmentsFetched, r.TimelineExcess, r.MaxAudioHole, r.MaxAVStartSkew, r.BackwardJumps, len(r.Errors))
			}
		}
	}()
	p.Run(ctx)
	wg.Wait()
	r := p.Report()
	p.Stop(context.Background())
	end := time.Now()

	t.Logf("renditions=%v segments=%d segErrors=%d playlistErrors=%d probeErrors=%d excess=%.2fs audioHoleBy=%v avSkew=%v probed=%d/%d maxGap=%v",
		r.Renditions, r.SegmentsFetched, r.SegmentErrors, r.PlaylistErrors, r.ProbeErrors, r.TimelineExcess, r.AudioHoleBy,
		r.MaxAVStartSkew, r.ProbedAudio, r.ProbedVideo, r.MaxGap)
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
	if r.MaxAVStartSkew > 100*time.Millisecond {
		t.Errorf("A/V start skew %v, want ≤ 100ms", r.MaxAVStartSkew)
	}
	if r.SegmentErrors+r.PlaylistErrors+r.ProbeErrors > 0 {
		t.Errorf("fetch errors: segments %d, playlists %d, probes %d", r.SegmentErrors, r.PlaylistErrors, r.ProbeErrors)
	}
	for _, name := range r.Renditions {
		if r.RenditionSegments[name] == 0 {
			t.Errorf("rendition %s delivered no segments", name)
		}
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
echo "Invalid frame dimensions: $(cat "$soak"/ff/*.log 2>/dev/null | grep -c 'Invalid frame dimensions')"
echo "transcoder behind:        $(grep -c 'transcoder behind' "$L")"
echo "drained (read nothing):   $(grep -c 'transcoder read nothing' "$L")"
echo "timeline gaps:            $(grep -c 'stream: timeline .* gap before' "$L")"
echo "timeline merges/skips:    $(grep -cE 'stream: timeline .*(shares|skipped|unobserved)' "$L")"
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
  in step.
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
- `stream: timeline … gap before` outside real reception problems.
FFmpeg 5.1 logs "timestamp discontinuity" only at debug level, so it
won't appear at Bowtie's normal log level.
```

Of the two AC-3 sentences, keep exactly one:
- the first if Task 5 skipped its Step 6, because input hardening alone kept the copy's holes at or under 50 ms;
- the second if Task 5 re-encoded AC-3.

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
