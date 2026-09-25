package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

type HTTPClient struct{ Client *http.Client }

func NewHTTPClient() *HTTPClient {
	return &HTTPClient{Client: &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || req.URL.Scheme != "https" || req.URL.Hostname() != "api.tapas.io" || req.URL.Port() != "" || req.URL.User != nil {
			return errors.New("Tapas API redirected to an unsupported host")
		}
		return nil
	}}}
}
func (c *HTTPClient) Get(url string, header http.Header) (*http.Response, error) {
	return c.GetContext(context.Background(), url, header)
}
func (c *HTTPClient) GetContext(ctx context.Context, url string, header http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header = header.Clone()
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", url, err)
	}
	return resp, nil
}
