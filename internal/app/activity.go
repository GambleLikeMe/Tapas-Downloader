package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"
)

const maxRecentSearches = 8
const maxRecentSeries = 12

type recentSeries struct {
	ID       int64     `json:"id"`
	Title    string    `json:"title"`
	OpenedAt time.Time `json:"openedAt"`
}

type activityState struct {
	Searches []string       `json:"searches"`
	Opened   []recentSeries `json:"opened"`
}

func (a *app) loadActivity() error {
	a.activity = activityState{Searches: []string{}, Opened: []recentSeries{}}
	data, err := os.ReadFile(a.activityPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &a.activity)
}

func (a *app) recordSearch(query string) error {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	previous := a.activity.Searches
	searches := []string{query}
	for _, existing := range previous {
		if !strings.EqualFold(existing, query) && len(searches) < maxRecentSearches {
			searches = append(searches, existing)
		}
	}
	a.activity.Searches = searches
	if err := saveJSONAtomic(a.activityPath, a.activity); err != nil {
		a.activity.Searches = previous
		return err
	}
	return nil
}

func (a *app) recordOpened(id int64, title string) error {
	if id <= 0 || strings.TrimSpace(title) == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	previous := a.activity.Opened
	opened := []recentSeries{{ID: id, Title: title, OpenedAt: time.Now().UTC()}}
	for _, existing := range previous {
		if existing.ID != id && len(opened) < maxRecentSeries {
			opened = append(opened, existing)
		}
	}
	a.activity.Opened = opened
	if err := saveJSONAtomic(a.activityPath, a.activity); err != nil {
		a.activity.Opened = previous
		return err
	}
	return nil
}

func (a *app) clearActivity(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	if kind != "searches" && kind != "opened" {
		fail(w, 400, errors.New("choose recent searches or opened series"))
		return
	}
	a.mu.Lock()
	previous := a.activity
	if kind == "searches" {
		a.activity.Searches = []string{}
	} else {
		a.activity.Opened = []recentSeries{}
	}
	err := saveJSONAtomic(a.activityPath, a.activity)
	if err != nil {
		a.activity = previous
	}
	a.mu.Unlock()
	if err != nil {
		fail(w, 500, err)
		return
	}
	send(w, map[string]bool{"ok": true})
}

func (a *app) removeRecentSearch(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Query string `json:"query"`
	}
	if err := decode(r, &input); err != nil {
		fail(w, 400, err)
		return
	}
	a.mu.Lock()
	previous := a.activity.Searches
	searches := []string{}
	for _, query := range previous {
		if query != input.Query {
			searches = append(searches, query)
		}
	}
	a.activity.Searches = searches
	err := saveJSONAtomic(a.activityPath, a.activity)
	if err != nil {
		a.activity.Searches = previous
	}
	a.mu.Unlock()
	if err != nil {
		fail(w, 500, err)
		return
	}
	send(w, map[string]bool{"ok": true})
}
