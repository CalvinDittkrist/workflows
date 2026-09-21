package main

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// The HTTP interface is read-only. The factory is steered on GitHub — routed by a label, released by
// an assignee, cancelled by a label — so its own interface has no endpoint that writes anything, and
// it needs no login of its own: it binds to one address, by default the loopback, and is reached
// over the tailnet.
func (f *Factory) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/", uiHandler())
	mux.HandleFunc("/api", f.index)
	mux.HandleFunc("/api/status", f.status)
	mux.HandleFunc("/api/repositories", f.repositories)
	mux.HandleFunc("/api/line", f.line)
	mux.HandleFunc("/api/runs/{id}", f.run)
	return readOnly(mux)
}

// readOnly refuses every writing method, whatever the path, so the interface cannot grow one by
// accident.
func readOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "the factory's interface is read-only; it is steered on GitHub", http.StatusMethodNotAllowed)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// index is what the interface offers, for a reader with a terminal rather than a browser. The
// browser gets the dashboard under /, which reads exactly these four.
func (f *Factory) index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("factory\n\n" +
		"GET /api/status        what the factory is doing\n" +
		"GET /api/repositories  the connected repositories\n" +
		"GET /api/line          what runs now, what waits, what is done\n" +
		"GET /api/runs/{id}     one run with its record and its events (?after=<seq> for the rest)\n"))
}

// status is what the factory is doing: running, paused, or waiting for the Claude quota to reset.
func (f *Factory) status(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	quotaUntil, polledAt := f.quotaUntil, f.polledAt
	f.mu.Unlock()
	state := "running"
	switch {
	case f.settings.Paused:
		state = "paused"
	case quotaUntil != nil:
		state = "waiting-for-quota"
	}
	writeJSON(w, map[string]any{
		"state":      state,
		"quotaUntil": quotaUntil,
		"fake":       f.fake,
		"label":      f.settings.Label,
		"deadline":   f.settings.Deadline.String(),
		"startedAt":  f.started,
		"polledAt":   polledAt,
	})
}

// repositories are the connected repositories with what waits in each of them.
func (f *Factory) repositories(w http.ResponseWriter, _ *http.Request) {
	queued := map[string]int{}
	for _, issue := range f.waiting() {
		queued[issue.Repository]++
	}
	out := make([]map[string]any, 0, len(f.settings.Repositories))
	for _, repository := range f.settings.Repositories {
		out = append(out, map[string]any{"repository": repository, "queued": queued[repository]})
	}
	writeJSON(w, out)
}

// line is the one line of work across all connected repositories: what runs now, what waits in which
// order, and what is done, oldest first.
func (f *Factory) line(w http.ResponseWriter, _ *http.Request) {
	now, done := []Run{}, []Run{}
	for _, run := range f.runs.list() {
		if run.EndedAt == nil {
			now = append(now, run)
		} else {
			done = append(done, run)
		}
	}
	writeJSON(w, map[string]any{"now": now, "queue": f.waiting(), "done": done})
}

// run is one run with its record and its events. after skips the events the reader already has, so
// following a running worker costs one request per poll and not the whole log again.
func (f *Factory) run(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	record, ok := f.runs.get(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	after, _ := strconv.Atoi(r.URL.Query().Get("after"))
	if after < 0 {
		after = 0
	}
	events, err := f.runs.events(id, after)
	if err != nil {
		http.Error(w, "the event log of run "+strconv.Itoa(id)+" cannot be read", http.StatusInternalServerError)
		return
	}
	writeJSON(w, struct {
		Run
		Events []Event `json:"events"`
	}{record, events})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(v)
}
