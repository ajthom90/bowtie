package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/ajthom90/bowtie/server/internal/api"
	"github.com/ajthom90/bowtie/server/internal/auth"
	"github.com/ajthom90/bowtie/server/internal/config"
	"github.com/ajthom90/bowtie/server/internal/epg"
	"github.com/ajthom90/bowtie/server/internal/store"
	"github.com/ajthom90/bowtie/server/internal/tuner"
)

const version = "0.1.0-dev"

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

	if err := os.MkdirAll(cfg.SegmentDir, 0o755); err != nil {
		log.Fatalf("create segment dir: %v", err)
	}

	st, err := store.Open(filepath.Join(cfg.DataDir, "bowtie.db"))
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	secret, err := loadOrCreateJWTSecret(st)
	if err != nil {
		log.Fatalf("jwt secret: %v", err)
	}

	if err := bootstrapAdmin(st); err != nil {
		log.Fatalf("bootstrap admin: %v", err)
	}

	authSvc := &auth.Auth{Secret: secret, Store: st}

	tuners := tuner.New(st, cfg)
	// Initial refresh (best-effort) then periodic every 60s.
	go runTunerRefresh(tuners)

	epgSvc := epg.NewService(st, cfg)
	// Background EPG refresh loops (xmltv / schedules direct when configured).
	go epgSvc.Run(context.Background())

	apiHandler := api.New(api.Deps{
		Cfg:    cfg,
		Store:  st,
		Auth:   authSvc,
		Tuners: tuners,
		EPG:    epgSvc,
	})

	log.Printf("bowtie %s listening on %s (data=%s)", version, cfg.ListenAddr, cfg.DataDir)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "ok")
	})
	mux.Handle("/", apiHandler)

	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: mux,
	}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}

// runTunerRefresh does an immediate Refresh, then every 60s.
func runTunerRefresh(m *tuner.Manager) {
	do := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := m.Refresh(ctx); err != nil {
			log.Printf("tuner refresh: %v", err)
		}
	}
	do()
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		do()
	}
}

func loadOrCreateJWTSecret(st *store.Store) ([]byte, error) {
	const key = "jwt_secret"
	hexSecret, err := st.GetSetting(key)
	if err != nil {
		return nil, err
	}
	if hexSecret == "" {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return nil, fmt.Errorf("generate jwt secret: %w", err)
		}
		hexSecret = hex.EncodeToString(raw)
		if err := st.SetSetting(key, hexSecret); err != nil {
			return nil, err
		}
	}
	secret, err := hex.DecodeString(hexSecret)
	if err != nil {
		return nil, fmt.Errorf("decode jwt secret: %w", err)
	}
	if len(secret) == 0 {
		return nil, fmt.Errorf("empty jwt secret")
	}
	return secret, nil
}

func bootstrapAdmin(st *store.Store) error {
	n, err := st.CountUsers()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}

	pw, err := randomPassword(16)
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return err
	}
	_, err = st.CreateUser(store.User{
		Username:     "admin",
		PasswordHash: hash,
		Role:         "admin",
		MaxQuality:   "",
		CreatedAt:    time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	log.Printf("first run: created admin user %q with password %q — change it after login", "admin", pw)
	return nil
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
