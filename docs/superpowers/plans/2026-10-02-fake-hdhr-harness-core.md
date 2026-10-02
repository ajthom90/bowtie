# Fake HDHomeRun Harness Core (Plan 1 of 3) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A fault-injecting fake HDHomeRun with a continuous, real-time,
per-channel broadcast timeline, YAML scenarios, a virtual HLS player, and
Bowtie pipeline/e2e tests that run locally and in CI.

**Architecture:** Four new leaf packages under `server/internal/hdhr/hdhrfake/`
(`tsloop` timeline, `faults` engine, `scenario` timelines, `synth` source
generation) feed the existing `hdhrfake` device emulator. `internal/testplayer`
plays Bowtie's HLS like a client. `internal/e2e` wires the real Bowtie stack
(store, tuner manager, ingest, stream manager, API over httptest) to the fake
for fast stub-runner pipeline tests (default build) and real-FFmpeg scenario
tests (`-tags ffmpeg`).

**Tech Stack:** Go 1.22 stdlib, `gopkg.in/yaml.v3` (already in go.mod),
FFmpeg (tests tagged `ffmpeg` only).

**Spec:** `docs/superpowers/specs/2026-10-02-fake-hdhomerun-harness-design.md`
(§A1–A4, §C, §E). Plan 2 = §D freeze fix; Plan 3 = §B standalone tool.

## Global Constraints

- `go 1.22` in `server/go.mod`: no stdlib APIs newer than Go 1.22 (local toolchain is 1.26 — don't let it leak in).
- `cd server && golangci-lint run ./...` and `go vet ./...` clean before every commit.
- `hdhrfake.New(t, opts)` signature and existing `Options`/`LineupEntry` fields frozen; all current callers compile and pass unchanged.
- No broadcast captures in git. Generated synthetic sources are produced at test time, never committed.
- Clock injection uses the `stream.WithIngestClock` shape: `Now func() time.Time`, `After func(time.Duration) <-chan time.Time`.
- No production behavior changes in Plan 1 (only test infrastructure + CI). Known Bowtie gaps are pinned with strict-xfail, not fixed.
- Commit messages: conventional prefix (`feat:`, `test:`, `ci:`, `docs:`), ending with the Co-Authored-By trailer.

## Review Focus

1. **Half-open device connection** (bytes stop, TCP stays open): ingest `pump()` has no read deadline → viewers starve. Pinned by `half-open-stall.yaml` (xfail) and `TestPipelineHalfOpenStallRedials` (xfail) in Task 8/9.
2. **Device dial hang** (accepts TCP, never answers): `HTTPDial` uses the request context with no timeout → `POST /sessions` hangs. Pinned by `TestPipelineDialHangFailsFast` (xfail) in Task 8.
3. **Mid-stream join**: connections now start at the channel's current position, not at a PAT. Pinned by `TestFakeJoinMidStream` (Task 5) plus every existing api/stream test passing unchanged.
4. **33-bit PTS/PCR wrap** during a session: pinned by `TestChannelWrap` (Task 2) and `pts-wrap.yaml` (Task 9).
5. **CPU starvation** in parallel real-FFmpeg scenarios causing spurious slow-consumer restarts: `baseline.yaml` asserts `maxFfmpegRestarts: 0`; scenarios default to the 480i source; CI runs `-parallel 3`.

---

## File Structure

| File | Responsibility |
|---|---|
| `server/internal/hdhr/hdhrfake/tsloop/packet.go` | TS packet field codecs: PID, CC, PUSI, adaptation, PCR, PES PTS/DTS read/write |
| `server/internal/hdhr/hdhrfake/tsloop/source.go` | `Source`: load/analyze a `.ts` (schedule, loop duration, CC shifts, tracks) |
| `server/internal/hdhr/hdhrfake/tsloop/channel.go` | `Channel` timeline (segments, Switch, Jump) + `Reader` (paced, rewritten batches) |
| `server/internal/hdhr/hdhrfake/tsloop/clock.go` | `Clock` struct + `RealClock()` |
| `server/internal/hdhr/hdhrfake/faults/spec.go` | `Kind`, `Spec`, `Target`, validation |
| `server/internal/hdhr/hdhrfake/faults/engine.go` | Active-fault store, signal model, per-packet `Decide` |
| `server/internal/hdhr/hdhrfake/scenario/scenario.go` | YAML schema, `Parse`/`Load`, validation |
| `server/internal/hdhr/hdhrfake/scenario/run.go` | `Run` (timeline driver), `Expect.Check` |
| `server/internal/hdhr/hdhrfake/synth/synth.go` | FFmpeg presets + `Generate` (480i / 720p / 1080i MPEG-2 + AC-3) |
| `server/internal/hdhr/hdhrfake/fake.go` | (modify) `Start`/`Close`, channels/timelines, faults in stream handler, status signal, busy/hang/drop, events |
| `server/internal/testplayer/player.go` | Virtual HLS player + `Report` |
| `server/internal/e2e/harness.go` | Real Bowtie stack wired to a fake; login/device/channel bootstrap |
| `server/internal/e2e/runners.go` | `CountingRunner`, `Gate`, `GatedRunner`, `StubRunner` |
| `server/internal/e2e/pipeline_test.go` | C2: stub-runner pipeline tests (default build) |
| `server/internal/e2e/scenarios_test.go` | C3: `TestScenarios` (`//go:build ffmpeg`) |
| `server/internal/e2e/testdata/scenarios/*.yaml` | Scenario files |
| `.github/workflows/ci.yml` | (modify) install FFmpeg; run `-tags ffmpeg` |

---

### Task 1: `tsloop` packet codecs + clock

**Files:**
- Create: `server/internal/hdhr/hdhrfake/tsloop/packet.go`, `clock.go`
- Test: `server/internal/hdhr/hdhrfake/tsloop/packet_test.go`

**Interfaces:**
- Produces:
  ```go
  const PacketSize = 188
  const tsMask = 1<<33 - 1
  func PID(p []byte) uint16
  func CC(p []byte) byte
  func SetCC(p []byte, cc byte)
  func HasPayload(p []byte) bool
  func PUSI(p []byte) bool
  func PCR(p []byte) (base int64, ext int, ok bool)
  func SetPCR(p []byte, base int64, ext int)            // base masked to 33 bits
  func PESTimestamps(p []byte) (pts, dts int64, hasPTS, hasDTS bool)
  func SetPESTimestamps(p []byte, pts, dts int64)        // writes only fields present
  func StreamID(p []byte) (byte, bool)                   // PES stream_id when PUSI packet starts a PES
  type Clock struct { Now func() time.Time; After func(time.Duration) <-chan time.Time }
  func RealClock() Clock
  ```

- [ ] **Step 1: Write failing tests** — round-trip and layout tests:
  - `TestPCRRoundTrip`: build a packet with adaptation field (`p[3]=0x30`, `p[4]=7`, `p[5]=0x10`), `SetPCR(p, 0x1_2345_6789, 299)`; `PCR(p)` returns same base/ext, `ok`; reserved bits `p[10]&0x7E == 0x7E`.
  - `TestPCRWrapMask`: `SetPCR(p, 1<<33+5, 0)` → base `5`.
  - `TestPESTimestampsRoundTrip`: PUSI packet, payload `00 00 01 E0 00 00 80 C0 0A` + 10 timestamp bytes (prefix nibbles `0x3`/`0x1`); set PTS=`8589934000`, DTS=`8589933000`; read back; marker bits (`&1`) all 1; prefix nibbles preserved.
  - `TestPESOnlyPTS`: flags `0x80`, `hasDTS=false`; `SetPESTimestamps` leaves DTS bytes untouched.
  - `TestPESSkipsNoHeaderStreams`: stream_id `0xBE` (padding) → `hasPTS=false`.
  - `TestCCAndPayload`: `SetCC` keeps upper nibble of `p[3]`; `HasPayload` false for `afc=2`.
  - `TestRealFixtureFields`: open `../testdata/fixture.ts`; count PCR packets on PID 0x100 == 30; first video PES PTS == 129003, DTS == 126000; first audio (0x101) PTS == 128481 with no DTS.
- [ ] **Step 2: Run** `cd server && go test ./internal/hdhr/hdhrfake/tsloop/` → FAIL (undefined).
- [ ] **Step 3: Implement.** Key encodings:
  ```go
  func payloadOffset(p []byte) int { // -1 if no payload
      afc := (p[3] >> 4) & 0x3
      switch afc {
      case 1: return 4
      case 3: off := 5 + int(p[4]); if off >= PacketSize { return -1 }; return off
      default: return -1
      }
  }
  func readTS(b []byte) int64 {
      return int64(b[0]>>1&0x07)<<30 | int64(b[1])<<22 | int64(b[2]>>1)<<15 | int64(b[3])<<7 | int64(b[4]>>1)
  }
  func writeTS(b []byte, ts int64) {
      ts &= tsMask
      b[0] = b[0]&0xF1 | byte(ts>>29)&0x0E
      b[1] = byte(ts >> 22)
      b[2] = byte(ts>>14)&0xFE | 1
      b[3] = byte(ts >> 7)
      b[4] = byte(ts<<1)&0xFE | 1
  }
  // PCR at p[6:12]: base 33b, 6 reserved (1s), ext 9b.
  // PES: off=payloadOffset; p[off:off+3]==00 00 01; sid=p[off+3];
  // header-less sids: 0xBC,0xBE,0xBF,0xF0,0xF1,0xF2,0xF8,0xFF.
  // flags := p[off+7]>>6 (2=PTS, 3=PTS+DTS); PTS at off+9, DTS at off+14.
  ```
- [ ] **Step 4: Run tests** → PASS. `go vet`, `golangci-lint run ./...`.
- [ ] **Step 5: Commit** `feat: tsloop packet field codecs for fake hdhomerun`

---

### Task 2: `tsloop` Source analysis + Channel timeline + Reader

**Files:**
- Create: `server/internal/hdhr/hdhrfake/tsloop/source.go`, `channel.go`
- Test: `server/internal/hdhr/hdhrfake/tsloop/source_test.go`, `channel_test.go`

**Interfaces:**
- Consumes: Task 1 codecs, `Clock`.
- Produces:
  ```go
  type Track int
  const (TrackOther Track = iota; TrackVideo; TrackAudio)
  type Source struct{ /* immutable */ }
  func Load(name string, r io.Reader) (*Source, error)
  func LoadFile(path string) (*Source, error)
  func (s *Source) Name() string
  func (s *Source) LoopDuration() time.Duration
  func (s *Source) Origin90k() int64          // interpolated PCR (90 kHz) of packet 0
  func (s *Source) TrackOf(pid uint16) Track

  type Channel struct{ /* mu; current segment */ }
  func NewChannel(src *Source, clock Clock, base90k int64) *Channel // base90k: channel clock value at creation
  func (c *Channel) Now90k() int64
  func (c *Channel) Source() *Source
  func (c *Channel) Switch(src *Source)        // new segment at now; timeline continues
  func (c *Channel) Jump(delta time.Duration)  // shift all subsequent timestamps
  func (c *Channel) NewReader() *Reader        // joins at current position

  type Reader struct{ /* seg, loop L, index i */ }
  func (r *Reader) Next(ctx context.Context) ([][]byte, error) // ≥1 due packets (rewritten copies), max 512
  ```
- Rules (from spec §A1 as amended):
  - `Load`: require length multiple of 188 and every packet `p[0]==0x47`; find PCR PID (first PID with PCR); require ≥2 PCRs. Interpolate `pcr90(i)` linearly between PCR packets (extrapolate at both ends with the overall PCR byte-rate). `sched[i] = pcr90(i) − pcr90(0)` as `time.Duration`. Per PES PID collect DTS-else-PTS; `typicalΔ` = mode of consecutive deltas; `loop90k = max(last−first+typicalΔ)`; require `sched[last] < loop` (error otherwise). `ccShift[pid] = (lastCC+1−firstCC)&0xF` over payload-carrying packets. Track: stream_id `0xE0–0xEF` video, `0xC0–0xDF` or `0xBD` audio.
  - Rewrite for packet `i` in loop `L` of segment `seg`: `off = seg.base − src.Origin90k() + L*loop90k + seg.jump`; PTS/DTS `+= off` (mask 33 bits); PCR base `+= off` (mask); CC `= (cc + L*ccShift[pid]) & 0xF`.
  - Packet `(L,i)` is due at `seg.epoch + L*loop + sched[i]`.
  - `Switch(src)`: new segment `{src, epoch: now, base: Now90k()}`. `Jump(d)`: same segment, `jump += d*90000/time.Second` (content continues, timestamps shift).
  - `Reader.Next`: if channel's segment changed since last call, re-join at now. Wait (via `clock.After`) until the next packet is due; return every packet due by `clock.Now()` (cap 512). `ctx.Done()` → `ctx.Err()`.

- [ ] **Step 1: Failing tests (`source_test.go`)** using `../testdata/fixture.ts`:
  - `TestLoadFixture`: `LoopDuration()` == `time.Duration(181557)*time.Second/90000` (audio span 307158−128481+2880 is the max; assert computed value equals max of video `303177−126000+3003=180180` and audio `181557`); `TrackOf(0x100)==TrackVideo`, `TrackOf(0x101)==TrackAudio`.
  - `TestLoadRejectsMisaligned`: 187 bytes → error; bad sync byte → error.
- [ ] **Step 2: Failing tests (`channel_test.go`)** with a manual fake clock (`now` var + `After` returning an already-closed channel after advancing `now`):
  - `TestReaderTimestampsMonotonicAcrossLoops`: read 100 loops; per PID, DTS-else-PTS strictly increasing; PCR strictly increasing; CC continuous per payload PID (`(prev+1)&0xF`) except where `afc==2`.
  - `TestReaderPacing`: with fake clock, total packets returned after advancing exactly `5*LoopDuration()` equals `5 * packetsPerLoop ± 1`.
  - `TestJoinMidStream`: advance clock by `LoopDuration()/2` before `NewReader()`; first packet returned is the one at `sched ≥ elapsed` (not index 0), and its timestamps continue the channel clock (`Now90k`) within one PCR interval.
  - `TestChannelWrap`: `NewChannel(src, clk, tsMask-90000*2)` (2 s before wrap); read 3 loops; timestamps wrap to small values exactly once per PID; unwrapped sequence (add 2^33 after wrap) strictly increasing.
  - `TestSwitchKeepsTimelineMonotonic`: switch fixture→fixture mid-loop; PCR after switch ≥ PCR before.
  - `TestJumpShiftsTimestamps`: `Jump(10*time.Second)`; next PCR ≈ previous + 10 s (±1 PCR interval).
  - `TestRealClockPacingSmoke` (real clock, 1 s): bytes in 1 s within ±10% of `fileBytes/LoopDuration()`.
- [ ] **Step 3: Run** → FAIL.
- [ ] **Step 4: Implement** `source.go`, `channel.go` per rules above.
- [ ] **Step 5: Run** → PASS; vet + lint.
- [ ] **Step 6: Commit** `feat: tsloop continuous per-channel timeline with timestamp rewrite`

---

### Task 3: `faults` spec + engine + signal model

**Files:**
- Create: `server/internal/hdhr/hdhrfake/faults/spec.go`, `engine.go`
- Test: `server/internal/hdhr/hdhrfake/faults/engine_test.go`

**Interfaces:**
- Consumes: `tsloop.Clock`, `tsloop.Track`, `tsloop.PID`.
- Produces:
  ```go
  type Kind string
  const (Signal Kind="signal"; Stall="stall"; Drop="drop"; Corrupt="corrupt"; Slow="slow"
         TimestampJump="timestamp-jump"; PIDLoss="pid-loss"; SourceSwitch="source-switch"; Busy="busy"; Hang="hang")
  type Spec struct {
      Fault    Kind          `yaml:"fault" json:"fault"`
      For      time.Duration `yaml:"for,omitempty" json:"for,omitempty"` // 0 = until removed (one-shot for drop/jump/switch)
      Strength *int          `yaml:"strength,omitempty" json:"strength,omitempty"`
      Quality  *int          `yaml:"quality,omitempty" json:"quality,omitempty"`
      Symbol   *int          `yaml:"symbol,omitempty" json:"symbol,omitempty"`
      Burst    bool          `yaml:"burst,omitempty" json:"burst,omitempty"`
      Mode     string        `yaml:"mode,omitempty" json:"mode,omitempty"`   // drop: clean|reset
      Rate     float64       `yaml:"rate,omitempty" json:"rate,omitempty"`   // corrupt probability | slow factor
      Type     string        `yaml:"type,omitempty" json:"type,omitempty"`   // corrupt: bitflip|tei|sync
      By       time.Duration `yaml:"by,omitempty" json:"by,omitempty"`       // timestamp-jump
      Track    string        `yaml:"track,omitempty" json:"track,omitempty"` // pid-loss: audio|video
      To       string        `yaml:"to,omitempty" json:"to,omitempty"`       // source-switch: source name
  }
  func (s Spec) Validate() error
  type Scope string // "channel" (default) | "connections" (only conns open at activation) | "device"
  type Target struct { Channel string; Scope Scope }
  type Action int
  const (Emit Action = iota; Skip; Hold; Close)
  type Decision struct { Action Action; Packets [][]byte; Burst bool; Reset bool }
  type Levels struct { Strength, Quality, Symbol int }
  type Engine struct{ /* mu, clock, rng, active faults */ }
  func NewEngine(clock tsloop.Clock, seed int64) *Engine
  func (e *Engine) Add(t Target, s Spec, liveConns []string) (id string, err error) // packet/device faults only
  func (e *Engine) Remove(id string)
  func (e *Engine) Active() []ActiveFault         // ActiveFault{ID string; Target Target; Spec Spec; Since time.Time}
  func (e *Engine) Signal(channel string) Levels  // default {100,100,100}
  func (e *Engine) Busy() bool
  func (e *Engine) Hang() bool
  func (e *Engine) Decide(channel, conn string, pkt []byte, track tsloop.Track) Decision
  ```
- Semantics (spec §A2): expiry by `For` on the injected clock; `drop` is one-shot per targeted connection (`Close`, `Reset: Mode=="reset"`); `stall` → `Hold` (`Burst` from spec); `signal` thresholds: q≥80 Emit; 50≤q<80 per-packet probability `(80−q)/30*0.05` split evenly TEI-set vs Skip; 30≤q<50 start a Skip burst of rng 20–200 packets with per-packet probability 0.002 (plus the 50–80 rate at q=50); q<30 → Hold (discard; loss of lock); `corrupt` rate per packet: `bitflip` flips one bit of one payload byte, `tei` sets `p[1]|=0x80`, `sync` returns `[junk(1..187 bytes)][pkt]` as two entries in `Packets` (junk entry is not 188 long); `pid-loss` → Skip packets whose track matches; `slow` → token bucket: allow `Rate` × the channel's observed packet rate (EMA over 1 s), Skip excess; `busy`/`hang` device flags. `timestamp-jump`/`source-switch` are rejected by `Engine.Add` with `ErrTimelineFault` (the fake routes them to `tsloop.Channel`). Decide never mutates the input slice (copy before modifying).

- [ ] **Step 1: Failing tests** (fake clock, seed 1):
  - `TestSignalDefaultLevels`, `TestSignalSetAndExpire` (`For: 5s` → after 5 s back to 100).
  - `TestSignalErrorRates`: q=90 → 0 non-Emit in 100k; q=65 → non-Emit fraction 0.025±0.005; q=40 → Skip bursts present (runs ≥20); q=20 → all Hold.
  - `TestStallHoldsThenResumes` (+ `Burst` propagated), `TestDropOneShotPerConn` (conn A gets one Close; conn B joining after activation with Scope connections gets Emit), `TestDropReset`.
  - `TestCorruptKinds`: tei sets bit; bitflip changes exactly one bit outside the 4-byte header; sync yields junk+packet; input slice unchanged.
  - `TestPIDLossAudioOnly`, `TestSlowRateLimits` (Rate 0.5 → ~50% Skip at steady state ±10%), `TestBusyHangFlags`, `TestTimelineKindsRejected`, `TestValidate` (unknown kind, quality >100, negative For, drop mode bogus).
  - `TestDeterministicWithSeed`: same seed + same input → identical decisions.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** → PASS; vet + lint.
- [ ] **Step 5: Commit** `feat: fault engine and signal model for fake hdhomerun`

---

### Task 4: `synth` source generator

**Files:**
- Create: `server/internal/hdhr/hdhrfake/synth/synth.go`
- Test: `server/internal/hdhr/hdhrfake/synth/synth_ffmpeg_test.go` (`//go:build ffmpeg`)
- Modify: `server/internal/hdhr/hdhrfake/testdata/README.md` (point to `synth` for larger sources)

**Interfaces:**
- Produces:
  ```go
  type Preset string
  const (P480i Preset = "480i"; P720p Preset = "720p"; P1080i Preset = "1080i")
  func Args(p Preset, dur time.Duration, out string) []string // ffmpeg argv (no binary)
  func Generate(ctx context.Context, ffmpegPath string, p Preset, dur time.Duration, out string) error
  func Cached(ctx context.Context, ffmpegPath string, p Preset, dur time.Duration, dir string) (string, error) // reuse dir/<preset>-<dur>.ts if present
  ```
- Presets: common args `-f lavfi -i testsrc2=size=WxH:rate=R -f lavfi -i sine=frequency=1000:sample_rate=48000 -t D -c:v mpeg2video -g 15 -bf 2 -c:a ac3 -b:a 384k -ac 2 -f mpegts -mpegts_pmt_start_pid 4096 -mpegts_start_pid 256`; 480i: `720x480`, rate `30000/1001`, `-b:v 3M -flags +ilme+ildct -top 1`; 720p: `1280x720`, `60000/1001`, `-b:v 10M`; 1080i: `1920x1080`, `30000/1001`, `-b:v 15M -maxrate 19M -bufsize 9781k -flags +ilme+ildct -top 1`.

- [ ] **Step 1: Failing test** `TestGenerateLoadsAsSource`: for each preset, `Generate` 3 s into `t.TempDir()`, `tsloop.LoadFile` succeeds, `LoopDuration()` within 3 s ± 100 ms, ffprobe reports `mpeg2video` WxH and `ac3`.
- [ ] **Step 2: Run** `go test -tags ffmpeg ./internal/hdhr/hdhrfake/synth/` → FAIL. **Step 3: Implement.** **Step 4:** PASS.
- [ ] **Step 5: Commit** `feat: synthetic atsc-like source generator for fake hdhomerun`

---

### Task 5: `hdhrfake` integration

**Files:**
- Modify: `server/internal/hdhr/hdhrfake/fake.go`
- Test: `server/internal/hdhr/hdhrfake/fake_test.go` (append)

**Interfaces:**
- Consumes: Tasks 1–3.
- Produces (additions; existing API frozen):
  ```go
  type Channel struct { GuideNumber, Name string; Source *tsloop.Source }
  // Options gains:
  //   Channels []Channel           // if nil, built from Lineup with the embedded fixture
  //   Sources  map[string]*tsloop.Source // extra sources for source-switch by name
  //   Clock    tsloop.Clock        // zero → RealClock()
  //   Seed     int64
  //   Listen   string              // default "127.0.0.1:0"
  func Start(opts Options) (*Fake, error)
  func (f *Fake) Close()
  func (f *Fake) Apply(t faults.Target, s faults.Spec) (string, error) // routes timeline faults to tsloop.Channel
  func (f *Fake) Remove(id string)
  func (f *Fake) Engine() *faults.Engine
  func (f *Fake) Events() []Event   // Event{At time.Time; Kind, Channel, Conn, Detail string}
  func (f *Fake) Connections() []ConnInfo // ConnInfo{ID, Channel, Tuner string; ClientIP string; Bytes int64}
  ```
- Behavior: one `tsloop.Channel` per channel created at `Start`; `handleStream` → device `Hang` (block until client gone, no tuner) / `Busy` (503 "all tuners in use") → tuner alloc (unchanged 503 path) → `reader := ch.NewReader()` → loop `Next` → per packet `Engine.Decide` (track via `src.TrackOf(PID)`) → `Emit` write / `Skip` / `Hold` (buffer if burst, max 32 MiB; flush when Hold ends) / `Close` (clean: return; reset: `http.Hijacker` → `(*net.TCPConn).SetLinger(0)` → `Close`). Flush after each batch. `status.json` uses `Engine.Signal(guide)` for tuned tuners and `TargetIP` = request remote host. Events recorded for dial, reject(503), close, fault add/remove.

- [ ] **Step 1: Failing tests:**
  - `TestStartCloseWithoutTesting`: `Start` + `Close`; discover works; port freed.
  - `TestFakeJoinMidStream`: wait 1.2 s, connect, first bytes are a packet that is NOT necessarily PAT; read 2 s; parse PCRs → monotonic.
  - `TestFakeStatusReflectsSignal`: Apply signal quality 55 → `status.json` tuner shows 55 and `TargetIP` 127.0.0.1.
  - `TestFakeBusyAndHang`: Apply busy → 503; Remove → 200; Apply hang → client with 300 ms timeout times out; `ActiveStreams()==0` during hang.
  - `TestFakeDropClean` / `TestFakeDropReset`: Apply drop (scope connections) → reader gets EOF / `ECONNRESET`-class error (`errors.Is(err, syscall.ECONNRESET)` or `io.ErrUnexpectedEOF`).
  - `TestFakeStallBurst`: stall 1 s burst → no bytes for ~1 s, then a read returning ≥ (1 s worth × 0.8) bytes quickly.
  - `TestFakeTimestampJumpAndSwitch`: Apply `timestamp-jump` by 10 s → PCR jumps ≈ +10 s; `source-switch` to named source works; unknown name → error.
  - `TestFakeEventsRecorded`.
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4: Run** the whole server suite `go test ./...` (all existing tests must still pass — Review Focus 3) + vet + lint.
- [ ] **Step 5: Commit** `feat: fake hdhomerun timelines, fault injection, and non-test lifecycle`

---

### Task 6: `scenario` package

**Files:**
- Create: `server/internal/hdhr/hdhrfake/scenario/scenario.go`, `run.go`
- Test: `server/internal/hdhr/hdhrfake/scenario/scenario_test.go`

**Interfaces:**
- Consumes: `faults.Spec`, `faults.Target`, `faults.Scope`, `tsloop.Clock`.
- Produces:
  ```go
  type Scenario struct {
      Name     string        `yaml:"name"`
      Source   string        `yaml:"source"`    // "synthetic-480i" (default) | "synthetic-720p" | "synthetic-1080i" | file path
      Channel  string        `yaml:"channel"`   // default "90.1"
      Duration time.Duration `yaml:"duration"`
      Soak     bool          `yaml:"soak"`
      PTSWrapIn time.Duration `yaml:"ptsWrapIn"` // >0: channel clock starts this long before 2^33 wrap
      Timeline []Step        `yaml:"timeline"`
      Bowtie   *BowtieSpec   `yaml:"bowtie"`
      Expect   *Expect       `yaml:"expect"`
  }
  type Step struct { At time.Duration `yaml:"at"`; Scope faults.Scope `yaml:"scope"`; faults.Spec `yaml:",inline"` }
  type BowtieSpec struct { SlowConsumer *Window `yaml:"slowConsumer"` }
  type Window struct { At, For time.Duration }
  type Expect struct {
      SequenceMonotonic *bool    `yaml:"sequenceMonotonic"`
      MaxGapSeconds     *float64 `yaml:"maxGapSeconds"`
      SessionSurvives   *bool    `yaml:"sessionSurvives"`
      MaxFfmpegRestarts *int     `yaml:"maxFfmpegRestarts"`
      TunersBusy        *bool    `yaml:"tunersBusy"`
      XFail             string   `yaml:"xfail"`
  }
  type Observed struct { SequenceMonotonic bool; MaxGap time.Duration; SessionSurvived bool; FfmpegRestarts int; TunersBusy bool }
  func Parse(b []byte) (*Scenario, error)       // KnownFields(true); validates
  func Load(path string) (*Scenario, error)
  type Applier interface { Apply(faults.Target, faults.Spec) (string, error); Remove(id string) }
  func (s *Scenario) Run(ctx context.Context, a Applier, clock tsloop.Clock) error // fires steps at `At`; removes timed faults at At+For; returns at Duration
  func (e *Expect) Check(o Observed) []string   // violations; empty = pass (ignores XFail)
  ```
- Validation: name required; duration > 0; each step `At ≤ Duration`, `Spec.Validate()`; same Kind+Scope overlapping windows rejected; `slowConsumer` window within duration.

- [ ] **Step 1: Failing tests:** `TestParseFull` (the spec §A4 example parses to expected structs), `TestParseRejectsUnknownField`, `TestParseRejectsStepAfterDuration`, `TestParseRejectsOverlap`, `TestRunFiresInOrder` (fake applier records `(elapsed, kind)` with fake clock), `TestRunRemovesTimedFaults`, `TestCheck` (each field violation message; XFail ignored by Check).
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement.** **Step 4:** PASS; vet + lint.
- [ ] **Step 5: Commit** `feat: yaml fault scenarios for fake hdhomerun`

---

### Task 7: `testplayer`

**Files:**
- Create: `server/internal/testplayer/player.go`
- Test: `server/internal/testplayer/player_test.go`

**Interfaces:**
- Produces:
  ```go
  type Config struct {
      BaseURL   string; Token string; ChannelID int64
      Caps      map[string]any // POST /sessions caps; default {"videoCodecs":["h264"],"audioCodecs":["aac"],"maxHeight":480}
      PollEvery time.Duration  // default 1s
      Client    *http.Client
  }
  type Player struct{}
  func Start(ctx context.Context, cfg Config) (*Player, error) // *StartError{Status int; Body string} on non-200
  func (p *Player) Run(ctx context.Context)    // poll + fetch new segments + heartbeat every 15s until ctx done
  func (p *Player) Stop(ctx context.Context)   // DELETE /api/v1/sessions/{viewerId}
  func (p *Player) Report() Report
  type Report struct {
      Polls, SegmentsFetched, SegmentErrors, BackwardJumps, Discontinuities int
      Newest  []int64        // newest absolute segment number per poll
      MaxGap  time.Duration  // longest interval without a new segment (incl. tail to Stop)
      SessionGone bool
      Errors  []string
  }
  ```
- Absolute segment number = `#EXT-X-MEDIA-SEQUENCE` + index in playlist. `BackwardJumps` += 1 when newest decreases. A segment is "new" if its absolute number > highest seen. Segment fetch = GET URL from playlist line; error if status ≠ 200 or first byte ≠ 0x47. Discontinuities = count of `#EXT-X-DISCONTINUITY` attached to new segments. Playlist 404 → `SessionGone=true`, stop polling.

- [ ] **Step 1: Failing tests** against an `httptest.Server` that scripts playlists: steady growth → 0 backward, gap ≈ poll; reset to seq 0 → `BackwardJumps==1`; discontinuity tag counted once; 404 → `SessionGone`; segment 500 → `SegmentErrors`; `Start` 503 → `*StartError{Status:503}`; heartbeat POSTed (with `?token=` from playlistUrl).
- [ ] **Step 2–4:** FAIL → implement → PASS; vet + lint.
- [ ] **Step 5: Commit** `feat: virtual hls test player`

---

### Task 8: `e2e` harness + pipeline tests (default build)

**Files:**
- Create: `server/internal/e2e/harness.go`, `runners.go`, `pipeline_test.go`, `xfail_test.go`

**Interfaces:**
- Consumes: `hdhrfake.Start`, `testplayer`, `stream`, `api`, `store`, `tuner`, `auth`, `config`, `transcode`.
- Produces:
  ```go
  type Options struct { Fake hdhrfake.Options; Runner stream.Runner; Backends []transcode.Backend /* default software */ }
  type Harness struct {
      Fake *hdhrfake.Fake; Store *store.Store; Streams *stream.Manager; Ingest *stream.IngestManager
      Server *httptest.Server; AdminToken string; Channels map[string]int64 // guide number → channel id
  }
  func New(t testing.TB, o Options) *Harness // admin user, POST admin/devices, PATCH enable all channels
  func (h *Harness) Player(t testing.TB, guide string) (*testplayer.Player, error)
  type CountingRunner struct { Inner stream.Runner }  // Starts() int
  type Gate struct{}                                  // Pause(), Resume(); NewGate()
  type GatedRunner struct { Inner stream.Runner; Gate *Gate } // wraps spec.Stdin with a reader that blocks while paused
  type StubRunner struct{}  // drains stdin into a counter, writes growing live.m3u8 + seg files every 500ms; process exits on stdin EOF
  func (r *StubRunner) BytesIn() int64
  ```
- `xfail_test.go`: `func xfail(t *testing.T, reason string, check func() error)` — `check()==nil` → `t.Fatalf("known gap fixed (%s): remove xfail", reason)`; else `t.Logf("xfail (%s): %v", reason, err)`.

- [ ] **Step 1: Failing tests** (each ≤ ~20 s, `t.Parallel()`):
  - `TestPipelineDropReconnects`: Apply drop (scope connections); within 5 s `Fake.TotalDials()==2`; `CountingRunner.Starts()==1`; `StubRunner.BytesIn()` keeps growing; session listed in admin sessions.
  - `TestPipelineSignalFadeKeepsSession`: quality 40 for 4 s → Starts()==1, bytes grow.
  - `TestPipelineSlowConsumerRestarts`: Gate.Pause 8 s → Starts()==2 within 15 s (documents current force-close → restart).
  - `TestPipelineBusy`: Apply busy before start → player `StartError.Status==503`.
  - `TestPipelineHalfOpenStallRedials` (xfail "ingest has no read deadline"): stall scope connections, no `For`; check: `TotalDials()==2` within 12 s.
  - `TestPipelineDialHangFailsFast` (xfail "device dial has no timeout"): Apply hang; check: `Player` start returns within 15 s (use a client with 20 s timeout and measure).
- [ ] **Step 2: Run** → FAIL. **Step 3: Implement** harness/runners. **Step 4:** PASS (xfails logged); `go test ./...` full suite; vet + lint.
- [ ] **Step 5: Commit** `test: bowtie pipeline tests against fault-injecting fake hdhomerun`

---

### Task 9: Real-FFmpeg scenario tests

**Files:**
- Create: `server/internal/e2e/scenarios_test.go` (`//go:build ffmpeg`)
- Create: `server/internal/e2e/testdata/scenarios/{baseline,baseline-1080i,device-drop,signal-fade,slow-consumer-restart,half-open-stall,timestamp-jump,audio-loss,pts-wrap,tuners-busy}.yaml`

**Interfaces:**
- Consumes: everything above; `stream.FFmpegRunner{Path}` wrapped by `CountingRunner{GatedRunner{...}}`.
- `TestScenarios`: glob scenarios with `expect:`; skip `soak` unless built with `soak` tag (`//go:build soak` const file); each `t.Run(name, …)` with `t.Parallel()`: resolve source (`synthetic-*` → `synth.Cached` in `os.UserCacheDir()/bowtie-fakehdhr` (fallback temp); `BOWTIE_FAKE_SOURCE` env overrides), build harness with that channel (base near wrap if `PTSWrapIn`), start player (if `tunersBusy` expected, assert start 503 and stop), run `Scenario.Run` + `slowConsumer` gate schedule concurrently with player `Run` for `Duration`, stop player, build `Observed{SequenceMonotonic: report.BackwardJumps==0, MaxGap, SessionSurvived: !report.SessionGone && session listed, FfmpegRestarts: runner.Starts()-1, TunersBusy}`, `v := Expect.Check(o)`; XFail set → require `len(v)>0` else fail "xfail fixed"; otherwise require `len(v)==0`.
- Scenarios (durations ≤ 45 s; default source synthetic-480i):
  - `baseline`: 30 s, no faults; expect monotonic, maxGap 10, survives, restarts 0.
  - `baseline-1080i`: same with `source: synthetic-1080i`.
  - `device-drop`: drop (scope connections, mode reset) at 10 s; 35 s; monotonic, maxGap 12, survives, restarts 0.
  - `signal-fade`: signal quality 65 at 5 s for 10 s, quality 45 at 15 s for 6 s; 35 s; survives, monotonic, maxGap 14.
  - `slow-consumer-restart`: `bowtie.slowConsumer {at: 10s, for: 10s}`; 40 s; monotonic, survives; `xfail: "playlist resets on ffmpeg restart (Plan 2)"`.
  - `half-open-stall`: stall scope connections at 10 s (no `for`); 40 s; maxGap 15, survives; `xfail: "ingest has no read deadline (Plan 2)"`.
  - `timestamp-jump`: by 30 s at 10 s; 30 s; survives, maxGap 12.
  - `audio-loss`: pid-loss audio at 10 s for 6 s; 30 s; survives, maxGap 12.
  - `pts-wrap`: `ptsWrapIn: 10s`; 30 s; monotonic, survives, maxGap 10, restarts 0.
  - `tuners-busy`: busy at 0 s; 5 s; `tunersBusy: true`.
- [ ] **Step 1:** Write `scenarios_test.go` + YAML files.
- [ ] **Step 2: Run** `cd server && go test -tags ffmpeg -run TestScenarios -parallel 3 -v ./internal/e2e/` and record per-scenario results. For any non-xfail scenario that fails: determine whether the expectation is wrong (fix the YAML with a comment explaining the observed behavior) or Bowtie is wrong (convert to `xfail` with a precise reason and add it to Plan 2's input list). Do not loosen an expectation without stating why in the YAML.
- [ ] **Step 3:** Full `go test -tags ffmpeg ./...` (includes existing `ffmpeg_e2e_test.go` and Task 4 synth test); vet + lint.
- [ ] **Step 4: Commit** `test: real-ffmpeg fault scenarios against fake hdhomerun`

---

### Task 10: CI + docs

**Files:**
- Modify: `.github/workflows/ci.yml` (server job), `server/internal/hdhr/hdhrfake/testdata/README.md`
- Create: `docs/dev/fake-hdhomerun.md` (how to write/run scenarios, xfail semantics, `BOWTIE_FAKE_SOURCE`)

- [ ] **Step 1:** In the `server` job add before `test`:
  ```yaml
      - name: install ffmpeg
        run: sudo apt-get update && sudo apt-get install -y --no-install-recommends ffmpeg
  ```
  and after `test`:
  ```yaml
      - name: test (ffmpeg + fault scenarios)
        run: go test -tags ffmpeg -parallel 3 -timeout 15m ./...
        env:
          CGO_ENABLED: "0"
  ```
  and `timeout-minutes: 25` on the job.
- [ ] **Step 2:** actionlint (`docker run --rm -v "$PWD:/repo" -w /repo rhysd/actionlint:latest .github/workflows/ci.yml`).
- [ ] **Step 3:** Write docs.
- [ ] **Step 4: Commit** `ci: run ffmpeg-tagged tests and fault scenarios`; push; watch CI to green (fix forward on red).
