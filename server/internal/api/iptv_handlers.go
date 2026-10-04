package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ajthom90/bowtie/server/internal/auth"
	"github.com/ajthom90/bowtie/server/internal/epg"
	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/stream"
	"github.com/ajthom90/bowtie/server/internal/transcode"
)

// IPTV feed: an M3U playlist and XMLTV guide behind a personal key, for apps
// that can't sign in (Kodi — including on Xbox — VLC, TiviMate, Plex,
// Jellyfin, Channels). Streams start as the key's account, so limits and
// parental controls apply.

const iptvGuideHours = 72

func hashFeedKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// baseURL is the scheme and host the client used (honoring a TLS proxy).
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func iptvChannelID(id int64) string { return fmt.Sprintf("bowtie.%d", id) }

// handleCreateFeed serves POST /api/v1/me/feed: a new personal feed key
// (any previous one stops working).
func (s *Server) handleCreateFeed(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFrom(r.Context())
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create key")
		return
	}
	key := base64.RawURLEncoding.EncodeToString(raw)
	if err := s.deps.Store.SetFeedKeyHash(claims.UserID, hashFeedKey(key)); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save key")
		return
	}
	base := baseURL(r) + "/api/v1/iptv/" + key
	writeJSON(w, http.StatusOK, map[string]string{
		"key":      key,
		"m3uUrl":   base + "/playlist.m3u",
		"xmltvUrl": base + "/guide.xml",
	})
}

// handleDeleteFeed serves DELETE /api/v1/me/feed: turns the feed off.
func (s *Server) handleDeleteFeed(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.ClaimsFrom(r.Context())
	if err := s.deps.Store.SetFeedKeyHash(claims.UserID, ""); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to turn the feed off")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) feedUser(w http.ResponseWriter, r *http.Request) (store.User, bool) {
	u, err := s.deps.Store.UserByFeedKeyHash(hashFeedKey(r.PathValue("key")))
	if err != nil {
		writeError(w, http.StatusNotFound, "feed not found")
		return store.User{}, false
	}
	return u, true
}

// handleIPTVPlaylist serves GET /api/v1/iptv/{key}/playlist.m3u.
func (s *Server) handleIPTVPlaylist(w http.ResponseWriter, r *http.Request) {
	u, ok := s.feedUser(w, r)
	if !ok {
		return
	}
	chans, err := s.deps.Store.ListChannels(true)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channels")
		return
	}
	icons, _ := epgIconByID(s.deps.Store)
	policy := policyFor(u)
	base := baseURL(r) + "/api/v1/iptv/" + r.PathValue("key")
	var b strings.Builder
	fmt.Fprintf(&b, "#EXTM3U url-tvg=\"%s/guide.xml\"\n", base)
	for _, c := range chans {
		if !policy.ChannelAllowed(c.ID) {
			continue
		}
		logo := ""
		if c.EPGChannelID != "" {
			logo = icons[c.EPGChannelID]
		}
		fmt.Fprintf(&b, "#EXTINF:-1 tvg-id=\"%s\" tvg-chno=\"%s\" tvg-name=\"%s\" tvg-logo=\"%s\" group-title=\"Bowtie\",%s\n%s/stream/%d\n",
			iptvChannelID(c.ID), attr(c.GuideNumber), attr(c.Name), attr(logo), oneLine(c.Name), base, c.ID)
	}
	w.Header().Set("Content-Type", "audio/x-mpegurl; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

func attr(s string) string    { return strings.ReplaceAll(oneLine(s), `"`, "'") }
func oneLine(s string) string { return strings.NewReplacer("\n", " ", "\r", " ").Replace(s) }

// handleIPTVStream serves GET /api/v1/iptv/{key}/stream/{channelId}: starts
// or joins the channel as the feed's account and redirects to its HLS
// playlist. ?quality=high|medium|low picks a profile.
func (s *Server) handleIPTVStream(w http.ResponseWriter, r *http.Request) {
	u, ok := s.feedUser(w, r)
	if !ok {
		return
	}
	if s.deps.Streams == nil {
		writeError(w, http.StatusServiceUnavailable, "streaming not available")
		return
	}
	id, err := parsePathID(r, "channelId")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid channel id")
		return
	}
	if why := s.parentalStartBlock(r, u, id); why != "" {
		writeParentalBlock(w, why)
		return
	}
	caps := transcode.ClientCaps{VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, Profile: r.URL.Query().Get("quality")}
	h, err := s.deps.Streams.Start(r.Context(), u, id, caps)
	if err != nil {
		s.writeStartError(w, err, u)
		return
	}
	tok := signViewerToken(s.deps.StreamTokenSecret, h.ViewerID)
	http.Redirect(w, r, baseURL(r)+"/api/v1/stream/"+h.ViewerID+"/index.m3u8?token="+tok, http.StatusFound)
}

// XMLTV document types.
type xmltvDoc struct {
	XMLName    xml.Name    `xml:"tv"`
	Generator  string      `xml:"generator-info-name,attr"`
	Channels   []xmltvChan `xml:"channel"`
	Programmes []xmltvProg `xml:"programme"`
}
type xmltvChan struct {
	ID    string    `xml:"id,attr"`
	Names []string  `xml:"display-name"`
	Icon  *xmltvSrc `xml:"icon,omitempty"`
}
type xmltvSrc struct {
	Src string `xml:"src,attr"`
}
type xmltvProg struct {
	Start    string        `xml:"start,attr"`
	Stop     string        `xml:"stop,attr"`
	Channel  string        `xml:"channel,attr"`
	Title    string        `xml:"title"`
	SubTitle string        `xml:"sub-title,omitempty"`
	Desc     string        `xml:"desc,omitempty"`
	Category string        `xml:"category,omitempty"`
	Episode  *xmltvEpisode `xml:"episode-num,omitempty"`
	New      *struct{}     `xml:"new,omitempty"`
	Rating   *xmltvRating  `xml:"rating,omitempty"`
}
type xmltvEpisode struct {
	System string `xml:"system,attr"`
	Value  string `xml:",chardata"`
}
type xmltvRating struct {
	System string `xml:"system,attr"`
	Value  string `xml:"value"`
}

func xmltvTime(t time.Time) string { return t.UTC().Format("20060102150405 -0700") }

// ddProgID writes a program ID the way XMLTV's dd_progid does
// (EP012345670001 → EP01234567.0001).
func ddProgID(id string) string {
	if len(id) > 10 {
		return id[:10] + "." + id[10:]
	}
	return id
}

// handleIPTVGuide serves GET /api/v1/iptv/{key}/guide.xml: XMLTV for the
// next 72 hours of the feed's channels.
func (s *Server) handleIPTVGuide(w http.ResponseWriter, r *http.Request) {
	u, ok := s.feedUser(w, r)
	if !ok {
		return
	}
	doc := xmltvDoc{Generator: "Bowtie"}
	if s.deps.EPG != nil {
		now := time.Now().UTC()
		guide, err := s.deps.EPG.Guide(r.Context(), now.Add(-time.Hour), now.Add(iptvGuideHours*time.Hour))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load guide")
			return
		}
		guide = applyParental(guide, policyFor(u))
		for _, g := range guide {
			doc.Channels = append(doc.Channels, xmltvChannel(g))
			for _, p := range g.Programs {
				doc.Programmes = append(doc.Programmes, xmltvProgramme(g, p))
			}
		}
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	_, _ = w.Write([]byte(xml.Header))
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	_ = enc.Encode(doc)
}

func xmltvChannel(g epg.GuideChannel) xmltvChan {
	c := xmltvChan{ID: iptvChannelID(g.ChannelID), Names: []string{g.Name, g.GuideNumber}}
	if g.LogoURL != "" {
		c.Icon = &xmltvSrc{Src: g.LogoURL}
	}
	return c
}

func xmltvProgramme(g epg.GuideChannel, p epg.GuideProgram) xmltvProg {
	x := xmltvProg{Start: xmltvTime(p.Start), Stop: xmltvTime(p.Stop), Channel: iptvChannelID(g.ChannelID),
		Title: p.Title, SubTitle: p.Subtitle, Desc: p.Description, Category: p.Category}
	if p.ProgramID != "" {
		x.Episode = &xmltvEpisode{System: "dd_progid", Value: ddProgID(p.ProgramID)}
	}
	if p.IsNew {
		x.New = &struct{}{}
	}
	if p.Rating != "" {
		x.Rating = &xmltvRating{System: "VCHIP", Value: p.Rating}
	}
	return x
}

func signViewerToken(secret []byte, viewerID string) string {
	return stream.SignStreamToken(secret, viewerID, time.Now().UTC().Add(streamTokenTTL))
}
