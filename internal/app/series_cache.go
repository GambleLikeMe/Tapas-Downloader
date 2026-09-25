package app

import (
	"api-scraper/internal/api"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const seriesCacheTTL = 5 * time.Minute
const maxCachedSeries = 24

type cachedSeries struct {
	Details   api.ComicDetails `json:"details"`
	Episodes  []api.Episode    `json:"episodes"`
	UpdatedAt time.Time        `json:"updatedAt"`
}

type seriesCache struct {
	mu      sync.Mutex
	path    string
	entries map[string]cachedSeries
	now     func() time.Time
}

func loadSeriesCache(path string) (*seriesCache, error) {
	cache := &seriesCache{path: path, entries: map[string]cachedSeries{}, now: time.Now}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cache, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &cache.entries); err != nil {
		if removeErr := os.Remove(path); removeErr != nil {
			return nil, removeErr
		}
		cache.entries = map[string]cachedSeries{}
		return cache, nil
	}
	cache.prune()
	return cache, nil
}

func cacheKey(account string, id int64) string { return fmt.Sprintf("%s:%d", account, id) }

func (cache *seriesCache) prune() {
	for key, entry := range cache.entries {
		if cache.now().Sub(entry.UpdatedAt) >= seriesCacheTTL {
			delete(cache.entries, key)
		}
	}
	if len(cache.entries) <= maxCachedSeries {
		return
	}
	keys := make([]string, 0, len(cache.entries))
	for key := range cache.entries {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return cache.entries[keys[i]].UpdatedAt.Before(cache.entries[keys[j]].UpdatedAt) })
	for _, key := range keys[:len(keys)-maxCachedSeries] {
		delete(cache.entries, key)
	}
}

func (cache *seriesCache) get(account string, id int64) (cachedSeries, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	entry, ok := cache.entries[cacheKey(account, id)]
	if !ok || cache.now().Sub(entry.UpdatedAt) >= seriesCacheTTL {
		return cachedSeries{}, false
	}
	return entry, true
}

func (cache *seriesCache) set(account string, id int64, details api.ComicDetails, episodes []api.Episode) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	minimal := make([]api.Episode, len(episodes))
	copy(minimal, episodes)
	for i := range minimal {
		minimal[i].Thumb.FileURL = ""
	}
	cache.entries[cacheKey(account, id)] = cachedSeries{Details: details, Episodes: minimal, UpdatedAt: cache.now()}
	cache.prune()
	return saveJSONAtomic(cache.path, cache.entries)
}

func (cache *seriesCache) invalidate(account string, id int64) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	delete(cache.entries, cacheKey(account, id))
	return saveJSONAtomic(cache.path, cache.entries)
}

func (cache *seriesCache) removeAccount(account string) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	for key := range cache.entries {
		if strings.HasPrefix(key, account+":") {
			delete(cache.entries, key)
		}
	}
	return saveJSONAtomic(cache.path, cache.entries)
}

func (cache *seriesCache) clear() error {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if err := os.Remove(cache.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	cache.entries = map[string]cachedSeries{}
	return nil
}

func (cache *seriesCache) stats() (int, int64) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	count := 0
	for _, entry := range cache.entries {
		if cache.now().Sub(entry.UpdatedAt) < seriesCacheTTL {
			count++
		}
	}
	info, err := os.Stat(cache.path)
	if err != nil {
		return count, 0
	}
	return count, info.Size()
}

func (a *app) cacheInfo(w http.ResponseWriter, r *http.Request) {
	entries, bytes := a.cache.stats()
	send(w, map[string]any{"entries": entries, "bytes": bytes, "ttlMinutes": int(seriesCacheTTL.Minutes())})
}

func (a *app) clearCache(w http.ResponseWriter, r *http.Request) {
	if err := a.cache.clear(); err != nil {
		fail(w, 500, err)
		return
	}
	send(w, map[string]bool{"ok": true})
}
