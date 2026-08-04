// Package web embeds the Vite-built SPA and serves it with SPA fallback.
//
// dist/ is populated by `make build-web` (npm run build → outDir here).
// A committed placeholder index.html keeps //go:embed valid when the UI
// has not been built yet; the placeholder body reads "bowtie: web ui not built".
package web

import (
	"embed"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var dist embed.FS

// Handler returns an http.Handler that serves the embedded SPA.
//
// Behaviour:
//   - Paths under /api are never served (404) — API routes own those.
//   - Existing static files under dist/ are served as-is.
//   - Unknown non-file paths fall back to index.html (client-side routing).
//   - If dist has no usable index.html, responds 200 with plain text
//     "bowtie: web ui not built".
func Handler() http.Handler {
	root, err := fs.Sub(dist, "dist")
	if err != nil {
		return notBuiltHandler()
	}
	if !hasIndex(root) {
		return notBuiltHandler()
	}
	fileServer := http.FileServer(http.FS(root))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api") {
			http.NotFound(w, r)
			return
		}

		// Clean path and strip leading slash for fs lookups.
		upath := path.Clean("/" + r.URL.Path)
		if upath == "/" {
			serveIndex(w, r, root)
			return
		}
		rel := strings.TrimPrefix(upath, "/")

		// If the path maps to a real file (not a directory), serve it.
		if f, err := root.Open(rel); err == nil {
			stat, statErr := f.Stat()
			_ = f.Close()
			if statErr == nil && !stat.IsDir() {
				fileServer.ServeHTTP(w, r)
				return
			}
		}

		// SPA fallback for client routes.
		serveIndex(w, r, root)
	})
}

func hasIndex(root fs.FS) bool {
	f, err := root.Open("index.html")
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

func serveIndex(w http.ResponseWriter, r *http.Request, root fs.FS) {
	f, err := root.Open("index.html")
	if err != nil {
		notBuilt(w)
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		notBuilt(w)
		return
	}
	// http.ServeContent needs an io.ReadSeeker.
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		// Fallback: buffer whole file (index is small).
		data, err := fs.ReadFile(root, "index.html")
		if err != nil {
			notBuilt(w)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	http.ServeContent(w, r, "index.html", info.ModTime(), rs)
}

func notBuilt(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "bowtie: web ui not built")
}

func notBuiltHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api") {
			http.NotFound(w, r)
			return
		}
		notBuilt(w)
	})
}
