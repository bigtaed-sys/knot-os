// Package web exposes the embedded SvelteKit UI bundle as an http.Handler.
//
// The UI build artifacts live in ./dist and are populated by `scripts/build-ui.sh`
// (or CI) before `go build`. The directory is kept under version control with a
// .gitkeep file so that the embed directive always finds at least one entry.
package web

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

// Handler returns an http.Handler that serves the embedded UI.
//
// SPA routing: any request whose path does not match a real file is served
// the SvelteKit fallback (index.html). Static assets under /_app/ are served
// with long-lived cache headers because their filenames are content-hashed.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		// embed guarantees this cannot fail at runtime; panic is appropriate.
		panic("web: failed to open embedded dist subtree: " + err.Error())
	}

	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		// Hashed asset paths under /_app/ are immutable — long cache.
		if strings.HasPrefix(r.URL.Path, "/_app/immutable/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}

		// If the requested file does not exist in the embedded FS, fall
		// back to index.html so SvelteKit's client-side router can handle
		// the route.
		if _, err := fs.Stat(sub, path); err != nil {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			fileServer.ServeHTTP(w, r2)
			return
		}

		fileServer.ServeHTTP(w, r)
	})
}

// IsBuilt reports whether the embedded FS contains a real UI build (an
// index.html). Returns false when only the .gitkeep placeholder is present,
// which happens if the binary was built without first running the UI build.
func IsBuilt() bool {
	_, err := fs.Stat(distFS, "dist/index.html")
	return err == nil
}
