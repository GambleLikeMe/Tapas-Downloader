package api

import (
	"api-scraper/internal/client"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNovelEpisodePaginationMatchesReference(t *testing.T) {
	pages := 0
	c := &client.HTTPClient{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		pages++
		if r.URL.Path != "/v3/series/55122/episodes-pagination" || r.URL.Query().Get("max_limit") != "20" || r.URL.Query().Get("desc") != "false" || r.URL.Query().Get("page") != strconv.Itoa(pages) {
			t.Errorf("unexpected novel request: %s", r.URL.Redacted())
		}
		body := `{"pagination":{"has_next":true},"episodes":[{"id":11,"scene":1,"title":"First","free":true}]}`
		if pages == 2 {
			body = `{"pagination":{"has_next":false},"episodes":[{"id":12,"scene":2,"title":"Second","free":false}]}`
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	episodes, err := GetNovelList(c, 55122, http.Header{})
	if err != nil || len(episodes) != 2 || episodes[0].Scene != 1 || episodes[1].Scene != 2 || pages != 2 {
		t.Fatalf("episodes = %#v, pages = %d, error = %v", episodes, pages, err)
	}
}

func TestNovelDetailsAcceptNullableMetadata(t *testing.T) {
	c := &client.HTTPClient{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"id":55122,"title":"Novel","type":"BOOKS","description":null,"book_cover_url":"","completed":false,"genre":{"name":"Fantasy"},"tags":["magic","adventure"],"creators":[{"display_name":"Author"}]}`
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	details, err := GetComicDetails(c, 55122, http.Header{})
	if err != nil || details.Type != "BOOKS" || details.Description != "" || details.Genre.Name != "Fantasy" || len(details.Tags) != 2 {
		t.Fatalf("details = %#v, error = %v", details, err)
	}
}

func TestNovelSearchMatchesReferenceRoute(t *testing.T) {
	c := &client.HTTPClient{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v3/search/books" || r.URL.Query().Get("q") != "a novel" || r.URL.Query().Get("page") != "1" {
			t.Errorf("unexpected novel search route: %s", r.URL.Redacted())
		}
		body := `{"result":[{"series":{"id":5,"title":"Novel","type":"BOOKS","thumb_url":"https://tapas.io/cover"}}]}`
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	results, err := SearchBooks(c, "a novel", http.Header{})
	if err != nil || len(results) != 1 || results[0].Type != "BOOKS" {
		t.Fatalf("novel search = %#v, error = %v", results, err)
	}
}

func TestNovelEpisodeRequestMatchesReference(t *testing.T) {
	c := &client.HTTPClient{Client: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v4/series/55122/episodes/571162" || r.URL.Query().Get("ics") != "true" {
			t.Errorf("unexpected novel episode route: %s", r.URL.Redacted())
		}
		body := `{"id":571162,"title":"First","scene":1,"free":true,"contents":[{"file_url":"https://example.test/chapter"}]}`
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(body))}, nil
	})}}
	episode, err := GetNovelEpisode(context.Background(), c, 55122, 571162, http.Header{})
	if err != nil || episode.ID != 571162 || len(episode.Contents) != 1 {
		t.Fatalf("novel episode = %#v, error = %v", episode, err)
	}
}
