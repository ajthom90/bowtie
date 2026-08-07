package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/ajthom90/bowtie/server/internal/api"
	"github.com/ajthom90/bowtie/server/internal/auth"
	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/epg"
	"github.com/ajthom90/bowtie/server/internal/settings"
	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/stream"
	"github.com/ajthom90/bowtie/server/internal/transcode"
	"github.com/ajthom90/bowtie/server/internal/tuner"
)

// Stamped by GoReleaser via -ldflags "-X main.version=...". Dev default stays "0.1.0-dev".
var version = "0.1.0-dev"

func main() {
	dataDirDefault := "./data"
	if v := os.Getenv("BOWTIE_DATA_DIR"); v != "" {
		dataDirDefault = v
	}
	dataDir := flag.String("data-dir", dataDirDefault, "data directory (env BOWTIE_DATA_DIR)")
	flag.Parse()

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		log.Fatalf("create data dir: %v", err)
	}

	cfg, err := config.Load(*dataDir)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Background stack hangs off its own root ctx (cancelled only in shutdown)
	// so SIGTERM sequence can be: stop HTTP → cancel workers → close store.
	addr, shutdown, err := run(context.Background(), cfg)
	if err != nil {
		log.Fatalf("run: %v", err)
	}
	log.Printf("bowtie %s listening on %s (data=%s)", version, addr, cfg.DataDir)

	<-sigCtx.Done()
	log.Printf("shutdown signal received")
	shutdown()
}

// run assembles the full server stack and starts listening.
// Background work (tuner refresh, EPG, stream manager) hangs off a root context
// that is cancelled only by the returned shutdown func (HTTP stop first, then
// cancel, then store close). The parent ctx is reserved for future use /
// test control of startup lifetime; it is not used as the worker root so that
// signal handling in main can enforce the graceful-shutdown order.
// Returns the actual listen address (useful when ListenAddr is ":0" or "127.0.0.1:0").
func run(ctx context.Context, cfg config.Config) (addr string, shutdown func(), err error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(cfg.SegmentDir, 0o755); err != nil {
		return "", nil, fmt.Errorf("create segment dir: %w", err)
	}

	st, err := store.Open(filepath.Join(cfg.DataDir, "bowtie.db"))
	if err != nil {
		return "", nil, fmt.Errorf("open store: %w", err)
	}

	// Presence-seed product settings from config/env (first boot only).
	// After seed the DB is the sole source of truth for these keys.
	settingsProv := settings.NewProvider(st)
	if err := settingsProv.SeedFromConfig(cfg); err != nil {
		_ = st.Close()
		return "", nil, fmt.Errorf("seed settings: %w", err)
	}

	secret, err := loadOrCreateJWTSecret(st)
	if err != nil {
		_ = st.Close()
		return "", nil, fmt.Errorf("jwt secret: %w", err)
	}

	if _, err := bootstrapAdmin(st); err != nil {
		_ = st.Close()
		return "", nil, fmt.Errorf("bootstrap admin: %w", err)
	}

	authSvc := &auth.Auth{Secret: secret, Store: st}

	streamSecret, err := loadOrCreateStreamTokenSecret(st)
	if err != nil {
		_ = st.Close()
		return "", nil, fmt.Errorf("stream token secret: %w", err)
	}

	// Root context for ALL background goroutines (tuners, epg, stream manager).
	rootCtx, rootCancel := context.WithCancel(context.Background())

	tuners := tuner.New(st, cfg)
	go runTunerRefresh(rootCtx, tuners)

	// EPG supervisor always-on; sources/intervals from settingsProv (live, no restart).
	epgSvc := epg.NewService(st, settingsProv)
	go epgSvc.Run(rootCtx)

	// Probe encoders once at startup; cache for negotiation + admin endpoint.
	probeCtx, probeCancel := context.WithTimeout(context.Background(), 30*time.Second)
	caps := transcode.Probe(probeCtx, cfg.FFmpegPath)
	probeCancel()
	log.Printf("encoder probe: available=%v version=%s", caps.Available, caps.FFmpegVersion)

	ingest := stream.NewIngestManager(stream.HTTPDial)
	streamMgr := stream.NewManager(stream.ManagerDeps{
		Cfg:      cfg,
		Store:    st,
		Tuners:   tuners,
		Caps:     caps,
		Runner:   &stream.FFmpegRunner{Path: cfg.FFmpegPath},
		Settings: settingsProv,
		Ingest:   ingest,
	})
	go streamMgr.Run(rootCtx)

	apiHandler := api.New(api.Deps{
		Cfg:               cfg,
		Store:             st,
		Auth:              authSvc,
		Tuners:            tuners,
		EPG:               epgSvc,
		Probe:             func() transcode.Capabilities { return caps },
		Streams:           streamMgr,
		StreamTokenSecret: streamSecret,
		Settings:          settingsProv,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "ok")
	})
	mux.Handle("/", apiHandler)

	ln, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		rootCancel()
		_ = st.Close()
		return "", nil, fmt.Errorf("listen %s: %w", cfg.ListenAddr, err)
	}
	actualAddr := ln.Addr().String()

	srv := &http.Server{Handler: mux}

	serveErr := make(chan error, 1)
	go func() {
		err := srv.Serve(ln)
		if err != nil && err != http.ErrServerClosed {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	var once sync.Once
	doShutdown := func() {
		once.Do(func() {
			log.Printf("shutdown: stopping HTTP (10s timeout)")
			httpCtx, httpCancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer httpCancel()
			if err := srv.Shutdown(httpCtx); err != nil {
				log.Printf("shutdown: http.Server.Shutdown: %v", err)
			}

			log.Printf("shutdown: cancelling root context (sessions, tuners, epg)")
			rootCancel()

			// Brief wait so stream manager reaper can finish teardown.
			time.Sleep(100 * time.Millisecond)

			log.Printf("shutdown: closing store")
			if err := st.Close(); err != nil {
				log.Printf("shutdown: store.Close: %v", err)
			}
			log.Printf("shutdown: complete")
		})
	}

	// Surface unexpected Serve errors (listener already closed is normal on shutdown).
	go func() {
		if err := <-serveErr; err != nil {
			log.Printf("serve error: %v", err)
		}
	}()

	return actualAddr, doShutdown, nil
}

// runTunerRefresh does an immediate Refresh, then every 60s until ctx is done.
func runTunerRefresh(ctx context.Context, m *tuner.Manager) {
	do := func() {
		rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := m.Refresh(rctx); err != nil {
			// Don't log on context cancel during shutdown.
			if ctx.Err() == nil {
				log.Printf("tuner refresh: %v", err)
			}
		}
	}
	do()
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			do()
		}
	}
}

func loadOrCreateJWTSecret(st *store.Store) ([]byte, error) {
	return loadOrCreateHexSecret(st, "jwt_secret")
}

func loadOrCreateStreamTokenSecret(st *store.Store) ([]byte, error) {
	return loadOrCreateHexSecret(st, "stream_token_secret")
}

func loadOrCreateHexSecret(st *store.Store, key string) ([]byte, error) {
	hexSecret, err := st.GetSetting(key)
	if err != nil {
		return nil, err
	}
	if hexSecret == "" {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return nil, fmt.Errorf("generate %s: %w", key, err)
		}
		hexSecret = hex.EncodeToString(raw)
		if err := st.SetSetting(key, hexSecret); err != nil {
			return nil, err
		}
	}
	secret, err := hex.DecodeString(hexSecret)
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", key, err)
	}
	if len(secret) == 0 {
		return nil, fmt.Errorf("empty %s", key)
	}
	return secret, nil
}

// bootstrapAdmin creates the first admin user when the store is empty.
// Returns the generated password when a user was created, or "" if users already exist.
func bootstrapAdmin(st *store.Store) (password string, err error) {
	n, err := st.CountUsers()
	if err != nil {
		return "", err
	}
	if n > 0 {
		return "", nil
	}

	pw, err := randomPassword(16)
	if err != nil {
		return "", err
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return "", err
	}
	_, err = st.CreateUser(store.User{
		Username:     "admin",
		PasswordHash: hash,
		Role:         "admin",
		MaxQuality:   "",
		CreatedAt:    time.Now().UTC(),
	})
	if err != nil {
		return "", err
	}
	log.Printf("first run: created admin user %q with password %q — change it after login", "admin", pw)
	return pw, nil
}

func randomPassword(n int) (string, error) {
	// 16 hex chars from 8 random bytes when n==16; general: ceil(n/2) bytes hex-truncated.
	nbytes := (n + 1) / 2
	b := make([]byte, nbytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	s := hex.EncodeToString(b)
	if len(s) > n {
		s = s[:n]
	}
	return s, nil
}
