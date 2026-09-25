package api

import (
	"api-scraper/internal/client"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

type Episode struct {
	ID           int64  `json:"id"`
	Title        string `json:"title"`
	Scene        int64  `json:"scene"`
	Free         bool   `json:"free"`
	Unlocked     bool   `json:"unlocked"`
	Downloadable bool   `json:"downloadable"`
	Thumb        struct {
		FileURL string `json:"file_url"`
	} `json:"thumb"`
}

func GetComicList(c *client.HTTPClient, id int64, header http.Header) ([]Episode, error) {
	episodes := make([]Episode, 0)
	complete := false
	for page := 1; page <= 500; page++ {
		url := "https://api.tapas.io/v3/series/" + strconv.FormatInt(id, 10) + "/episodes-pagination?page=" + strconv.Itoa(page) + "&sort=OLDEST&max_limit=20"
		resp, err := c.Get(url, header)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("episode list: %s", resp.Status)
		}
		var result struct {
			Pagination struct {
				HasNext bool `json:"has_next"`
			} `json:"pagination"`
			Episodes []Episode `json:"episodes"`
		}
		err = json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&result)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		episodes = append(episodes, result.Episodes...)
		if len(episodes) > 10000 {
			return nil, fmt.Errorf("series has too many episodes")
		}
		if len(result.Episodes) == 0 || !result.Pagination.HasNext {
			complete = true
			break
		}
	}
	if !complete || len(episodes) >= 10000 {
		return nil, fmt.Errorf("series has too many episodes")
	}
	return episodes, nil
}
