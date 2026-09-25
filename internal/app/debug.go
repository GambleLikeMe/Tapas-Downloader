package app

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

type debugEntry struct {
	At      time.Time `json:"at"`
	Message string    `json:"message"`
}

type debugState struct {
	mu      sync.Mutex
	enabled bool
	entries []debugEntry
}

func (d *debugState) record(message string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.enabled {
		return
	}
	d.entries = append(d.entries, debugEntry{At: time.Now().UTC(), Message: message})
	if len(d.entries) > 200 {
		d.entries = append([]debugEntry(nil), d.entries[len(d.entries)-200:]...)
	}
}

func (d *debugState) snapshot() (bool, []debugEntry) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.enabled, append([]debugEntry{}, d.entries...)
}

func (d *debugState) setEnabled(enabled bool) {
	d.mu.Lock()
	d.enabled = enabled
	if !enabled {
		d.entries = nil
	}
	d.mu.Unlock()
}

func (a *app) logEvent(level, message string) {
	if level == "DEBUG" {
		enabled, _ := a.debug.snapshot()
		if !enabled {
			return
		}
	}
	log.Printf("[%s] %s", level, message)
	a.debug.record(level + " " + message)
}

func (a *app) getDebug(w http.ResponseWriter, r *http.Request) {
	enabled, entries := a.debug.snapshot()
	send(w, map[string]any{"enabled": enabled, "entries": entries})
}

func (a *app) setDebug(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &input); err != nil {
		fail(w, 400, err)
		return
	}
	a.mu.Lock()
	next := a.config
	next.DebugEnabled = input.Enabled
	err := saveJSONAtomic(a.configPath, next)
	if err == nil {
		a.config = next
	}
	a.mu.Unlock()
	if err != nil {
		fail(w, 500, err)
		return
	}
	a.debug.setEnabled(input.Enabled)
	if input.Enabled {
		a.logEvent("INFO", "Debug logging enabled")
	}
	if !input.Enabled {
		a.logEvent("INFO", "Debug logging disabled")
	}
	a.getDebug(w, r)
}

func (a *app) clearDebug(w http.ResponseWriter, r *http.Request) {
	a.debug.mu.Lock()
	a.debug.entries = nil
	a.debug.mu.Unlock()
	a.getDebug(w, r)
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(data)
}

func (a *app) debugMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/api/debug") || (r.Method == http.MethodGet && r.URL.Path == "/api/tasks") {
			next.ServeHTTP(w, r)
			return
		}
		started := time.Now()
		tracked := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(tracked, r)
		status := tracked.status
		if status == 0 {
			status = http.StatusOK
		}
		a.logEvent("DEBUG", fmt.Sprintf("%s %s → %d (%s)", r.Method, r.URL.Path, status, time.Since(started).Round(time.Millisecond)))
	})
}
