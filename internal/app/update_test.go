package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type updateRoundTrip func(*http.Request) (*http.Response, error)

func (f updateRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSemanticVersionOrdering(t *testing.T) {
	for _, test := range []struct{ older, newer string }{
		{"v1.9.9", "v1.10.0"},
		{"1.2.3-alpha.2", "1.2.3-alpha.10"},
		{"1.2.3-rc.1", "1.2.3"},
		{"1.2.3+build.1", "1.2.4"},
	} {
		older, err := parseVersion(test.older)
		if err != nil {
			t.Fatal(err)
		}
		newer, err := parseVersion(test.newer)
		if err != nil {
			t.Fatal(err)
		}
		if compareVersions(older, newer) >= 0 || compareVersions(newer, older) <= 0 {
			t.Errorf("wrong ordering: %s < %s", test.older, test.newer)
		}
	}
	left, _ := parseVersion("v1.2.3+one")
	right, _ := parseVersion("1.2.3+two")
	if compareVersions(left, right) != 0 {
		t.Fatal("build metadata changed precedence")
	}
	for _, invalid := range []string{"dev", "1.02.3", "1.2", "1.2.3-01"} {
		if _, err := parseVersion(invalid); err == nil {
			t.Errorf("accepted invalid version %q", invalid)
		}
	}
}

func TestUpdateCheckCachesAndHandlesFailures(t *testing.T) {
	for _, test := range []struct {
		name    string
		version string
		body    string
		status  int
		want    bool
		wantErr bool
	}{
		{"newer", "v1.9.0", `{"tag_name":"v1.10.0","html_url":"https://github.com/example/project/releases/tag/v1.10.0"}`, 200, true, false},
		{"same", "v1.10.0", `{"tag_name":"v1.10.0","html_url":"https://github.com/example/project/releases/tag/v1.10.0"}`, 200, false, false},
		{"prerelease", "v1.9.0", `{"tag_name":"v2.0.0-rc.1","html_url":"https://github.com/example/project/releases/tag/v2.0.0-rc.1","prerelease":true}`, 200, false, true},
		{"draft", "v1.9.0", `{"tag_name":"v2.0.0","html_url":"https://github.com/example/project/releases/tag/v2.0.0","draft":true}`, 200, false, true},
		{"unavailable", "v1.9.0", "", 503, false, true},
		{"invalid", "v1.9.0", `{`, 200, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			checker, err := newUpdateChecker(test.version, "example/project", "")
			if err != nil {
				t.Fatal(err)
			}
			checker.client = &http.Client{Transport: updateRoundTrip(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Path != "/repos/example/project/releases/latest" {
					t.Errorf("unexpected path %s", r.URL.Path)
				}
				return &http.Response{StatusCode: test.status, Body: io.NopCloser(strings.NewReader(test.body))}, nil
			})}
			request := httptest.NewRequest(http.MethodGet, "/api/update", nil)
			result, err := checker.check(request)
			if (result != nil) != test.want || (err != nil) != test.wantErr {
				t.Fatalf("result = %#v, error = %v", result, err)
			}
			if result != nil && result.URL != "https://github.com/example/project/releases/latest" {
				t.Fatalf("unexpected update link %q", result.URL)
			}
			checker.check(request)
			if calls != 1 {
				t.Fatalf("expected one API request after cached check, got %d", calls)
			}
		})
	}
}

func TestUpdateCheckRetriesAfterFailure(t *testing.T) {
	now := time.Now()
	checker, err := newUpdateChecker("v1.0.0", "example/project", "")
	if err != nil {
		t.Fatal(err)
	}
	checker.now = func() time.Time { return now }
	calls := 0
	checker.client = &http.Client{Transport: updateRoundTrip(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	request := httptest.NewRequest(http.MethodGet, "/api/update", nil)
	checker.check(request)
	now = now.Add(31 * time.Minute)
	checker.check(request)
	if calls != 2 {
		t.Fatalf("expected retry after backoff, got %d calls", calls)
	}
}

func TestManualUpdateURL(t *testing.T) {
	for _, raw := range []string{
		"https://github.com/example/project",
		"https://github.com/example/project/releases/latest",
	} {
		checker, err := newUpdateChecker("v1.0.0", "other/repo", raw)
		if err != nil {
			t.Fatal(err)
		}
		if checker.repository != "example/project" || checker.releaseURL != "https://github.com/example/project/releases/latest" {
			t.Fatalf("wrong update target for %s: %#v", raw, checker)
		}
		checker.client = &http.Client{Transport: updateRoundTrip(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "https://api.github.com/repos/example/project/releases/latest" {
				t.Errorf("wrong API target: %s", r.URL)
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"tag_name":"v1.1.0","html_url":"https://github.com/example/project/releases/tag/v1.1.0"}`))}, nil
		})}
		result, err := checker.check(httptest.NewRequest(http.MethodGet, "/api/update", nil))
		if err != nil || result == nil || result.URL != checker.releaseURL {
			t.Fatalf("update = %#v, error = %v", result, err)
		}
	}
	for _, raw := range []string{
		"http://github.com/example/project",
		"https://github.com.evil.test/example/project",
		"https://github.com/example/project/releases/tag/v1.1.0",
		"https://github.com/example/project?token=secret",
	} {
		if _, err := newUpdateChecker("v1.0.0", "", raw); err == nil {
			t.Errorf("accepted invalid update URL %q", raw)
		}
	}
}
