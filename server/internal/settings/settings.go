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
	"strconv"

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
)

// Default product values used when seeding from empty/zero config.
const (
	DefaultRefreshHours  = 12
	DefaultEncoder       = "auto"
	DefaultBufferMinutes = 15
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
	return Streaming{BufferMinutes: mins}, nil
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
// allowHevc=false, bufferMinutes=15.
func (p *Provider) SeedFromConfig(cfg config.Config) error {
	refreshHours := cfg.XMLTV.RefreshHours
	if refreshHours == 0 {
		refreshHours = DefaultRefreshHours
	}
	encoder := cfg.Encoder
	if encoder == "" {
		encoder = DefaultEncoder
	}

	seeds := []struct {
		key string
		val string
	}{
		{KeyXMLTVSource, cfg.XMLTV.Source},
		{KeyXMLTVRefreshHours, strconv.Itoa(refreshHours)},
		{KeySDUsername, cfg.SchedulesDirect.Username},
		{KeySDPassword, cfg.SchedulesDirect.Password},
		{KeySDLineupID, cfg.SchedulesDirect.LineupID},
		{KeyTranscodeEncoder, encoder},
		{KeyTranscodeAllowHEVC, strconv.FormatBool(cfg.AllowHEVC)},
		{KeyStreamingBufferMinutes, strconv.Itoa(DefaultBufferMinutes)},
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
		if cur != s.val {
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
