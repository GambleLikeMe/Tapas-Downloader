package app

import (
	"api-scraper/internal/api"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSeriesCacheExpiryRefreshAndClear(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "cache.json")
	cache, err := loadSeriesCache(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	cache.now = func() time.Time { return now }
	details := api.ComicDetails{ID: 42, Title: "Series", Type: "COMICS"}
	episodes := []api.Episode{{ID: 7, Title: "Chapter", Free: true}}
	if err := cache.set("account-a", 42, details, episodes); err != nil {
		t.Fatal(err)
	}
	if _, hit := cache.get("account-b", 42); hit {
		t.Fatal("cache mixed accounts")
	}
	if entry, hit := cache.get("account-a", 42); !hit || len(entry.Episodes) != 1 {
		t.Fatal("fresh chapter list was not cached")
	}
	reloaded, err := loadSeriesCache(path)
	if err != nil {
		t.Fatal(err)
	}
	reloaded.now = func() time.Time { return now.Add(4 * time.Minute) }
	if _, hit := reloaded.get("account-a", 42); !hit {
		t.Fatal("cache did not survive restart within TTL")
	}
	reloaded.now = func() time.Time { return now.Add(seriesCacheTTL) }
	if _, hit := reloaded.get("account-a", 42); hit {
		t.Fatal("expired chapter list was returned")
	}
	reloaded.now = func() time.Time { return now.Add(time.Minute) }
	if err := reloaded.invalidate("account-a", 42); err != nil {
		t.Fatal(err)
	}
	if _, hit := reloaded.get("account-a", 42); hit {
		t.Fatal("series refresh did not invalidate the entry")
	}
	if err := reloaded.set("account-a", 42, details, episodes); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"settings.json", "history.json", "saved.pdf"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("keep"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := reloaded.clear(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("cache file was not cleared")
	}
	for _, name := range []string{"settings.json", "history.json", "saved.pdf"} {
		if data, err := os.ReadFile(filepath.Join(root, name)); err != nil || string(data) != "keep" {
			t.Fatalf("cache clear touched %s: %v", name, err)
		}
	}
}

func TestSeriesHandlerUsesCachedChaptersWithoutTapasLogin(t *testing.T) {
	root := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(previous)
	if err := os.Mkdir("accounts", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("accounts", "a.txt"), []byte("unused@example.com\nunused"), 0600); err != nil {
		t.Fatal(err)
	}
	cache, err := loadSeriesCache("cache.json")
	if err != nil {
		t.Fatal(err)
	}
	details := api.ComicDetails{ID: 42, Title: "Cached Series", Type: "COMICS"}
	if err := cache.set("a.txt", 42, details, []api.Episode{{ID: 7, Scene: 1, Free: true}}); err != nil {
		t.Fatal(err)
	}
	server := &app{root: root, cache: cache, activityPath: "history.json"}
	if err := server.loadActivity(); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.series(response, httptest.NewRequest(http.MethodGet, "/api/series?id=42&account=a.txt", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Cached Series") {
		t.Fatalf("cache hit required network or failed: %d %s", response.Code, response.Body.String())
	}
	if len(server.activity.Opened) != 1 || server.activity.Opened[0].ID != 42 {
		t.Fatal("successful series view was not recorded")
	}
}
