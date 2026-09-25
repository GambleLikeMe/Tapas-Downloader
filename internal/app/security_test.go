package app

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestLocalSessionRequiredForAPI(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/session", sessionHandler("test-secret"))
	mux.HandleFunc("GET /api/private", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := localOnly(mux, "test-secret")
	protected := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787/api/private", nil)
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, protected)
	if denied.Code != http.StatusUnauthorized {
		t.Fatalf("missing session: %d", denied.Code)
	}
	login := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/api/session", strings.NewReader(`{"token":"test-secret"}`))
	login.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, login)
	if response.Code != http.StatusOK || len(response.Result().Cookies()) != 1 {
		t.Fatalf("session handshake: %d", response.Code)
	}
	authorized := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787/api/private", nil)
	authorized.AddCookie(response.Result().Cookies()[0])
	accepted := httptest.NewRecorder()
	handler.ServeHTTP(accepted, authorized)
	if accepted.Code != http.StatusNoContent {
		t.Fatalf("valid session: %d", accepted.Code)
	}
}

func TestLibraryIncludesConfiguredFolderAndRejectsSymlink(t *testing.T) {
	defaultRoot := t.TempDir()
	configuredRoot := t.TempDir()
	series := filepath.Join(configuredRoot, "sample")
	if err := os.MkdirAll(series, 0755); err != nil {
		t.Fatal(err)
	}
	book := filepath.Join(series, "sample.pdf")
	if err := os.WriteFile(book, []byte("pdf"), 0644); err != nil {
		t.Fatal(err)
	}
	a := &app{root: defaultRoot, config: settings{DownloadDir: configuredRoot}}
	library, err := a.scanLibrary()
	if err != nil || len(library) != 1 || len(library[0].Files) != 1 || library[0].Files[0].Path != book {
		t.Fatalf("library: %+v, %v", library, err)
	}
	if _, err := a.safeLibraryPath(book); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.pdf")
	if err := os.WriteFile(outside, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(series, "link.pdf")
	if err := os.Symlink(outside, link); err != nil {
		if runtime.GOOS == "windows" && errors.Is(err, syscall.Errno(1314)) {
			t.Skip("Windows symlink privilege is unavailable")
		}
		t.Fatal(err)
	}
	if _, err := a.safeLibraryPath(link); err == nil {
		t.Fatal("symlink escaped the library root")
	}
}

func TestSavedSearchesConcurrent(t *testing.T) {
	t.Chdir(t.TempDir())
	a := &app{}
	var workers sync.WaitGroup
	for i := 0; i < 20; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			body := fmt.Sprintf(`{"query":"series-%d"}`, i)
			response := httptest.NewRecorder()
			a.addSaved(response, httptest.NewRequest(http.MethodPost, "/api/saved", strings.NewReader(body)))
			if response.Code != http.StatusOK {
				t.Errorf("save %d returned %d", i, response.Code)
			}
		}(i)
	}
	workers.Wait()
	entries, err := saved()
	if err != nil || len(entries) != 20 {
		t.Fatalf("saved %d searches: %v", len(entries), err)
	}
}

func TestTaskMutationRollsBackWhenSaveFails(t *testing.T) {
	a := &app{statePath: filepath.Join(t.TempDir(), "missing", "queue.json"), tasks: []task{{ID: "one", State: "queued"}}}
	if err := a.updateTask("one", true, func(item *task) { item.State = "downloading" }); err == nil {
		t.Fatal("expected save failure")
	}
	if a.tasks[0].State != "queued" {
		t.Fatalf("state changed despite save failure: %s", a.tasks[0].State)
	}
}

func TestStateReturnsAfterAccountChange(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("accounts", 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("accounts", "account.txt"), []byte("reader@example.test\nsecret"), 0600); err != nil {
		t.Fatal(err)
	}
	a := &app{}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		a.state(response, httptest.NewRequest(http.MethodGet, "/api/state", nil))
		done <- response
	}()
	select {
	case response := <-done:
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "reader@example.test") {
			t.Fatalf("state response: %d %s", response.Code, response.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("state handler deadlocked")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func TestConnectAccountThenReadState(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("accounts", 0700); err != nil {
		t.Fatal(err)
	}
	original := http.DefaultTransport
	http.DefaultTransport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://api.tapas.io/auth/login" {
			t.Errorf("unexpected login URL: %s", request.URL)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-User-Id": {"user"}, "X-Auth-Token": {"token"}}, Body: io.NopCloser(strings.NewReader("{}")), Request: request}, nil
	})
	defer func() { http.DefaultTransport = original }()
	a := &app{}
	added := httptest.NewRecorder()
	a.addAccount(added, httptest.NewRequest(http.MethodPost, "/api/accounts", strings.NewReader(`{"email":"reader@example.test","password":"secret"}`)))
	if added.Code != http.StatusOK {
		t.Fatalf("add account: %d %s", added.Code, added.Body.String())
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		response := httptest.NewRecorder()
		a.state(response, httptest.NewRequest(http.MethodGet, "/api/state", nil))
		done <- response
	}()
	select {
	case response := <-done:
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "reader@example.test") {
			t.Fatalf("state after add: %d %s", response.Code, response.Body.String())
		}
	case <-time.After(time.Second):
		t.Fatal("state request blocked after adding account")
	}
}
