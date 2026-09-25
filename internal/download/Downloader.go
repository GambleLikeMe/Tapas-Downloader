package download

import (
	"api-scraper/internal/client"
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/image/webp"
)

const maxEpisodeBytes = 8 << 20
const maxEpisodeImages = 1000
const maxImageBytes = 30 << 20
const maxImagePixels = 50_000_000

type episode struct {
	Title    string `json:"title"`
	Contents []struct {
		URL string `json:"file_url"`
	} `json:"contents"`
}

var invalidName = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1f]`)

func SafeName(name string) string {
	name = strings.TrimSpace(invalidName.ReplaceAllString(name, ""))
	name = strings.TrimRight(name, ". ")
	if len(name) > 180 {
		var short strings.Builder
		for _, r := range name {
			if short.Len()+utf8.RuneLen(r) > 180 {
				break
			}
			short.WriteRune(r)
		}
		name = strings.TrimRight(short.String(), ". ")
	}
	if name == "" || name == "." || name == ".." {
		return "Untitled"
	}
	stem := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || (len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9') {
		name = "_" + name
	}
	return name
}
func Slugify(name string) string {
	return strings.ReplaceAll(strings.ToLower(SafeName(name)), " ", "-")
}

func DownloadComic(ctx context.Context, c *client.HTTPClient, seriesID, episodeID int64, seriesDir string, header http.Header, progress func(done, total int)) (string, int, error) {
	resp, err := c.GetContext(ctx, "https://api.tapas.io/v4/series/"+strconv.FormatInt(seriesID, 10)+"/episodes/"+strconv.FormatInt(episodeID, 10), header)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("episode details: %s", resp.Status)
	}
	var item episode
	if err = json.NewDecoder(io.LimitReader(resp.Body, maxEpisodeBytes+1)).Decode(&item); err != nil {
		return "", 0, err
	}
	if len(item.Contents) > maxEpisodeImages {
		return "", 0, errors.New("episode has too many images")
	}
	if len(item.Contents) == 0 {
		return "", 0, fmt.Errorf("episode %d has no images", episodeID)
	}
	title := SafeName(item.Title)
	if !strings.Contains(strings.ToLower(title), "episode") {
		title = "Episode " + title
	}
	folder := filepath.Join(seriesDir, fmt.Sprintf("%s [%d]", title, episodeID))
	if err = os.MkdirAll(seriesDir, 0755); err != nil {
		return "", 0, err
	}
	pending, err := os.MkdirTemp(seriesDir, ".episode-*")
	if err != nil {
		return "", 0, err
	}
	defer os.RemoveAll(pending)
	if progress != nil {
		progress(0, len(item.Contents))
	}
	for i, img := range item.Contents {
		if img.URL == "" {
			return "", i, fmt.Errorf("image %d has no URL", i+1)
		}
		parsed, err := url.Parse(img.URL)
		if err != nil || !client.AllowedImageURL(parsed) {
			return "", i, fmt.Errorf("image %d has an unsupported URL", i+1)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, img.URL, nil)
		if err != nil {
			return "", i, err
		}
		req.Header.Set("Referer", "https://tapas.io/")
		imageClient := *c.Client
		imageClient.CheckRedirect = client.CheckImageRedirect
		res, err := imageClient.Do(req)
		if err != nil {
			return "", i, fmt.Errorf("image %d: %w", i+1, err)
		}
		if res.StatusCode != http.StatusOK {
			res.Body.Close()
			return "", i, fmt.Errorf("image %d: %s", i+1, res.Status)
		}
		reader := bufio.NewReader(io.LimitReader(res.Body, maxImageBytes+1))
		header, peekErr := reader.Peek(512)
		if peekErr != nil && peekErr != io.EOF {
			res.Body.Close()
			return "", i, peekErr
		}
		media := http.DetectContentType(header)
		if len(header) >= 12 && string(header[:4]) == "RIFF" && string(header[8:12]) == "WEBP" {
			media = "image/webp"
		}
		ext := map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".png"}[media]
		if ext == "" {
			res.Body.Close()
			return "", i, fmt.Errorf("image %d has unsupported format %s", i+1, media)
		}
		out, err := os.Create(filepath.Join(pending, fmt.Sprintf("%04d%s", i, ext)))
		if err != nil {
			res.Body.Close()
			return "", i, err
		}
		if media == "image/webp" {
			data, readErr := io.ReadAll(reader)
			if readErr != nil {
				err = readErr
			} else if len(data) > maxImageBytes {
				err = errors.New("image is too large")
			} else {
				config, configErr := webp.DecodeConfig(bytes.NewReader(data))
				if configErr != nil {
					err = configErr
				} else if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maxImagePixels {
					err = errors.New("image dimensions are unsupported")
				} else {
					decoded, decodeErr := webp.Decode(bytes.NewReader(data))
					if decodeErr != nil {
						err = decodeErr
					} else {
						err = png.Encode(out, decoded)
					}
				}
			}
		} else {
			written, copyErr := io.Copy(out, reader)
			if copyErr != nil {
				err = copyErr
			} else if written > maxImageBytes {
				err = errors.New("image is too large")
			}
		}
		closeErr := out.Close()
		res.Body.Close()
		if err != nil {
			return "", i, fmt.Errorf("image %d: %w", i+1, err)
		}
		if closeErr != nil {
			return "", i, closeErr
		}
		if progress != nil {
			progress(i+1, len(item.Contents))
		}
	}
	if err := ctx.Err(); err != nil {
		return "", len(item.Contents), err
	}
	backup := ""
	if _, err := os.Stat(folder); err == nil {
		backup, err = os.MkdirTemp(seriesDir, ".backup-*")
		if err != nil {
			return "", 0, err
		}
		os.Remove(backup)
		if err = os.Rename(folder, backup); err != nil {
			return "", 0, err
		}
	}
	if err = os.Rename(pending, folder); err != nil {
		if backup != "" {
			os.Rename(backup, folder)
		}
		return "", 0, err
	}
	if backup != "" {
		os.RemoveAll(backup)
	}
	return folder, len(item.Contents), nil
}
