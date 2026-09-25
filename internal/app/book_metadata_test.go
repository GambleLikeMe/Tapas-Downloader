package app

import (
	"api-scraper/internal/api"
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTapasSeriesMetadataMapping(t *testing.T) {
	var details api.ComicDetails
	payload := `{"id":7,"title":"Comic","description":"A story","book_cover_url":"https://covers.example/book.jpg","human_url":"comic-slug","thumb":{"file_url":"https://covers.example/thumb.jpg"},"genre":{"name":"Fantasy"},"tags":["Adventure",{"name":"Magic"},"fantasy"],"creators":[{"display_name":"Artist"},{"display_name":"Writer"}]}`
	if err := json.Unmarshal([]byte(payload), &details); err != nil {
		t.Fatal(err)
	}
	book := bookFromDetails(details)
	if book.Creator != "Artist, Writer" || book.Description != "A story" || book.Source != "https://tapas.io/series/comic-slug" || book.CoverURL != "https://covers.example/book.jpg" || book.FallbackCoverURL != "https://covers.example/thumb.jpg" || strings.Join(book.Keywords, ",") != "Fantasy,Adventure,Magic" {
		t.Fatalf("Tapas metadata was not mapped: %+v", book)
	}
}

func TestPDFMetadataKeepsPageCountAndValidXref(t *testing.T) {
	var output bytes.Buffer
	book := bookMetadata{Title: "A & B — Chapter 10", Creator: "Artist", SeriesTitle: "A & B", Description: "A story (with art)", Keywords: []string{"fantasy", "comic"}}
	if err := writePDFWithMetadata(&output, testChapters(t), book); err != nil {
		t.Fatal(err)
	}
	data := output.Bytes()
	for _, field := range []string{"/Info 9 0 R", "/Count 2", "/Title " + pdfText(book.Title), "/Author " + pdfText(book.Creator), "/Subject " + pdfText(book.Description), "/Keywords " + pdfText("fantasy, comic")} {
		if !bytes.Contains(data, []byte(field)) {
			t.Errorf("PDF is missing %q", field)
		}
	}
	if bytes.Count(data, []byte("/Type /Page ")) != 2 {
		t.Fatal("metadata or cover added an extra visible PDF page")
	}
	assertPDFXref(t, data)
}

func assertPDFXref(t *testing.T, data []byte) {
	t.Helper()
	marker := []byte("startxref\n")
	pos := bytes.LastIndex(data, marker)
	if pos < 0 {
		t.Fatal("missing PDF cross-reference offset")
	}
	var offset int
	if _, err := fmt.Sscanf(string(data[pos+len(marker):]), "%d", &offset); err != nil || offset >= len(data) || !bytes.HasPrefix(data[offset:], []byte("xref\n")) {
		t.Fatal("invalid PDF cross-reference offset")
	}
	lines := strings.Split(string(data[offset:]), "\n")
	var first, size int
	if len(lines) < 3 || func() bool { _, err := fmt.Sscanf(lines[1], "%d %d", &first, &size); return err != nil }() || first != 0 || len(lines) < size+2 {
		t.Fatal("invalid PDF cross-reference table")
	}
	for id := 1; id < size; id++ {
		var objectOffset int
		if _, err := fmt.Sscanf(lines[id+2], "%d", &objectOffset); err != nil || objectOffset >= len(data) || !bytes.HasPrefix(data[objectOffset:], []byte(fmt.Sprintf("%d 0 obj", id))) {
			t.Fatalf("invalid cross-reference for object %d", id)
		}
	}
}

func TestEPUBMetadataAndCover(t *testing.T) {
	chapters := testChapters(t)
	data, err := os.ReadFile(chapters[0].Images[0])
	if err != nil {
		t.Fatal(err)
	}
	cover, err := readCover(data)
	if err != nil {
		t.Fatal(err)
	}
	book := bookMetadata{Title: "Comic — Episode 10", Creator: "A & B", SeriesTitle: "Comic", Description: "A short summary", Source: "https://tapas.io", SeriesID: 7, EpisodeID: 123}
	var output bytes.Buffer
	if err := writeEPUBWithMetadata(&output, book, chapters, cover); err != nil {
		t.Fatal(err)
	}
	archive, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string][]byte{}
	for _, item := range archive.File {
		reader, err := item.Open()
		if err != nil {
			t.Fatal(err)
		}
		entries[item.Name], err = io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	opf := string(entries["OEBPS/content.opf"])
	for _, fragment := range []string{"<dc:title>Comic — Episode 10</dc:title>", "<dc:creator>A &amp; B</dc:creator>", "<dc:language>und</dc:language>", "<dc:description>A short summary</dc:description>", "<dc:source>https://tapas.io</dc:source>", `property="belongs-to-collection"`, `properties="cover-image"`, `<meta name="cover" content="cover-image"/>`, `urn:sha256:`} {
		if !strings.Contains(opf, fragment) {
			t.Errorf("EPUB metadata is missing %q", fragment)
		}
	}
	if !bytes.Equal(entries["OEBPS/cover.png"], data) {
		t.Fatal("cover bytes were changed or omitted")
	}
	if _, exists := entries["OEBPS/chapter-0002.xhtml"]; exists {
		t.Fatal("cover was added as a visible page")
	}
}

func TestCoverFailureDoesNotBlockEPUBExport(t *testing.T) {
	root := t.TempDir()
	book := bookMetadata{Title: "Comic", CoverURL: "http://invalid.local/cover.png"}
	output, err := exportFile(context.Background(), root, "Comic", "epub", testChapters(t), exportMetadata{Book: book, Template: "{chapter_name}"})
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(output)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	stat, _ := file.Stat()
	archive, err := zip.NewReader(file, stat.Size())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range archive.File {
		if strings.HasPrefix(entry.Name, "OEBPS/cover.") {
			t.Fatal("invalid cover was included")
		}
	}
}

func TestFetchCoverChecksImage(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 3))
	var imageData bytes.Buffer
	if err := png.Encode(&imageData, img); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			w.Write([]byte("not an image"))
			return
		}
		w.Write(imageData.Bytes())
	}))
	defer server.Close()
	testClient := server.Client()
	transport := testClient.Transport.(*http.Transport).Clone()
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	transport.TLSClientConfig.ServerName = "127.0.0.1"
	testClient.Transport = transport
	cover, err := fetchCover(context.Background(), "https://us-a.tapas.io/cover", testClient)
	if err != nil || cover.MediaType != "image/png" || !bytes.Equal(cover.Data, imageData.Bytes()) {
		t.Fatalf("valid cover: %#v, %v", cover, err)
	}
	if _, err := fetchCover(context.Background(), "https://us-a.tapas.io/bad", testClient); err == nil {
		t.Fatal("corrupt cover accepted")
	}
	if _, err := fetchCover(context.Background(), "http://example.com/cover.jpg", server.Client()); err == nil {
		t.Fatal("insecure cover URL accepted")
	}
}

func TestSavedImagesExportUsesStoredSeriesMetadata(t *testing.T) {
	root := t.TempDir()
	episodeDir := filepath.Join(root, "series", "Episode 10 [123]")
	if err := os.MkdirAll(episodeDir, 0755); err != nil {
		t.Fatal(err)
	}
	imageData, err := os.ReadFile(testChapters(t)[0].Images[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(episodeDir, "0000.png"), imageData, 0644); err != nil {
		t.Fatal(err)
	}
	book := bookMetadata{Title: "Comic", Creator: "Artist", Description: "Series summary"}
	if err := saveJSONAtomic(filepath.Join(root, "series", ".series.json"), book); err != nil {
		t.Fatal(err)
	}
	app := &app{root: root}
	request := httptest.NewRequest(http.MethodPost, "/api/export", strings.NewReader(`{"path":"series/Episode 10 [123]","format":"pdf"}`))
	response := httptest.NewRecorder()
	app.export(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("export returned %d: %s", response.Code, response.Body.String())
	}
	pdf, err := os.ReadFile(filepath.Join(episodeDir, "Episode 10 [123].pdf"))
	if err != nil || !bytes.Contains(pdf, []byte("/Author "+pdfText("Artist"))) || !bytes.Contains(pdf, []byte("/Subject "+pdfText("Series summary"))) {
		t.Fatal("stored metadata did not reach manual PDF export")
	}
	var result map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result["path"] == "" {
		t.Fatal("export response missing file path")
	}
}
