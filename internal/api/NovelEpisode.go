package api

import (
	"api-scraper/internal/client"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

type NovelEpisode struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Scene    int64  `json:"scene"`
	Free     bool   `json:"free"`
	Unlocked bool   `json:"unlocked"`
	Contents []struct {
		FileURL string `json:"file_url"`
	} `json:"contents"`
}

func GetNovelEpisode(ctx context.Context, c *client.HTTPClient, seriesID, episodeID int64, header http.Header) (NovelEpisode, error) {
	endpoint := "https://api.tapas.io/v4/series/" + strconv.FormatInt(seriesID, 10) + "/episodes/" + strconv.FormatInt(episodeID, 10) + "?ics=true"
	response, err := c.GetContext(ctx, endpoint, header)
	if err != nil {
		return NovelEpisode{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return NovelEpisode{}, fmt.Errorf("novel episode: %s", response.Status)
	}
	var episode NovelEpisode
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&episode); err != nil {
		return NovelEpisode{}, err
	}
	return episode, nil
}
