package client

import (
	"net/http"
	"net/url"
	"testing"
)

func TestAllowedImageURL(t *testing.T) {
	cases := []struct {
		raw     string
		allowed bool
	}{
		{"https://us-a.tapas.io/image", true},
		{"https://d30womf5coomej.cloudfront.net/image", true},
		{"http://us-a.tapas.io/image", false},
		{"https://127.0.0.1/image", false},
		{"https://tapas.io.evil.example/image", false},
		{"https://user@us-a.tapas.io/image", false},
	}
	for _, tc := range cases {
		u, err := url.Parse(tc.raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := AllowedImageURL(u); got != tc.allowed {
			t.Errorf("%s: got %v", tc.raw, got)
		}
	}
}

func TestRedirectPolicies(t *testing.T) {
	apiClient := NewHTTPClient().Client
	for _, raw := range []string{"https://other.example/image", "http://api.tapas.io/image"} {
		req, err := http.NewRequest(http.MethodGet, raw, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := apiClient.CheckRedirect(req, []*http.Request{{}}); err == nil {
			t.Errorf("API accepted %s", raw)
		}
		if err := CheckImageRedirect(req, []*http.Request{{}}); err == nil {
			t.Errorf("image accepted %s", raw)
		}
	}
}
