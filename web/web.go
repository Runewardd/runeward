// Package web embeds the runeward dashboard's static assets and exposes an
// http.Handler that serves them. It depends only on the Go standard library.
package web

import (
	"embed"
	"io/fs"
	"net/http"
)

// assets holds the embedded single-page dashboard (HTML, CSS, JS).
//
//go:embed index.html app.js style.css logo.png
var assets embed.FS

// FS returns the embedded filesystem containing the dashboard assets.
func FS() fs.FS {
	return assets
}

// Handler returns an http.Handler that serves the embedded dashboard assets.
// Requests for "/" resolve to index.html via the standard file server.
//
// The assets are embedded with a zero modtime and carry no content hash, so
// without an explicit directive browsers heuristically cache them and can keep
// serving a stale app.js/index.html across rebuilds. We send no-store so every
// reload picks up the current binary's dashboard.
func Handler() http.Handler {
	fileServer := http.FileServerFS(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store, max-age=0, must-revalidate")
		fileServer.ServeHTTP(w, r)
	})
}
