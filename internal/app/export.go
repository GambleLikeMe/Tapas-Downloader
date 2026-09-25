package app

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

const maxExportImageBytes = 30 << 20
const maxExportImagePixels = 50_000_000
const maxExportPages = 5000
const maxExportBytes = 2 << 30

type libraryEpisode struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Images int    `json:"images"`
	Scene  int64  `json:"-"`
	PDF    bool   `json:"pdf"`
	EPUB   bool   `json:"epub"`
}
type libraryFile struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Format string `json:"format"`
}
type librarySeries struct {
	Name     string           `json:"name"`
	Title    string           `json:"title"`
	Path     string           `json:"path"`
	Episodes []libraryEpisode `json:"episodes"`
	Files    []libraryFile    `json:"files"`
	PDF      bool             `json:"pdf"`
	EPUB     bool             `json:"epub"`
}
type chapter struct {
	Name   string
	Images []string
}

func imagePaths(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	paths := []string{}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".jpg", ".jpeg", ".png", ".gif":
			paths = append(paths, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func naturalLess(left, right string) bool {
	left, right = strings.ToLower(left), strings.ToLower(right)
	for i, j := 0, 0; i < len(left) && j < len(right); {
		a, b := left[i], right[j]
		if a >= '0' && a <= '9' && b >= '0' && b <= '9' {
			startI, startJ := i, j
			for i < len(left) && left[i] >= '0' && left[i] <= '9' {
				i++
			}
			for j < len(right) && right[j] >= '0' && right[j] <= '9' {
				j++
			}
			x, y := strings.TrimLeft(left[startI:i], "0"), strings.TrimLeft(right[startJ:j], "0")
			if len(x) != len(y) {
				return len(x) < len(y)
			}
			if x != y {
				return x < y
			}
			continue
		}
		if a != b {
			return a < b
		}
		i++
		j++
	}
	return len(left) < len(right)
}

func scanLibrary(root string) ([]librarySeries, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	result := []librarySeries{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		children, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		series := librarySeries{Name: entry.Name(), Title: strings.ReplaceAll(entry.Name(), "-", " "), Path: entry.Name(), Episodes: []libraryEpisode{}, Files: []libraryFile{}}
		if title, err := os.ReadFile(filepath.Join(dir, ".title")); err == nil && strings.TrimSpace(string(title)) != "" {
			series.Title = strings.TrimSpace(string(title))
		}
		for _, child := range children {
			if strings.HasPrefix(child.Name(), ".") {
				continue
			}
			if child.Type().IsRegular() {
				ext := strings.ToLower(filepath.Ext(child.Name()))
				if ext == ".pdf" || ext == ".epub" {
					series.Files = append(series.Files, libraryFile{Name: strings.TrimSuffix(child.Name(), filepath.Ext(child.Name())), Path: filepath.Join(entry.Name(), child.Name()), Format: strings.TrimPrefix(ext, ".")})
				}
				continue
			}
			if !child.IsDir() {
				continue
			}
			path := filepath.Join(dir, child.Name())
			images, err := imagePaths(path)
			if err != nil {
				return nil, err
			}
			if len(images) == 0 {
				continue
			}
			rel := filepath.Join(entry.Name(), child.Name())
			_, pdfErr := os.Stat(filepath.Join(path, child.Name()+".pdf"))
			_, epubErr := os.Stat(filepath.Join(path, child.Name()+".epub"))
			sceneData, _ := os.ReadFile(filepath.Join(path, ".scene"))
			scene, _ := strconv.ParseInt(strings.TrimSpace(string(sceneData)), 10, 64)
			series.Episodes = append(series.Episodes, libraryEpisode{Name: child.Name(), Path: rel, Images: len(images), Scene: scene, PDF: pdfErr == nil, EPUB: epubErr == nil})
		}
		if len(series.Episodes) == 0 && len(series.Files) == 0 {
			continue
		}
		sort.Slice(series.Episodes, func(i, j int) bool {
			left, right := series.Episodes[i], series.Episodes[j]
			if left.Scene > 0 && right.Scene > 0 {
				return left.Scene < right.Scene
			}
			return naturalLess(left.Name, right.Name)
		})
		_, pdfErr := os.Stat(filepath.Join(dir, entry.Name()+".pdf"))
		_, epubErr := os.Stat(filepath.Join(dir, entry.Name()+".epub"))
		series.PDF = pdfErr == nil
		series.EPUB = epubErr == nil
		result = append(result, series)
	}
	return result, nil
}
func (a *app) library(w http.ResponseWriter, r *http.Request) {
	list, err := a.scanLibrary()
	if err != nil {
		fail(w, 500, err)
		return
	}
	send(w, list)
}
func (a *app) export(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path   string `json:"path"`
		Format string `json:"format"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		fail(w, 400, err)
		return
	}
	if input.Format != "pdf" && input.Format != "epub" {
		fail(w, 400, errors.New("choose PDF or EPUB"))
		return
	}
	list, err := a.scanLibrary()
	if err != nil {
		fail(w, 500, err)
		return
	}
	chapters := []chapter{}
	var target, name string
	var selectedSeries librarySeries
	for _, series := range list {
		if a.matchesLibraryPath(input.Path, series.Path) {
			selectedSeries = series
			target = series.Path
			name = series.Name
			for _, ep := range series.Episodes {
				images, err := imagePaths(ep.Path)
				if err != nil {
					fail(w, 500, err)
					return
				}
				chapters = append(chapters, chapter{ep.Name, images})
			}
			break
		}
		for _, ep := range series.Episodes {
			if a.matchesLibraryPath(input.Path, ep.Path) {
				selectedSeries = series
				target = ep.Path
				name = ep.Name
				images, err := imagePaths(target)
				if err != nil {
					fail(w, 500, err)
					return
				}
				chapters = []chapter{{ep.Name, images}}
				break
			}
		}
		if target != "" {
			break
		}
	}
	if target == "" {
		fail(w, 404, errors.New("download not found"))
		return
	}
	book := bookMetadata{}
	if data, err := os.ReadFile(filepath.Join(selectedSeries.Path, ".series.json")); err == nil {
		_ = json.Unmarshal(data, &book)
	}
	book.SeriesTitle = selectedSeries.Title
	book.Title = selectedSeries.Title
	if !a.matchesLibraryPath(input.Path, selectedSeries.Path) {
		book.Title += " — " + name
	}
	if book.Source == "" {
		book.Source = "https://tapas.io"
	}
	a.logEvent("INFO", "Export started ("+input.Format+")")
	final, err := exportFile(r.Context(), target, name, input.Format, chapters, exportMetadata{Book: book, Template: "{chapter_name}"})
	if err != nil {
		a.logEvent("ERROR", "Export failed: "+err.Error())
		fail(w, 500, err)
		return
	}
	a.logEvent("INFO", "Export completed ("+input.Format+")")
	send(w, map[string]string{"path": final})
}

type exportMetadata struct {
	Book     bookMetadata
	Template string
	Filename filenameData
}

type limitedWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *limitedWriter) Write(data []byte) (int, error) {
	if int64(len(data)) > w.remaining {
		return 0, errors.New("export exceeds the size limit")
	}
	n, err := w.writer.Write(data)
	w.remaining -= int64(n)
	return n, err
}

type contextWriter struct {
	ctx    context.Context
	writer io.Writer
}

func (w contextWriter) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	return w.writer.Write(data)
}

func exportFile(ctx context.Context, target, name, format string, chapters []chapter, metadata exportMetadata) (string, error) {
	if format != "pdf" && format != "epub" {
		return "", errors.New("choose PDF or EPUB")
	}
	filename := metadata.Filename
	if filename.ChapterName == "" {
		filename.ChapterName = name
	}
	base, err := filenameBase(metadata.Template, filename)
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(target, ".export-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	writer := contextWriter{ctx: ctx, writer: &limitedWriter{writer: tmp, remaining: maxExportBytes}}
	book := metadata.Book
	if book.Title == "" {
		book.Title = name
	}
	if format == "pdf" {
		err = writePDFWithMetadata(writer, chapters, book)
	} else {
		var cover *coverImage
		for _, coverURL := range []string{book.CoverURL, book.FallbackCoverURL} {
			if coverURL != "" && cover == nil {
				cover, _ = fetchCover(ctx, coverURL, nil)
			}
		}
		err = writeEPUBWithMetadata(writer, book, chapters, cover)
	}
	if err == nil {
		err = ctx.Err()
	}
	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	for suffix := 1; suffix <= 10000; suffix++ {
		candidate := base
		if suffix > 1 {
			candidate = fmt.Sprintf("%s (%d)", base, suffix)
		}
		final := filepath.Join(target, candidate+"."+format)
		if err = os.Link(tmp.Name(), final); err == nil {
			return final, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", errors.New("too many files with the same name")
}

type countedWriter struct {
	writer io.Writer
	count  int64
}

func (w *countedWriter) Write(data []byte) (int, error) {
	n, err := w.writer.Write(data)
	w.count += int64(n)
	return n, err
}

func writePDF(dst io.Writer, chapters []chapter) error {
	return writePDFWithMetadata(dst, chapters, bookMetadata{})
}

func pdfText(value string) string {
	encoded := []byte{0xfe, 0xff}
	for _, unit := range utf16.Encode([]rune(value)) {
		encoded = append(encoded, byte(unit>>8), byte(unit))
	}
	return "<" + strings.ToUpper(hex.EncodeToString(encoded)) + ">"
}

func writePDFWithMetadata(dst io.Writer, chapters []chapter, metadata bookMetadata) error {
	images := []string{}
	for _, ch := range chapters {
		images = append(images, ch.Images...)
	}
	if len(images) == 0 {
		return errors.New("nothing to export")
	}
	if len(images) > maxExportPages {
		return errors.New("too many images to export")
	}
	out := &countedWriter{writer: dst}
	write := func(s string) error { _, err := io.WriteString(out, s); return err }
	if err := write("%PDF-1.4\n%\xE2\xE3\xCF\xD3\n"); err != nil {
		return err
	}
	offsets := []int64{0}
	begin := func(n int) error { offsets = append(offsets, out.count); return write(fmt.Sprintf("%d 0 obj\n", n)) }
	end := func() error { return write("\nendobj\n") }
	if err := begin(1); err != nil {
		return err
	}
	if err := write("<< /Type /Catalog /Pages 2 0 R >>"); err != nil {
		return err
	}
	if err := end(); err != nil {
		return err
	}
	if err := begin(2); err != nil {
		return err
	}
	var kids strings.Builder
	for i := range images {
		fmt.Fprintf(&kids, "%d 0 R ", 3+i*3)
	}
	if err := write(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", kids.String(), len(images))); err != nil {
		return err
	}
	if err := end(); err != nil {
		return err
	}
	for i, path := range images {
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		info, statErr := file.Stat()
		if statErr != nil || info.Size() > maxExportImageBytes {
			file.Close()
			return fmt.Errorf("image is too large: %s", path)
		}
		config, _, configErr := image.DecodeConfig(file)
		if configErr != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maxExportImagePixels {
			file.Close()
			return fmt.Errorf("unsupported image dimensions: %s", path)
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			file.Close()
			return err
		}
		decoded, _, err := image.Decode(file)
		file.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		width, height := decoded.Bounds().Dx(), decoded.Bounds().Dy()
		if width <= 0 || height <= 0 {
			return fmt.Errorf("invalid image dimensions: %s", path)
		}
		canvas := image.NewRGBA(image.Rect(0, 0, width, height))
		draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
		draw.Draw(canvas, canvas.Bounds(), decoded, decoded.Bounds().Min, draw.Over)
		var jpegData bytes.Buffer
		if err = jpeg.Encode(&jpegData, canvas, &jpeg.Options{Quality: 90}); err != nil {
			return err
		}
		pageWidth, pageHeight := float64(width)*0.75, float64(height)*0.75
		if pageWidth > 14400 {
			pageHeight *= 14400 / pageWidth
			pageWidth = 14400
		}
		if pageHeight > 14400 {
			pageWidth *= 14400 / pageHeight
			pageHeight = 14400
		}
		base := 3 + i*3
		if err = begin(base); err != nil {
			return err
		}
		if err = write(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] /Resources << /XObject << /Im0 %d 0 R >> >> /Contents %d 0 R >>", pageWidth, pageHeight, base+1, base+2)); err != nil {
			return err
		}
		if err = end(); err != nil {
			return err
		}
		if err = begin(base + 1); err != nil {
			return err
		}
		if err = write(fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace %s /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n", width, height, "/DeviceRGB", jpegData.Len())); err != nil {
			return err
		}
		if _, err = out.Write(jpegData.Bytes()); err != nil {
			return err
		}
		if err = write("\nendstream"); err != nil {
			return err
		}
		if err = end(); err != nil {
			return err
		}
		content := fmt.Sprintf("q %.2f 0 0 %.2f 0 0 cm /Im0 Do Q\n", pageWidth, pageHeight)
		if err = begin(base + 2); err != nil {
			return err
		}
		if err = write(fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content)); err != nil {
			return err
		}
		if err = end(); err != nil {
			return err
		}
	}
	infoID := 0
	if metadata.Title != "" || metadata.Creator != "" || metadata.Description != "" || metadata.SeriesTitle != "" || len(metadata.Keywords) > 0 {
		infoID = 3 + len(images)*3
		if err := begin(infoID); err != nil {
			return err
		}
		var info strings.Builder
		info.WriteString("<<")
		for _, field := range []struct{ key, value string }{
			{"Title", metadata.Title},
			{"Author", metadata.Creator},
			{"Subject", metadata.Description},
			{"Keywords", strings.Join(metadata.Keywords, ", ")},
		} {
			if field.value != "" {
				fmt.Fprintf(&info, " /%s %s", field.key, pdfText(field.value))
			}
		}
		if metadata.Description == "" && metadata.SeriesTitle != "" {
			fmt.Fprintf(&info, " /Subject %s", pdfText(metadata.SeriesTitle))
		}
		info.WriteString(" >>")
		if err := write(info.String()); err != nil {
			return err
		}
		if err := end(); err != nil {
			return err
		}
	}
	start := out.count
	if err := write(fmt.Sprintf("xref\n0 %d\n0000000000 65535 f \n", len(offsets))); err != nil {
		return err
	}
	for _, offset := range offsets[1:] {
		if err := write(fmt.Sprintf("%010d 00000 n \n", offset)); err != nil {
			return err
		}
	}
	infoRef := ""
	if infoID != 0 {
		infoRef = fmt.Sprintf(" /Info %d 0 R", infoID)
	}
	return write(fmt.Sprintf("trailer\n<< /Size %d /Root 1 0 R%s >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), infoRef, start))
}
func xmlText(value string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(value))
	return b.String()
}
func zipText(z *zip.Writer, name, value string) error {
	w, err := z.Create(name)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, value)
	return err
}
func writeEPUB(w io.Writer, title string, chapters []chapter) error {
	return writeEPUBWithMetadata(w, bookMetadata{Title: title}, chapters, nil)
}
func writeEPUBWithCreator(w io.Writer, title, creator string, chapters []chapter) error {
	return writeEPUBWithMetadata(w, bookMetadata{Title: title, Creator: creator}, chapters, nil)
}
func writeEPUBWithMetadata(w io.Writer, metadata bookMetadata, chapters []chapter, cover *coverImage) error {
	z := zip.NewWriter(w)
	mimetype := &zip.FileHeader{Name: "mimetype", Method: zip.Store}
	first, err := z.CreateHeader(mimetype)
	if err != nil {
		return err
	}
	if _, err = io.WriteString(first, "application/epub+zip"); err != nil {
		return err
	}
	if err = zipText(z, "META-INF/container.xml", `<?xml version="1.0" encoding="UTF-8"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`); err != nil {
		return err
	}
	var manifest, spine, nav strings.Builder
	manifest.WriteString(`<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>`)
	nav.WriteString(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Contents</title></head><body><nav epub:type="toc" id="toc"><h1>Contents</h1><ol>`)
	page := 0
	chapterNumber := 0
	for _, ch := range chapters {
		if len(ch.Images) == 0 {
			continue
		}
		chapterName := fmt.Sprintf("chapter-%04d.xhtml", chapterNumber)
		fmt.Fprintf(&nav, `<li><a href="%s">%s</a></li>`, chapterName, xmlText(ch.Name))
		var html strings.Builder
		fmt.Fprintf(&html, `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>%s</title><meta name="viewport" content="width=device-width, initial-scale=1"/><style>html,body{margin:0;padding:0;background:#fff}img{display:block;width:100%%;height:auto;margin:0;padding:0}</style></head><body>`, xmlText(ch.Name))
		for _, source := range ch.Images {
			if page >= maxExportPages {
				return errors.New("too many images to export")
			}
			ext := strings.ToLower(filepath.Ext(source))
			if ext == ".jpeg" {
				ext = ".jpg"
			}
			mime := map[string]string{".jpg": "image/jpeg", ".png": "image/png", ".gif": "image/gif"}[ext]
			if mime == "" {
				return fmt.Errorf("unsupported image: %s", source)
			}
			imgName := fmt.Sprintf("image-%04d%s", page, ext)
			imageFile, err := os.Open(source)
			if err != nil {
				return err
			}
			info, statErr := imageFile.Stat()
			if statErr != nil || info.Size() > maxExportImageBytes {
				imageFile.Close()
				return fmt.Errorf("image is too large: %s", source)
			}
			zw, err := z.Create("OEBPS/" + imgName)
			if err != nil {
				imageFile.Close()
				return err
			}
			_, err = io.Copy(zw, imageFile)
			imageFile.Close()
			if err != nil {
				return err
			}
			fmt.Fprintf(&html, `<img src="%s" alt="%s"/>`, imgName, xmlText(ch.Name))
			fmt.Fprintf(&manifest, `<item id="image%d" href="%s" media-type="%s"/>`, page, imgName, mime)
			page++
		}
		html.WriteString(`</body></html>`)
		if err = zipText(z, "OEBPS/"+chapterName, html.String()); err != nil {
			return err
		}
		fmt.Fprintf(&manifest, `<item id="chapter%d" href="%s" media-type="application/xhtml+xml"/>`, chapterNumber, chapterName)
		fmt.Fprintf(&spine, `<itemref idref="chapter%d"/>`, chapterNumber)
		chapterNumber++
	}
	if page == 0 {
		return errors.New("nothing to export")
	}
	if cover != nil {
		coverName := "cover" + cover.Extension
		writer, err := z.Create("OEBPS/" + coverName)
		if err != nil {
			return err
		}
		if _, err := writer.Write(cover.Data); err != nil {
			return err
		}
		fmt.Fprintf(&manifest, `<item id="cover-image" href="%s" media-type="%s" properties="cover-image"/>`, coverName, cover.MediaType)
	}
	nav.WriteString(`</ol></nav></body></html>`)
	if err = zipText(z, "OEBPS/nav.xhtml", nav.String()); err != nil {
		return err
	}
	id := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%d", metadata.Title, metadata.SeriesID, metadata.EpisodeID)))
	var fields strings.Builder
	fmt.Fprintf(&fields, `<dc:identifier id="bookid">urn:sha256:%s</dc:identifier><dc:title>%s</dc:title>`, hex.EncodeToString(id[:]), xmlText(metadata.Title))
	for _, field := range []struct{ tag, value string }{
		{"creator", metadata.Creator},
		{"language", metadata.epubLanguage()},
		{"description", metadata.Description},
		{"publisher", metadata.Publisher},
		{"date", metadata.Date},
		{"source", metadata.Source},
	} {
		if field.value != "" {
			fmt.Fprintf(&fields, "<dc:%s>%s</dc:%s>", field.tag, xmlText(field.value), field.tag)
		}
	}
	for _, keyword := range metadata.Keywords {
		if strings.TrimSpace(keyword) != "" {
			fmt.Fprintf(&fields, "<dc:subject>%s</dc:subject>", xmlText(keyword))
		}
	}
	if metadata.SeriesTitle != "" {
		fmt.Fprintf(&fields, `<meta property="belongs-to-collection" id="series">%s</meta><meta refines="#series" property="collection-type">series</meta>`, xmlText(metadata.SeriesTitle))
	}
	if cover != nil {
		fields.WriteString(`<meta name="cover" content="cover-image"/>`)
	}
	fmt.Fprintf(&fields, `<meta property="dcterms:modified">%s</meta>`, time.Now().UTC().Format("2006-01-02T15:04:05Z"))
	opf := `<?xml version="1.0" encoding="UTF-8"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bookid"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/">` + fields.String() + `</metadata><manifest>` + manifest.String() + `</manifest><spine>` + spine.String() + `</spine></package>`
	if err = zipText(z, "OEBPS/content.opf", opf); err != nil {
		return err
	}
	return z.Close()
}
