package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLoadTasksMarksInterruptedWorkFailed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.json")
	original := []task{{ID: "active", State: "downloading"}, {ID: "waiting", Free: true, State: "queued"}, {ID: "done", State: "completed"}}
	if err := saveJSONAtomic(path, original); err != nil {
		t.Fatal(err)
	}
	app := &app{statePath: path}
	if err := app.loadTasks(); err != nil {
		t.Fatal(err)
	}
	if app.tasks[0].State != "failed" || app.tasks[0].Message != "Interrupted by app restart" || app.tasks[1].State != "queued" || app.tasks[2].State != "completed" {
		t.Fatalf("unexpected restored tasks: %+v", app.tasks)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var persisted []task
	if err = json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted[0].State != "failed" {
		t.Fatal("interrupted state was not persisted")
	}
}
func TestCancelQueuedTask(t *testing.T) {
	app := &app{statePath: filepath.Join(t.TempDir(), "queue.json"), tasks: []task{{ID: "waiting", Free: true, State: "queued"}}}
	request := httptest.NewRequest(http.MethodPost, "/api/tasks/cancel", strings.NewReader(`{"id":"waiting"}`))
	response := httptest.NewRecorder()
	app.cancelTask(response, request)
	if response.Code != http.StatusOK || app.tasks[0].State != "canceled" {
		t.Fatalf("cancel returned %d, task=%+v", response.Code, app.tasks[0])
	}
	var persisted []task
	data, err := os.ReadFile(app.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted[0].State != "canceled" {
		t.Fatal("cancel was not persisted")
	}
}
func TestTasksEndpointReturnsEmptyArray(t *testing.T) {
	app := &app{tasks: []task{}}
	response := httptest.NewRecorder()
	app.listTasks(response, httptest.NewRequest(http.MethodGet, "/api/tasks", nil))
	if strings.TrimSpace(response.Body.String()) != "[]" {
		t.Fatalf("empty tasks response: %s", response.Body.String())
	}
}

func TestQueueProcessesMultipleExistingChapters(t *testing.T) {
	root := t.TempDir()
	source := testChapters(t)[0].Images[0]
	imageData, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	tasks := []task{}
	for i := 1; i <= 2; i++ {
		folder := filepath.Join(root, "series", "Episode "+strconv.Itoa(i)+" ["+strconv.Itoa(i)+"]")
		if err := os.MkdirAll(folder, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, "0000.png"), imageData, 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, ".scene"), []byte(strconv.Itoa(i)), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(folder, ".complete"), []byte("1"), 0644); err != nil {
			t.Fatal(err)
		}
		tasks = append(tasks, task{ID: strconv.Itoa(i), SeriesID: 1, EpisodeID: int64(i), Scene: int64(i), SeriesTitle: "Series", EpisodeTitle: "Episode " + strconv.Itoa(i), Creator: "Artist", Description: "Series summary", Format: "raw", Directory: root, Free: true, State: "queued"})
	}
	app := &app{statePath: filepath.Join(t.TempDir(), "queue.json"), tasks: tasks}
	app.work()
	for _, item := range app.tasks {
		if item.State != "completed" || item.ImagesDone != 1 || item.Result == "" {
			t.Fatalf("queue did not complete: %+v", item)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "series", ".series.json"))
	if err != nil {
		t.Fatal(err)
	}
	var book bookMetadata
	if err := json.Unmarshal(data, &book); err != nil || book.Title != "Series" || book.Creator != "Artist" || book.Description != "Series summary" {
		t.Fatalf("raw download metadata: %+v, %v", book, err)
	}
}

func TestSettingsSaveAndReload(t *testing.T) {
	dir := t.TempDir()
	server := &app{configPath: filepath.Join(dir, "settings.json"), config: settings{DownloadDir: dir, DefaultFormat: "pdf", FilenameTemplate: defaultFilenameTemplate}}
	target := filepath.Join(dir, "new downloads")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(settings{DownloadDir: target, DefaultFormat: "epub", FilenameTemplate: "{series_name} - {chapter_number}"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(string(body)))
	response := httptest.NewRecorder()
	server.setSettings(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("settings returned %d: %s", response.Code, response.Body.String())
	}
	reloaded := &app{configPath: server.configPath}
	if err := reloaded.loadSettings(); err != nil {
		t.Fatal(err)
	}
	if reloaded.config.DownloadDir != target || reloaded.config.DefaultFormat != "epub" || reloaded.config.FilenameTemplate != "{series_name} - {chapter_number}" {
		t.Fatalf("wrong settings: %+v", reloaded.config)
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatal("download folder is missing")
	}
}

func TestRetryFailedChapterFromSavedImages(t *testing.T) {
	root := t.TempDir()
	folder := filepath.Join(root, "series", "Episode 3 [3]")
	if err := os.MkdirAll(folder, 0755); err != nil {
		t.Fatal(err)
	}
	source := testChapters(t)[0].Images[0]
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "0000.png"), data, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, ".scene"), []byte("3"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, ".complete"), []byte("1"), 0644); err != nil {
		t.Fatal(err)
	}
	server := &app{statePath: filepath.Join(t.TempDir(), "queue.json"), tasks: []task{{ID: "retry", SeriesID: 1, EpisodeID: 3, Scene: 3, SeriesTitle: "Series", EpisodeTitle: "Episode 3", Format: "raw", Directory: root, Free: true, State: "failed"}}}
	response := httptest.NewRecorder()
	server.retryTask(response, httptest.NewRequest(http.MethodPost, "/api/tasks/retry", strings.NewReader(`{"id":"retry"}`)))
	if response.Code != http.StatusOK {
		t.Fatalf("retry returned %d: %s", response.Code, response.Body.String())
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		server.mu.Lock()
		current := server.tasks[0]
		server.mu.Unlock()
		if current.State == "completed" {
			if current.Result != folder {
				t.Fatalf("wrong retry result: %s", current.Result)
			}
			return
		}
		if current.State == "failed" {
			t.Fatalf("retry failed: %s", current.Message)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("retry did not complete")
}

func TestLegacyLockedJobCannotRun(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "queue.json")
	legacy, err := json.Marshal([]map[string]any{{"id": "old", "state": "queued", "format": "pdf", "directory": root, "seriesTitle": "Series", "unlock": true, "free": false, "unlocked": false}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, legacy, 0644); err != nil {
		t.Fatal(err)
	}
	server := &app{statePath: path}
	if err := server.loadTasks(); err != nil {
		t.Fatal(err)
	}
	if server.tasks[0].State != "failed" {
		t.Fatalf("legacy paid job remained runnable: %+v", server.tasks[0])
	}
	if result, err := server.runTask(context.Background(), server.tasks[0]); err == nil || result != "" {
		t.Fatalf("locked job ran: result=%q, err=%v", result, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), `"unlock":`) {
		t.Fatal("retired purchase setting remained in saved queue")
	}
	if _, err := os.Stat(filepath.Join(root, "series")); !os.IsNotExist(err) {
		t.Fatal("locked job created output files")
	}
}
