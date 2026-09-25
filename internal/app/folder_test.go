package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDownloadFolderValidation(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "Comics 日本語")
	if err := os.Mkdir(selected, 0755); err != nil {
		t.Fatal(err)
	}
	clean, err := validateDownloadDir(selected + string(filepath.Separator))
	if err != nil || clean != selected {
		t.Fatalf("valid folder: %q, %v", clean, err)
	}
	file := filepath.Join(root, "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"relative", file, filepath.Join(root, "missing")} {
		if _, err := validateDownloadDir(path); err == nil {
			t.Errorf("accepted unusable folder %q", path)
		}
	}
}

func TestSettingsRejectsMissingFolder(t *testing.T) {
	root := t.TempDir()
	a := &app{configPath: filepath.Join(root, "settings.json"), config: settings{DownloadDir: root, DefaultFormat: "pdf", FilenameTemplate: defaultFilenameTemplate}}
	body := `{"downloadDir":"` + filepath.ToSlash(filepath.Join(root, "missing")) + `","defaultFormat":"epub","filenameTemplate":"Episode {chapter_number}"}`
	response := httptest.NewRecorder()
	a.setSettings(response, httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body)))
	if response.Code != http.StatusBadRequest || a.config.DownloadDir != root {
		t.Fatalf("invalid folder changed settings: %d %+v", response.Code, a.config)
	}
}
