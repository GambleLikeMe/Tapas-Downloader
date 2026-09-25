package app

import (
	"api-scraper/internal/api"
	"api-scraper/internal/client"
	download "api-scraper/internal/download"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type settings struct {
	DownloadDir      string `json:"downloadDir"`
	DefaultFormat    string `json:"defaultFormat"`
	FilenameTemplate string `json:"filenameTemplate"`
	DebugEnabled     bool   `json:"debugEnabled"`
}

func (a *app) loadSettings() error {
	data, err := os.ReadFile(a.configPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &a.config); err != nil {
		return err
	}
	if !filepath.IsAbs(a.config.DownloadDir) {
		return errors.New("saved download directory must be an absolute path")
	}
	if !validFormat(a.config.DefaultFormat) {
		a.config.DefaultFormat = "pdf"
	}
	if a.config.FilenameTemplate == "" {
		a.config.FilenameTemplate = defaultFilenameTemplate
	}
	return validateFilenameTemplate(a.config.FilenameTemplate)
}
func (a *app) getSettings(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	current := a.config
	a.mu.Unlock()
	send(w, current)
}
func (a *app) setSettings(w http.ResponseWriter, r *http.Request) {
	var input settings
	if err := decode(r, &input); err != nil {
		fail(w, 400, err)
		return
	}
	if !validFormat(input.DefaultFormat) {
		fail(w, 400, errors.New("choose PDF, EPUB, or Images"))
		return
	}
	var err error
	input.DownloadDir, err = validateDownloadDir(input.DownloadDir)
	if err != nil {
		fail(w, 400, err)
		return
	}
	if err := validateFilenameTemplate(input.FilenameTemplate); err != nil {
		fail(w, 400, err)
		return
	}
	a.mu.Lock()
	input.DebugEnabled = a.config.DebugEnabled
	err = saveJSONAtomic(a.configPath, input)
	if err == nil {
		a.config = input
	}
	a.mu.Unlock()
	if err != nil {
		fail(w, 500, err)
		return
	}
	send(w, input)
}
func validFormat(format string) bool { return format == "pdf" || format == "epub" || format == "raw" }

var tapasMetaTag = regexp.MustCompile(`(?i)<meta\s+[^>]*>`)
var tapasSeriesID = regexp.MustCompile(`tapastic://series/([0-9]+)/info`)

func seriesIDFromPage(page []byte) (int64, error) {
	for _, tag := range tapasMetaTag.FindAll(page, -1) {
		if !strings.Contains(strings.ToLower(string(tag)), "twitter:app:url:googleplay") {
			continue
		}
		if match := tapasSeriesID.FindSubmatch(tag); match != nil {
			return strconv.ParseInt(string(match[1]), 10, 64)
		}
	}
	return 0, errors.New("could not find the series ID on the Tapas page")
}

func tapasHost(host string) bool {
	return host == "tapas.io" || host == "www.tapas.io"
}

func seriesQuery(ctx context.Context, query string) (string, int64, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", 0, errors.New("enter a series title or Tapas URL")
	}
	if id, err := strconv.ParseInt(query, 10, 64); err == nil && id > 0 {
		return "", id, nil
	}
	if u, err := url.Parse(query); err == nil && u.Host != "" {
		if (u.Scheme != "https" && u.Scheme != "http") || !tapasHost(strings.ToLower(u.Hostname())) || u.User != nil || u.Port() != "" {
			return "", 0, errors.New("only Tapas series URLs are supported")
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if (len(parts) != 2 && (len(parts) != 3 || parts[2] != "info")) || parts[0] != "series" || parts[1] == "" || parts[1] == "." || parts[1] == ".." {
			return "", 0, errors.New("enter a Tapas series URL")
		}
		pageURL := "https://tapas.io/series/" + url.PathEscape(parts[1]) + "/info"
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
		if err != nil {
			return "", 0, err
		}
		webClient := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) >= 5 || next.URL.Scheme != "https" || !tapasHost(strings.ToLower(next.URL.Hostname())) {
				return errors.New("Tapas redirected outside its site")
			}
			return nil
		}}
		response, err := webClient.Do(request)
		if err != nil {
			return "", 0, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return "", 0, fmt.Errorf("Tapas page returned %d", response.StatusCode)
		}
		page, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
		if err != nil {
			return "", 0, err
		}
		id, err := seriesIDFromPage(page)
		return "", id, err
	}
	return query, 0, nil
}
func (a *app) search(w http.ResponseWriter, r *http.Request) {
	query, id, err := seriesQuery(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		fail(w, 400, err)
		return
	}
	header, err := headerFor(r.URL.Query().Get("account"))
	if err != nil {
		fail(w, 400, err)
		return
	}
	c := client.NewHTTPClient()
	if id > 0 {
		details, err := api.GetComicDetails(c, id, header)
		if err != nil {
			fail(w, 400, err)
			return
		}
		if details.Type != "COMICS" {
			fail(w, 400, errors.New("this downloader currently supports comics"))
			return
		}
		if err := a.recordSearch(r.URL.Query().Get("q")); err != nil {
			log.Printf("save recent search: %v", err)
		}
		send(w, []api.ComicSummary{{ID: details.ID, Title: details.Title, Description: details.Description, Type: details.Type, ThumbURL: details.Thumb.FileURL, Creators: details.Creators}})
		return
	}
	results, err := api.SearchComics(c, query, header)
	if err != nil {
		fail(w, 400, err)
		return
	}
	if err := a.recordSearch(r.URL.Query().Get("q")); err != nil {
		log.Printf("save recent search: %v", err)
	}
	send(w, results)
}

type chapterView struct {
	api.Episode
	Access     string `json:"access"`
	Downloaded bool   `json:"downloaded"`
}

func (a *app) series(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil || id <= 0 {
		fail(w, 400, errors.New("select a series"))
		return
	}
	account := r.URL.Query().Get("account")
	if _, _, err := credentials(account); err != nil {
		fail(w, 400, err)
		return
	}
	refresh := r.URL.Query().Get("refresh") == "1"
	if refresh {
		if err := a.cache.invalidate(account, id); err != nil {
			log.Printf("invalidate series cache: %v", err)
		}
	}
	cached, found := a.cache.get(account, id)
	if found {
		w.Header().Set("X-Cache", "HIT")
	} else {
		w.Header().Set("X-Cache", "MISS")
	}
	if !found {
		header, err := headerFor(account)
		if err != nil {
			fail(w, 400, err)
			return
		}
		c := client.NewHTTPClient()
		cached.Details, err = api.GetComicDetails(c, id, header)
		if err != nil {
			fail(w, 400, err)
			return
		}
		if cached.Details.Type != "COMICS" {
			fail(w, 400, errors.New("this downloader currently supports comics"))
			return
		}
		cached.Episodes, err = api.GetComicList(c, id, header)
		if err != nil {
			fail(w, 400, err)
			return
		}
		if err := a.cache.set(account, id, cached.Details, cached.Episodes); err != nil {
			log.Printf("save series cache: %v", err)
		}
	}
	details, episodes := cached.Details, cached.Episodes
	a.mu.Lock()
	tasks := append([]task(nil), a.tasks...)
	a.mu.Unlock()
	views := make([]chapterView, 0, len(episodes))
	downloadedIDs := map[int64]bool{}
	for _, item := range tasks {
		if item.SeriesID == id && item.State == "completed" {
			downloadedIDs[item.EpisodeID] = true
		}
	}
	for _, root := range a.libraryRoots() {
		entries, _ := os.ReadDir(filepath.Join(root, download.Slugify(details.Title)))
		for _, entry := range entries {
			name := entry.Name()
			if !entry.IsDir() {
				ext := strings.ToLower(filepath.Ext(name))
				if ext != ".pdf" && ext != ".epub" {
					continue
				}
				name = strings.TrimSuffix(name, filepath.Ext(name))
			}
			start := strings.LastIndex(name, "[")
			if start < 0 || !strings.HasSuffix(name, "]") {
				continue
			}
			episodeID, err := strconv.ParseInt(name[start+1:len(name)-1], 10, 64)
			if err == nil {
				downloadedIDs[episodeID] = true
			}
		}
	}
	for _, episode := range episodes {
		access := "locked"
		if episode.Free {
			access = "free"
		}
		if episode.Unlocked {
			access = "unlocked"
		}
		downloaded := downloadedIDs[episode.ID]
		views = append(views, chapterView{Episode: episode, Access: access, Downloaded: downloaded})
	}
	if !refresh {
		if err := a.recordOpened(details.ID, details.Title); err != nil {
			log.Printf("save recently opened series: %v", err)
		}
	}
	send(w, map[string]any{"id": details.ID, "title": details.Title, "description": details.Description, "type": details.Type, "thumbUrl": details.Thumb.FileURL, "creators": details.Creators, "episodes": views})
}

func safePath(root, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) {
		return "", errors.New("invalid path")
	}
	target := filepath.Join(root, relative)
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedTarget)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", errors.New("path is outside downloads")
	}
	return resolvedTarget, nil
}
func serveDownload(w http.ResponseWriter, r *http.Request, target string) {
	ext := strings.ToLower(filepath.Ext(target))
	if ext != ".pdf" && ext != ".epub" {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	name := filepath.Base(target)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s", "download"+ext, strings.ReplaceAll(url.QueryEscape(name), "+", "%20")))
	http.ServeFile(w, r, target)
}
func (a *app) file(w http.ResponseWriter, r *http.Request) {
	target, err := a.safeLibraryPath(r.URL.Query().Get("path"))
	if err != nil {
		fail(w, 400, errors.New("downloaded file is unavailable"))
		return
	}
	serveDownload(w, r, target)
}
