package app

import (
	"api-scraper/internal/api"
	"api-scraper/internal/client"
	download "api-scraper/internal/download"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type task struct {
	ID               string    `json:"id"`
	Account          string    `json:"account"`
	SeriesID         int64     `json:"seriesId"`
	EpisodeID        int64     `json:"episodeId"`
	Scene            int64     `json:"scene"`
	SeriesTitle      string    `json:"seriesTitle"`
	EpisodeTitle     string    `json:"episodeTitle"`
	Creator          string    `json:"creator"`
	Description      string    `json:"description,omitempty"`
	CoverURL         string    `json:"coverUrl,omitempty"`
	FallbackCoverURL string    `json:"fallbackCoverUrl,omitempty"`
	Source           string    `json:"source,omitempty"`
	Keywords         []string  `json:"keywords,omitempty"`
	Format           string    `json:"format"`
	FilenameTemplate string    `json:"filenameTemplate"`
	Directory        string    `json:"directory"`
	Free             bool      `json:"free"`
	Unlocked         bool      `json:"unlocked"`
	State            string    `json:"state"`
	Message          string    `json:"message"`
	ImagesDone       int       `json:"imagesDone"`
	ImagesTotal      int       `json:"imagesTotal"`
	Result           string    `json:"result"`
	AddedAt          time.Time `json:"addedAt"`
}

func saveJSONAtomic(path string, value any) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err = json.NewEncoder(tmp).Encode(value); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
func (a *app) loadTasks() error {
	data, err := os.ReadFile(a.statePath)
	if os.IsNotExist(err) {
		a.tasks = []task{}
		return nil
	}
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &a.tasks); err != nil {
		return err
	}
	changed := false
	for i := range a.tasks {
		if a.tasks[i].State == "queued" && !a.tasks[i].Free && !a.tasks[i].Unlocked {
			a.tasks[i].State = "failed"
			a.tasks[i].Message = "Chapter is locked or unavailable"
			changed = true
		}
		switch a.tasks[i].State {
		case "preparing", "downloading", "converting", "canceling":
			a.tasks[i].State = "failed"
			a.tasks[i].Message = "Interrupted by app restart"
			changed = true
		}
	}
	if changed || strings.Contains(string(data), `"unlock":`) {
		return saveJSONAtomic(a.statePath, a.tasks)
	}
	return nil
}
func (a *app) listTasks(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	current := append([]task{}, a.tasks...)
	a.mu.Unlock()
	send(w, current)
}
func newTaskID() (string, error) {
	var id [8]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(id[:]), nil
}
func (a *app) enqueue(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Account    string  `json:"account"`
		SeriesID   int64   `json:"seriesId"`
		EpisodeIDs []int64 `json:"episodeIds"`
		Format     string  `json:"format"`
		Directory  string  `json:"directory"`
	}
	if err := decode(r, &input); err != nil {
		fail(w, 400, err)
		return
	}
	if input.SeriesID <= 0 || len(input.EpisodeIDs) == 0 || len(input.EpisodeIDs) > 500 || !validFormat(input.Format) {
		fail(w, 400, errors.New("select 1–500 chapters and an output format"))
		return
	}
	a.mu.Lock()
	defaultDir := a.config.DownloadDir
	filenameTemplate := a.config.FilenameTemplate
	a.mu.Unlock()
	directory := strings.TrimSpace(input.Directory)
	if directory == "" {
		directory = defaultDir
	}
	directory, err := validateDownloadDir(directory)
	if err != nil {
		fail(w, 400, err)
		return
	}
	header, err := headerFor(input.Account)
	if err != nil {
		fail(w, 400, err)
		return
	}
	c := client.NewHTTPClient()
	details, err := api.GetComicDetails(c, input.SeriesID, header)
	if err != nil {
		fail(w, 400, err)
		return
	}
	if details.Type != "COMICS" {
		fail(w, 400, errors.New("this downloader currently supports comics"))
		return
	}
	episodes, err := api.GetComicList(c, input.SeriesID, header)
	if err != nil {
		fail(w, 400, err)
		return
	}
	byID := make(map[int64]api.Episode, len(episodes))
	for _, ep := range episodes {
		byID[ep.ID] = ep
	}
	seriesBook := bookFromDetails(details)
	selected := make([]task, 0, len(input.EpisodeIDs))
	seen := map[int64]bool{}
	for _, id := range input.EpisodeIDs {
		ep, exists := byID[id]
		if !exists || seen[id] {
			fail(w, 400, fmt.Errorf("chapter %d is not in this series or was selected twice", id))
			return
		}
		seen[id] = true
		if !ep.Free && !ep.Unlocked {
			fail(w, 400, fmt.Errorf("chapter %d is locked or unavailable", ep.Scene))
			return
		}
		taskID, err := newTaskID()
		if err != nil {
			fail(w, 500, err)
			return
		}
		selected = append(selected, task{ID: taskID, Account: input.Account, SeriesID: details.ID, EpisodeID: ep.ID, Scene: ep.Scene, SeriesTitle: details.Title, EpisodeTitle: ep.Title, Creator: seriesBook.Creator, Description: seriesBook.Description, CoverURL: seriesBook.CoverURL, FallbackCoverURL: seriesBook.FallbackCoverURL, Source: seriesBook.Source, Keywords: seriesBook.Keywords, Format: input.Format, FilenameTemplate: filenameTemplate, Directory: directory, Free: ep.Free, Unlocked: ep.Unlocked, State: "queued", Message: "Waiting", AddedAt: time.Now()})
	}
	a.mu.Lock()
	for _, candidate := range selected {
		for _, existing := range a.tasks {
			if candidate.Account == existing.Account && candidate.SeriesID == existing.SeriesID && candidate.EpisodeID == existing.EpisodeID && candidate.Format == existing.Format && candidate.Directory == existing.Directory && (existing.State == "queued" || existing.State == "preparing" || existing.State == "downloading" || existing.State == "converting" || existing.State == "canceling") {
				a.mu.Unlock()
				fail(w, 409, fmt.Errorf("chapter %d is already queued", candidate.Scene))
				return
			}
		}
	}
	start := len(a.tasks)
	a.tasks = append(a.tasks, selected...)
	err = saveJSONAtomic(a.statePath, a.tasks)
	if err != nil {
		a.tasks = a.tasks[:start]
	}
	a.mu.Unlock()
	if err != nil {
		fail(w, 500, err)
		return
	}
	go a.work()
	send(w, map[string]any{"queued": len(selected)})
}
func (a *app) taskIndex(id string) int {
	for i := range a.tasks {
		if a.tasks[i].ID == id {
			return i
		}
	}
	return -1
}
func (a *app) updateTask(id string, persist bool, change func(*task)) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	index := a.taskIndex(id)
	if index < 0 {
		return errors.New("download not found")
	}
	previous := a.tasks[index]
	change(&a.tasks[index])
	if persist {
		if err := saveJSONAtomic(a.statePath, a.tasks); err != nil {
			a.tasks[index] = previous
			return err
		}
	}
	return nil
}
func (a *app) work() {
	a.mu.Lock()
	if a.worker {
		a.mu.Unlock()
		return
	}
	a.worker = true
	a.mu.Unlock()
	for {
		a.mu.Lock()
		index := -1
		for i := range a.tasks {
			if a.tasks[i].State == "queued" {
				index = i
				break
			}
		}
		if index < 0 {
			a.worker = false
			a.mu.Unlock()
			return
		}
		current := a.tasks[index]
		ctx, cancel := context.WithCancel(context.Background())
		a.cancel = cancel
		a.tasks[index].State = "preparing"
		a.tasks[index].Message = "Connecting to Tapas"
		if err := saveJSONAtomic(a.statePath, a.tasks); err != nil {
			a.tasks[index].State = "failed"
			a.tasks[index].Message = err.Error()
			a.cancel = nil
			cancel()
			a.mu.Unlock()
			continue
		}
		a.mu.Unlock()
		a.logEvent("INFO", fmt.Sprintf("Download %s started (%s)", current.ID, current.Format))
		result, err := a.runTask(ctx, current)
		canceled := ctx.Err() != nil
		cancel()
		a.mu.Lock()
		a.cancel = nil
		index = a.taskIndex(current.ID)
		if index >= 0 {
			item := &a.tasks[index]
			switch {
			case canceled:
				item.State = "canceled"
				item.Message = "Canceled"
			case err != nil:
				item.State = "failed"
				item.Message = err.Error()
			default:
				item.State = "completed"
				item.Message = "Completed"
				item.Result = result
			}
			if saveErr := saveJSONAtomic(a.statePath, a.tasks); saveErr != nil {
				item.State = "failed"
				item.Message = "Could not save download status: " + saveErr.Error()
			}
		}
		a.mu.Unlock()
		switch {
		case canceled:
			a.logEvent("INFO", "Download "+current.ID+" canceled")
		case err != nil:
			a.logEvent("ERROR", "Download "+current.ID+" failed: "+err.Error())
		default:
			a.logEvent("INFO", "Download "+current.ID+" completed")
		}
	}
}
func existingChapter(seriesDir string, episodeID int64) (string, int) {
	entries, err := os.ReadDir(seriesDir)
	if err != nil {
		return "", 0
	}
	suffix := fmt.Sprintf("[%d]", episodeID)
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		folder := filepath.Join(seriesDir, entry.Name())
		if _, err := os.Stat(filepath.Join(folder, ".scene")); err != nil {
			continue
		}
		complete, err := os.ReadFile(filepath.Join(folder, ".complete"))
		if err != nil {
			continue
		}
		expected, err := strconv.Atoi(strings.TrimSpace(string(complete)))
		if err != nil || expected < 1 {
			continue
		}
		images, err := imagePaths(folder)
		if err == nil && len(images) == expected {
			return folder, len(images)
		}
	}
	return "", 0
}
func (a *app) runTask(ctx context.Context, item task) (string, error) {
	if _, err := validateDownloadDir(item.Directory); err != nil {
		return "", err
	}
	if !item.Free && !item.Unlocked {
		return "", errors.New("chapter is locked or unavailable")
	}
	seriesDir := filepath.Join(item.Directory, download.Slugify(item.SeriesTitle))
	workDir := seriesDir
	if item.Format != "raw" {
		staging, err := os.MkdirTemp("", "tapas-chapter-*")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(staging)
		workDir = staging
	} else {
		if err := os.MkdirAll(seriesDir, 0755); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(seriesDir, ".title"), []byte(item.SeriesTitle), 0644); err != nil {
			return "", err
		}
	}
	folder, count := "", 0
	if item.Format == "raw" {
		folder, count = existingChapter(seriesDir, item.EpisodeID)
	}
	if folder == "" {
		header, err := headerFor(item.Account)
		if err != nil {
			return "", err
		}
		if err = ctx.Err(); err != nil {
			return "", err
		}
		c := client.NewHTTPClient()
		if err := a.updateTask(item.ID, true, func(t *task) { t.State = "downloading"; t.Message = "Downloading images" }); err != nil {
			return "", err
		}
		folder, count, err = download.DownloadComic(ctx, c, item.SeriesID, item.EpisodeID, workDir, header, func(done, total int) {
			a.updateTask(item.ID, false, func(t *task) { t.ImagesDone = done; t.ImagesTotal = total })
		})
		if err != nil {
			return "", err
		}
	} else {
		a.updateTask(item.ID, false, func(t *task) { t.ImagesDone = count; t.ImagesTotal = count; t.Message = "Using downloaded images" })
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if item.Format == "raw" {
		if err := os.WriteFile(filepath.Join(folder, ".scene"), []byte(strconv.FormatInt(item.Scene, 10)), 0644); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(folder, ".complete"), []byte(strconv.Itoa(count)), 0644); err != nil {
			return "", err
		}
		seriesBook := item.bookMetadata()
		seriesBook.Title = item.SeriesTitle
		seriesBook.EpisodeID = 0
		if err := saveJSONAtomic(filepath.Join(seriesDir, ".series.json"), seriesBook); err != nil {
			log.Printf("Could not save optional series metadata: %v", err)
		}
		return folder, nil
	}
	a.logEvent("INFO", fmt.Sprintf("Export %s started (%s)", item.ID, item.Format))
	if err := a.updateTask(item.ID, true, func(t *task) { t.State = "converting"; t.Message = "Converting to " + strings.ToUpper(item.Format) }); err != nil {
		return "", err
	}
	images, err := imagePaths(folder)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(seriesDir, 0755); err != nil {
		return "", err
	}
	return exportFile(ctx, seriesDir, filepath.Base(folder), item.Format, []chapter{{Name: item.EpisodeTitle, Images: images}}, exportMetadata{Book: item.bookMetadata(), Template: item.FilenameTemplate, Filename: filenameData{SeriesName: item.SeriesTitle, SeriesID: item.SeriesID, ChapterNumber: item.Scene, ChapterID: item.EpisodeID, ChapterTitle: item.EpisodeTitle, ChapterName: filepath.Base(folder), CreatorName: item.Creator}})
}
func (a *app) taskAction(w http.ResponseWriter, r *http.Request, action string) {
	var input struct {
		ID string `json:"id"`
	}
	if err := decode(r, &input); err != nil {
		fail(w, 400, err)
		return
	}
	a.mu.Lock()
	index := a.taskIndex(input.ID)
	if index < 0 {
		a.mu.Unlock()
		fail(w, 404, errors.New("download not found"))
		return
	}
	previous := append([]task(nil), a.tasks...)
	item := &a.tasks[index]
	shouldCancel := false
	switch action {
	case "cancel":
		if item.State == "queued" {
			item.State = "canceled"
			item.Message = "Canceled"
		} else if item.State == "preparing" || item.State == "downloading" || item.State == "converting" {
			item.State = "canceling"
			item.Message = "Canceling"
			shouldCancel = true
		} else {
			a.mu.Unlock()
			fail(w, 409, errors.New("this download is not active"))
			return
		}
	case "retry":
		if item.State != "failed" && item.State != "canceled" {
			a.mu.Unlock()
			fail(w, 409, errors.New("only failed or canceled downloads can be retried"))
			return
		}
		item.State = "queued"
		item.Message = "Waiting"
		item.ImagesDone = 0
		item.ImagesTotal = 0
		item.Result = ""
	case "remove":
		if item.State != "completed" && item.State != "failed" && item.State != "canceled" {
			a.mu.Unlock()
			fail(w, 409, errors.New("cancel the download before removing it"))
			return
		}
		a.tasks = append(a.tasks[:index], a.tasks[index+1:]...)
	}
	err := saveJSONAtomic(a.statePath, a.tasks)
	if err != nil {
		a.tasks = previous
	} else if shouldCancel && a.cancel != nil {
		a.cancel()
	}
	a.mu.Unlock()
	if err != nil {
		fail(w, 500, err)
		return
	}
	if action == "retry" {
		go a.work()
	}
	send(w, map[string]bool{"ok": true})
}
func (a *app) cancelTask(w http.ResponseWriter, r *http.Request) { a.taskAction(w, r, "cancel") }
func (a *app) retryTask(w http.ResponseWriter, r *http.Request)  { a.taskAction(w, r, "retry") }
func (a *app) removeTask(w http.ResponseWriter, r *http.Request) { a.taskAction(w, r, "remove") }
func (a *app) completedTask(id string) (task, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	index := a.taskIndex(id)
	if index < 0 || a.tasks[index].State != "completed" {
		return task{}, errors.New("completed download not found")
	}
	return a.tasks[index], nil
}
func (a *app) taskFile(w http.ResponseWriter, r *http.Request) {
	item, err := a.completedTask(r.URL.Query().Get("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	serveDownload(w, r, item.Result)
}
func (a *app) openCompleted(w http.ResponseWriter, r *http.Request, file bool) {
	var input struct {
		ID string `json:"id"`
	}
	if err := decode(r, &input); err != nil {
		fail(w, 400, err)
		return
	}
	item, err := a.completedTask(input.ID)
	if err != nil {
		fail(w, 404, err)
		return
	}
	if file && item.Format == "raw" {
		fail(w, 400, errors.New("raw images are a folder"))
		return
	}
	target := item.Result
	if !file && item.Format != "raw" {
		target = filepath.Dir(target)
	}
	info, err := os.Stat(target)
	if err != nil || (file && info.IsDir()) || (!file && !info.IsDir()) {
		fail(w, 404, errors.New("downloaded file or folder no longer exists"))
		return
	}
	if err = launchLocal(target); err != nil {
		fail(w, 500, err)
		return
	}
	send(w, map[string]bool{"ok": true})
}
func (a *app) openTask(w http.ResponseWriter, r *http.Request)     { a.openCompleted(w, r, false) }
func (a *app) openFileTask(w http.ResponseWriter, r *http.Request) { a.openCompleted(w, r, true) }
