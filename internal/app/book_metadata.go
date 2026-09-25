package app

import (
	"api-scraper/internal/api"
	safeclient "api-scraper/internal/client"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Book metadata contains only public series information. Empty fields are omitted.
type bookMetadata struct {
	Title            string   `json:"title,omitempty"`
	Creator          string   `json:"creator,omitempty"`
	SeriesTitle      string   `json:"seriesTitle,omitempty"`
	Description      string   `json:"description,omitempty"`
	Language         string   `json:"language,omitempty"`
	Source           string   `json:"source,omitempty"`
	Publisher        string   `json:"publisher,omitempty"`
	Date             string   `json:"date,omitempty"`
	Keywords         []string `json:"keywords,omitempty"`
	CoverURL         string   `json:"coverUrl,omitempty"`
	FallbackCoverURL string   `json:"fallbackCoverUrl,omitempty"`
	SeriesID         int64    `json:"seriesId,omitempty"`
	EpisodeID        int64    `json:"episodeId,omitempty"`
}

type coverImage struct {
	Data      []byte
	MediaType string
	Extension string
}

func readCover(data []byte) (*coverImage, error) {
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 30_000_000 {
		return nil, errors.New("cover dimensions are unsupported")
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return nil, err
	}
	switch format {
	case "jpeg":
		return &coverImage{Data: data, MediaType: "image/jpeg", Extension: ".jpg"}, nil
	case "png":
		return &coverImage{Data: data, MediaType: "image/png", Extension: ".png"}, nil
	case "gif":
		return &coverImage{Data: data, MediaType: "image/gif", Extension: ".gif"}, nil
	default:
		return nil, fmt.Errorf("unsupported cover image: %s", format)
	}
}

func fetchCover(ctx context.Context, rawURL string, client *http.Client) (*coverImage, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || !safeclient.AllowedImageURL(parsed) {
		return nil, errors.New("cover URL must use an approved HTTPS host")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	checked := *client
	checked.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 4 {
			return errors.New("too many cover redirects")
		}
		return safeclient.CheckImageRedirect(req, via)
	}
	response, err := checked.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cover request: %s", response.Status)
	}
	const maxCoverBytes = 8 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, maxCoverBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxCoverBytes {
		return nil, errors.New("cover image is too large")
	}
	return readCover(data)
}

func (m bookMetadata) epubLanguage() string {
	if strings.TrimSpace(m.Language) == "" {
		return "und"
	}
	return strings.TrimSpace(m.Language)
}

func bookFromDetails(details api.ComicDetails) bookMetadata {
	creators := make([]string, 0, len(details.Creators))
	for _, creator := range details.Creators {
		if creator.DisplayName != "" {
			creators = append(creators, creator.DisplayName)
		}
	}
	coverURL, fallbackCoverURL := details.BookCoverURL, details.Thumb.FileURL
	if coverURL == "" {
		coverURL, fallbackCoverURL = fallbackCoverURL, ""
	}
	source := "https://tapas.io"
	if details.HumanURL != "" && !strings.ContainsAny(details.HumanURL, "/?#") {
		source += "/series/" + url.PathEscape(details.HumanURL)
	}
	keywords := []string{}
	seen := map[string]bool{}
	addKeyword := func(value string) {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if value != "" && !seen[key] {
			keywords = append(keywords, value)
			seen[key] = true
		}
	}
	addKeyword(details.Genre.Name)
	for _, tag := range details.Tags {
		var value string
		if json.Unmarshal(tag, &value) == nil {
			addKeyword(value)
			continue
		}
		var object struct {
			Name string `json:"name"`
			Tag  string `json:"tag"`
		}
		if json.Unmarshal(tag, &object) == nil {
			if object.Name != "" {
				addKeyword(object.Name)
			} else {
				addKeyword(object.Tag)
			}
		}
	}
	return bookMetadata{Title: details.Title, Creator: strings.Join(creators, ", "), SeriesTitle: details.Title, Description: details.Description, Source: source, Keywords: keywords, CoverURL: coverURL, FallbackCoverURL: fallbackCoverURL, SeriesID: details.ID}
}

func (item task) bookMetadata() bookMetadata {
	source := item.Source
	if source == "" {
		source = "https://tapas.io"
	}
	return bookMetadata{Title: item.SeriesTitle + " — " + item.EpisodeTitle, Creator: item.Creator, SeriesTitle: item.SeriesTitle, Description: item.Description, Source: source, Keywords: item.Keywords, CoverURL: item.CoverURL, FallbackCoverURL: item.FallbackCoverURL, SeriesID: item.SeriesID, EpisodeID: item.EpisodeID}
}
