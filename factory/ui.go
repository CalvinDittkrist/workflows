package main

import (
	"embed"
	"io/fs"
	"net/http"
)

// The dashboard is in the binary. The factory host runs one file and no web server, no Node and no
// checkout: `make ui` builds ui/dist/app with Vite and this embeds it.
//
// Go refuses an embed pattern that matches nothing, so ui/dist is in git with a placeholder while
// the build output under it is not — and a binary built without that step answers with what to do
// rather than with nothing (see uiHandler).
//
//go:embed all:ui/dist
var ui embed.FS

// uiHandler serves the built dashboard. Its file names carry a content hash, so a reader that
// reloads never meets a stale bundle; the selected run lives in the URL's fragment, which the
// browser keeps to itself, so no path but the files themselves is ever asked for.
func uiHandler() http.Handler {
	dist, err := fs.Sub(ui, "ui/dist/app")
	if err != nil || !built(dist) {
		return http.HandlerFunc(notBuilt)
	}
	return http.FileServerFS(dist)
}

func built(dist fs.FS) bool {
	_, err := fs.Stat(dist, "index.html")
	return err == nil
}

// notBuilt answers a binary that was built without the dashboard. The API is unaffected, which is
// why this is an answer and not a refusal to start.
func notBuilt(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte("this factory was built without the dashboard; run make ui and build it again. The interface answers under /api\n"))
}
