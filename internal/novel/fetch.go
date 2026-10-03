package novel

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxEncryptedChapterBytes = 8 << 20

func allowedChapterURL(value *url.URL) bool {
	return value != nil && value.Scheme == "https" && value.Hostname() == "d30womf5coomej.cloudfront.net" && value.Port() == "" && value.User == nil && strings.HasPrefix(value.Path, "/r/r.tapas.io/")
}

func Fetch(ctx context.Context, sourceURL string) ([]byte, error) {
	parsed, err := url.Parse(sourceURL)
	if err != nil || !allowedChapterURL(parsed) {
		return nil, errors.New("novel chapter has an unsupported content URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, errors.New("novel chapter URL is invalid")
	}
	request.Header.Set("User-Agent", "okhttp/4.11.0")
	client := &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(next *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !allowedChapterURL(next.URL) {
			return errors.New("novel chapter redirected outside its content host")
		}
		return nil
	}}
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("novel chapter content is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errors.New("novel chapter content is unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxEncryptedChapterBytes+1))
	if err != nil || len(body) > maxEncryptedChapterBytes {
		return nil, errors.New("novel chapter content is too large or unreadable")
	}
	return Decrypt(sourceURL, body)
}
