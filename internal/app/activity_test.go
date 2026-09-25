package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecentSearchesDeduplicateAndStayBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.json")
	server := &app{activityPath: path}
	if err := server.loadActivity(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxRecentSearches+3; i++ {
		if err := server.recordSearch(strings.Repeat("x", i+1)); err != nil {
			t.Fatal(err)
		}
	}
	if err := server.recordSearch("  XX  "); err != nil {
		t.Fatal(err)
	}
	if len(server.activity.Searches) != maxRecentSearches || server.activity.Searches[0] != "XX" {
		t.Fatalf("recent searches are not bounded or reordered: %v", server.activity.Searches)
	}
	for _, query := range server.activity.Searches[1:] {
		if strings.EqualFold(query, "xx") {
			t.Fatal("repeated search was duplicated")
		}
	}
	if err := server.recordSearch("   "); err != nil || len(server.activity.Searches) != maxRecentSearches {
		t.Fatal("blank search entered history")
	}
	reloaded := &app{activityPath: path}
	if err := reloaded.loadActivity(); err != nil {
		t.Fatal(err)
	}
	if reloaded.activity.Searches[0] != "XX" {
		t.Fatal("search history did not persist")
	}
}

func TestRecentlyOpenedOnlyRecordsViewsAndCanBeCleared(t *testing.T) {
	root := t.TempDir()
	server := &app{activityPath: filepath.Join(root, "history.json")}
	if err := server.loadActivity(); err != nil {
		t.Fatal(err)
	}
	for i := int64(1); i <= maxRecentSeries+2; i++ {
		if err := server.recordOpened(i, "Series"); err != nil {
			t.Fatal(err)
		}
	}
	if err := server.recordOpened(3, "Series 3"); err != nil {
		t.Fatal(err)
	}
	if len(server.activity.Opened) != maxRecentSeries || server.activity.Opened[0].ID != 3 {
		t.Fatalf("recent series order/limit: %+v", server.activity.Opened)
	}
	count := 0
	for _, series := range server.activity.Opened {
		if series.ID == 3 {
			count++
		}
	}
	if count != 1 {
		t.Fatal("opened series was duplicated")
	}
	if err := server.recordSearch("Keep search"); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/history/opened/clear", strings.NewReader(`{}`))
	request.SetPathValue("kind", "opened")
	response := httptest.NewRecorder()
	server.clearActivity(response, request)
	if response.Code != http.StatusOK || len(server.activity.Opened) != 0 || len(server.activity.Searches) != 1 {
		t.Fatal("clearing recent series failed")
	}
	if _, err := os.Stat(server.activityPath); err != nil {
		t.Fatal("history clear was not persisted")
	}
}
