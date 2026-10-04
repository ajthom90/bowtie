// Package settings provides a typed, store-backed provider for product-level
// runtime settings (EPG sources and transcode options). Values live in the
// settings table; reads are cheap single-row lookups with no in-memory cache.
//
// Presence-based seeding: SeedFromConfig writes a key only when it is absent.
// A stored empty string is a deliberate value (e.g. XMLTV disabled) and is never
// overwritten by config/env on restart.
package settings

import (
	"fmt"
	"log"
	"slices"
	"strconv"
	"time"

	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/store"
)

// Settings keys (exact strings; also used by Admin API and docs).
const (
	KeyXMLTVSource            = "xmltv.source"
	KeyXMLTVRefreshHours      = "xmltv.refreshHours"
	KeySDUsername             = "sd.username"
	KeySDPassword             = "sd.password"
	KeySDLineupID             = "sd.lineupId"
	KeyTranscodeEncoder       = "transcode.encoder"
	KeyTranscodeAllowHEVC     = "transcode.allowHevc"
	KeyStreamingBufferMinutes = "streaming.bufferMinutes"
	// KeyStreamingAdaptive: one shared multi-quality transcode per channel.
	KeyStreamingAdaptive = "streaming.adaptive"
	// KeyEPGHDHomeRun: fetch the free guide from SiliconDust's HDHomeRun
	// XMLTV API (default on).
	KeyEPGHDHomeRun = "epg.hdhomerun"
	// DVR padding applied to recordings scheduled from now on.
	KeyDVRPadStartSeconds = "dvr.padStartSeconds"
	KeyDVRPadEndSeconds   = "dvr.padEndSeconds"
	// KeyDVRQuality is the resolution recordings are converted at (applies
	// to recordings converted afterwards).
	KeyDVRQuality = "dvr.quality"
	// Admin notifications (ntfy, Discord or a webhook). Never seeded: an
	// absent key reads as its default, and the URL (which may carry a token)
	// never reaches the seed's override log.
	KeyNotifyURL             = "notifications.url"
	KeyNotifyRecordingFailed = "notifications.recordingFailed"
	KeyNotifyDiskLow         = "notifications.diskLow"
	KeyNotifyRecordingReady  = "notifications.recordingReady"
	KeyNotifyGuideFailed     = "notifications.guideFailed"
)

// Recording qualities (dvr.quality).
const (
	// DVRQuality720p converts every recording to 720p (the default).
	DVRQuality720p = "720p"
	// DVRQuality1080p keeps up to the broadcast's resolution (1080i is
	// deinterlaced to 1080p) at a higher bitrate.
	DVRQuality1080p = "1080p"
)

// DVRQualities are the allowed dvr.quality values, default first.
var DVRQualities = []string{DVRQuality720p, DVRQuality1080p}

// ValidDVRQuality reports whether q is an allowed dvr.quality value.
func ValidDVRQuality(q string) bool {
	return slices.Contains(DVRQualities, q)
}

// Default product values used when seeding from empty/zero config.
const (
	DefaultRefreshHours  = 12
	DefaultEncoder       = "auto"
	DefaultBufferMinutes = 15
	// DefaultPadStartSeconds / DefaultPadEndSeconds match dvr.DefaultPadStart
	// and dvr.DefaultPadEnd.
	DefaultPadStartSeconds = 60
	DefaultPadEndSeconds   = 180
)

// Provider is a typed facade over store settings. It is safe for concurrent use
// (the store serializes SQLite access); there is no cache to invalidate.
type Provider struct {
	st *store.Store
}

// NewProvider returns a store-backed settings provider.
func NewProvider(st *store.Store) *Provider {
	return &Provider{st: st}
}

// XMLTV is the XMLTV EPG source section.
type XMLTV struct {
	Source       string
	RefreshHours int
}

// SD is the Schedules Direct section.
type SD struct {
	Username string
	Password string
	LineupID string
}

// Transcode is the transcode preference section.
type Transcode struct {
	Encoder   string
	AllowHEVC bool
}

// Streaming is the live DVR buffer section (pause/rewind window).
type Streaming struct {
	BufferMinutes int
	// Adaptive: every viewer of a channel shares one quality ladder (more GPU).
	Adaptive bool
}

// HDHomeRunGuide is the free SiliconDust guide section.
type HDHomeRunGuide struct {
	Enabled bool
}

// DVR is the recording section: padding and conversion quality.
type DVR struct {
	PadStartSeconds int
	PadEndSeconds   int
	// Quality is one of DVRQualities ("" in SetDVR keeps the stored value).
	Quality string
}

// NotificationEvents chooses which events are sent.
type NotificationEvents struct {
	RecordingFailed bool
	DiskLow         bool
	RecordingReady  bool
	GuideFailed     bool
}

// Notifications is the admin notification section. An empty URL is off.
type Notifications struct {
	URL    string
	Events NotificationEvents
}

// DefaultNotificationEvents: failures and low disk on, ready recordings off.
var DefaultNotificationEvents = NotificationEvents{RecordingFailed: true, DiskLow: true, GuideFailed: true}

// Notifications returns the notification section; absent keys read as the
// defaults (no URL; failures, low disk and guide failures on).
func (p *Provider) Notifications() (Notifications, error) {
	u, err := p.st.GetSetting(KeyNotifyURL)
	if err != nil {
		return Notifications{}, err
	}
	out := Notifications{URL: u, Events: DefaultNotificationEvents}
	for _, f := range []struct {
		key string
		dst *bool
	}{
		{KeyNotifyRecordingFailed, &out.Events.RecordingFailed},
		{KeyNotifyDiskLow, &out.Events.DiskLow},
		{KeyNotifyRecordingReady, &out.Events.RecordingReady},
		{KeyNotifyGuideFailed, &out.Events.GuideFailed},
	} {
		raw, err := p.st.GetSetting(f.key)
		if err != nil {
			return Notifications{}, err
		}
		if raw == "" {
			continue
		}
		on, err := strconv.ParseBool(raw)
		if err != nil {
			return Notifications{}, fmt.Errorf("%s: %w", f.key, err)
		}
		*f.dst = on
	}
	return out, nil
}

// SetNotifications writes the notification section atomically.
func (p *Provider) SetNotifications(v Notifications) error {
	return p.st.SetSettings(map[string]string{
		KeyNotifyURL:             v.URL,
		KeyNotifyRecordingFailed: strconv.FormatBool(v.Events.RecordingFailed),
		KeyNotifyDiskLow:         strconv.FormatBool(v.Events.DiskLow),
		KeyNotifyRecordingReady:  strconv.FormatBool(v.Events.RecordingReady),
		KeyNotifyGuideFailed:     strconv.FormatBool(v.Events.GuideFailed),
	})
}

// DVR returns the recording section. An absent or empty key reads as its
// default (0 is a real padding value).
func (p *Provider) DVR() (DVR, error) {
	start, err := p.intOr(KeyDVRPadStartSeconds, DefaultPadStartSeconds)
	if err != nil {
		return DVR{}, err
	}
	end, err := p.intOr(KeyDVRPadEndSeconds, DefaultPadEndSeconds)
	if err != nil {
		return DVR{}, err
	}
	quality, err := p.DVRQuality()
	if err != nil {
		return DVR{}, err
	}
	return DVR{PadStartSeconds: start, PadEndSeconds: end, Quality: quality}, nil
}

// DVRQuality is the recording conversion quality (the converter's hook). An
// absent, empty or unknown stored value reads as DVRQuality720p.
func (p *Provider) DVRQuality() (string, error) {
	raw, err := p.st.GetSetting(KeyDVRQuality)
	if err != nil {
		return "", err
	}
	if !ValidDVRQuality(raw) {
		return DVRQuality720p, nil
	}
	return raw, nil
}

// SetDVR writes the DVR section atomically; an empty Quality is left as stored.
func (p *Provider) SetDVR(v DVR) error {
	kv := map[string]string{
		KeyDVRPadStartSeconds: strconv.Itoa(v.PadStartSeconds),
		KeyDVRPadEndSeconds:   strconv.Itoa(v.PadEndSeconds),
	}
	if v.Quality != "" {
		kv[KeyDVRQuality] = v.Quality
	}
	return p.st.SetSettings(kv)
}

// DVRPadding is DVR as durations (the dvr.Deps.Padding hook).
func (p *Provider) DVRPadding() (start, end time.Duration, err error) {
	d, err := p.DVR()
	if err != nil {
		return 0, 0, err
	}
	clamp := func(n, hi int) time.Duration { return time.Duration(min(max(n, 0), hi)) * time.Second }
	return clamp(d.PadStartSeconds, MaxPadStartSeconds), clamp(d.PadEndSeconds, MaxPadEndSeconds), nil
}

// Padding limits (seconds) for the admin setting.
const (
	MaxPadStartSeconds = 1800
	MaxPadEndSeconds   = 3600
)

func (p *Provider) intOr(key string, def int) (int, error) {
	raw, err := p.st.GetSetting(key)
	if err != nil {
		return 0, err
	}
	if raw == "" {
		return def, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}

// HDHomeRunGuide returns the free-guide setting. An absent or empty key
// means on (the default).
func (p *Provider) HDHomeRunGuide() (HDHomeRunGuide, error) {
	raw, err := p.st.GetSetting(KeyEPGHDHomeRun)
	if err != nil {
		return HDHomeRunGuide{}, err
	}
	if raw == "" {
		return HDHomeRunGuide{Enabled: true}, nil
	}
	on, err := strconv.ParseBool(raw)
	if err != nil {
		return HDHomeRunGuide{}, fmt.Errorf("%s: %w", KeyEPGHDHomeRun, err)
	}
	return HDHomeRunGuide{Enabled: on}, nil
}

// SetHDHomeRunGuide writes the free-guide setting.
func (p *Provider) SetHDHomeRunGuide(v HDHomeRunGuide) error {
	return p.st.SetSetting(KeyEPGHDHomeRun, strconv.FormatBool(v.Enabled))
}

// XMLTV returns the current XMLTV settings.
func (p *Provider) XMLTV() (XMLTV, error) {
	source, err := p.st.GetSetting(KeyXMLTVSource)
	if err != nil {
		return XMLTV{}, err
	}
	raw, err := p.st.GetSetting(KeyXMLTVRefreshHours)
	if err != nil {
		return XMLTV{}, err
	}
	hours, err := parseIntSetting(raw)
	if err != nil {
		return XMLTV{}, fmt.Errorf("%s: %w", KeyXMLTVRefreshHours, err)
	}
	return XMLTV{Source: source, RefreshHours: hours}, nil
}

// SD returns the current Schedules Direct settings.
func (p *Provider) SD() (SD, error) {
	user, err := p.st.GetSetting(KeySDUsername)
	if err != nil {
		return SD{}, err
	}
	pass, err := p.st.GetSetting(KeySDPassword)
	if err != nil {
		return SD{}, err
	}
	lineup, err := p.st.GetSetting(KeySDLineupID)
	if err != nil {
		return SD{}, err
	}
	return SD{Username: user, Password: pass, LineupID: lineup}, nil
}

// Transcode returns the current transcode settings.
func (p *Provider) Transcode() (Transcode, error) {
	enc, err := p.st.GetSetting(KeyTranscodeEncoder)
	if err != nil {
		return Transcode{}, err
	}
	raw, err := p.st.GetSetting(KeyTranscodeAllowHEVC)
	if err != nil {
		return Transcode{}, err
	}
	allow, err := parseBoolSetting(raw)
	if err != nil {
		return Transcode{}, fmt.Errorf("%s: %w", KeyTranscodeAllowHEVC, err)
	}
	return Transcode{Encoder: enc, AllowHEVC: allow}, nil
}

// Streaming returns the current streaming (DVR buffer) settings.
func (p *Provider) Streaming() (Streaming, error) {
	raw, err := p.st.GetSetting(KeyStreamingBufferMinutes)
	if err != nil {
		return Streaming{}, err
	}
	mins, err := parseIntSetting(raw)
	if err != nil {
		return Streaming{}, fmt.Errorf("%s: %w", KeyStreamingBufferMinutes, err)
	}
	adaptive := false
	if raw, err := p.st.GetSetting(KeyStreamingAdaptive); err == nil && raw != "" {
		if adaptive, err = strconv.ParseBool(raw); err != nil {
			return Streaming{}, fmt.Errorf("%s: %w", KeyStreamingAdaptive, err)
		}
	}
	return Streaming{BufferMinutes: mins, Adaptive: adaptive}, nil
}

// SetXMLTV writes the full XMLTV section atomically.
func (p *Provider) SetXMLTV(v XMLTV) error {
	return p.st.SetSettings(map[string]string{
		KeyXMLTVSource:       v.Source,
		KeyXMLTVRefreshHours: strconv.Itoa(v.RefreshHours),
	})
}

// SetSD writes the full Schedules Direct section atomically.
func (p *Provider) SetSD(v SD) error {
	return p.st.SetSettings(map[string]string{
		KeySDUsername: v.Username,
		KeySDPassword: v.Password,
		KeySDLineupID: v.LineupID,
	})
}

// SetTranscode writes the full transcode section atomically.
func (p *Provider) SetTranscode(v Transcode) error {
	return p.st.SetSettings(map[string]string{
		KeyTranscodeEncoder:   v.Encoder,
		KeyTranscodeAllowHEVC: strconv.FormatBool(v.AllowHEVC),
	})
}

// SetStreaming writes the full streaming section atomically.
func (p *Provider) SetStreaming(v Streaming) error {
	return p.st.SetSettings(map[string]string{
		KeyStreamingBufferMinutes: strconv.Itoa(v.BufferMinutes),
		KeyStreamingAdaptive:      strconv.FormatBool(v.Adaptive),
	})
}

// Apply upserts all keys in a single store transaction. Used by the admin PUT
// settings handler to write multiple sections atomically after full validation
// (A3: no partial application across sections).
func (p *Provider) Apply(kv map[string]string) error {
	return p.st.SetSettings(kv)
}

// SeedFromConfig presence-seeds each product key from cfg when the key is
// absent in the DB. Stored empty strings are real values and are never
// re-seeded. When a key is already present and the config value differs, a
// notice is logged (DB is the sole source of truth after first seed).
//
// Defaults applied when cfg leaves fields zero: refreshHours=12, encoder=auto,
// allowHevc=false, bufferMinutes=15, dvr padding 60 s / 180 s, dvr quality 720p.
func (p *Provider) SeedFromConfig(cfg config.Config) error {
	refreshHours := cfg.XMLTV.RefreshHours
	if refreshHours == 0 {
		refreshHours = DefaultRefreshHours
	}
	encoder := cfg.Encoder
	if encoder == "" {
		encoder = DefaultEncoder
	}

	// fromConfig: the value comes from config/env (not just a default), so a
	// different database value is worth a log line.
	seeds := []struct {
		key        string
		val        string
		fromConfig bool
	}{
		{KeyXMLTVSource, cfg.XMLTV.Source, true},
		{KeyXMLTVRefreshHours, strconv.Itoa(refreshHours), true},
		{KeySDUsername, cfg.SchedulesDirect.Username, true},
		{KeySDPassword, cfg.SchedulesDirect.Password, true},
		{KeySDLineupID, cfg.SchedulesDirect.LineupID, true},
		{KeyTranscodeEncoder, encoder, true},
		{KeyTranscodeAllowHEVC, strconv.FormatBool(cfg.AllowHEVC), true},
		{KeyStreamingBufferMinutes, strconv.Itoa(DefaultBufferMinutes), false},
		{KeyStreamingAdaptive, "false", false},
		{KeyEPGHDHomeRun, "true", false},
		{KeyDVRPadStartSeconds, strconv.Itoa(DefaultPadStartSeconds), false},
		{KeyDVRPadEndSeconds, strconv.Itoa(DefaultPadEndSeconds), false},
		{KeyDVRQuality, DVRQuality720p, false},
	}

	for _, s := range seeds {
		has, err := p.st.HasSetting(s.key)
		if err != nil {
			return fmt.Errorf("HasSetting %s: %w", s.key, err)
		}
		if !has {
			if err := p.st.SetSetting(s.key, s.val); err != nil {
				return fmt.Errorf("seed %s: %w", s.key, err)
			}
			continue
		}
		cur, err := p.st.GetSetting(s.key)
		if err != nil {
			return fmt.Errorf("GetSetting %s: %w", s.key, err)
		}
		if cur != s.val && s.fromConfig {
			// Do not log secret values (password); key name is enough.
			if s.key == KeySDPassword {
				log.Printf("settings: %s is set in the database; config/env value ignored (Admin → Settings is the control plane)", s.key)
			} else {
				log.Printf("settings: %s is set in the database (%q); config/env value %q ignored (Admin → Settings is the control plane)", s.key, cur, s.val)
			}
		}
	}
	return nil
}

func parseIntSetting(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	return strconv.Atoi(s)
}

func parseBoolSetting(s string) (bool, error) {
	if s == "" {
		return false, nil
	}
	return strconv.ParseBool(s)
}
