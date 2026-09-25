package app

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/xml"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func testChapters(t *testing.T) []chapter {
	t.Helper()
	dir := t.TempDir()
	paths := []string{}
	for i := 0; i < 2; i++ {
		img := image.NewNRGBA(image.Rect(0, 0, 6, 4))
		img.Set(0, 0, color.NRGBA{R: uint8(100 + i), A: 255})
		path := filepath.Join(dir, strconv.Itoa(i)+".png")
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if err = png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
		if err = f.Close(); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	return []chapter{{Name: "A & B", Images: paths[:1]}, {Name: "Second", Images: paths[1:]}}
}
func TestPDFExport(t *testing.T) {
	var out bytes.Buffer
	if err := writePDF(&out, testChapters(t)); err != nil {
		t.Fatal(err)
	}
	data := out.Bytes()
	if !bytes.HasPrefix(data, []byte("%PDF-1.4")) || !bytes.Contains(data, []byte("/Count 2")) {
		t.Fatal("missing PDF header or page count")
	}
	marker := []byte("startxref\n")
	pos := bytes.LastIndex(data, marker)
	if pos < 0 {
		t.Fatal("missing cross-reference offset")
	}
	offset, err := strconv.Atoi(strings.TrimSpace(string(bytes.SplitN(data[pos+len(marker):], []byte("\n"), 2)[0])))
	if err != nil || offset >= len(data) || !bytes.HasPrefix(data[offset:], []byte("xref\n")) {
		t.Fatal("invalid PDF cross-reference offset")
	}
}
func TestEPUBExport(t *testing.T) {
	var out bytes.Buffer
	if err := writeEPUB(&out, "A & B", testChapters(t)); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.File) == 0 || archive.File[0].Name != "mimetype" || archive.File[0].Method != zip.Store {
		t.Fatal("EPUB mimetype must be the first uncompressed entry")
	}
	found := map[string]bool{}
	for _, f := range archive.File {
		found[f.Name] = true
		if strings.HasSuffix(f.Name, ".xml") || strings.HasSuffix(f.Name, ".opf") || strings.HasSuffix(f.Name, ".xhtml") {
			reader, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			decoder := xml.NewDecoder(reader)
			for {
				_, err = decoder.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("%s: %v", f.Name, err)
				}
			}
			reader.Close()
		}
	}
	for _, name := range []string{"META-INF/container.xml", "OEBPS/content.opf", "OEBPS/nav.xhtml", "OEBPS/chapter-0000.xhtml", "OEBPS/chapter-0001.xhtml", "OEBPS/image-0000.png", "OEBPS/image-0001.png"} {
		if !found[name] {
			t.Errorf("missing %s", name)
		}
	}
}
func TestScanLibraryIgnoresPendingEpisodes(t *testing.T) {
	root := t.TempDir()
	series := filepath.Join(root, "comic")
	if err := os.MkdirAll(filepath.Join(series, ".episode-incomplete"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(series, "Episode 1"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, folder := range []string{".episode-incomplete", "Episode 1"} {
		if err := os.WriteFile(filepath.Join(series, folder, "0000.png"), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	list, err := scanLibrary(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || len(list[0].Episodes) != 1 || list[0].Episodes[0].Name != "Episode 1" {
		t.Fatalf("unexpected library: %#v", list)
	}
}

func TestExportEndpoint(t *testing.T) {
	root := t.TempDir()
	episodeDir := filepath.Join(root, "series", "Episode 1")
	if err := os.MkdirAll(episodeDir, 0755); err != nil {
		t.Fatal(err)
	}
	source := testChapters(t)[0].Images[0]
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(episodeDir, "0000.png"), data, 0644); err != nil {
		t.Fatal(err)
	}
	app := &app{root: root}
	request := httptest.NewRequest(http.MethodPost, "/api/export", strings.NewReader(`{"path":"series/Episode 1","format":"pdf"}`))
	response := httptest.NewRecorder()
	app.export(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("export returned %d: %s", response.Code, response.Body.String())
	}
	pdf, err := os.ReadFile(filepath.Join(episodeDir, "Episode 1.pdf"))
	if err != nil || !bytes.HasPrefix(pdf, []byte("%PDF-1.4")) {
		t.Fatalf("PDF missing or invalid: %v", err)
	}
}
func TestSafePathRejectsTraversal(t *testing.T) {
	if _, err := safePath(t.TempDir(), "../outside.pdf"); err == nil {
		t.Fatal("accepted path outside downloads")
	}
}

func TestLibraryEpisodeOrder(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"Episode 10", "Episode 2", "Episode 1"} {
		dir := filepath.Join(root, "series", name)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "0000.png"), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	list, err := scanLibrary(root)
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, episode := range list[0].Episodes {
		got = append(got, episode.Name)
	}
	if strings.Join(got, ",") != "Episode 1,Episode 2,Episode 10" {
		t.Fatalf("wrong reading order: %v", got)
	}
}

func TestEPUBIncludesKnownCreator(t *testing.T) {
	var out bytes.Buffer
	if err := writeEPUBWithCreator(&out, "Series & Chapter", "A & B", testChapters(t)); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range archive.File {
		if file.Name != "OEBPS/content.opf" {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(data, []byte("<dc:title>Series &amp; Chapter</dc:title>")) || !bytes.Contains(data, []byte("<dc:creator>A &amp; B</dc:creator>")) {
			t.Fatalf("missing escaped EPUB metadata: %s", data)
		}
		return
	}
	t.Fatal("EPUB package metadata missing")
}

func TestFailedExportKeepsSourceAndNoPartialOutput(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.png")
	if err := os.WriteFile(source, []byte("not an image"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := exportFile(context.Background(), root, "Chapter", "pdf", []chapter{{Name: "Chapter", Images: []string{source}}}, exportMetadata{}); err == nil {
		t.Fatal("invalid image was exported")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "source.png" {
		t.Fatalf("failure changed source folder: %v", entries)
	}
}

func TestScanLibraryFindsConvertedFilesWithoutImages(t *testing.T) {
	root := t.TempDir()
	series := filepath.Join(root, "comic")
	if err := os.MkdirAll(series, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Episode 1 [10].pdf", "Episode 2 [11].epub"} {
		if err := os.WriteFile(filepath.Join(series, name), []byte("output"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	list, err := scanLibrary(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || len(list[0].Episodes) != 0 || len(list[0].Files) != 2 {
		t.Fatalf("converted downloads missing from history: %#v", list)
	}
	if list[0].Files[0].Format != "pdf" || list[0].Files[1].Format != "epub" {
		t.Fatalf("wrong formats: %#v", list[0].Files)
	}
}

func TestPDFRejectsOversizedSourceImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huge.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(maxExportImageBytes + 1); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err = writePDF(&output, []chapter{{Name: "Chapter", Images: []string{path}}})
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized image was accepted: %v", err)
	}
}

func TestEPUBFlowsImagesWithinChapter(t *testing.T) {
	images := testChapters(t)
	var output bytes.Buffer
	if err := writeEPUB(&output, "Comic", []chapter{{Name: "Chapter", Images: []string{images[0].Images[0], images[1].Images[0]}}}); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(t.TempDir(), "chapter-flow.epub")
	if err := os.WriteFile(artifact, output.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.OpenReader(artifact)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	var body, packageData string
	for _, file := range archive.File {
		if file.Name != "OEBPS/chapter-0000.xhtml" && file.Name != "OEBPS/content.opf" {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		if file.Name == "OEBPS/content.opf" {
			packageData = string(data)
		} else {
			body = string(data)
		}
	}
	if strings.Count(body, "<img ") != 2 || !strings.Contains(body, `src="image-0000.png"`) || !strings.Contains(body, `src="image-0001.png"`) || strings.Index(body, `src="image-0000.png"`) > strings.Index(body, `src="image-0001.png"`) {
		t.Fatalf("images are not in one flowing chapter: %s", body)
	}
	if strings.Count(packageData, `<itemref idref="chapter0"/>`) != 1 || strings.Contains(packageData, `idref="chapter1"`) || strings.Contains(body, "page-break") || strings.Contains(body, "100vh") {
		t.Fatalf("unexpected forced page layout: %s", packageData)
	}
}
