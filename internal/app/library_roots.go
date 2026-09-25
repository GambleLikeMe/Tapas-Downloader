package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func (a *app) libraryRoots() []string {
	a.mu.Lock()
	roots := []string{a.root, a.config.DownloadDir}
	for _, item := range a.tasks {
		roots = append(roots, item.Directory)
	}
	a.mu.Unlock()
	seen := map[string]bool{}
	result := []string{}
	for _, root := range roots {
		if root == "" || !filepath.IsAbs(root) {
			continue
		}
		root = filepath.Clean(root)
		if !seen[root] {
			seen[root] = true
			result = append(result, root)
		}
	}
	return result
}

func (a *app) scanLibrary() ([]librarySeries, error) {
	combined := []librarySeries{}
	for _, root := range a.libraryRoots() {
		list, err := scanLibrary(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, series := range list {
			series.Path = filepath.Join(root, series.Path)
			for i := range series.Episodes {
				series.Episodes[i].Path = filepath.Join(root, series.Episodes[i].Path)
			}
			for i := range series.Files {
				series.Files[i].Path = filepath.Join(root, series.Files[i].Path)
			}
			combined = append(combined, series)
		}
	}
	return combined, nil
}

func (a *app) safeLibraryPath(target string) (string, error) {
	if !filepath.IsAbs(target) {
		return "", errors.New("invalid path")
	}
	for _, root := range a.libraryRoots() {
		relative, err := filepath.Rel(root, target)
		if err != nil || relative == "." {
			continue
		}
		resolved, err := safePath(root, relative)
		if err == nil {
			return resolved, nil
		}
	}
	return "", errors.New("path is outside downloads")
}

func (a *app) matchesLibraryPath(input, absolute string) bool {
	input = filepath.FromSlash(input)
	if input == absolute {
		return true
	}
	relative, err := filepath.Rel(a.root, absolute)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) && input == relative
}
