package app

import (
	"api-scraper/internal/api"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed web/*
var webFiles embed.FS

type account struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}
type app struct {
	mu           sync.Mutex
	tasks        []task
	worker       bool
	cancel       context.CancelFunc
	root         string
	statePath    string
	configPath   string
	config       settings
	cache        *seriesCache
	activityPath string
	activity     activityState
	debug        debugState
}

func Run(version string) {
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println(version)
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "--smoke-test" {
		for _, name := range []string{"web/index.html", "web/app.js", "web/style.css"} {
			if _, err := webFiles.ReadFile(name); err != nil {
				log.Fatal(err)
			}
		}
		fmt.Println("Embedded web assets OK")
		return
	}
	portableBase, err := prepareDataDir()
	if err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll("accounts", 0700); err != nil {
		log.Fatal(err)
	}
	root, err := defaultDownloadRoot(portableBase)
	if err != nil {
		log.Fatal(err)
	}
	if os.Getenv("TAPAS_DOWNLOAD_DIR") == "" {
		if err := os.MkdirAll(root, 0755); err != nil {
			log.Fatal(err)
		}
	}
	cache, err := loadSeriesCache("cache.json")
	if err != nil {
		log.Fatal(err)
	}
	a := &app{root: root, statePath: "queue.json", configPath: "settings.json", activityPath: "history.json", cache: cache, config: settings{DownloadDir: root, DefaultFormat: "pdf", FilenameTemplate: defaultFilenameTemplate}}
	if err := a.loadActivity(); err != nil {
		log.Fatal(err)
	}
	if err := a.loadSettings(); err != nil {
		log.Fatal(err)
	}
	a.debug.setEnabled(a.config.DebugEnabled)
	if dataDir, err := os.Getwd(); err == nil {
		a.logEvent("INFO", "App data: "+dataDir)
	}
	a.logEvent("INFO", "Download folder: "+a.config.DownloadDir)
	if err := a.loadTasks(); err != nil {
		log.Fatal(err)
	}
	var sessionKey [32]byte
	if _, err := rand.Read(sessionKey[:]); err != nil {
		log.Fatal(err)
	}
	sessionToken := hex.EncodeToString(sessionKey[:])
	mux := http.NewServeMux()
	static, err := fs.Sub(webFiles, "web")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("GET /", http.FileServer(http.FS(static)))
	mux.HandleFunc("POST /api/session", sessionHandler(sessionToken))
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("GET /api/state", a.state)
	mux.HandleFunc("POST /api/accounts", a.addAccount)
	mux.HandleFunc("POST /api/accounts/delete", a.deleteAccount)
	mux.HandleFunc("POST /api/saved", a.addSaved)
	mux.HandleFunc("POST /api/saved/delete", a.deleteSaved)
	mux.HandleFunc("GET /api/search", a.search)
	mux.HandleFunc("GET /api/series", a.series)
	mux.HandleFunc("GET /api/cache", a.cacheInfo)
	mux.HandleFunc("POST /api/cache/clear", a.clearCache)
	mux.HandleFunc("POST /api/history/{kind}/clear", a.clearActivity)
	mux.HandleFunc("POST /api/history/searches/remove", a.removeRecentSearch)
	mux.HandleFunc("GET /api/tasks", a.listTasks)
	mux.HandleFunc("POST /api/tasks", a.enqueue)
	mux.HandleFunc("POST /api/tasks/cancel", a.cancelTask)
	mux.HandleFunc("POST /api/tasks/retry", a.retryTask)
	mux.HandleFunc("POST /api/tasks/remove", a.removeTask)
	mux.HandleFunc("POST /api/tasks/open", a.openTask)
	mux.HandleFunc("POST /api/tasks/open-file", a.openFileTask)
	mux.HandleFunc("GET /api/tasks/file", a.taskFile)
	mux.HandleFunc("GET /api/library", a.library)
	mux.HandleFunc("POST /api/export", a.export)
	mux.HandleFunc("GET /api/file", a.file)
	mux.HandleFunc("GET /api/debug", a.getDebug)
	mux.HandleFunc("POST /api/debug", a.setDebug)
	mux.HandleFunc("POST /api/debug/clear", a.clearDebug)
	mux.HandleFunc("POST /api/folder/pick", a.pickDownloadFolder)
	mux.HandleFunc("GET /api/settings", a.getSettings)
	mux.HandleFunc("POST /api/settings", a.setSettings)
	mux.HandleFunc("GET /api/filename-preview", a.filenamePreview)
	port := os.Getenv("PORT")
	if port == "" {
		port = "8787"
	}
	server := &http.Server{Addr: "127.0.0.1:" + port, Handler: localOnly(a.debugMiddleware(mux), sessionToken), ReadHeaderTimeout: 10 * time.Second}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		log.Fatal(err)
	}
	url := "http://" + listener.Addr().String()
	launchURL := url + "/#token=" + sessionToken
	go a.work()
	if shouldOpenBrowser() {
		go openWhenReady(url, func(_ string) error { return openBrowser(launchURL) })
	}
	a.logEvent("INFO", "Server listening on "+url)
	fmt.Printf("Open this local link: %s\n", launchURL)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	go func() {
		<-stop
		a.logEvent("INFO", "Server stopping")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			a.logEvent("ERROR", "Server shutdown failed: "+err.Error())
		}
	}()
	if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	a.logEvent("INFO", "Server stopped")
}

func sessionHandler(sessionToken string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Token string `json:"token"`
		}
		if err := decode(r, &input); err != nil || subtle.ConstantTimeCompare([]byte(input.Token), []byte(sessionToken)) != 1 {
			http.Error(w, "Invalid session link", http.StatusUnauthorized)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "tapas_session", Value: sessionToken, Path: "/api", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		send(w, map[string]bool{"ok": true})
	}
}

func localOnly(next http.Handler, sessionToken string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' https: data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		host, _, err := strings.Cut(r.Host, ":")
		if !err || (host != "localhost" && host != "127.0.0.1") {
			http.Error(w, "Local access only", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost {
			origin := r.Header.Get("Origin")
			if origin != "" {
				u, err := url.Parse(origin)
				if err != nil || u.Host != r.Host {
					http.Error(w, "Invalid origin", http.StatusForbidden)
					return
				}
			}
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				http.Error(w, "JSON required", http.StatusUnsupportedMediaType)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/health" && r.URL.Path != "/api/session" {
			cookie, err := r.Cookie("tapas_session")
			if err != nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(sessionToken)) != 1 {
				http.Error(w, "Open the current link printed by the app", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func send(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(value)
}
func fail(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	message := err.Error()
	if status >= 500 {
		log.Printf("request failed: %v", err)
		message = "The request could not be completed"
	}
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
func decode(r *http.Request, dst any) error {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	return d.Decode(dst)
}

func accounts() ([]account, error) {
	entries, err := os.ReadDir("accounts")
	if err != nil {
		return nil, err
	}
	list := make([]account, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join("accounts", entry.Name()))
		if err != nil {
			return nil, err
		}
		lines := strings.SplitN(string(data), "\n", 2)
		if len(lines) == 2 {
			list = append(list, account{entry.Name(), strings.TrimSpace(lines[0])})
		}
	}
	return list, nil
}
func credentials(id string) (string, string, error) {
	if id == "" || filepath.Base(id) != id || strings.HasPrefix(id, ".") {
		return "", "", errors.New("invalid account")
	}
	data, err := os.ReadFile(filepath.Join("accounts", id))
	if err != nil {
		return "", "", err
	}
	lines := strings.SplitN(string(data), "\n", 2)
	if len(lines) != 2 || strings.TrimSpace(lines[0]) == "" || strings.TrimSpace(lines[1]) == "" {
		return "", "", errors.New("account file needs email and password on separate lines")
	}
	return strings.TrimSpace(lines[0]), strings.TrimSpace(lines[1]), nil
}
func headerFor(id string) (http.Header, error) {
	email, password, err := credentials(id)
	if err != nil {
		return nil, err
	}
	user, token, err := api.Login(email, password)
	if err != nil {
		return nil, err
	}
	return api.BuildHeader(user, token)
}
func saved() ([]string, error) {
	data, err := os.ReadFile("urls.txt")
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	values := []string{}
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			values = append(values, line)
		}
	}
	return values, nil
}
func saveSaved(values []string) error {
	tmp, err := os.CreateTemp(".", ".saved-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.WriteString(strings.Join(values, "\n") + "\n"); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), "urls.txt")
}
func (a *app) state(w http.ResponseWriter, r *http.Request) {
	acc, err := accounts()
	if err != nil {
		fail(w, 500, err)
		return
	}
	values, err := saved()
	if err != nil {
		fail(w, 500, err)
		return
	}
	a.mu.Lock()
	searches := append([]string{}, a.activity.Searches...)
	opened := append([]recentSeries{}, a.activity.Opened...)
	a.mu.Unlock()
	send(w, map[string]any{"accounts": acc, "saved": values, "recentSearches": searches, "recentSeries": opened})
}
func (a *app) addAccount(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decode(r, &input); err != nil {
		fail(w, 400, err)
		return
	}
	input.Email = strings.TrimSpace(input.Email)
	if input.Email == "" || input.Password == "" || strings.ContainsAny(input.Email, "\r\n") || strings.ContainsAny(input.Password, "\r\n") {
		fail(w, 400, errors.New("enter an email and password without line breaks"))
		return
	}
	a.logEvent("INFO", "Connecting account to Tapas")
	if _, _, err := api.Login(input.Email, input.Password); err != nil {
		a.logEvent("WARN", "Tapas login failed: "+err.Error())
		fail(w, 400, err)
		return
	}
	sum := sha256.Sum256([]byte(strings.ToLower(input.Email)))
	id := hex.EncodeToString(sum[:8]) + ".txt"
	if err := os.WriteFile(filepath.Join("accounts", id), []byte(input.Email+"\n"+input.Password), 0600); err != nil {
		a.logEvent("ERROR", "Could not save account file: "+err.Error())
		fail(w, 500, err)
		return
	}
	a.logEvent("INFO", "Account connected and saved")
	send(w, account{id, input.Email})
}
func (a *app) deleteAccount(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID string `json:"id"`
	}
	if err := decode(r, &input); err != nil {
		fail(w, 400, err)
		return
	}
	if _, _, err := credentials(input.ID); err != nil {
		fail(w, 400, err)
		return
	}
	if err := os.Remove(filepath.Join("accounts", input.ID)); err != nil {
		fail(w, 500, err)
		return
	}
	if err := a.cache.removeAccount(input.ID); err != nil {
		log.Printf("clear account cache: %v", err)
	}
	send(w, map[string]bool{"ok": true})
}
func (a *app) addSaved(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Query string `json:"query"`
	}
	if err := decode(r, &input); err != nil {
		fail(w, 400, err)
		return
	}
	input.Query = strings.TrimSpace(input.Query)
	if input.Query == "" || strings.ContainsAny(input.Query, "\r\n") {
		fail(w, 400, errors.New("enter a series title or URL"))
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	values, err := saved()
	if err != nil {
		fail(w, 500, err)
		return
	}
	for _, v := range values {
		if v == input.Query {
			send(w, map[string]bool{"ok": true})
			return
		}
	}
	values = append(values, input.Query)
	if err := saveSaved(values); err != nil {
		fail(w, 500, err)
		return
	}
	send(w, map[string]bool{"ok": true})
}
func (a *app) deleteSaved(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Query string `json:"query"`
	}
	if err := decode(r, &input); err != nil {
		fail(w, 400, err)
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	values, err := saved()
	if err != nil {
		fail(w, 500, err)
		return
	}
	result := []string{}
	for _, v := range values {
		if v != input.Query {
			result = append(result, v)
		}
	}
	if err := saveSaved(result); err != nil {
		fail(w, 500, err)
		return
	}
	send(w, map[string]bool{"ok": true})
}
