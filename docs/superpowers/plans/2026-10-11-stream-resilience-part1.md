# Stream Resilience Part 1 (Server) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Live streams survive dirty broadcasts, stalls and transcoder restarts without duplicate-frame storms, audio holes, needless FFmpeg kills or a broken timeline. Every served playlist carries a Bowtie-owned wall-clock timeline (PDT, discontinuities, discontinuity sequence) that matches across renditions. A timeline endpoint serves clients that can't read PDT.

**Architecture:** The test harness comes first: a `pts-jump` fault, a multi-rendition testplayer with a pure consistency checker and ffprobe audio checks, and new fault scenarios marked `xfail` until the task that fixes them. Then FFmpeg input and audio hardening, plus a timestamp option chosen on FFmpeg 5.1 (the production image) through a Docker wrapper. Ingest changes come next: a stuck transcoder is first drained, then killed only if it still reads nothing, and new subscribers join at the last video key. A per-session `stream.Timeline` stamps each segment once, keyed by (FFmpeg run, segment index) so every rendition shares the stamp. It rewrites playlists on the way out and backs `GET /api/v1/sessions/{viewerId}/timeline`. A caption sweeper in the maintenance loop deletes stray `.vtt` files. A one-hour real-broadcast soak is the final gate for the timestamp option.

**Tech Stack:** Go 1.22 (`server/`), FFmpeg 5.1.9 (Debian bookworm, production image) and the host FFmpeg for e2e, ffprobe, Docker Desktop on the dev Mac, the fake HDHomeRun (`internal/hdhr/hdhrfake`), testplayer.

**Spec:** `docs/superpowers/specs/2026-10-11-stream-resilience-and-resume-design.md` (Part 1 §1.1–1.6 and the Part 1 items under Testing). Evidence: `.superpowers/sdd/av-sync/investigation.md` §A–C and §4.

## Global Constraints

- VOD and concat inputs keep their current flags. Only the live pipe input (`JobSpec.Stdin != nil`) and its audio outputs change (spec §1.1).
- Main pipe input gets `-fflags +discardcorrupt+genpts -err_detect ignore_err -thread_queue_size 1024 -probesize 20M -analyzeduration 10M`. The caption `movie` input gets the same `-thread_queue_size`, probe and analyze options.
- Every live AAC output gets `aresample=async=1:min_hard_comp=0.02:first_pts=0`. AC-3 outputs are stream copies and can't be filtered.
- Timestamp option: `-dts_delta_threshold 1` or `-fps_mode vfr`/`passthrough`, chosen by test on FFmpeg 5.1. Prefer the option that doesn't fire on splices; if both pass, prefer `fps_mode`.
- Stuck rule: drain after 30 s without reads, then kill only after a further 30 s grace without reads.
- Every FFmpeg flag must work on FFmpeg 5.1 (checked in Task 6 against `debian:bookworm-slim`'s 5.1.9).
- Update existing tests; don't delete them. Don't add Go module dependencies.
- Wall-clock format everywhere Bowtie writes one: RFC 3339 with milliseconds, `2006-01-02T15:04:05.000Z07:00`, in UTC.
- Never hard-code a host (no `njathome`, no IPs) in code or tests. Hosts come from env vars or flags at run time.
- Every local test server runs with `BOWTIE_HDHOMERUN_GUIDE=off`. Never call api.hdhomerun.com.
- Every commit message ends with the line `Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>`. Don't name a model anywhere else.
- Run Go commands from `server/` (the module root): `cd server && …`.

## Review Focus

1. **An ad splice re-based when it shouldn't be (or not re-based when it should).** A viewer expects no frozen or duplicated video, no A/V offset and an unchanged Σ EXTINF across a real ad break. Pinned by the `splice` scenario (Task 4, xfail removed in Task 6) and the one-hour real soak (Task 13), whose gates are |timeline excess| ≤ 2 s, audio holes ≤ 50 ms and A/V start skew ≤ 100 ms.
2. **PDT going backwards at a restart.** The new FFmpeg's first segment can carry an mtime-derived start before the end of the old run's last segment. A viewer expects PDT never to move backwards. Pinned by `TestTimelineNeverGoesBackwardsAtRestart` (Task 9) and `pdtMonotonic: true` on `long-outage-restart` (Task 10).
3. **Renditions misaligned after a kill.** When FFmpeg dies, the audio playlist can be one segment ahead of video, so media sequence numbers no longer line up. A viewer expects the DISCONTINUITY and the PDT for the same moment in every rendition. Pinned by `TestTimelineAlignsRenditionsByIndexAcrossRestart` (Task 9) and `TestConsistencyAlignsByIndexWhenAudioWasAhead` (Task 2).
4. **The drain loop never killing a truly hung encoder.** A drain that refreshes the stuck clock, or a pop counter that never moves, would hold a wedged QSV encoder forever. A viewer expects a restart within about 60 s. Pinned by `TestStuckSubDrainedThenClosedAfterGrace` (Task 7) and the `stuck-hung` scenario (Task 4).
5. **The timeline endpoint leaking another user's session.** Heartbeat and DELETE accept any valid Bearer token; the timeline must not. A viewer expects another account to get 404. Pinned by `TestSessionTimelineOwnerOnly` (Task 11).

---

## File map

| File | Responsibility | Tasks |
|---|---|---|
| `server/internal/hdhr/hdhrfake/faults/spec.go`, `engine.go` | `pts-jump` fault | 1 |
| `server/internal/hdhr/hdhrfake/scenario/scenario.go`, `run.go` | pts-jump is one-shot; new expectations and observations; `windowSegments` | 1, 4 |
| `server/internal/testplayer/consistency.go` (new) | pure cross-rendition playlist checks | 2 |
| `server/internal/testplayer/player.go` | follow every rendition, probe segments, timeline excess | 3 |
| `server/internal/e2e/harness.go`, `scenarios_test.go`, `ffprobe_test.go` (new) | plumbing, ffprobe probe, observed mapping | 4 |
| `server/internal/e2e/testdata/scenarios/*.yaml` | new scenarios plus guards on existing ones | 4, 5, 6, 7, 10 |
| `server/internal/stream/manager.go` | `HLSListSize` override; timeline per session; RetimePlaylist; TimelineOf; caption sweep | 4, 10, 11, 12 |
| `server/internal/transcode/ffmpeg.go` | live input hardening, aresample, timestamp option | 5, 6 |
| `server/scripts/ffmpeg51` (new) | FFmpeg 5.1 (bookworm) through Docker for e2e on the Mac | 6 |
| `server/internal/stream/subqueue.go`, `ingest.go` | drain-then-kill; join at video key | 7, 8 |
| `server/internal/stream/probe.go` | PMT video stream type | 8 |
| `server/internal/stream/hlsplaylist.go` (new), `timeline.go` (new) | playlist parse, Bowtie timeline | 9 |
| `server/internal/transcode/layout.go` | `Layout.Renditions()` | 10 |
| `server/internal/stream/session.go` | `session.timeline` | 10 |
| `server/internal/api/stream_handlers.go`, `server.go`, `docs/api/openapi.yaml` | retime on serve; timeline endpoint | 10, 11 |
| `server/internal/stream/captions_sweep.go` (new) | `.vtt` sweeper | 12 |
| `server/internal/e2e/realsoak_test.go` (new), `server/scripts/ffmpeg-soak` (new) | real-broadcast soak | 13 |
| `CHANGELOG.md`, `docs/deploy/truenas.md`, `docs/dev/fake-hdhomerun.md` | release notes, QSV watch list, harness docs | 1, 6, 14 |

## Expected failures before each fix

New scenarios start with `xfail:`. The e2e runner then requires at least one violation, so a fix forces removing the marker ("known gap fixed … remove xfail"). The table says which expectation should fail before the fix and which task removes the marker.

| Scenario | Fails before the fix because | Remove `xfail` in |
|---|---|---|
| `ptsjump` | duplicate-frame storm: Σ EXTINF grows by about 18 s (`maxTimelineExcessSeconds`) | Task 6 |
| `splice` | +1.5 s step is under FFmpeg's default 10 s threshold, so CFR duplicates fill it (`maxTimelineExcessSeconds`) | Task 6 |
| `brief-dropout` | the 4 s hole is filled with duplicated frames, so Σ EXTINF ≈ wall time instead of wall time − 4 s (`maxTimelineExcessSeconds`) | Task 6 |
| `threshold-dropout` | same for the 9 s and 12 s holes | Task 6 |
| `corrupt-audio` | AAC holes where TEI packets are discarded (`maxAudioHoleMs`) | Task 5 |
| `stuck-recovers` | Bowtie kills FFmpeg at 30 s (`maxFfmpegRestarts: 0`) | Task 7 |
| `long-outage-restart` | FFmpeg writes no `#EXT-X-DISCONTINUITY-SEQUENCE` (`minDiscontinuitySequence`, `discontinuitySequenceOK`) | Task 10 |
| `stuck-hung` | none (guard: must restart before and after Task 7) | — |

**Rule when reality differs from the table:** if a pre-fix run passes an expectation the table lists as failing, re-run that scenario once. If it still passes, say so in the commit message. Keep `xfail` while another expectation still fails; otherwise remove `xfail`, and the scenario becomes a guard. Never loosen a threshold to make a fix pass. If a fix doesn't clear its row, stop and report the observed values.

**FFmpeg 5.1 experiment already run (recorded for Task 6).** Setup: `debian:bookworm-slim` FFmpeg 5.1.9, libx264, a 30.04 s clip with +9 s added to one video PES PTS, fed through a pipe at full speed:

| Variant | dup frames | Σ EXTINF | AAC hole |
|---|---|---|---|
| current argv | 270 | 39.04 s | 9.0 s |
| hardened input + aresample, no timestamp option | 270 | 39.04 s | filled |
| A: `-dts_delta_threshold 1` | 0 | 30.03 s | none |
| B: `-fps_mode:v vfr` | 0 | 39.04 s (9 s PTS hole kept) | none |
| C: `-fps_mode:v passthrough` | 0 | 39.04 s (9 s PTS hole kept) | none |

B and C fail the "no output timeline inflation" rule. A is the provisional choice. Task 6 re-checks it through the e2e suite, and the real soak (Task 13) is the final gate.

---

### Task 1: `pts-jump` fault in the fake HDHomeRun

**Files:**
- Modify: `server/internal/hdhr/hdhrfake/faults/spec.go`
- Modify: `server/internal/hdhr/hdhrfake/faults/engine.go`
- Modify: `server/internal/hdhr/hdhrfake/scenario/scenario.go` (`overlaps`)
- Test: `server/internal/hdhr/hdhrfake/faults/engine_test.go`, `server/internal/hdhr/hdhrfake/scenario/scenario_test.go`
- Modify: `docs/dev/fake-hdhomerun.md` (fault table)

**Interfaces:**
- Produces: `faults.PTSJump Kind = "pts-jump"`. `Spec.By` shifts the PTS and DTS of **one** video PES per connection by `By` (90 kHz, mod 2^33). Default scope is the channel; it fires once on each connection that sends a video PES within 10 s (`dropFaultLife`) of the fault starting. Scenario YAML: `{at: 8s, fault: pts-jump, by: 3s}`.

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

func TestPTSJumpNeedsBy(t *testing.T) {
	if err := (Spec{Fault: PTSJump}).Validate(); err == nil {
		t.Fatal("pts-jump without by validated")
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

Change the `By` field comment to `// timestamp-jump, pts-jump`. In `Validate`, add a case before `case SourceSwitch:`:

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

In `Decide`, insert this block right after the `own := func() {…}` closure, before `if q < cleanQuality {`:

```go
	for _, f := range hits {
		if f.Spec.Fault != PTSJump || f.fired[conn] || track != tsloop.TrackVideo {
			continue
		}
		pts, dts, hasPTS, hasDTS := tsloop.PESTimestamps(pkt)
		if !hasPTS {
			continue
		}
		f.fired[conn] = true
		if !hasDTS {
			dts = pts
		}
		by := int64(f.Spec.By/time.Microsecond) * 9 / 100 // 90 kHz ticks
		own()
		tsloop.SetPESTimestamps(out, pts+by, dts+by) // writeTS masks to 33 bits
	}
```

In `scenario/scenario.go` `overlaps`, treat pts-jump as one-shot:

```go
	if a.Fault != b.Fault || a.Scope != b.Scope || a.Fault == faults.Drop || a.Fault == faults.PTSJump || a.Fault.IsTimeline() {
```

In `docs/dev/fake-hdhomerun.md`, add a row to the fault table next to `timestamp-jump`:

```markdown
| `pts-jump` | `by` | Adds `by` to the PTS/DTS of one video PES on each connection (fires once per connection within 10 s; 33-bit wrap). Models a single corrupt timestamp. |
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
  - New `Report` fields: `Renditions []string` (primary first), `TimelineExcess float64` (seconds), `MaxAudioHole time.Duration`, `MaxAVStartSkew time.Duration`, `ProbedAudio, ProbedVideo int`, `Consistency Consistency`.
  - Timeline excess is Σ EXTINF of the primary rendition's segments that first appear after the first poll that listed any, minus the wall time between that poll and the poll that first listed the newest of them.
  - A/V start skew compares the first packet PTS of an audio and a video segment with the same media sequence number. Don't set `maxAVStartSkewMs` on scenarios with transcoder restarts, because after a kill the audio numbering can run one ahead.
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `cd server && go test ./internal/testplayer/ -run TestPlayerFollowsEveryRenditionAndProbes -v`
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
	// MaxAudioHole is the largest gap between consecutive audio packets.
	MaxAudioHole time.Duration
	// MaxAVStartSkew is the largest first-PTS difference between an audio
	// and a video segment with the same media sequence number.
	MaxAVStartSkew           time.Duration
	ProbedAudio, ProbedVideo int
	// Consistency checks every polled media playlist (CheckConsistency).
	Consistency Consistency
```

5. Add the rendition type and helpers at the end of the file:

```go
// rendition is one followed media playlist and its probe state.
type rendition struct {
	name    string
	url     string
	highest int64
	lastSeq int64   // last probed audio segment (-2: none)
	lastEnd float64 // end PTS of that segment
}

func newRendition(name, url string) *rendition {
	return &rendition{name: name, url: url, highest: -1, lastSeq: -2}
}

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

// renditionRefs lists a master's variants (in order), then its audio renditions.
func renditionRefs(master []byte) []string {
	var variants, audio []string
	sc := bufio.NewScanner(bytes.NewReader(master))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "#EXT-X-MEDIA:") && strings.Contains(line, "TYPE=AUDIO"):
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
	firstPTS map[string]map[int64]float64 // rendition → seq → first packet PTS

	excessStarted bool
	excessBase    int64 // newest segment at the first poll that listed any
	excessFirst   time.Time
	excessLast    time.Time
	excessSum     float64
```

In `Start`, initialize them in the returned `Player` literal:

```go
		primary:     newRendition(renditionName(out.PlaylistURL), out.PlaylistURL),
		firstPTS:    map[string]map[int64]float64{},
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
	p.mu.Lock()
	p.snaps = append(p.snaps, ParseSnapshot(p.primary.name, body, pollAt))
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
			p.probe(ctx, p.primary, e.abs, e.discontinuity, seg)
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
		return // not written yet, or the session is ending (the primary reports that)
	case status != http.StatusOK:
		p.errorf("%s playlist: HTTP %d", r.name, status)
		return
	}
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
		if err != nil || st != http.StatusOK || len(seg) == 0 || seg[0] != 0x47 {
			p.mu.Lock()
			p.report.SegmentErrors++
			p.mu.Unlock()
			continue
		}
		p.probe(ctx, r, g.Seq, g.Discontinuity, seg)
	}
}

// probe measures audio holes and the A/V start skew of one fetched segment.
func (p *Player) probe(ctx context.Context, r *rendition, seq int64, disc bool, seg []byte) {
	if p.cfg.Probe == nil {
		return
	}
	pk, err := p.cfg.Probe(ctx, seg)
	if err != nil {
		if ctx.Err() == nil {
			p.errorf("probe %s segment %d: %v", r.name, seq, err)
		}
		return
	}
	if len(pk) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
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
		if hole > p.report.MaxAudioHole {
			p.report.MaxAudioHole = hole
		}
		last := pk[len(pk)-1]
		r.lastSeq, r.lastEnd = seq, last.PTS+last.Dur
	} else {
		p.report.ProbedVideo++
	}
	if p.firstPTS[r.name] == nil {
		p.firstPTS[r.name] = map[int64]float64{}
	}
	p.firstPTS[r.name][seq] = pk[0].PTS
	for name, m := range p.firstPTS {
		if name == r.name || isAudioRendition(name) == audio {
			continue
		}
		if other, ok := m[seq]; ok {
			if s := absSeconds(pk[0].PTS - other); s > p.report.MaxAVStartSkew {
				p.report.MaxAVStartSkew = s
			}
		}
	}
}
```

9. In `Report()`, before `return r`, add:

```go
	r.Renditions = []string{p.primary.name}
	for _, o := range p.others {
		r.Renditions = append(r.Renditions, o.name)
	}
	if !p.excessLast.IsZero() {
		r.TimelineExcess = p.excessSum - p.excessLast.Sub(p.excessFirst).Seconds()
	}
	r.Consistency = CheckConsistency(p.snaps)
```

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
- Create: `server/internal/e2e/testdata/scenarios/{ptsjump,corrupt-audio,brief-dropout,threshold-dropout,splice,long-outage-restart,stuck-recovers,stuck-hung}.yaml`
- Modify: `server/internal/e2e/testdata/scenarios/transcoder-restart.yaml`

**Interfaces:**
- Consumes: the testplayer `Config.AllRenditions`, `Config.Probe`, `Packet`, `SegmentProbe` and the `Report` fields (Task 3).
- Produces:
  - `scenario.Scenario.WindowSegments int` (`windowSegments`: the session's `-hls_list_size`; 0 = default 30; otherwise ≥ 3).
  - `scenario.Expect` gains `RenditionsMonotonic, DiscontinuitiesAligned, DiscontinuitySequenceOK, PDTMonotonic, PDTAligned *bool`, `MinDiscontinuitySequence *int64`, `MaxTimelineExcessSeconds, MinTimelineExcessSeconds, MaxAudioHoleMs, MaxAVStartSkewMs *float64` and `MinFfmpegRestarts *int`. YAML keys are the lowerCamel field names.
  - `scenario.Observed` gains the matching fields: `RenditionsMonotonic`, `DiscontinuitiesAligned`, `DiscontinuitySequenceOK`, `MaxDiscontinuitySequence`, `PDTMonotonic`, `PDTAligned`, `TimelineExcess`, `MaxAudioHole`, `MaxAVStartSkew`, `ProbedAudio`, `ProbedVideo`.
  - `stream.ManagerDeps.HLSListSize int` overrides `-hls_list_size` for new sessions (tests).
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

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd server && go test ./internal/hdhr/hdhrfake/scenario/ ./internal/stream/ -run 'TestCheckTimelineExpectations|TestParseWindowSegments|TestHLSListSizeOverride' -v`
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
```

In `validate()`, after the duration check:

```go
	if s.WindowSegments < 0 || (s.WindowSegments > 0 && s.WindowSegments < 3) {
		return fmt.Errorf("scenario %s: windowSegments %d (want 0 or ≥ 3)", s.Name, s.WindowSegments)
	}
```

Replace `empty()`:

```go
func (e *Expect) empty() bool {
	return e.SequenceMonotonic == nil && e.MaxGapSeconds == nil && e.SessionSurvives == nil &&
		e.MaxFfmpegRestarts == nil && e.TunersBusy == nil &&
		e.RenditionsMonotonic == nil && e.DiscontinuitiesAligned == nil && e.DiscontinuitySequenceOK == nil &&
		e.MinDiscontinuitySequence == nil && e.PDTMonotonic == nil && e.PDTAligned == nil &&
		e.MaxTimelineExcessSeconds == nil && e.MinTimelineExcessSeconds == nil &&
		e.MaxAudioHoleMs == nil && e.MaxAVStartSkewMs == nil && e.MinFfmpegRestarts == nil
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

Run: `cd server && go test ./internal/hdhr/hdhrfake/scenario/ ./internal/stream/ -run 'TestCheck|TestParse|TestHLSListSize' -v`
Expected: PASS.

- [ ] **Step 4: e2e plumbing**

`server/internal/e2e/harness.go`: add to `Options`:

```go
	// HLSListSize overrides the sessions' -hls_list_size (0: default 30).
	HLSListSize int
```

Pass it in `New`'s `stream.NewManager(stream.ManagerDeps{…})` call as `HLSListSize: o.HLSListSize,`. Replace `PlayerWith` and add `PlayerConfig`:

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
- In the `New(t, Options{…})` call add `HLSListSize: sc.WindowSegments,`.
- Replace `p, err := h.Player(t, sc.Channel)` with:

```go
	cfg := h.PlayerConfig(t, sc.Channel)
	cfg.AllRenditions = true
	cfg.Probe = FFprobe(ffprobePath())
	p, err := testplayer.Start(context.Background(), cfg)
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
		for _, pr := range c.Problems {
			t.Logf("consistency: %s", pr)
		}
		t.Logf("player: renditions=%v excess=%.2fs audioHole=%v avSkew=%v probed=%d/%d",
			r.Renditions, r.TimelineExcess, r.MaxAudioHole, r.MaxAVStartSkew, r.ProbedAudio, r.ProbedVideo)
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
  sessionSurvives: true
  sequenceMonotonic: true
  maxFfmpegRestarts: 0
  maxTimelineExcessSeconds: 2
  minTimelineExcessSeconds: -2
  maxAVStartSkewMs: 100
```

`corrupt-audio.yaml`:

```yaml
name: corrupt-audio
duration: 35s
timeline:
  - {at: 10s, fault: corrupt, type: tei, rate: 0.01, for: 10s}
expect:
  xfail: "AAC holes where TEI packets are discarded (fixed in Task 5)"
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
  # Force the append restart inside the outage: the new FFmpeg run starts
  # producing when the signal returns, and the 32 s window later slides
  # past the restart.
  killTranscoderAt: 30s
expect:
  xfail: "FFmpeg serves no DISCONTINUITY-SEQUENCE; Bowtie owns the timeline in Task 10"
  sessionSurvives: true
  sequenceMonotonic: true
  renditionsMonotonic: true
  minFfmpegRestarts: 1
  minDiscontinuitySequence: 1
  discontinuitySequenceOK: true
  discontinuitiesAligned: true
  pdtMonotonic: true
  pdtAligned: true
```

`stuck-recovers.yaml`:

```yaml
name: stuck-recovers
duration: 80s
bowtie:
  # FFmpeg reads nothing for 40 s, then resumes: drained at 30 s, never killed.
  slowConsumer: {at: 10s, for: 40s}
expect:
  xfail: "Bowtie kills FFmpeg after 30 s without reads (fixed in Task 7)"
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
```

Append to `transcoder-restart.yaml`'s `expect:` block:

```yaml
  renditionsMonotonic: true
```

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
- Modify: `server/internal/e2e/testdata/scenarios/corrupt-audio.yaml`, `baseline.yaml`

**Interfaces:**
- Produces: unexported `liveInputArgs() []string` and `const liveAudioFilter = "aresample=async=1:min_hard_comp=0.02:first_pts=0"` in `transcode`. Task 6 adds the timestamp option to `liveInputArgs`.
- Argv contract (live pipe only):
  - `… -thread_queue_size 1024 -probesize 20M -analyzeduration 10M -fflags +discardcorrupt+genpts -err_detect ignore_err -i pipe:0`
  - captions: `-thread_queue_size 1024 -data_field first -f lavfi -i movie='pipe\:3':format_opts='probesize=20M\:analyzeduration=10M'[out0+subcc]`
  - each AAC output n: `-c:a:n aac -ac:a:n 2 -b:a:n Xk -filter:a:n aresample=async=1:min_hard_comp=0.02:first_pts=0`. AC-3 copies are unchanged.

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
Expected: PASS. The `ffmpeg`-tagged run executes the new argv on the host FFmpeg, including the caption `movie` input with `format_opts`.

- [ ] **Step 5: Re-run the scenarios and remove the `corrupt-audio` xfail**

Run: `cd server && go test -count=1 -tags ffmpeg -p 1 -parallel 2 -timeout 30m -run 'TestScenarios/(corrupt-audio|baseline|brief-dropout|threshold-dropout)$' -v ./internal/e2e/ 2>&1 | grep -E -- '--- |xfail \(|known gap|observed:'`
Expected: `corrupt-audio` fails with `known gap fixed … remove xfail`. Delete its `xfail:` line. Add `maxAudioHoleMs: 50` to `baseline.yaml`'s `expect:` block. Re-run the same command: all PASS. `brief-dropout` and `threshold-dropout` still log `xfail (…)` with a timeline-excess violation.

- [ ] **Step 6: Commit**

```bash
git add server/internal/transcode server/internal/e2e/testdata/scenarios
git commit -m "fix(transcode): harden the live pipe input and keep live AAC sample-continuous

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Choose the timestamp option on FFmpeg 5.1 and apply it

**Files:**
- Create: `server/scripts/ffmpeg51` (executable)
- Modify: `server/internal/transcode/ffmpeg.go` (`liveInputArgs`)
- Test: `server/internal/transcode/ffmpeg_test.go`
- Modify: `server/internal/e2e/testdata/scenarios/{ptsjump,splice,brief-dropout,threshold-dropout,baseline}.yaml`
- Modify: `docs/dev/fake-hdhomerun.md` (running the suite on FFmpeg 5.1)

**Interfaces:**
- Consumes: `liveInputArgs()` (Task 5).
- Produces:
  - `server/scripts/ffmpeg51` is a drop-in `BOWTIE_FFMPEG_PATH` that runs `debian:bookworm-slim`'s FFmpeg 5.1.9 in Docker. Env: `FF51_PRE` adds args first; `FF51_PRE_OUTPUT` adds args before the final (output) argument; `FF51_LOGLEVEL` replaces the `-loglevel` value. It refuses the fd-3 caption input; the e2e harness runs without captions.
  - The chosen option, recorded in `liveInputArgs` and its comment. Expected: `"-dts_delta_threshold", "1"` first in the list.

**Decision rule (recorded here, applied in Step 4):**
1. Run `ptsjump`, `splice`, `brief-dropout`, `threshold-dropout` and `baseline` on FFmpeg 5.1 four times: base (Task 5 argv), A `-dts_delta_threshold 1`, B `-fps_mode:v vfr`, C `-fps_mode:v passthrough`.
2. A variant qualifies if every one of those scenarios meets all its expectations (ignore `xfail`; read the `observed:` and `player:` lines). In particular, ptsjump's |timeline excess| ≤ 2 s means no duplicate storm and no inflation.
3. If A and (B or C) both qualify, choose B (or C if only C qualifies). The spec prefers `fps_mode`.
4. If only A qualifies, choose A. This is the expected outcome: in the clip experiment above, B and C kept the 9 s PTS hole.
5. If nothing qualifies, stop and report the observed table. Don't loosen thresholds.
6. The real soak (Task 13) is the final gate. If it shows false re-bases with A, change `1` to `2` (a +3 s `ptsjump` still re-bases), re-run step 1 for A only, and soak again; then `3` the same way. If 3 still fails the soak, stop and report.

- [ ] **Step 1: Create the wrapper**

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
#
# $TMPDIR, the synthetic-source cache and $PWD are mounted at the same paths,
# so absolute paths work. Bowtie stops FFmpeg with SIGKILL, which kills this
# script but not the container, so a watchdog kills the container when this
# script dies. The caption input (fd 3) can't cross into the container.
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
# A background job's stdin is /dev/null unless redirected explicitly.
exec 9<&0
docker run --rm -i --init --network none --name "$name" -w "$PWD" \
  ${mounts[@]+"${mounts[@]}"} "$image" ffmpeg ${args[@]+"${args[@]}"} <&9 &
child=$!
exec 9<&-
self=$$
(
  while kill -0 "$self" 2>/dev/null; do sleep 0.2; done
  docker kill "$name" >/dev/null 2>&1
) </dev/null >/dev/null 2>&1 &
wait "$child"
```

Smoke test:

Run: `cd server && scripts/ffmpeg51 -version | head -1`
Expected: `ffmpeg version 5.1.9-0+deb12u1 …` (the first run builds the image).

Run the hardened argv and the caption `movie` syntax on 5.1:

```bash
cd server && d="${TMPDIR%/}/ff51-flags" && mkdir -p "$d" && \
scripts/ffmpeg51 -hide_banner -loglevel error -f lavfi -i testsrc=d=2:s=320x240 -f lavfi -i sine=d=2 \
  -c:v mpeg2video -c:a ac3 -f mpegts "$d/in.ts" && \
scripts/ffmpeg51 -hide_banner -loglevel warning -nostats -dts_delta_threshold 1 \
  -thread_queue_size 1024 -probesize 20M -analyzeduration 10M -fflags +discardcorrupt+genpts \
  -err_detect ignore_err -i pipe:0 -map 0:v -map 0:a -c:v libx264 -c:a:0 aac -ac:a:0 2 -b:a:0 96k \
  -filter:a:0 aresample=async=1:min_hard_comp=0.02:first_pts=0 -f null - < "$d/in.ts" && \
scripts/ffmpeg51 -hide_banner -loglevel warning -thread_queue_size 1024 -data_field first -f lavfi \
  -i "movie='$d/in.ts':format_opts='probesize=20M\:analyzeduration=10M'[out0+subcc]" -map 0:v -f null - && echo FLAGS-OK
```

Expected: `FLAGS-OK`, with no `Unrecognized option` or `Option not found` lines.

Kill test: the container must die when the wrapper is SIGKILLed.

```bash
cd server && (scripts/ffmpeg51 -hide_banner -f lavfi -re -i testsrc -f null - & pid=$!; sleep 3; kill -9 $pid; sleep 2; docker ps --filter name=ffmpeg51- --format '{{.Names}}' | wc -l)
```

Expected: `0`.

- [ ] **Step 2: Run the four variants**

```bash
cd server
for v in base A B C; do
  pre=""; post=""
  case $v in
  A) pre="-dts_delta_threshold 1" ;;
  B) post="-fps_mode:v vfr" ;;
  C) post="-fps_mode:v passthrough" ;;
  esac
  FF51_PRE="$pre" FF51_PRE_OUTPUT="$post" BOWTIE_FFMPEG_PATH="$PWD/scripts/ffmpeg51" \
    go test -count=1 -tags ffmpeg -p 1 -parallel 2 -timeout 40m \
    -run 'TestScenarios/(ptsjump|splice|brief-dropout|threshold-dropout|baseline)$' -v ./internal/e2e/ \
    > "${TMPDIR%/}/ff51-$v.log" 2>&1
  echo "== $v"; grep -E 'observed:|player: renditions|xfail \(|known gap|--- (PASS|FAIL)' "${TMPDIR%/}/ff51-$v.log"
done
```

Record a table in the task report: variant × scenario → timeline excess, audio hole, A/V skew, restarts, and whether every expectation held.

- [ ] **Step 3: Write the failing test for the chosen option**

For A (expected), add to `TestBuildArgsLiveInputHardening` right after the first `mustContainSeq`:

```go
	mustContainSeq(t, live, "-dts_delta_threshold", "1", "-thread_queue_size", "1024")
```

And for the URL/VOD loop, add `a == "-dts_delta_threshold" ||` to the live-only condition.

Update the goldens:

```bash
cd server && perl -i -pe 's/"-thread_queue_size", "1024", "-probesize", "20M",/"-dts_delta_threshold", "1", "-thread_queue_size", "1024", "-probesize", "20M",/' internal/transcode/ffmpeg_test.go
```

If B or C was chosen instead, add instead:

```go
	mustContainSeq(t, live, "-fps_mode:v", "vfr") // or "passthrough"
```

Then put `"-fps_mode:v", "<mode>",` in each `TestBuildArgsStdinPipeAcrossBackends` golden right after `"-profile:v", "high",`.

Run: `cd server && go test ./internal/transcode/ -run TestBuildArgs -v`
Expected: FAIL (option missing).

- [ ] **Step 4: Implement the chosen option**

For A, make `liveInputArgs`:

```go
// liveInputArgs harden the live pipe (dirty ATSC transport streams).
// dts_delta_threshold 1: a timestamp step over 1 s (a corrupt PES, a splice,
// a dropout) is re-based instead of filled with duplicate frames; chosen on
// FFmpeg 5.1 by the ptsjump/splice/dropout scenarios and a real-broadcast
// soak. genpts fills missing timestamps, ignore_err keeps decoding past
// damaged frames, the larger probe still finds every PMT stream when the
// first seconds are damaged, and the thread queue absorbs the burst after a stall.
func liveInputArgs() []string {
	return []string{
		"-dts_delta_threshold", "1",
		"-thread_queue_size", "1024", "-probesize", "20M", "-analyzeduration", "10M",
		"-fflags", "+discardcorrupt+genpts", "-err_detect", "ignore_err",
	}
}
```

For B or C, leave `liveInputArgs` as it is. In `BuildArgs`, right after `args = append(args, "-c:v", s.D.VideoEncoder)` and the profile block, add:

```go
	if s.Stdin != nil {
		// Keep the input's frame timing: no duplicate storm on a timestamp step.
		args = append(args, "-fps_mode:v", "vfr") // or "passthrough"
	}
```

Run: `cd server && go test ./internal/transcode/ && go test -tags ffmpeg ./internal/transcode/`
Expected: PASS.

- [ ] **Step 5: Remove the fixed xfails and add guards**

Delete the `xfail:` lines from `ptsjump.yaml`, `splice.yaml`, `brief-dropout.yaml` and `threshold-dropout.yaml`. Add to `baseline.yaml`'s `expect:` block:

```yaml
  maxTimelineExcessSeconds: 2
  minTimelineExcessSeconds: -2
  maxAVStartSkewMs: 100
```

Run the whole suite on both FFmpeg builds:

Run: `cd server && go test -count=1 -tags ffmpeg -p 1 -parallel 2 -timeout 30m -run TestScenarios ./internal/e2e/`
Expected: PASS (host FFmpeg).

Run: `cd server && BOWTIE_FFMPEG_PATH="$PWD/scripts/ffmpeg51" go test -count=1 -tags ffmpeg -p 1 -parallel 2 -timeout 60m -run TestScenarios ./internal/e2e/`
Expected: PASS (FFmpeg 5.1). The `stuck-recovers` and `long-outage-restart` xfails still hold.

- [ ] **Step 6: Document the wrapper**

Add to `docs/dev/fake-hdhomerun.md`, in the section on running the scenario suite:

```markdown
### On FFmpeg 5.1 (the production image's FFmpeg)

`server/scripts/ffmpeg51` runs Debian bookworm's FFmpeg 5.1 in Docker as a
drop-in for `BOWTIE_FFMPEG_PATH` (software encode):

    cd server
    BOWTIE_FFMPEG_PATH=$PWD/scripts/ffmpeg51 go test -tags ffmpeg -p 1 -parallel 2 -timeout 60m -run TestScenarios ./internal/e2e/

`FF51_PRE`, `FF51_PRE_OUTPUT` and `FF51_LOGLEVEL` add or change arguments
for experiments (see the script). Captions (fd 3) can't cross into the
container; the suite runs without them. Segments are probed with the host
`ffprobe` (`BOWTIE_FFPROBE_PATH` overrides).
```

- [ ] **Step 7: Commit**

```bash
git add server/scripts/ffmpeg51 server/internal/transcode server/internal/e2e/testdata/scenarios docs/dev/fake-hdhomerun.md
git commit -m "fix(transcode): re-base timestamp steps over 1 s instead of duplicating frames

Chosen on FFmpeg 5.1 (bookworm) with the ptsjump, splice and dropout
scenarios: <paste the variant table from Step 2>.

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

(If B or C was chosen, the subject is `fix(transcode): keep input frame timing (fps_mode <mode>) on the live pipe`.)

---
### Task 7: Stuck transcoder: drain first, kill only if it still reads nothing

**Files:**
- Modify: `server/internal/stream/subqueue.go`
- Modify: `server/internal/stream/ingest.go`
- Test: `server/internal/stream/subqueue_test.go`, `server/internal/stream/ingest_test.go`
- Modify: `server/internal/e2e/testdata/scenarios/stuck-recovers.yaml`

**Interfaces:**
- Consumes: `channelIngest.joinSnapshotLocked()` (PAT, PMT and the stream since the join point; Task 8 moves the join point to the last video key).
- Produces:
  - `subQueue.popCount() uint64`, `subQueue.size() int` and `subQueue.reset(head []byte) int`. `reset` replaces the backlog and returns the bytes dropped. It never touches `lastPop`.
  - `const ingestSubKillGrace = 30 * time.Second`, and `IngestSub.drainedAt`/`popsAtDrain`, guarded by `channelIngest.mu`.
  - The rule: at 30 s without reads, drain once. After a drain, kill only if the pop count hasn't moved for `ingestSubKillGrace`; any pop clears the drain. A kill goes through `forceClose`, whose hook (`proc.Stop`, SIGKILL) the manager registered. Supervise then sees the exit and restarts with append, as today. `manager.go` needs no change.
  - Log lines:
    - drain: `ingest: channel N: transcoder read nothing for 30s; dropped its K KiB backlog and resent the stream headers`
    - recovery: `ingest: channel N: transcoder reading again Xs after its backlog was dropped`
    - kill: `ingest: channel N: transcoder stuck for 1m0s, closing its input` (the release note watches "transcoder stuck").
  - FFmpeg runs with `-nostats`, so `speed`/`dup` aren't available to log. The drain line carries the idle time and the bytes dropped instead.

- [ ] **Step 1: Write the failing tests**

Append to `server/internal/stream/subqueue_test.go`:

```go
func TestSubQueueResetKeepsStuckClockAndCountsPops(t *testing.T) {
	now := time.Date(2026, 10, 11, 20, 0, 0, 0, time.UTC)
	q := newSubQueue(1<<20, func() time.Time { return now })
	q.push([]byte("aaaa"), nil)
	q.push([]byte("bbbb"), nil)
	if _, ok := q.pop(); !ok || q.popCount() != 1 {
		t.Fatalf("popCount=%d after one pop", q.popCount())
	}
	now = now.Add(40 * time.Second)
	if n := q.reset([]byte("HEAD")); n != 4 {
		t.Fatalf("reset dropped %d bytes, want 4", n)
	}
	if q.size() != 4 {
		t.Fatalf("size=%d after reset, want 4", q.size())
	}
	if got := q.idleFor(now); got != 40*time.Second {
		t.Fatalf("idleFor=%v after reset, want 40s (reset must not touch the stuck clock)", got)
	}
	if c, _ := q.pop(); string(c) != "HEAD" || q.popCount() != 2 {
		t.Fatalf("pop after reset = %q, popCount=%d", c, q.popCount())
	}
}
```

In `server/internal/stream/ingest_test.go`, replace `TestStuckSubClosedAfter30s` with:

```go
// A reader that takes nothing is drained at the stuck timeout and closed only
// after the grace period passes without a single read.
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
```

In `TestStuckSubRunsForceCloseHook`, change the step count to `int((ingestSubStuckTimeout+ingestSubKillGrace)/time.Second)+2`. In `TestConcurrentCloseVsForceClose`, change the first `feedAndStep` count to `int((ingestSubStuckTimeout+ingestSubKillGrace)/time.Second)-1` and its comment to `// Bring the sub to the edge of the kill (drained, grace nearly over).`

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd server && go test ./internal/stream/ -run 'TestSubQueueReset|TestStuckSub|TestConcurrentCloseVsForceClose' -v`
Expected: FAIL to compile (`q.popCount undefined`, `undefined: ingestSubKillGrace`).

- [ ] **Step 3: Implement**

`subqueue.go`: add a `pops uint64` field to `subQueue` (after `lastPop`) and `q.pops++` in `pop` (after `q.lastPop = q.now()`). Add:

```go
// popCount is how many chunks the reader has taken.
func (q *subQueue) popCount() uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.pops
}

// size is the queued byte count.
func (q *subQueue) size() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.bytes
}

// reset replaces the backlog with head and returns the bytes dropped. The
// stuck clock is left alone: a reader that still takes nothing stays stuck.
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
	if len(head) > 0 {
		q.chunks = append(q.chunks, head)
		q.bytes = len(head)
		q.cond.Signal()
	}
	return dropped
}
```

`ingest.go`: replace the stuck constant's comment and add the grace:

```go
	// ingestSubStuckTimeout: a transcoder that takes nothing for this long is
	// drained: its backlog is dropped and it gets the stream headers again.
	ingestSubStuckTimeout = 30 * time.Second
	// ingestSubKillGrace: a drained transcoder that still takes nothing for
	// this long is hung (a wedged encoder); its input is closed and the
	// manager kills and restarts it.
	ingestSubKillGrace = 30 * time.Second
```

Add to `IngestSub` (after `lastDropLog`):

```go
	// drainedAt is when ingest dropped this stuck sub's backlog (zero: not
	// drained); popsAtDrain is the queue's pop count then. Under channelIngest.mu.
	drainedAt   time.Time
	popsAtDrain uint64
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
// input must be closed. A transcoder that takes nothing for
// ingestSubStuckTimeout is drained first: its backlog is replaced by PAT, PMT
// and the stream since the join point, as after an overflow. It is closed
// only if it then takes nothing for ingestSubKillGrace; one that reads again
// was just slow and is left alone.
func (c *channelIngest) checkStuckLocked(s *IngestSub, now time.Time) bool {
	if !s.drainedAt.IsZero() {
		if s.q.popCount() != s.popsAtDrain {
			log.Printf("ingest: channel %d: transcoder reading again %v after its backlog was dropped",
				c.channelID, now.Sub(s.drainedAt).Round(time.Second))
			s.drainedAt = time.Time{}
			return false
		}
		return now.Sub(s.drainedAt) >= ingestSubKillGrace
	}
	if idle := s.q.idleFor(now); idle >= ingestSubStuckTimeout {
		dropped := s.q.reset(c.joinSnapshotLocked())
		s.drainedAt, s.popsAtDrain = now, s.q.popCount()
		log.Printf("ingest: channel %d: transcoder read nothing for %v; dropped its %d KiB backlog and resent the stream headers",
			c.channelID, idle.Round(time.Second), dropped>>10)
	}
	return false
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd server && go test ./internal/stream/ -race -count=1`
Expected: PASS.

- [ ] **Step 5: Remove the `stuck-recovers` xfail**

Run: `cd server && go test -count=1 -tags ffmpeg -p 1 -parallel 2 -timeout 20m -run 'TestScenarios/(stuck-recovers|stuck-hung|slow-consumer-drops)$' -v ./internal/e2e/ 2>&1 | grep -E -- '--- |xfail \(|known gap|observed:'`
Expected: `stuck-recovers` fails with `known gap fixed`. Delete its `xfail:` line and re-run: all three PASS. `stuck-hung` shows `FfmpegRestarts:1` or more.

- [ ] **Step 6: Commit**

```bash
git add server/internal/stream/subqueue.go server/internal/stream/subqueue_test.go server/internal/stream/ingest.go server/internal/stream/ingest_test.go server/internal/e2e/testdata/scenarios/stuck-recovers.yaml
git commit -m "fix(ingest): drain a stuck transcoder first; kill it only if it still reads nothing

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: New subscribers join at the last video key

**Files:**
- Modify: `server/internal/stream/probe.go` (`ProgramInfo.VideoStreamType`)
- Modify: `server/internal/stream/ingest.go`
- Test: `server/internal/stream/join_test.go` (new)

**Interfaces:**
- Produces:
  - `ProgramInfo.VideoStreamType byte`, the PMT stream_type of the first video PID.
  - `isVideoKeyStart(pkt []byte, streamType byte) bool`: true for a PUSI packet on the video PID carrying an MPEG-2 sequence header (`00 00 01 B3`, stream types 0x01/0x02) or an H.264 SPS or IDR NAL (types 7/5, stream type 0x1B).
  - `tsPayload(pkt []byte) []byte`.
  - `channelIngest.sinceJoin` (renamed from `sincePAT`) holds the packets since the last video key once a key has been seen. Before the first key, it holds the packets since the last PAT (today's behavior). A PAT no longer restarts the buffer once keys are known.
  - `ingestJoinBufMax` rises from 1 MiB to 4 MiB, enough for a GOP of 19 Mbps MPEG-2. Past the cap, the oldest packets are trimmed as today.
  - Every new sub uses the join: a restart's sub, the caption tap and the Task 7 drain.

- [ ] **Step 1: Write the failing tests**

Create `server/internal/stream/join_test.go`:

```go
package stream

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"
)

// h264Key is a PES start carrying an access unit delimiter and an SPS.
var h264Key = []byte{0, 0, 1, 0xE0, 0, 0, 0x80, 0x80, 0x05, 0x21, 0, 1, 0, 1, 0, 0, 0, 1, 0x09, 0xF0, 0, 0, 0, 1, 0x67, 0x64}

// joinAfter feeds body to a channel (read by a first sub), then attaches a
// second sub and returns its first n bytes.
func joinAfter(t *testing.T, body []byte, n int) []byte {
	t.Helper()
	pb := newPipeBody()
	im, _ := newTestIngest(t, func(context.Context, string) (io.ReadCloser, int, error) { return pb, 200, nil }, nil)
	first, err := im.Attach(context.Background(), 1, "u")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	go func() { _, _ = pb.Write(body) }()
	readN(t, first.R, len(body), 2*time.Second)
	late, err := im.Attach(context.Background(), 1, "u")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = late.Close() }()
	return readN(t, late.R, n, 2*time.Second)
}

func TestJoinStartsAtLastH264Key(t *testing.T) {
	b := newTSBuilder() // PMT: H.264 on mediaPID
	pat1, pmt := b.PAT(), b.PMT()
	before := b.Media(1)
	key := b.packet(b.mediaPID, true, h264Key)
	m2 := b.Media(2)
	pat2 := b.PAT() // a PAT mid-GOP must not restart the join
	m3 := b.Media(3)
	body := bytes.Join([][]byte{pat1, pmt, before, key, m2, pat2, m3}, nil)
	want := bytes.Join([][]byte{pat2, pmt, key, m2, m3}, nil)
	if got := joinAfter(t, body, len(want)); !bytes.Equal(got, want) {
		t.Fatalf("join is not PAT+PMT+key+2 packets")
	}
}

func TestJoinStartsAtLastMPEG2SequenceHeader(t *testing.T) {
	b := newTSBuilder()
	b.pmtPID = 0x30
	pat, pmt := b.PAT(), pmtPacket() // MPEG-2 video on 0x31
	gop1 := b.packet(0x31, true, []byte{0, 0, 1, 0xB3, 0x50, 0x02, 0xD0, 0x37})
	mid := b.packet(0x31, false, []byte{1, 2, 3})
	gop2 := b.packet(0x31, true, []byte{0, 0, 1, 0xB3, 0x50, 0x02, 0xD0, 0x37})
	tail := b.packet(0x31, false, []byte{4, 5, 6})
	body := bytes.Join([][]byte{pat, pmt, gop1, mid, gop2, tail}, nil)
	want := bytes.Join([][]byte{pat, pmt, gop2, tail}, nil)
	if got := joinAfter(t, body, len(want)); !bytes.Equal(got, want) {
		t.Fatalf("join is not PAT+PMT+last sequence header+1 packet")
	}
}

func TestIsVideoKeyStart(t *testing.T) {
	b := newTSBuilder()
	cases := []struct {
		name string
		pkt  []byte
		st   byte
		want bool
	}{
		{"h264 sps", b.packet(0x31, true, h264Key), 0x1B, true},
		{"h264 idr", b.packet(0x31, true, []byte{0, 0, 1, 0xE0, 0, 0, 0x80, 0, 0, 0, 0, 1, 0x65}), 0x1B, true},
		{"h264 non-idr slice", b.packet(0x31, true, []byte{0, 0, 1, 0xE0, 0, 0, 0x80, 0, 0, 0, 0, 1, 0x41}), 0x1B, false},
		{"mpeg2 sequence header", b.packet(0x31, true, []byte{0, 0, 1, 0xB3, 1}), 0x02, true},
		{"mpeg2 picture only", b.packet(0x31, true, []byte{0, 0, 1, 0x00, 1}), 0x02, false},
		{"not a unit start", b.packet(0x31, false, []byte{0, 0, 1, 0xB3, 1}), 0x02, false},
		{"hevc falls back", b.packet(0x31, true, []byte{0, 0, 1, 0x40}), 0x24, false},
	}
	for _, c := range cases {
		if got := isVideoKeyStart(c.pkt, c.st); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd server && go test ./internal/stream/ -run 'TestJoinStartsAt|TestIsVideoKeyStart' -v`
Expected: FAIL to compile (`undefined: isVideoKeyStart`).

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
	// last video key): a 19 Mbps MPEG-2 GOP fits.
	ingestJoinBufMax = 4 << 20
```

- In `channelIngest`, rename `sincePAT` to `sinceJoin` everywhere in the file (`joinSnapshotLocked`, `handleTSPacketLocked`, `capJoinLocked`, the struct). Change its comment and add two fields:

```go
	videoStreamType byte   // PMT stream_type of videoPID
	keySeen         bool   // a video key was seen: joins start at the last one
	sinceJoin       []byte // non-table packets since the last video key (before any: since the last PAT)
```

- Replace `handleTSPacketLocked`:

```go
func (c *channelIngest) handleTSPacketLocked(pkt []byte) {
	pid := tsPID(pkt)
	switch {
	case pid == tsPIDPAT:
		c.lastPAT = pkt
		c.pmtPIDs = parsePATPMTPIDs(pkt)
		if !c.keySeen {
			c.sinceJoin = c.sinceJoin[:0]
		}
		c.capJoinLocked()
	case c.isPMTPID(pid):
		c.lastPMT = pkt
		info := parsePMT(pkt)
		c.videoPID, c.videoStreamType = info.VideoPID, info.VideoStreamType
		// Tables are served from lastPAT/lastPMT; do not duplicate into sinceJoin.
		c.capJoinLocked()
	default:
		if c.videoPID != 0 && pid == c.videoPID {
			if c.sourceHeight == 0 {
				// A header split across packets only delays detection.
				c.sourceHeight = sequenceHeaderHeight(pkt[4:])
			}
			if isVideoKeyStart(pkt, c.videoStreamType) {
				// New subs (restart, caption tap, drain) start here: the
				// decoder gets a sequence header at once, no 0x0 storm.
				c.keySeen = true
				c.sinceJoin = c.sinceJoin[:0]
			}
		}
		c.sinceJoin = append(c.sinceJoin, pkt...)
		c.capJoinLocked()
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

// isVideoKeyStart reports whether pkt starts a video access unit a decoder
// can start from: an MPEG-2 sequence header, or an H.264 SPS or IDR slice.
// Other codecs (HEVC) return false; their joins start at the last PAT.
func isVideoKeyStart(pkt []byte, streamType byte) bool {
	if len(pkt) < tsPacketSize || pkt[1]&0x40 == 0 {
		return false
	}
	p := tsPayload(pkt)
	if len(p) >= 9 && p[0] == 0 && p[1] == 0 && p[2] == 1 {
		if h := 9 + int(p[8]); h <= len(p) {
			p = p[h:] // skip the PES header
		}
	}
	for i := 0; i+3 < len(p); i++ {
		if p[i] != 0 || p[i+1] != 0 || p[i+2] != 1 {
			continue
		}
		code := p[i+3]
		switch streamType {
		case 0x01, 0x02:
			if code == 0xB3 {
				return true
			}
		case 0x1B:
			if t := code & 0x1F; t == 7 || t == 5 {
				return true
			}
		}
	}
	return false
}
```

Note on the test packets: `tsBuilder.packet` puts a pointer byte (`0x00`) before the payload when PUSI is set, so the payload starts `00 00 00 01 …` and isn't taken for a PES header. The scan still finds the start code. Real PES packets take the header-skip path.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd server && go test ./internal/stream/ -race -count=1`
Expected: PASS, including every existing join, probe and multitrack test.

Run: `cd server && go test -count=1 -tags ffmpeg -p 1 -parallel 2 -timeout 30m -run TestScenarios ./internal/e2e/`
Expected: PASS (only `long-outage-restart` is still xfail).

- [ ] **Step 5: Commit**

```bash
git add server/internal/stream/probe.go server/internal/stream/ingest.go server/internal/stream/join_test.go
git commit -m "fix(ingest): new transcoders join at the last video sequence header or IDR

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: Bowtie-owned timeline (pure core)

**Files:**
- Create: `server/internal/stream/hlsplaylist.go`
- Create: `server/internal/stream/timeline.go`
- Test: `server/internal/stream/timeline_test.go`

**Interfaces:**
- Produces (package `stream`):
  - `const WallClockLayout = "2006-01-02T15:04:05.000Z07:00"`.
  - `type MediaPlaylist struct { Header []string; MediaSequence int64; Segments []PlaylistSegment; Trailer []string }` and `type PlaylistSegment struct { Duration float64; Tags []string; URI string }`.
  - `func ParseMediaPlaylist(raw []byte) MediaPlaylist` drops `#EXT-X-PROGRAM-DATE-TIME`, `#EXT-X-DISCONTINUITY` and `#EXT-X-DISCONTINUITY-SEQUENCE`.
  - `func NewTimeline(dir string, renditions []string, now func() time.Time) *Timeline`. `now` must be the real clock, because stamps are compared with segment file mtimes.
  - `(*Timeline).MarkRestart()`: call it before an append restart starts FFmpeg. It records, per rendition, the media sequence the new run will start at (the playlist's MEDIA-SEQUENCE plus its segment count).
  - `(*Timeline).Retime(rendition string, raw []byte) []byte` returns the playlist with Bowtie's `#EXT-X-PROGRAM-DATE-TIME` on every segment, `#EXT-X-DISCONTINUITY` only on the first segment of a restarted run, and `#EXT-X-DISCONTINUITY-SEQUENCE`.
  - `(*Timeline).Window(rendition string, raw []byte) WindowTimeline`, with `type WindowTimeline struct { MediaSequence int64; Segments []TimelineSegment }`, `type TimelineSegment struct { Start time.Time; Duration float64 }`, and `WindowStart()`/`LiveEdge() time.Time`.
- How it works:
  - Each segment is keyed by (run, index in run); every rendition shares one stamp per key.
  - A stamp is made the first time any rendition lists the segment:
    1. Start at the segment file's mtime minus its EXTINF. If the file is missing, or its mtime is later than now, use now minus its EXTINF.
    2. If the previous segment of the same run is stamped, chain onto its end, unless the observed start is more than 2 s later (a real gap). Then jump forward and log.
    3. With no previous stamp, start no later than the next stamped segment's start minus this segment's duration.
    4. Never start before the latest end of any earlier run.
  - The DISCONTINUITY-SEQUENCE is the number of distinct restart boundaries below MEDIA-SEQUENCE. Stamps more than two segments behind every polled rendition's window are pruned.

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

func parseServed(t *testing.T, out []byte) (dseq int64, segs []servedSeg) {
	t.Helper()
	var cur servedSeg
	for _, line := range strings.Split(string(out), "\n") {
		switch {
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
	return dseq, segs
}

func TestTimelineStampsFromMtimeAndChains(t *testing.T) {
	dir := t.TempDir()
	tl, _ := newTestTimeline(dir, "v480")
	_, segs := parseServed(t, tl.Retime("v480", writeRun(t, dir, "v480", 0, seg{4, 4}, seg{4, 8}, seg{4, 12.3})))
	for i, want := range []time.Time{sec(0), sec(4), sec(8)} {
		if !segs[i].pdt.Equal(want) || segs[i].disc {
			t.Fatalf("segment %d: %+v, want PDT %v and no discontinuity", i, segs[i], want)
		}
	}
}

func TestTimelineJumpsForwardOverAGap(t *testing.T) {
	dir := t.TempDir()
	tl, logs := newTestTimeline(dir, "v480")
	tl.Retime("v480", writeRun(t, dir, "v480", 0, seg{4, 4}, seg{4, 8}))
	// 5 s without output (a dropout FFmpeg skipped), then segment 2.
	_, segs := parseServed(t, tl.Retime("v480", writeRun(t, dir, "v480", 0, seg{4, 4}, seg{4, 8}, seg{4, 17})))
	if !segs[2].pdt.Equal(sec(13)) {
		t.Fatalf("segment 2 PDT %v, want %v", segs[2].pdt, sec(13))
	}
	if segs[2].disc {
		t.Fatal("a gap inside one FFmpeg run is not a discontinuity")
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "gap") {
		t.Fatalf("logs %v, want one gap line", *logs)
	}
}

func TestTimelineAlignsRenditionsByIndexAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	tl, _ := newTestTimeline(dir, "v480", "aac0")
	tl.Retime("v480", writeRun(t, dir, "v480", 0, seg{4.004, 4}, seg{4.004, 8}, seg{4.004, 12}))
	// Audio was one segment ahead when FFmpeg died.
	tl.Retime("aac0", writeRun(t, dir, "aac0", 0, seg{4, 4}, seg{4, 8}, seg{4, 12}, seg{4, 16}))
	tl.MarkRestart()
	// The new run's first segment in each rendition; audio's file landed 300 ms later.
	aac := tl.Retime("aac0", writeRun(t, dir, "aac0", 2, seg{4, 12}, seg{4, 16}, seg{4, 80.3}))
	vid := tl.Retime("v480", writeRun(t, dir, "v480", 1, seg{4.004, 8}, seg{4.004, 12}, seg{4.004, 80}))
	_, as := parseServed(t, aac)
	_, vs := parseServed(t, vid)
	if !as[2].disc || !vs[2].disc || as[1].disc || vs[1].disc {
		t.Fatalf("DISCONTINUITY must mark exactly the restart: audio %+v video %+v", as, vs)
	}
	if !as[2].pdt.Equal(vs[2].pdt) || !as[2].pdt.Equal(sec(76.3)) {
		t.Fatalf("restart PDT audio %v video %v, want both %v", as[2].pdt, vs[2].pdt, sec(76.3))
	}
	// The window slides past the restart: the tag leaves, the sequence counts it.
	dseq, vs2 := parseServed(t, tl.Retime("v480", writeRun(t, dir, "v480", 4, seg{4.004, 84}, seg{4.004, 88})))
	if dseq != 1 || vs2[0].disc {
		t.Fatalf("after sliding: DISCONTINUITY-SEQUENCE %d, first %+v; want 1 and no tag", dseq, vs2[0])
	}
}

func TestTimelineNeverGoesBackwardsAtRestart(t *testing.T) {
	dir := t.TempDir()
	tl, _ := newTestTimeline(dir, "v480")
	tl.Retime("v480", writeRun(t, dir, "v480", 0, seg{4, 4}, seg{4, 8}, seg{4, 12}))
	tl.MarkRestart()
	// The new run's first segment claims to end at 10 s, before the old run's 12 s end.
	_, segs := parseServed(t, tl.Retime("v480", writeRun(t, dir, "v480", 0, seg{4, 4}, seg{4, 8}, seg{4, 12}, seg{4, 10})))
	if !segs[3].disc || !segs[3].pdt.Equal(sec(12)) {
		t.Fatalf("restart segment %+v, want DISCONTINUITY at %v", segs[3], sec(12))
	}
}

func TestTimelineDropsFFmpegTimelineTags(t *testing.T) {
	dir := t.TempDir()
	tl, _ := newTestTimeline(dir, "v720")
	raw := []byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-DISCONTINUITY\n" +
		"#EXT-X-PROGRAM-DATE-TIME:2026-10-11T19:00:00.000+0000\n#EXTINF:4.004000,\nv720_00000.ts\n" +
		"#EXT-X-DISCONTINUITY\n#EXT-X-PROGRAM-DATE-TIME:2026-10-11T18:00:00.000+0000\n#EXTINF:4.004000,\nv720_00001.ts\n")
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

func TestTimelineRestartWithoutSegmentsCountsOnce(t *testing.T) {
	dir := t.TempDir()
	tl, _ := newTestTimeline(dir, "v480")
	tl.Retime("v480", writeRun(t, dir, "v480", 0, seg{4, 4}, seg{4, 8}))
	tl.MarkRestart() // this run died before writing a segment
	tl.MarkRestart()
	_, segs := parseServed(t, tl.Retime("v480", writeRun(t, dir, "v480", 0, seg{4, 4}, seg{4, 8}, seg{4, 30})))
	if !segs[2].disc || segs[1].disc {
		t.Fatalf("segments %+v, want one DISCONTINUITY on segment 2", segs)
	}
	if dseq, _ := parseServed(t, tl.Retime("v480", writeRun(t, dir, "v480", 3, seg{4, 34}))); dseq != 1 {
		t.Fatalf("DISCONTINUITY-SEQUENCE %d, want 1", dseq)
	}
}

func TestTimelinePrunesOldStamps(t *testing.T) {
	dir := t.TempDir()
	tl, _ := newTestTimeline(dir, "v480")
	for n := int64(0); n < 50; n++ {
		var segs []seg
		for i := n; i < n+5; i++ {
			segs = append(segs, seg{4, float64(4 * (i + 1))})
		}
		_, out := parseServed(t, tl.Retime("v480", writeRun(t, dir, "v480", n, segs...)))
		if !out[0].pdt.Equal(sec(float64(4 * n))) {
			t.Fatalf("window %d starts at %v, want %v", n, out[0].pdt, sec(float64(4*n)))
		}
	}
	if len(tl.stamps) > 8 {
		t.Fatalf("%d stamps kept for a 5-segment window", len(tl.stamps))
	}
}

func TestTimelineWindow(t *testing.T) {
	dir := t.TempDir()
	tl, _ := newTestTimeline(dir, "v480")
	w := tl.Window("v480", writeRun(t, dir, "v480", 5, seg{4, 4}, seg{4, 8}))
	if w.MediaSequence != 5 || len(w.Segments) != 2 || !w.WindowStart().Equal(sec(0)) || !w.LiveEdge().Equal(sec(8)) {
		t.Fatalf("window %+v start %v edge %v", w, w.WindowStart(), w.LiveEdge())
	}
	if (WindowTimeline{}).LiveEdge() != (time.Time{}) {
		t.Fatal("empty window has a live edge")
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
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// WallClockLayout is how Bowtie writes wall-clock times (playlists and the
// timeline API): RFC 3339 with milliseconds.
const WallClockLayout = "2006-01-02T15:04:05.000Z07:00"

// timelineGapThreshold: a segment observed this much later than the chained
// timeline says follows a real gap (a dropout FFmpeg skipped); the timeline
// jumps forward instead of absorbing it.
const timelineGapThreshold = 2 * time.Second

// Timeline is a session's wall-clock record of its segments. Each segment is
// stamped once, the first time any rendition lists it, keyed by (FFmpeg run,
// index in run): the renditions of one run are cut together, so the same
// key is the same broadcast moment in all of them even when their media
// sequence numbers differ (after a kill, audio can be one segment ahead).
type Timeline struct {
	dir        string
	renditions []string
	now        func() time.Time
	logf       func(format string, args ...any)

	mu     sync.Mutex
	epochs []map[string]int64  // run → rendition → media sequence of its first segment (run 0: 0)
	stamps map[stampKey]stamp  // pruned behind every polled rendition's window
	runEnd map[int]time.Time   // run → latest segment end stamped in it (never pruned)
	low    map[string]stampKey // rendition → key of its window's first segment
}

type stampKey struct {
	run   int
	index int64
}

func (a stampKey) less(b stampKey) bool {
	return a.run < b.run || (a.run == b.run && a.index < b.index)
}

type stamp struct {
	start time.Time
	dur   float64
}

func seconds(f float64) time.Duration { return time.Duration(f * float64(time.Second)) }

func (s stamp) end() time.Time { return s.start.Add(seconds(s.dur)) }

// NewTimeline returns the timeline of the session in dir. now must be the
// real clock: stamps come from segment file mtimes.
func NewTimeline(dir string, renditions []string, now func() time.Time) *Timeline {
	return &Timeline{
		dir: dir, renditions: renditions, now: now, logf: log.Printf,
		epochs: []map[string]int64{{}},
		stamps: map[stampKey]stamp{},
		runEnd: map[int]time.Time{},
		low:    map[string]stampKey{},
	}
}

// MarkRestart starts a new run: an append restart's FFmpeg continues each
// rendition playlist after the segments it lists now. Call it before the
// new process starts.
func (t *Timeline) MarkRestart() {
	first := map[string]int64{}
	for _, r := range t.renditions {
		raw, err := os.ReadFile(filepath.Join(t.dir, r+".m3u8"))
		if err != nil {
			continue
		}
		p := ParseMediaPlaylist(raw)
		first[r] = p.MediaSequence + int64(len(p.Segments))
	}
	t.mu.Lock()
	t.epochs = append(t.epochs, first)
	t.mu.Unlock()
}

// keyLocked maps a rendition's media sequence number to (run, index in run).
func (t *Timeline) keyLocked(rendition string, seq int64) stampKey {
	for run := len(t.epochs) - 1; run > 0; run-- {
		if first, ok := t.epochs[run][rendition]; ok && seq >= first {
			return stampKey{run, seq - first}
		}
	}
	return stampKey{0, seq}
}

type stampedSegment struct {
	PlaylistSegment
	Seq      int64
	Start    time.Time
	Boundary bool // first segment of a restarted run
}

func (t *Timeline) stampAll(rendition string, p MediaPlaylist) ([]stampedSegment, int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]stampedSegment, len(p.Segments))
	for i, sg := range p.Segments {
		seq := p.MediaSequence + int64(i)
		k := t.keyLocked(rendition, seq)
		st, ok := t.stamps[k]
		if !ok {
			st = t.newStampLocked(k, sg)
			t.stamps[k] = st
			if e := st.end(); e.After(t.runEnd[k.run]) {
				t.runEnd[k.run] = e
			}
		}
		out[i] = stampedSegment{PlaylistSegment: sg, Seq: seq, Start: st.start, Boundary: k.run > 0 && k.index == 0}
	}
	dseq := t.discontinuitySequenceLocked(rendition, p.MediaSequence)
	t.pruneLocked(rendition, p.MediaSequence)
	return out, dseq
}

func (t *Timeline) newStampLocked(k stampKey, sg PlaylistSegment) stamp {
	d := seconds(sg.Duration)
	end := t.now()
	if fi, err := os.Stat(filepath.Join(t.dir, sg.URI)); err == nil && fi.ModTime().Before(end) {
		end = fi.ModTime()
	}
	start := end.Add(-d)
	if prev, ok := t.stamps[stampKey{k.run, k.index - 1}]; ok {
		chained := prev.end()
		if gap := start.Sub(chained); gap > timelineGapThreshold {
			t.logf("stream: timeline %s: %v gap before segment %d of run %d",
				filepath.Base(t.dir), gap.Round(100*time.Millisecond), k.index, k.run)
		} else {
			start = chained
		}
	} else if next, ok := t.stamps[stampKey{k.run, k.index + 1}]; ok {
		if latest := next.start.Add(-d); start.After(latest) {
			start = latest
		}
	}
	if floor := t.floorLocked(k.run); start.Before(floor) {
		start = floor
	}
	return stamp{start: start, dur: sg.Duration}
}

// floorLocked is the latest end stamped in any earlier run: a restarted run
// never starts before the run it follows ended.
func (t *Timeline) floorLocked(run int) time.Time {
	var f time.Time
	for r, e := range t.runEnd {
		if r < run && e.After(f) {
			f = e
		}
	}
	return f
}

// discontinuitySequenceLocked counts the restart boundaries that slid out of
// the window. Runs that died before writing a segment share their boundary
// with the next run, so boundaries are counted once.
func (t *Timeline) discontinuitySequenceLocked(rendition string, mseq int64) int64 {
	seen := map[int64]bool{}
	for run := 1; run < len(t.epochs); run++ {
		if first, ok := t.epochs[run][rendition]; ok && first < mseq {
			seen[first] = true
		}
	}
	return int64(len(seen))
}

// pruneLocked drops stamps more than two segments behind every polled
// rendition's window (the previous stamp is kept for chaining).
func (t *Timeline) pruneLocked(rendition string, mseq int64) {
	t.low[rendition] = t.keyLocked(rendition, mseq)
	var low stampKey
	first := true
	for _, k := range t.low {
		if first || k.less(low) {
			low, first = k, false
		}
	}
	cut := stampKey{low.run, low.index - 2}
	for k := range t.stamps {
		if k.less(cut) {
			delete(t.stamps, k)
		}
	}
}

// Retime rewrites one rendition playlist (FFmpeg's bytes, segment URIs still
// bare) with the timeline: Bowtie's #EXT-X-PROGRAM-DATE-TIME on every
// segment, #EXT-X-DISCONTINUITY only where a restarted run begins (the same
// moment in every rendition) and #EXT-X-DISCONTINUITY-SEQUENCE.
func (t *Timeline) Retime(rendition string, raw []byte) []byte {
	p := ParseMediaPlaylist(raw)
	segs, dseq := t.stampAll(rendition, p)
	var b bytes.Buffer
	for _, h := range p.Header {
		b.WriteString(h)
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "#EXT-X-MEDIA-SEQUENCE:%d\n#EXT-X-DISCONTINUITY-SEQUENCE:%d\n", p.MediaSequence, dseq)
	for _, s := range segs {
		if s.Boundary {
			b.WriteString("#EXT-X-DISCONTINUITY\n")
		}
		b.WriteString("#EXT-X-PROGRAM-DATE-TIME:" + s.Start.UTC().Format(WallClockLayout) + "\n")
		for _, tag := range s.Tags {
			b.WriteString(tag)
			b.WriteByte('\n')
		}
		b.WriteString(s.URI)
		b.WriteByte('\n')
	}
	for _, tr := range p.Trailer {
		b.WriteString(tr)
		b.WriteByte('\n')
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

// Window stamps raw like Retime and returns its wall-clock map.
func (t *Timeline) Window(rendition string, raw []byte) WindowTimeline {
	p := ParseMediaPlaylist(raw)
	segs, _ := t.stampAll(rendition, p)
	w := WindowTimeline{MediaSequence: p.MediaSequence}
	for _, s := range segs {
		w.Segments = append(w.Segments, TimelineSegment{Start: s.Start, Duration: s.Duration})
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
git commit -m "feat(stream): Bowtie-owned segment timeline (PDT, restart discontinuities)

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---
### Task 10: Serve every media playlist through the session timeline

**Files:**
- Modify: `server/internal/transcode/layout.go` (`Renditions`)
- Test: `server/internal/transcode/layout_test.go`
- Modify: `server/internal/stream/session.go`, `server/internal/stream/manager.go`
- Test: `server/internal/stream/manager_test.go`
- Modify: `server/internal/api/stream_handlers.go` (`StreamController`, `serveMediaPlaylist`)
- Test: `server/internal/api/stream_handlers_test.go`
- Modify: `server/internal/e2e/testdata/scenarios/{long-outage-restart,baseline,transcoder-restart}.yaml`

**Interfaces:**
- Consumes: `NewTimeline`, `(*Timeline).MarkRestart`, `(*Timeline).Retime` (Task 9).
- Produces:
  - `func (l Layout) Renditions() []string`: the media playlist names FFmpeg writes, without `.m3u8` (rungs, `aacN`, `ac3N`).
  - `session.timeline *Timeline`, created in `startAttempt` with `time.Now`.
  - `restartSession` calls `sess.timeline.MarkRestart()` right before `m.runner.Start`.
  - `func (m *Manager) RetimePlaylist(viewerID, rendition string, raw []byte) ([]byte, bool)`.
  - `func (m *Manager) timelineOf(viewerID string) (tl *Timeline, dir, top string, owner int64, ok bool)`; Task 11 uses it.
  - `api.StreamController` gains `RetimePlaylist(viewerID, rendition string, raw []byte) ([]byte, bool)`.
  - `serveMediaPlaylist` retimes after the caption repair and before URL rewriting. A caption playlist `<rung>_vtt.m3u8` uses the rung's key, because caption segments share the rung's sequence numbers.

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
// After an append restart, the served playlist marks the restart with a
// DISCONTINUITY on the new run's first segment, Bowtie's PDT on every segment.
func TestRestartStartsANewTimelineRun(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	m, _, _ := newTestManagerWithDial(st, cfg, clock, runner, nil)
	h, err := m.Start(context.Background(), user, chID, clientCaps(""))
	if err != nil {
		t.Fatal(err)
	}
	media, _ := m.SessionMediaOf(h.ViewerID)
	top := media.Layout.TopName()
	before := "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:4.0,\n" + top + "_00000.ts\n#EXTINF:4.0,\n" + top + "_00001.ts\n"
	if err := os.WriteFile(filepath.Join(h.SessionDir, top+".m3u8"), []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
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
	out, ok := m.RetimePlaylist(h.ViewerID, top, []byte(before+"#EXTINF:4.0,\n"+top+"_00002.ts\n"))
	if !ok {
		t.Fatal("no timeline for the viewer")
	}
	s := string(out)
	i := strings.Index(s, "#EXT-X-DISCONTINUITY\n#EXT-X-PROGRAM-DATE-TIME:")
	if i < 0 || strings.Index(s, top+"_00001.ts") > i || !strings.Contains(s[i:], top+"_00002.ts") ||
		strings.Count(s, "#EXT-X-DISCONTINUITY\n") != 1 || strings.Count(s, "#EXT-X-PROGRAM-DATE-TIME:") != 3 {
		t.Fatalf("retimed playlist:\n%s", s)
	}
	if _, ok := m.RetimePlaylist("no-such-viewer", top, []byte(before)); ok {
		t.Fatal("unknown viewer retimed")
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
// segment URLs are signed.
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

Run: `cd server && go test ./internal/transcode/ ./internal/stream/ ./internal/api/ -run 'TestLayoutRenditions|TestRestartStartsANewTimelineRun|TestMediaPlaylistServedThroughTimeline' -v`
Expected: FAIL to compile (`l.Renditions undefined`, `m.RetimePlaylist undefined`).

- [ ] **Step 3: Implement**

`transcode/layout.go`, after `CaptionPlaylist`:

```go
// Renditions are the media playlist names (without .m3u8) FFmpeg writes for
// l: every rung, then the AAC and AC-3 renditions (VarStreamMap's names).
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

`stream/session.go`, add to `session` after `hlsListSize`:

```go
	timeline *Timeline // wall-clock record of the segments (served PDT, discontinuities)
```

`stream/manager.go`:
- In `startAttempt`'s `sess := &session{…}` literal add:

```go
		// Real clock: stamps are compared with segment file mtimes.
		timeline: NewTimeline(dir, layout.Renditions(), time.Now),
```

- In `restartSession`, right before `log.Printf("stream: session %s: restarting ffmpeg (append to playlist)", sess.id)`:

```go
	// The new process continues every playlist after its last segment: a new
	// timeline run (DISCONTINUITY there, PDT never earlier than the old run's end).
	if sess.timeline != nil {
		sess.timeline.MarkRestart()
	}
```

- Add after `SessionMediaOf`:

```go
// timelineOf returns the viewer's session timeline, its dir and top
// rendition, and the viewer's user.
func (m *Manager) timelineOf(viewerID string) (tl *Timeline, dir, top string, owner int64, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.viewers[viewerID]
	if !ok {
		return nil, "", "", 0, false
	}
	sess, ok := m.sessions[v.SessionID]
	if !ok || sess.terminated || sess.timeline == nil {
		return nil, "", "", 0, false
	}
	return sess.timeline, sess.dir, sess.layout.TopName(), v.UserID, true
}

// RetimePlaylist rewrites one rendition playlist (FFmpeg's bytes, segment
// names still bare) with the session timeline: Bowtie's PDT on every
// segment, DISCONTINUITY only where a restarted FFmpeg run begins, and
// DISCONTINUITY-SEQUENCE. False when the viewer has no session.
func (m *Manager) RetimePlaylist(viewerID, rendition string, raw []byte) ([]byte, bool) {
	tl, _, _, _, ok := m.timelineOf(viewerID)
	if !ok {
		return nil, false
	}
	return tl.Retime(rendition, raw), true
}
```

`api/stream_handlers.go`:
- Add to `StreamController`:

```go
	// RetimePlaylist rewrites a rendition playlist with the session timeline
	// (PDT, discontinuities); false when the viewer has no session.
	RetimePlaylist(viewerID, rendition string, raw []byte) ([]byte, bool)
```

- In `serveMediaPlaylist`, after the caption-repair `if` block and before `rewritten := …`:

```go
	rendition := strings.TrimSuffix(name, ".m3u8")
	if rung, ok := strings.CutSuffix(rendition, "_vtt"); ok {
		rendition = rung // caption segments share the rung's sequence numbers
	}
	if retimed, ok := s.deps.Streams.RetimePlaylist(viewerID, rendition, raw); ok {
		raw = retimed
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd server && go test ./internal/transcode/ ./internal/stream/ ./internal/api/ -race -count=1`
Expected: PASS.

- [ ] **Step 5: Remove the `long-outage-restart` xfail and add guards**

Delete the `xfail:` line from `long-outage-restart.yaml`. Add to `baseline.yaml`'s `expect:` block:

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
```

Run: `cd server && go test -count=1 -tags ffmpeg -p 1 -parallel 2 -timeout 30m -run TestScenarios -v ./internal/e2e/ 2>&1 | grep -E -- '--- |xfail \(|known gap|consistency:'`
Expected: every scenario PASS, no `xfail (` lines left, no `consistency:` lines on passing scenarios.

Run: `cd server && BOWTIE_FFMPEG_PATH="$PWD/scripts/ffmpeg51" go test -count=1 -tags ffmpeg -p 1 -parallel 2 -timeout 60m -run 'TestScenarios/(long-outage-restart|transcoder-restart|baseline)$' -v ./internal/e2e/ 2>&1 | grep -E -- '--- |consistency:'`
Expected: PASS on FFmpeg 5.1. 5.1's spurious top DISCONTINUITY must not show up as a boundary.

- [ ] **Step 6: Commit**

```bash
git add server/internal/transcode/layout.go server/internal/transcode/layout_test.go server/internal/stream/session.go server/internal/stream/manager.go server/internal/stream/manager_test.go server/internal/api/stream_handlers.go server/internal/api/stream_handlers_test.go server/internal/e2e/testdata/scenarios
git commit -m "feat(stream): serve Bowtie's PDT and restart discontinuities in every rendition

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
  - `func (m *Manager) TimelineOf(viewerID string) (WindowTimeline, int64, bool)` returns the window of the session's top rendition and the viewer's user ID. It reports false when the viewer has no session.
  - `api.StreamController` gains `TimelineOf(viewerID string) (stream.WindowTimeline, int64, bool)`.
  - `GET /api/v1/sessions/{viewerId}/timeline` returns 200 with `{"mediaSequence": n, "windowStart": "<RFC 3339 ms>"|null, "liveEdge": "<RFC 3339 ms>"|null, "segments": [["<start>", <durationSec>], …]}`.
  - Access: the viewer's own stream token (`?token=`), or a Bearer token whose user ID equals the viewer's user. Anything else gets **404** (`session not found`), the same answer as a missing viewer, so viewer IDs can't be probed. It doesn't touch the viewer; playlist polls keep it alive.

- [ ] **Step 1: Write the failing tests**

Append to `server/internal/stream/manager_test.go`:

```go
func TestTimelineOfReportsOwnerAndWindow(t *testing.T) {
	st, cfg, clock, runner, chID, user := setupEnv(t)
	m := newTestManager(st, cfg, clock, runner)
	h, err := m.Start(context.Background(), user, chID, clientCaps(""))
	if err != nil {
		t.Fatal(err)
	}
	media, _ := m.SessionMediaOf(h.ViewerID)
	top := media.Layout.TopName()
	pl := "#EXTM3U\n#EXT-X-MEDIA-SEQUENCE:3\n#EXTINF:4.0,\n" + top + "_00003.ts\n#EXTINF:4.0,\n" + top + "_00004.ts\n"
	if err := os.WriteFile(filepath.Join(h.SessionDir, top+".m3u8"), []byte(pl), 0o644); err != nil {
		t.Fatal(err)
	}
	win, owner, ok := m.TimelineOf(h.ViewerID)
	if !ok || owner != user.ID || win.MediaSequence != 3 || len(win.Segments) != 2 {
		t.Fatalf("TimelineOf = %+v owner=%d ok=%v", win, owner, ok)
	}
	if !win.LiveEdge().Equal(win.WindowStart().Add(8 * time.Second)) {
		t.Fatalf("window %v → %v, want 8 s", win.WindowStart(), win.LiveEdge())
	}
	if _, _, ok := m.TimelineOf("no-such-viewer"); ok {
		t.Fatal("unknown viewer has a timeline")
	}
}
```

In `server/internal/api/stream_handlers_test.go`, add to `stubStreams`:

```go
	timelines map[string]timelineStub // viewerID → window + owner
```

and:

```go
type timelineStub struct {
	owner int64
	win   stream.WindowTimeline
}

func (s *stubStreams) TimelineOf(viewerID string) (stream.WindowTimeline, int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tl, ok := s.timelines[viewerID]
	return tl.win, tl.owner, ok
}
```

Append the test:

```go
// Only the viewer's own stream token or its owner's Bearer token sees the
// timeline; anyone else (even an admin) gets the same 404 as a missing viewer.
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

	viewerTok := stream.SignStreamToken([]byte(streamSecret), "viewer-a", time.Now().UTC().Add(time.Hour))
	if rr := doJSON(t, h, "GET", path+"?token="+viewerTok, nil, nil); rr.Code != http.StatusOK {
		t.Fatalf("viewer's stream token: %d", rr.Code)
	}
	otherTok := stream.SignStreamToken([]byte(streamSecret), "viewer-b", time.Now().UTC().Add(time.Hour))
	for name, req := range map[string]struct {
		path string
		hdr  map[string]string
	}{
		"another user's bearer":   {path, map[string]string{"Authorization": "Bearer " + bobTok}},
		"no auth":                 {path, nil},
		"another viewer's token":  {path + "?token=" + otherTok, nil},
		"owner, unknown viewer":   {"/api/v1/sessions/viewer-x/timeline", map[string]string{"Authorization": "Bearer " + aliceTok}},
		"garbage bearer":          {path, map[string]string{"Authorization": "Bearer nope"}},
	} {
		if rr := doJSON(t, h, "GET", req.path, nil, req.hdr); rr.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", name, rr.Code)
		}
	}
}
```

Add `"encoding/json"` to the test file's imports if it isn't there.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd server && go test ./internal/stream/ ./internal/api/ -run 'TestTimelineOf|TestSessionTimelineOwnerOnly' -v`
Expected: FAIL to compile (`m.TimelineOf undefined`), then (after Step 3's manager part) a 404 from the missing route.

- [ ] **Step 3: Implement**

`stream/manager.go`, after `RetimePlaylist`:

```go
// TimelineOf maps the viewer's current window (top rendition) to the wall
// clock and returns the viewer's user ID. False when the viewer has no session.
func (m *Manager) TimelineOf(viewerID string) (WindowTimeline, int64, bool) {
	tl, dir, top, owner, ok := m.timelineOf(viewerID)
	if !ok {
		return WindowTimeline{}, 0, false
	}
	raw, err := os.ReadFile(filepath.Join(dir, top+".m3u8"))
	if err != nil {
		return WindowTimeline{}, owner, true // no playlist yet: an empty window
	}
	return tl.Window(top, raw), owner, true
}
```

`api/stream_handlers.go`, add to `StreamController`:

```go
	// TimelineOf maps the viewer's window to the wall clock and returns the
	// viewer's user ID; false when the viewer has no session.
	TimelineOf(viewerID string) (stream.WindowTimeline, int64, bool)
```

and the handler (after `handleHeartbeat`):

```go
// handleSessionTimeline maps the viewer's current window to wall-clock time
// for clients that can't read EXT-X-PROGRAM-DATE-TIME (Roku). Only the
// viewer's own stream token or a Bearer token of the user who owns the
// viewer sees it; anything else gets the same 404 as a missing viewer.
func (s *Server) handleSessionTimeline(w http.ResponseWriter, r *http.Request) {
	viewerID := r.PathValue("viewerId")
	if s.deps.Streams == nil {
		writeError(w, http.StatusServiceUnavailable, "streaming not available")
		return
	}
	win, owner, ok := s.deps.Streams.TimelineOf(viewerID)
	if !ok || !s.ownsViewer(viewerID, owner, r) {
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

// ownsViewer accepts the viewer's own stream token, or a Bearer token of the
// user the viewer belongs to (unlike authorizeViewerRequest, any other
// signed-in user is refused).
func (s *Server) ownsViewer(viewerID string, owner int64, r *http.Request) bool {
	if s.verifyStreamAccess(viewerID, r) == nil {
		return true
	}
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || s.deps.Auth == nil {
		return false
	}
	claims, err := s.deps.Auth.ParseAccessToken(strings.TrimSpace(raw), time.Now().UTC())
	return err == nil && claims.UserID == owner
}
```

`api/server.go`, after the heartbeat route:

```go
	handleFunc("GET /api/v1/sessions/{viewerId}/timeline", s.handleSessionTimeline)
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
        Only the viewer's own stream token, or a Bearer token of the user who
        owns the viewer, sees it; anything else gets 404.
      operationId: getSessionTimeline
      tags: [stream]
      parameters:
        - name: token
          in: query
          required: false
          description: The viewer's signed stream token (alternative to Bearer auth)
          schema:
            type: string
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
git commit -m "feat(api): session timeline endpoint for clients without PDT

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 12: Caption `.vtt` sweeper

**Files:**
- Create: `server/internal/stream/captions_sweep.go`
- Test: `server/internal/stream/captions_sweep_test.go`
- Modify: `server/internal/stream/manager.go` (`maintain`)

**Interfaces:**
- Consumes: `ParseMediaPlaylist` (Task 9), `transcode.Layout.CaptionPlaylist()`/`TopName()`, `transcode.DefaultHLSListSize`.
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

// hlsSegmentDuration is FFmpeg's -hls_time for live sessions.
const hlsSegmentDuration = 4 * time.Second

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

### Task 13: One-hour real-broadcast soak (final gate for the timestamp option)

**Files:**
- Create: `server/scripts/ffmpeg-soak` (executable)
- Create: `server/internal/e2e/realsoak_test.go`

**Interfaces:**
- Consumes: testplayer (`AllRenditions`, `Probe`, `Report`, `Consistency`), `FFprobe`, `ffprobePath` (Task 4).
- Produces:
  - `TestRealSoak` (build tags `ffmpeg && realsoak`) watches a running Bowtie. It reads `BOWTIE_SOAK_URL`, `BOWTIE_SOAK_USER`, `BOWTIE_SOAK_PASS`, `BOWTIE_SOAK_CHANNEL`, `BOWTIE_SOAK_FOR` (default `1h`) and the optional `BOWTIE_SOAK_DEVICE` from the environment. Nothing in the code names a host.
  - `server/scripts/ffmpeg-soak` is an in-container FFmpeg wrapper for soak images only. It raises `-loglevel warning` to `level+debug`, because 5.1 logs "timestamp discontinuity" at debug level, and keeps only the lines the soak counts.
- Pass criteria, over a clean hour that includes ad breaks:
  - zero FFmpeg restarts
  - |timeline excess| ≤ 2 s
  - max audio hole ≤ 50 ms
  - max A/V start skew ≤ 100 ms
  - every consistency flag true
  - no backward jumps, segment errors or session loss
  - zero "transcoder stuck" lines
- A "timestamp discontinuity" line is a **false re-base** when it comes with any of: an audio hole over 50 ms, an A/V skew over 100 ms, or a timeline-excess change over 1 s within the next minute. Lines with none of these are true splice re-bases; count them and keep them.

- [ ] **Step 1: Write the soak wrapper**

Create `server/scripts/ffmpeg-soak` and `chmod +x` it:

```bash
#!/bin/bash
# ffmpeg-soak: FFmpeg for real-broadcast soak images only (never production).
# Raises "-loglevel warning" to level+debug so FFmpeg 5.1's "timestamp
# discontinuity" lines (debug level in 5.1) reach the log, and keeps only the
# lines the soak counts. Point BOWTIE_FFMPEG_PATH at it inside the image.
args=()
prev=""
for a in "$@"; do
  if [ "$prev" = "-loglevel" ] && [ "$a" = "warning" ]; then
    a="level+debug"
  fi
  args+=("$a")
  prev=$a
done
exec /usr/bin/ffmpeg "${args[@]}" 2> >(grep --line-buffered -E 'timestamp discontinuity|frames duplicated|Invalid frame dimensions|\[(warning|error|fatal)\]' >&2)
```

- [ ] **Step 2: Write the soak test**

Create `server/internal/e2e/realsoak_test.go`:

```go
//go:build ffmpeg && realsoak

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/ajthom90/bowtie/server/internal/testplayer"
)

// TestRealSoak watches one real channel on a running Bowtie with the virtual
// player following every rendition, and fails on what a viewer would notice.
//
//	BOWTIE_SOAK_URL      Bowtie base URL
//	BOWTIE_SOAK_USER     admin username
//	BOWTIE_SOAK_PASS     admin password
//	BOWTIE_SOAK_CHANNEL  guide number
//	BOWTIE_SOAK_FOR      duration (default 1h)
//	BOWTIE_SOAK_DEVICE   HDHomeRun address to add first (optional)
func TestRealSoak(t *testing.T) {
	need := func(k string) string {
		v := os.Getenv(k)
		if v == "" {
			t.Skipf("%s not set", k)
		}
		return v
	}
	base, user, pass, guide := need("BOWTIE_SOAK_URL"), need("BOWTIE_SOAK_USER"), need("BOWTIE_SOAK_PASS"), need("BOWTIE_SOAK_CHANNEL")
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

	p, err := testplayer.Start(context.Background(), testplayer.Config{
		BaseURL: base, Token: c.token, ChannelID: id, AllRenditions: true, Probe: FFprobe(ffprobePath()),
		Caps: map[string]any{"videoCodecs": []string{"h264"}, "audioCodecs": []string{"aac"}, "maxHeight": 720},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), dur)
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // progress every 5 minutes, with wall-clock times to line up with docker logs
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

	t.Logf("renditions=%v segments=%d segErrors=%d excess=%.2fs audioHole=%v avSkew=%v probed=%d/%d maxGap=%v",
		r.Renditions, r.SegmentsFetched, r.SegmentErrors, r.TimelineExcess, r.MaxAudioHole, r.MaxAVStartSkew, r.ProbedAudio, r.ProbedVideo, r.MaxGap)
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
		t.Errorf("audio hole %v (probed %d), want ≤ 50ms", r.MaxAudioHole, r.ProbedAudio)
	}
	if r.MaxAVStartSkew > 100*time.Millisecond {
		t.Errorf("A/V start skew %v, want ≤ 100ms", r.MaxAVStartSkew)
	}
	if r.SegmentErrors > 0 {
		t.Errorf("%d segment errors", r.SegmentErrors)
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

Run: `cd server && go vet -tags ffmpeg,realsoak ./internal/e2e/ && go test -tags ffmpeg,realsoak -run TestRealSoak ./internal/e2e/`
Expected: vet clean; the test SKIPs (`BOWTIE_SOAK_URL not set`).

Commit:

```bash
git add server/scripts/ffmpeg-soak server/internal/e2e/realsoak_test.go
git commit -m "test(e2e): real-broadcast soak against a running Bowtie

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

- [ ] **Step 3: Build and start the soak server (production image, FFmpeg 5.1, software encode)**

This runs on the dev Mac, never on the production server. It uses one tuner of the household HDHomeRun. Set `HDHR_ADDR` to its LAN address in your shell first (the local test setup uses 192.168.50.32); the address is passed only through env vars, never written into code. Guide download is off.

```bash
cd "$(git rev-parse --show-toplevel)"
docker build -f deploy/Dockerfile -t bowtie:soak .
printf 'FROM bowtie:soak\nCOPY ffmpeg-soak /usr/local/bin/ffmpeg-soak\n' | docker build -t bowtie:soak-debug -f - server/scripts
soak="${TMPDIR%/}/bowtie-soak"; rm -rf "$soak"; mkdir -p "$soak/data"
docker run -d --name bowtie-soak -p 8410:8400 -v "$soak/data:/data" \
  -e BOWTIE_HDHOMERUN_GUIDE=off -e BOWTIE_DEVICES="$HDHR_ADDR" \
  -e BOWTIE_FFMPEG_PATH=/usr/local/bin/ffmpeg-soak bowtie:soak-debug
sleep 5; docker logs bowtie-soak 2>&1 | grep 'first run: created admin user'
docker exec bowtie-soak /usr/local/bin/ffmpeg-soak -version | head -1
```

Expected: the admin line prints the generated password, and FFmpeg reports `5.1.9-0+deb12u1`. Before starting, make sure no one in the household needs more than the remaining tuners for the next hour.

- [ ] **Step 4: Run the soak (one hour, channel 9.1, one tuner)**

```bash
cd server && date -u +%FT%TZ > "${TMPDIR%/}/soak-start" && \
BOWTIE_SOAK_URL=http://127.0.0.1:8410 BOWTIE_SOAK_USER=admin BOWTIE_SOAK_PASS='<password from Step 3>' \
BOWTIE_SOAK_CHANNEL=9.1 BOWTIE_SOAK_FOR=1h \
go test -count=1 -tags ffmpeg,realsoak -run TestRealSoak -v -timeout 70m ./internal/e2e/ 2>&1 | tee "${TMPDIR%/}/soak-test.log"
docker logs -t bowtie-soak > "${TMPDIR%/}/soak-server.log" 2>&1
```

If the container can't keep up (`transcoder behind` lines, or a growing max gap), turn off Streaming → Adaptive in the soak server's admin UI (one rung) and run the soak again.

- [ ] **Step 5: Measure**

```bash
L="${TMPDIR%/}/soak-server.log"
echo "timestamp discontinuity: $(grep -c 'timestamp discontinuity' "$L")"
grep 'timestamp discontinuity' "$L" | sed -E 's/^([^ ]+) .*(delta [-0-9]+)?.*/\1/' | head -50
echo "frames duplicated:       $(grep -c 'frames duplicated' "$L")"
echo "Invalid frame dimensions: $(grep -c 'Invalid frame dimensions' "$L")"
echo "transcoder behind:       $(grep -c 'transcoder behind' "$L")"
echo "drained (read nothing):  $(grep -c 'transcoder read nothing' "$L")"
echo "transcoder stuck:        $(grep -c 'transcoder stuck' "$L")"
echo "restarting ffmpeg:       $(grep -c 'restarting ffmpeg' "$L")"
echo "timeline gaps:           $(grep -c 'stream: timeline .* gap before segment' "$L")"
echo "failed to delete:        $(grep -c 'failed to delete old segment' "$L")"
echo "vtt files now:           $(docker exec bowtie-soak sh -c 'ls /data/segments/*/*.vtt 2>/dev/null | wc -l')"
grep -E 'segments=|excess=|consistency:|--- (PASS|FAIL)' "${TMPDIR%/}/soak-test.log"
```

(The segment directory is `/data/segments` unless `BOWTIE_SEGMENT_DIR` says otherwise. Check with `docker exec bowtie-soak ls /data`.)

For each `timestamp discontinuity` timestamp, find the soak test's 5-minute progress lines around it and judge it by the false re-base definition above. The test's own pass/fail covers the whole hour; the per-line check says which re-bases fired on ad breaks.

- [ ] **Step 6: Apply the decision rule**

- **Pass** (test PASS, zero false re-bases, zero restarts, zero `transcoder stuck`): keep the Task 6 option. Record the counts in the task report and in the commit below.
- **False re-bases with `-dts_delta_threshold 1`**: change it to `2` (code and goldens as in Task 6, Steps 3–4). Re-run `TestScenarios/(ptsjump|splice|brief-dropout|threshold-dropout)$` on FFmpeg 5.1 (`ptsjump`'s +3 s step still exceeds 2 s), then repeat Steps 3–5. If 2 still fails, try `3` the same way. If 3 still fails, stop and report with the logs.
- **Any other failure** (audio holes, skew, consistency): stop and report the observed values and logs. Don't loosen the gate.

Clean up: `docker rm -f bowtie-soak`.

Record the result (no code change when it passed):

```bash
git commit --allow-empty -m "test: real-broadcast soak passed (channel 9.1, 1 h, FFmpeg 5.1)

<paste the Step 5 counts and the test summary line>

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

---

### Task 14: Release notes and docs

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `docs/deploy/truenas.md` (GPU notes)

**Interfaces:**
- Consumes: the final option from Tasks 6/13 and the log lines from Task 7.

- [ ] **Step 1: Write the changelog entry**

Add under the intro, above `## [0.23.0]`:

```markdown
## [Unreleased]

### Fixed

- **Live TV rides out bad reception without stutter or drift.** A damaged
  timestamp, a short signal dropout or a broadcaster's splice no longer
  freezes the picture on repeated frames or pulls the sound out of sync;
  missing audio is filled with silence so it stays in step with the video.
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
  to wall-clock time, for players that can't read program date-time.

### Notes for Intel Quick Sync (QSV) servers

After updating, please watch the server log around ad breaks for:
- `transcoder stuck` (should no longer appear on a healthy encoder);
- `transcoder read nothing … dropped its … backlog` followed by
  `transcoder reading again` (a slow moment that recovered — expected);
- `stream: timeline … gap before segment` outside real reception problems.
FFmpeg 5.1 logs "timestamp discontinuity" only at debug level, so it
won't appear at Bowtie's normal log level.
```

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

### Task 15: Full verification

**Files:** none (fix anything that fails in the task that owns it, then re-run).

- [ ] **Step 1: Unit tests and vet (CI's first job)**

Run: `cd server && go test ./... && go vet ./...`
Expected: PASS, vet clean.

- [ ] **Step 2: Race run of the touched packages**

Run: `cd server && go test -race -count=1 ./internal/stream/ ./internal/api/ ./internal/testplayer/ ./internal/hdhr/... ./internal/transcode/`
Expected: PASS.

- [ ] **Step 3: e2e suite, as CI runs it (host FFmpeg)**

Run: `cd server && go test -tags ffmpeg -p 1 -parallel 2 -timeout 15m ./...`
Expected: PASS. Every scenario passes with no `xfail` left in `server/internal/e2e/testdata/scenarios/` (`grep -l xfail server/internal/e2e/testdata/scenarios/*.yaml` prints nothing). If 15 m is too short now, raise it in `.github/workflows/ci.yml` to the measured time plus 50% and include that in the commit.

- [ ] **Step 4: e2e suite on FFmpeg 5.1**

Run: `cd server && BOWTIE_FFMPEG_PATH="$PWD/scripts/ffmpeg51" go test -count=1 -tags ffmpeg -p 1 -parallel 2 -timeout 60m -run TestScenarios ./internal/e2e/`
Expected: PASS.

- [ ] **Step 5: Lint**

Run: `cd server && golangci-lint run ./...`
Expected: no findings. Also run `shellcheck server/scripts/ffmpeg51 server/scripts/ffmpeg-soak` if shellcheck is installed; don't install it.

- [ ] **Step 6: Commit any fixes**

```bash
git add -A server .github
git commit -m "chore: stream resilience verification fixes

Co-Authored-By: Claude Opus 5.5 (1M context) <noreply@anthropic.com>"
```

(Skip if nothing changed.)
