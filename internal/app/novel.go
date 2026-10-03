package app

import (
	"api-scraper/internal/api"
	"api-scraper/internal/client"
	"api-scraper/internal/novel"
	"errors"
	"net/http"
	"strconv"
)

func (a *app) novelChapter(w http.ResponseWriter, r *http.Request) {
	seriesID, seriesErr := strconv.ParseInt(r.URL.Query().Get("series"), 10, 64)
	episodeID, episodeErr := strconv.ParseInt(r.URL.Query().Get("episode"), 10, 64)
	if seriesErr != nil || episodeErr != nil || seriesID <= 0 || episodeID <= 0 {
		fail(w, 400, errors.New("select a novel chapter"))
		return
	}
	account := r.URL.Query().Get("account")
	header, err := headerFor(account)
	if err != nil {
		fail(w, 400, err)
		return
	}
	c := client.NewHTTPClient()
	cached, found := a.cache.get(account, seriesID)
	if !found {
		cached.Details, err = api.GetComicDetails(c, seriesID, header)
		if err != nil {
			fail(w, 400, err)
			return
		}
	}
	if cached.Details.Type != "BOOKS" {
		fail(w, 400, errors.New("this series is not a novel"))
		return
	}
	episode, err := api.GetNovelEpisode(r.Context(), c, seriesID, episodeID, header)
	if err != nil {
		fail(w, 400, err)
		return
	}
	if episode.ID != episodeID || (!episode.Free && !episode.Unlocked) {
		fail(w, 403, errors.New("this chapter is locked or unavailable"))
		return
	}
	if len(episode.Contents) == 0 || len(episode.Contents) > 8 {
		fail(w, 400, errors.New("novel chapter has no supported content"))
		return
	}
	pages := make([]string, 0, len(episode.Contents))
	for _, part := range episode.Contents {
		content, err := novel.Fetch(r.Context(), part.FileURL)
		if err != nil {
			fail(w, 502, err)
			return
		}
		pages = append(pages, string(content))
	}
	w.Header().Set("Cache-Control", "no-store")
	send(w, map[string]any{"id": episode.ID, "title": episode.Title, "scene": episode.Scene, "pages": pages})
}
