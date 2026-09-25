package api

import (
	"api-scraper/internal/client"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

type ComicDetails struct {
	ID           int64  `json:"id"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Type         string `json:"type"`
	EpisodeCount int    `json:"episode_cnt"`
	Thumb        struct {
		FileURL string `json:"file_url"`
	} `json:"thumb"`
	BookCoverURL string `json:"book_cover_url"`
	HumanURL     string `json:"human_url"`
	Genre        struct {
		Name string `json:"name"`
	} `json:"genre"`
	Tags     []json.RawMessage `json:"tags"`
	Creators []Creator         `json:"creators"`
}

func GetComicDetails(c *client.HTTPClient, id int64, header http.Header) (ComicDetails, error) {
	resp, err := c.Get("https://api.tapas.io/v3/series/"+strconv.FormatInt(id, 10), header)
	if err != nil {
		return ComicDetails{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ComicDetails{}, fmt.Errorf("series details: %s", resp.Status)
	}
	var item ComicDetails
	if err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&item); err != nil {
		return ComicDetails{}, err
	}
	return item, nil
}
