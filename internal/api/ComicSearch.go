package api

import (
	"api-scraper/internal/client"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

type Creator struct {
	DisplayName string `json:"display_name"`
}

type ComicSummary struct {
	ID          int64     `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Type        string    `json:"type"`
	ThumbURL    string    `json:"thumb_url"`
	Creators    []Creator `json:"creators"`
}

func SearchComics(c *client.HTTPClient, query string, header http.Header) ([]ComicSummary, error) {
	return searchSeries(c, "comics", "COMICS", query, header)
}

func SearchBooks(c *client.HTTPClient, query string, header http.Header) ([]ComicSummary, error) {
	return searchSeries(c, "books", "BOOKS", query, header)
}

func searchSeries(c *client.HTTPClient, path, kind, query string, header http.Header) ([]ComicSummary, error) {
	endpoint := "https://api.tapas.io/v3/search/" + path + "?q=" + url.QueryEscape(query) + "&page=1"
	resp, err := c.Get(endpoint, header)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		label := "comic"
		if path == "books" {
			label = "novel"
		}
		return nil, fmt.Errorf("%s search: %s", label, resp.Status)
	}
	var result struct {
		Result []struct {
			Series ComicSummary `json:"series"`
		} `json:"result"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&result); err != nil {
		return nil, err
	}
	comics := make([]ComicSummary, 0, len(result.Result))
	for _, item := range result.Result {
		if item.Series.ID > 0 && item.Series.Type == kind {
			comics = append(comics, item.Series)
		}
	}
	return comics, nil
}
