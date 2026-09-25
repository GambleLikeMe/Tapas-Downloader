package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFilenameTemplates(t *testing.T) {
	data := filenameData{SeriesName: "My Comic", SeriesID: 77, ChapterNumber: 10, ChapterID: 123456, ChapterTitle: "Chapter 10", ChapterName: "Episode Chapter 10 [123456]"}
	cases := []struct{ template, want string }{
		{"", "Episode 10[123456]"},
		{"{chapter_name}", "Episode Chapter 10 [123456]"},
		{"{series_name} - Chapter {chapter_number}", "My Comic - Chapter 10"},
		{"{series_name} [{series_id}] - {chapter_number} [{chapter_id}]", "My Comic [77] - 10 [123456]"},
		{"{chapter_number} - {chapter_title}", "10 - Chapter 10"},
	}
	for _, test := range cases {
		got, err := filenameBase(test.template, data)
		if err != nil || got != test.want {
			t.Errorf("%q: got %q, %v; want %q", test.template, got, err, test.want)
		}
	}
}

func TestFilenameTemplateValidationAndSanitization(t *testing.T) {
	if err := validateFilenameTemplate("{chapter_numbr}"); err == nil || !strings.Contains(err.Error(), "chapter_numbr") {
		t.Fatalf("unknown variable was accepted: %v", err)
	}
	if err := validateFilenameTemplate("{chapter_title"); err == nil {
		t.Fatal("unclosed variable was accepted")
	}
	for _, template := range []string{`../{series_name}`, `{series_name} / \ : * ? " < > |`, "CON", "NUL.txt"} {
		got, err := filenameBase(template, filenameData{SeriesName: `Comic / Test: Chapter?`})
		if err != nil {
			t.Fatal(err)
		}
		if got == "" || strings.ContainsAny(got, `/\\:*?"<>|`) || filepath.Base(got) != got {
			t.Errorf("unsafe filename %q from %q", got, template)
		}
		if template == "CON" && got != "_CON" {
			t.Errorf("reserved name = %q", got)
		}
		if template == "NUL.txt" && got != "_NUL.txt" {
			t.Errorf("reserved filename = %q", got)
		}
	}
	chapterTitle, err := filenameBase("{chapter_number} - {chapter_title}", filenameData{ChapterNumber: 10, ChapterTitle: `Bad / \ : * ? " < > |`})
	if err != nil || strings.ContainsAny(chapterTitle, `/\:*?"<>|`) || !strings.HasPrefix(chapterTitle, "10 - Bad") {
		t.Fatalf("unsafe chapter title = %q, %v", chapterTitle, err)
	}
	missing, err := filenameBase("{chapter_title}", filenameData{})
	if err != nil || missing != "Untitled" {
		t.Fatalf("missing title fallback = %q, %v", missing, err)
	}
	long, err := filenameBase(strings.Repeat("ä", 200), filenameData{})
	if err != nil || len(long) > 180 {
		t.Fatalf("long filename: %d bytes, %v", len(long), err)
	}
}

func TestExportNamesAndCollisions(t *testing.T) {
	images := testChapters(t)
	dir := t.TempDir()
	metadata := exportMetadata{Template: "{series_name} - Chapter {chapter_number}", Filename: filenameData{SeriesName: "My Comic", ChapterNumber: 10, ChapterName: "Episode 10 [123456]"}}
	for _, format := range []string{"pdf", "epub"} {
		first, err := exportFile(context.Background(), dir, "Episode 10 [123456]", format, images, metadata)
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Base(first) != "My Comic - Chapter 10."+format {
			t.Fatalf("wrong %s name: %s", format, first)
		}
		original, err := os.ReadFile(first)
		if err != nil {
			t.Fatal(err)
		}
		second, err := exportFile(context.Background(), dir, "Episode 10 [123456]", format, images, metadata)
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Base(second) != "My Comic - Chapter 10 (2)."+format {
			t.Fatalf("collision name: %s", second)
		}
		remaining, err := os.ReadFile(first)
		if err != nil || string(remaining) != string(original) {
			t.Fatal("first export was overwritten")
		}
	}
	defaultMetadata := exportMetadata{Filename: filenameData{ChapterNumber: 10, ChapterID: 123456, ChapterName: "Wrong API title [999]"}}
	for _, format := range []string{"pdf", "epub"} {
		output, err := exportFile(context.Background(), dir, "Wrong API title [999]", format, images, defaultMetadata)
		if err != nil || filepath.Base(output) != "Episode 10[123456]."+format {
			t.Fatalf("default %s filename: %s, %v", format, output, err)
		}
	}
}

func TestFilenamePreviewRejectsUnknownVariable(t *testing.T) {
	app := &app{}
	bad := httptest.NewRecorder()
	app.filenamePreview(bad, httptest.NewRequest(http.MethodGet, "/api/filename-preview?template=%7Bchapter_numbr%7D", nil))
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "chapter_numbr") {
		t.Fatalf("unknown variable response: %d %s", bad.Code, bad.Body.String())
	}
	good := httptest.NewRecorder()
	app.filenamePreview(good, httptest.NewRequest(http.MethodGet, "/api/filename-preview?template=%7Bseries_name%7D+-+Chapter+%7Bchapter_number%7D", nil))
	if good.Code != http.StatusOK || !strings.Contains(good.Body.String(), "My Comic - Chapter 10.pdf") {
		t.Fatalf("preview response: %d %s", good.Code, good.Body.String())
	}
	defaultPreview := httptest.NewRecorder()
	app.filenamePreview(defaultPreview, httptest.NewRequest(http.MethodGet, "/api/filename-preview?template=Episode+%7Bchapter_number%7D%5B%7Bchapter_id%7D%5D", nil))
	if defaultPreview.Code != http.StatusOK || !strings.Contains(defaultPreview.Body.String(), "Episode 10[123456].pdf") {
		t.Fatalf("default preview response: %d %s", defaultPreview.Code, defaultPreview.Body.String())
	}
}

func TestLegacySettingsGetNewDefaultTemplate(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "settings.json")
	data, err := json.Marshal(map[string]string{"downloadDir": root, "defaultFormat": "pdf"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	server := &app{configPath: path}
	if err := server.loadSettings(); err != nil {
		t.Fatal(err)
	}
	if server.config.FilenameTemplate != defaultFilenameTemplate {
		t.Fatalf("missing template did not get the new default: %+v", server.config)
	}
}

func TestTemplateCannotEscapeDownloadFolder(t *testing.T) {
	root := t.TempDir()
	out, err := exportFile(context.Background(), root, "Episode 10 [123456]", "pdf", testChapters(t), exportMetadata{Template: "../{series_name}", Filename: filenameData{SeriesName: "My Comic"}})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(out) != root || filepath.Base(out) != "..My Comic.pdf" {
		t.Fatalf("template escaped output folder: %s", out)
	}
}
