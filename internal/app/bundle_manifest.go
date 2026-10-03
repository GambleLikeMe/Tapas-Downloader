package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type bundleManifest struct {
	File       string  `json:"file"`
	EpisodeIDs []int64 `json:"episodeIds"`
}

func savedBundleIDs(seriesDir string) map[int64]bool {
	result := map[int64]bool{}
	entries, err := os.ReadDir(seriesDir)
	if err != nil {
		return result
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasPrefix(entry.Name(), ".bundle-") || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(seriesDir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || info.Size() > 1<<20 {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var manifest bundleManifest
		if json.Unmarshal(data, &manifest) != nil || manifest.File == "" || filepath.Base(manifest.File) != manifest.File || len(manifest.EpisodeIDs) > 10000 {
			continue
		}
		output, err := os.Lstat(filepath.Join(seriesDir, manifest.File))
		if err != nil || !output.Mode().IsRegular() {
			continue
		}
		for _, id := range manifest.EpisodeIDs {
			if id > 0 {
				result[id] = true
			}
		}
	}
	return result
}
