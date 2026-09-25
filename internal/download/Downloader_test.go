package download

import (
	"api-scraper/internal/client"
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func testClient(t *testing.T) *client.HTTPClient {
	t.Helper()
	var imageData bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 200, A: 255})
	if err := png.Encode(&imageData, img); err != nil {
		t.Fatal(err)
	}
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if err := req.Context().Err(); err != nil {
			return nil, err
		}
		data := imageData.Bytes()
		media := "image/png"
		if req.URL.Host == "api.tapas.io" {
			data = []byte(`{"title":"1: First?","contents":[{"file_url":"https://us-a.tapas.io/0"},{"file_url":"https://us-a.tapas.io/1"}]}`)
			media = "application/json"
		}
		return &http.Response{StatusCode: 200, Status: "200 OK", Header: http.Header{"Content-Type": []string{media}}, Body: io.NopCloser(bytes.NewReader(data)), Request: req}, nil
	})
	return &client.HTTPClient{Client: &http.Client{Transport: transport}}
}
func TestDownloadComicSavesImagesAndProgress(t *testing.T) {
	dir := t.TempDir()
	updates := [][2]int{}
	folder, count, err := DownloadComic(context.Background(), testClient(t), 1, 2, dir, nil, func(done, total int) { updates = append(updates, [2]int{done, total}) })
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || filepath.Base(folder) != "Episode 1 First [2]" {
		t.Fatalf("unexpected result: %s, %d", folder, count)
	}
	for _, name := range []string{"0000.png", "0001.png"} {
		if _, err := os.Stat(filepath.Join(folder, name)); err != nil {
			t.Fatal(err)
		}
	}
	if len(updates) != 3 || updates[0] != [2]int{0, 2} || updates[2] != [2]int{2, 2} {
		t.Fatalf("wrong progress: %v", updates)
	}
}
func TestDownloadComicCancelLeavesNoVisibleChapter(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	_, _, err := DownloadComic(ctx, testClient(t), 1, 2, dir, nil, func(done, total int) {
		if done == 1 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) && (err == nil || !strings.Contains(err.Error(), "context canceled")) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("partial chapter became visible: %v", entries)
	}
}

func TestDownloadComicRejectsUnapprovedImageURL(t *testing.T) {
	calls := 0
	c := &client.HTTPClient{Client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		body := `{"title":"Episode 1","contents":[{"file_url":"https://127.0.0.1/private"}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: req}, nil
	})}}
	_, _, err := DownloadComic(context.Background(), c, 1, 2, t.TempDir(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported URL") {
		t.Fatalf("unexpected result: %v", err)
	}
	if calls != 1 {
		t.Fatalf("image request was made: %d calls", calls)
	}
}
