package app

import (
	"context"
	"strings"
	"testing"
)

func TestSeriesIDFromPage(t *testing.T) {
	page := []byte(`<meta name="twitter:app:url:googleplay" content="tapastic://series/111423/info"><a href="/series/999999">Related</a>`)
	id, err := seriesIDFromPage(page)
	if err != nil || id != 111423 {
		t.Fatalf("series ID = %d, %v", id, err)
	}
	if _, err := seriesIDFromPage([]byte(`<a href="tapastic://series/111423/info">Related</a>`)); err == nil {
		t.Fatal("accepted an ID outside the series meta tag")
	}
}

func TestSeriesQueryRejectsOtherHosts(t *testing.T) {
	for _, query := range []string{
		"https://example.com/series/name/info",
		"https://tapas.io.example.com/series/name/info",
		"https://tapas.io:444/series/name/info",
		"https://tapas.io/series/name/episodes",
	} {
		if _, _, err := seriesQuery(context.Background(), query); err == nil {
			t.Errorf("accepted %s", query)
		}
	}
	text, id, err := seriesQuery(context.Background(), "name of a series")
	if err != nil || id != 0 || !strings.Contains(text, "name of a series") {
		t.Fatalf("title query = %q, %d, %v", text, id, err)
	}
}
