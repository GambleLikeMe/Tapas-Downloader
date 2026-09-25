package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestBrowserOpensOnceAfterHealthIsReady(t *testing.T) {
	var ready atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" || !ready.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	opened := make(chan string, 2)
	go openWhenReady(server.URL, func(url string) error { opened <- url; return nil })
	select {
	case <-opened:
		t.Fatal("browser opened before server was healthy")
	case <-time.After(150 * time.Millisecond):
	}
	ready.Store(true)
	select {
	case url := <-opened:
		if url != server.URL {
			t.Fatalf("wrong URL: %s", url)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("browser did not open after health became ready")
	}
	select {
	case <-opened:
		t.Fatal("browser opened more than once")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestBrowserStaysClosedByDefaultInWSL(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("WSL runs on Linux")
	}
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	t.Setenv("OPEN_BROWSER", "")
	if shouldOpenBrowser() {
		t.Fatal("WSL should not auto-open a browser")
	}
	t.Setenv("OPEN_BROWSER", "true")
	if !shouldOpenBrowser() {
		t.Fatal("explicit browser opt-in should work")
	}
	t.Setenv("OPEN_BROWSER", "false")
	if shouldOpenBrowser() {
		t.Fatal("explicit browser opt-out should work")
	}
}

func TestPortableDataMigration(t *testing.T) {
	root := t.TempDir()
	legacy := filepath.Join(t.TempDir(), "Tapas Downloader")
	if err := os.MkdirAll(filepath.Join(legacy, "accounts"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"settings.json": `{"downloadDir":"saved"}`, "cache.json": "cached"} {
		if err := os.WriteFile(filepath.Join(legacy, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(legacy, "accounts", "reader.txt"), []byte("account data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(legacy, "unrelated"), 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "data")
	if err := initializePortableData(target, legacy); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"settings.json": `{"downloadDir":"saved"}`, "cache.json": "cached", filepath.Join("accounts", "reader.txt"): "account data"} {
		data, err := os.ReadFile(filepath.Join(target, name))
		if err != nil || string(data) != content {
			t.Fatalf("migration of %s: %q, %v", name, data, err)
		}
	}
	if _, err := os.Stat(filepath.Join(target, "unrelated")); !os.IsNotExist(err) {
		t.Fatal("unrelated legacy files were migrated")
	}
	if err := os.WriteFile(filepath.Join(target, "settings.json"), []byte("newer"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := initializePortableData(target, legacy); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(target, "settings.json"))
	if err != nil || string(data) != "newer" {
		t.Fatal("existing portable settings were overwritten")
	}
}

func TestApplicationRootAndDefaultDownloadFolder(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "TapasDownloader")
	if got := applicationRoot(executable, t.TempDir()); got != root {
		t.Fatalf("binary root = %q", got)
	}
	devExe := filepath.Join(root, "go-build123", "b001", "exe", "app")
	if got := applicationRoot(devExe, root); got != root {
		t.Fatalf("go run root = %q", got)
	}
	macExe := filepath.Join(root, "Tapas Downloader.app", "Contents", "MacOS", "TapasDownloader")
	if got := applicationRoot(macExe, root); got != root {
		t.Fatalf("app bundle root = %q", got)
	}
	t.Setenv("TAPAS_DOWNLOAD_DIR", "")
	if got, err := defaultDownloadRoot(root); err != nil || got != filepath.Join(root, "downloads") {
		t.Fatalf("default downloads = %q, %v", got, err)
	}
}
