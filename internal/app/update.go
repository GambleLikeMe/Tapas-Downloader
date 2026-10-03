package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

var repositoryName = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

type updateInfo struct {
	Version string `json:"version"`
	URL     string `json:"url"`
}

type updateChecker struct {
	mu         sync.Mutex
	version    string
	repository string
	client     *http.Client
	now        func() time.Time
	nextCheck  time.Time
	latest     *updateInfo
}

func newUpdateChecker(version, repository string) *updateChecker {
	return &updateChecker{
		version:    version,
		repository: repository,
		client: &http.Client{Timeout: 8 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 || req.URL.Scheme != "https" || req.URL.Host != "api.github.com" {
				return errors.New("GitHub redirected to an unsupported host")
			}
			return nil
		}},
		now: time.Now,
	}
}

func (a *app) getUpdate(w http.ResponseWriter, r *http.Request) {
	if a.updates == nil {
		send(w, map[string]any{"currentVersion": "dev", "update": nil})
		return
	}
	latest, err := a.updates.check(r)
	if err != nil {
		a.logEvent("DEBUG", "GitHub update check: "+err.Error())
	}
	send(w, map[string]any{"currentVersion": a.updates.version, "update": latest})
}

func (u *updateChecker) check(r *http.Request) (*updateInfo, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.version == "dev" || !repositoryName.MatchString(u.repository) {
		return nil, nil
	}
	if u.now().Before(u.nextCheck) {
		return u.latest, nil
	}
	u.nextCheck = u.now().Add(30 * time.Minute)
	request, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://api.github.com/repos/"+u.repository+"/releases/latest", nil)
	if err != nil {
		return u.latest, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "TapasDownloader/"+u.version)
	response, err := u.client.Do(request)
	if err != nil {
		return u.latest, errors.New("GitHub is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return u.latest, fmt.Errorf("GitHub returned HTTP %d", response.StatusCode)
	}
	var release struct {
		TagName    string `json:"tag_name"`
		HTMLURL    string `json:"html_url"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&release); err != nil {
		return u.latest, errors.New("GitHub returned an invalid release")
	}
	current, err := parseVersion(u.version)
	if err != nil {
		return u.latest, errors.New("installed version is invalid")
	}
	available, err := parseVersion(release.TagName)
	if err != nil || release.Draft || release.Prerelease || !validReleaseURL(u.repository, release.HTMLURL) {
		return u.latest, errors.New("GitHub returned an unsupported release")
	}
	u.latest = nil
	if compareVersions(current, available) < 0 {
		u.latest = &updateInfo{Version: release.TagName, URL: release.HTMLURL}
	}
	u.nextCheck = u.now().Add(6 * time.Hour)
	return u.latest, nil
}

func validReleaseURL(repository, raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == "github.com" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && strings.HasPrefix(strings.ToLower(u.Path), strings.ToLower("/"+repository+"/releases/tag/")) && len(u.Path) > len(repository)+len("/releases/tag/")+1
}

type semanticVersion struct {
	core [3]string
	pre  []string
}

var semanticVersionPattern = regexp.MustCompile(`^v?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

func parseVersion(value string) (semanticVersion, error) {
	match := semanticVersionPattern.FindStringSubmatch(value)
	if match == nil {
		return semanticVersion{}, fmt.Errorf("invalid version")
	}
	version := semanticVersion{core: [3]string{match[1], match[2], match[3]}}
	if match[4] != "" {
		version.pre = strings.Split(match[4], ".")
		for _, part := range version.pre {
			if allDigits(part) && len(part) > 1 && part[0] == '0' {
				return semanticVersion{}, fmt.Errorf("invalid prerelease")
			}
		}
	}
	return version, nil
}

func allDigits(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return value != ""
}

func compareNumber(a, b string) int {
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

func compareVersions(a, b semanticVersion) int {
	for i := range a.core {
		if order := compareNumber(a.core[i], b.core[i]); order != 0 {
			return order
		}
	}
	if len(a.pre) == 0 && len(b.pre) != 0 {
		return 1
	}
	if len(b.pre) == 0 && len(a.pre) != 0 {
		return -1
	}
	for i := 0; i < len(a.pre) && i < len(b.pre); i++ {
		aNumber, bNumber := allDigits(a.pre[i]), allDigits(b.pre[i])
		if aNumber != bNumber {
			if aNumber {
				return -1
			}
			return 1
		}
		order := strings.Compare(a.pre[i], b.pre[i])
		if aNumber {
			order = compareNumber(a.pre[i], b.pre[i])
		}
		if order != 0 {
			return order
		}
	}
	if len(a.pre) < len(b.pre) {
		return -1
	}
	if len(a.pre) > len(b.pre) {
		return 1
	}
	return 0
}
