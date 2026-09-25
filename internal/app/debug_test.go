package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDebugToggleAndRedaction(t *testing.T) {
	a := &app{configPath: filepath.Join(t.TempDir(), "settings.json")}
	enabled := httptest.NewRecorder()
	a.setDebug(enabled, httptest.NewRequest(http.MethodPost, "/api/debug", strings.NewReader(`{"enabled":true}`)))
	if enabled.Code != http.StatusOK {
		t.Fatalf("enable debug: %d %s", enabled.Code, enabled.Body.String())
	}
	handler := a.debugMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadRequest) }))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/accounts?password=secret", strings.NewReader(`{"password":"secret"}`)))
	active, entries := a.debug.snapshot()
	if !active || len(entries) != 2 || !strings.Contains(entries[1].Message, "POST /api/accounts → 400") || strings.Contains(entries[1].Message, "secret") {
		t.Fatalf("debug entries: %+v", entries)
	}
	disabled := httptest.NewRecorder()
	a.setDebug(disabled, httptest.NewRequest(http.MethodPost, "/api/debug", strings.NewReader(`{"enabled":false}`)))
	active, entries = a.debug.snapshot()
	if active || len(entries) != 0 {
		t.Fatalf("debug stayed active: %v %+v", active, entries)
	}
	var saved settings
	data, err := os.ReadFile(a.configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.DebugEnabled {
		t.Fatal("disabled setting was not saved")
	}
}
