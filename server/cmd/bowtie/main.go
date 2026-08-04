package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/ajthom90/bowtie/server/internal/config"
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

	log.Printf("bowtie %s listening on %s (data=%s)", version, cfg.ListenAddr, cfg.DataDir)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "ok")
	})

	srv := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: mux,
	}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("serve: %v", err)
	}
}
