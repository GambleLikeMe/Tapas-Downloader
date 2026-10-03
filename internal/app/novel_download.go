package app

import (
	"api-scraper/internal/api"
	"api-scraper/internal/client"
	"api-scraper/internal/download"
	"api-scraper/internal/novel"
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-pdf/fpdf"
	htmlparse "golang.org/x/net/html"
)

//go:embed fonts/DejaVuSerif*.ttf
var novelFonts embed.FS

type novelBlock struct {
	Text  string
	Image string
	Level int
}

type novelDocument struct {
	HTML   string
	Blocks []novelBlock
	Images []string
}

var novelTags = map[string]bool{
	"div": true, "span": true, "p": true, "br": true, "b": true, "strong": true,
	"i": true, "em": true, "u": true, "blockquote": true, "h1": true, "h2": true,
	"h3": true, "h4": true, "h5": true, "h6": true, "ul": true, "ol": true,
	"li": true, "hr": true, "small": true, "sup": true, "sub": true,
	"ruby": true, "rt": true, "rp": true, "img": true,
}

func findViewport(node *htmlparse.Node) *htmlparse.Node {
	if node.Type == htmlparse.ElementNode {
		for _, attr := range node.Attr {
			if attr.Key == "id" && attr.Val == "viewport" {
				return node
			}
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findViewport(child); found != nil {
			return found
		}
	}
	return nil
}

func fetchNovelImage(ctx context.Context, source string) ([]byte, string, error) {
	parsed, err := url.Parse(source)
	if err != nil || !client.AllowedImageURL(parsed) {
		return nil, "", errors.New("novel image has an unsupported URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Referer", "https://tapas.io/")
	httpClient := &http.Client{Timeout: 60 * time.Second, CheckRedirect: client.CheckImageRedirect}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("novel image: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("novel image: %s", res.Status)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxExportImageBytes+1))
	if err != nil || len(data) > maxExportImageBytes {
		return nil, "", errors.New("novel image is too large or unreadable")
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maxExportImagePixels {
		return nil, "", errors.New("novel image has unsupported dimensions")
	}
	ext := map[string]string{"jpeg": ".jpg", "png": ".png", "gif": ".gif"}[format]
	if ext == "" {
		return nil, "", errors.New("novel image format is unsupported")
	}
	return data, ext, nil
}

type novelAssembler struct {
	ctx    context.Context
	dir    string
	images []string
	byURL  map[string]string
	blocks []novelBlock
	line   strings.Builder
	level  int
}

func (a *novelAssembler) flush() {
	value := strings.Join(strings.Fields(a.line.String()), " ")
	if value != "" {
		a.blocks = append(a.blocks, novelBlock{Text: value, Level: a.level})
	}
	a.line.Reset()
}

func (a *novelAssembler) render(node *htmlparse.Node, base *url.URL, out *strings.Builder) error {
	if err := a.ctx.Err(); err != nil {
		return err
	}
	if node.Type == htmlparse.TextNode {
		out.WriteString(html.EscapeString(node.Data))
		a.line.WriteString(node.Data)
		return nil
	}
	if node.Type != htmlparse.ElementNode {
		return nil
	}
	tag := strings.ToLower(node.Data)
	if tag == "script" || tag == "style" || tag == "iframe" || tag == "object" || tag == "form" || tag == "svg" {
		return nil
	}
	if tag == "img" {
		var source, alt string
		for _, attr := range node.Attr {
			switch attr.Key {
			case "src":
				source = attr.Val
			case "alt":
				alt = attr.Val
			}
		}
		ref, err := url.Parse(source)
		if err != nil || source == "" {
			return errors.New("novel image URL is invalid")
		}
		absolute := base.ResolveReference(ref).String()
		name := a.byURL[absolute]
		if name == "" {
			if len(a.images) >= 100 {
				return errors.New("novel chapter has too many images")
			}
			data, ext, err := fetchNovelImage(a.ctx, absolute)
			if err != nil {
				return err
			}
			name = fmt.Sprintf("image-%03d%s", len(a.images)+1, ext)
			if err := os.WriteFile(filepath.Join(a.dir, name), data, 0644); err != nil {
				return err
			}
			a.byURL[absolute] = name
			a.images = append(a.images, name)
		}
		a.flush()
		a.blocks = append(a.blocks, novelBlock{Image: name})
		fmt.Fprintf(out, `<img src="%s" alt="%s"/>`, name, html.EscapeString(alt))
		return nil
	}
	if tag == "br" || tag == "hr" {
		a.flush()
		fmt.Fprintf(out, "<%s/>", tag)
		return nil
	}
	allowed := novelTags[tag]
	block := tag == "p" || tag == "div" || tag == "li" || tag == "blockquote" || (len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6')
	if block {
		a.flush()
	}
	previousLevel := a.level
	if len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6' {
		a.level = int(tag[1] - '0')
	}
	if tag == "li" {
		a.line.WriteString("• ")
	}
	if allowed {
		fmt.Fprintf(out, "<%s>", tag)
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if err := a.render(child, base, out); err != nil {
			return err
		}
	}
	if allowed {
		fmt.Fprintf(out, "</%s>", tag)
	}
	if block {
		a.flush()
	}
	a.level = previousLevel
	return nil
}

func assembleNovel(ctx context.Context, pages []string, sources []string, dir string) (novelDocument, error) {
	if len(pages) == 0 || len(pages) != len(sources) {
		return novelDocument{}, errors.New("novel chapter has no supported content")
	}
	a := &novelAssembler{ctx: ctx, dir: dir, byURL: map[string]string{}}
	var body strings.Builder
	for i, page := range pages {
		parsed, err := htmlparse.Parse(strings.NewReader(page))
		if err != nil {
			return novelDocument{}, err
		}
		viewport := findViewport(parsed)
		if viewport == nil {
			return novelDocument{}, errors.New("novel chapter has no readable content")
		}
		base, err := url.Parse(sources[i])
		if err != nil {
			return novelDocument{}, err
		}
		for child := viewport.FirstChild; child != nil; child = child.NextSibling {
			if err := a.render(child, base, &body); err != nil {
				return novelDocument{}, err
			}
		}
		a.flush()
	}
	if len(a.blocks) == 0 {
		return novelDocument{}, errors.New("novel chapter is empty")
	}
	return novelDocument{HTML: body.String(), Blocks: a.blocks, Images: a.images}, nil
}

func readNovelDocument(dir string) (novelDocument, error) {
	chapterPath := filepath.Join(dir, "chapter.html")
	chapterInfo, err := os.Lstat(chapterPath)
	if err != nil || !chapterInfo.Mode().IsRegular() {
		return novelDocument{}, errors.New("saved novel chapter is missing or invalid")
	}
	data, err := os.ReadFile(chapterPath)
	if err != nil {
		return novelDocument{}, err
	}
	parsed, err := htmlparse.Parse(bytes.NewReader(data))
	if err != nil {
		return novelDocument{}, err
	}
	viewport := findViewport(parsed)
	if viewport == nil {
		return novelDocument{}, errors.New("saved novel chapter is invalid")
	}
	var blocks []novelBlock
	var line strings.Builder
	level := 0
	flush := func() {
		if value := strings.Join(strings.Fields(line.String()), " "); value != "" {
			blocks = append(blocks, novelBlock{Text: value, Level: level})
		}
		line.Reset()
	}
	var walk func(*htmlparse.Node)
	walk = func(node *htmlparse.Node) {
		if node.Type == htmlparse.TextNode {
			line.WriteString(node.Data)
			return
		}
		if node.Type != htmlparse.ElementNode {
			return
		}
		tag := strings.ToLower(node.Data)
		if tag == "img" {
			flush()
			for _, attr := range node.Attr {
				if attr.Key == "src" {
					blocks = append(blocks, novelBlock{Image: filepath.Base(attr.Val)})
				}
			}
			return
		}
		if tag == "br" || tag == "hr" {
			flush()
			return
		}
		block := tag == "p" || tag == "div" || tag == "li" || tag == "blockquote" || (len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6')
		if block {
			flush()
		}
		previousLevel := level
		if len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6' {
			level = int(tag[1] - '0')
		}
		if tag == "li" {
			line.WriteString("• ")
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if block {
			flush()
		}
		level = previousLevel
	}
	for child := viewport.FirstChild; child != nil; child = child.NextSibling {
		walk(child)
	}
	flush()
	images := []string{}
	seen := map[string]bool{}
	for _, block := range blocks {
		if block.Image != "" && !seen[block.Image] {
			images = append(images, block.Image)
			seen[block.Image] = true
		}
	}
	for _, name := range images {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxExportImageBytes {
			return novelDocument{}, fmt.Errorf("saved novel image is missing or invalid: %s", name)
		}
		file, err := os.Open(path)
		if err != nil {
			return novelDocument{}, err
		}
		config, _, err := image.DecodeConfig(file)
		file.Close()
		if err != nil || config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > maxExportImagePixels {
			return novelDocument{}, fmt.Errorf("saved novel image is invalid: %s", name)
		}
	}
	return novelDocument{HTML: string(data), Blocks: blocks, Images: images}, nil
}

func novelHTML(title, body string) string {
	return "<!doctype html><html lang=\"en\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><title>" + html.EscapeString(title) + "</title><style>body{max-width:45rem;margin:3rem auto;padding:0 1.5rem;color:#202923;font:1.1rem/1.7 Georgia,serif}img{display:block;max-width:100%;height:auto;margin:1.5rem auto}blockquote{border-left:2px solid #aaa;padding-left:1rem}h1,h2,h3{line-height:1.3}</style></head><body><h1>" + html.EscapeString(title) + "</h1><main id=\"viewport\">" + body + "</main></body></html>"
}

type novelExportChapter struct {
	Title string
	Dir   string
	Doc   novelDocument
}

func fetchNovelDocument(ctx context.Context, c *client.HTTPClient, header http.Header, seriesID, episodeID int64, title, dir string, progress func(int, int)) (novelDocument, error) {
	episode, err := api.GetNovelEpisode(ctx, c, seriesID, episodeID, header)
	if err != nil {
		return novelDocument{}, err
	}
	if episode.ID != episodeID || (!episode.Free && !episode.Unlocked) {
		return novelDocument{}, errors.New("chapter is locked or unavailable")
	}
	if len(episode.Contents) == 0 || len(episode.Contents) > 8 {
		return novelDocument{}, errors.New("novel chapter has no supported content")
	}
	pages, sources := make([]string, 0, len(episode.Contents)), make([]string, 0, len(episode.Contents))
	for i, part := range episode.Contents {
		content, err := novel.Fetch(ctx, part.FileURL)
		if err != nil {
			return novelDocument{}, err
		}
		pages, sources = append(pages, string(content)), append(sources, part.FileURL)
		if progress != nil {
			progress(i+1, len(episode.Contents))
		}
	}
	doc, err := assembleNovel(ctx, pages, sources, dir)
	if err != nil {
		return novelDocument{}, err
	}
	doc.HTML = novelHTML(title, doc.HTML)
	return doc, os.WriteFile(filepath.Join(dir, "chapter.html"), []byte(doc.HTML), 0644)
}

func (a *app) runNovelBundleTask(ctx context.Context, item task) (string, error) {
	if len(item.Chapters) == 0 || item.Format == "raw" {
		return "", errors.New("invalid combined novel task")
	}
	seriesDir := filepath.Join(item.Directory, download.Slugify(item.SeriesTitle))
	if err := os.MkdirAll(seriesDir, 0755); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(seriesDir, ".novel-bundle-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	header, err := headerFor(item.Account)
	if err != nil {
		return "", err
	}
	c := client.NewHTTPClient()
	if err := a.updateTask(item.ID, true, func(t *task) {
		t.State = "downloading"
		t.Message = "Downloading chapters"
		t.ChaptersTotal = len(item.Chapters)
	}); err != nil {
		return "", err
	}
	chapters := make([]novelExportChapter, 0, len(item.Chapters))
	for i, chapter := range item.Chapters {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if !chapter.Free && !chapter.Unlocked {
			return "", errors.New("chapter is locked or unavailable")
		}
		dir := filepath.Join(staging, fmt.Sprintf("chapter-%04d", i+1))
		if err := os.Mkdir(dir, 0755); err != nil {
			return "", err
		}
		_, err := fetchNovelDocument(ctx, c, header, item.SeriesID, chapter.ID, chapter.Title, dir, nil)
		if err != nil {
			return "", fmt.Errorf("chapter %d: %w", chapter.Scene, err)
		}
		chapters = append(chapters, novelExportChapter{Title: chapter.Title, Dir: dir})
		a.updateTask(item.ID, false, func(t *task) { t.ChaptersDone = i + 1; t.ChaptersTotal = len(item.Chapters) })
	}
	if err := a.updateTask(item.ID, true, func(t *task) { t.State = "converting"; t.Message = "Converting to " + strings.ToUpper(item.Format) }); err != nil {
		return "", err
	}
	book := item.bookMetadata()
	book.Title = item.SeriesTitle
	book.EpisodeID = 0
	unit := "chapters"
	if len(chapters) == 1 {
		unit = "chapter"
	}
	base := download.SafeName(fmt.Sprintf("%s - %d %s", item.SeriesTitle, len(chapters), unit))
	final, err := exportNovelChapters(ctx, seriesDir, base, item.Format, chapters, book)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		os.Remove(final)
		return "", err
	}
	ids := make([]int64, 0, len(item.Chapters))
	for _, chapter := range item.Chapters {
		ids = append(ids, chapter.ID)
	}
	manifest := bundleManifest{File: filepath.Base(final), EpisodeIDs: ids}
	name := fmt.Sprintf(".bundle-%x.json", sha256.Sum256([]byte(manifest.File)))
	if err := saveJSONAtomic(filepath.Join(seriesDir, name), manifest); err != nil {
		os.Remove(final)
		return "", err
	}
	return final, nil
}

func (a *app) runNovelTask(ctx context.Context, item task) (string, error) {
	seriesDir := filepath.Join(item.Directory, download.Slugify(item.SeriesTitle))
	if err := os.MkdirAll(seriesDir, 0755); err != nil {
		return "", err
	}
	name := download.SafeName(item.EpisodeTitle)
	if name == "" {
		name = "Chapter " + strconv.FormatInt(item.Scene, 10)
	}
	folder := filepath.Join(seriesDir, fmt.Sprintf("%s [%d]", name, item.EpisodeID))
	if item.Format == "raw" {
		if _, err := os.Stat(filepath.Join(folder, ".novel")); err == nil {
			if _, err := readNovelDocument(folder); err == nil {
				return folder, nil
			}
		}
	}
	header, err := headerFor(item.Account)
	if err != nil {
		return "", err
	}
	c := client.NewHTTPClient()
	if err := a.updateTask(item.ID, true, func(t *task) { t.State = "downloading"; t.Message = "Downloading chapter content" }); err != nil {
		return "", err
	}
	staging, err := os.MkdirTemp(seriesDir, ".novel-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(staging)
	doc, err := fetchNovelDocument(ctx, c, header, item.SeriesID, item.EpisodeID, item.EpisodeTitle, staging, func(done, total int) {
		a.updateTask(item.ID, false, func(t *task) {
			t.ImagesDone = done
			t.ImagesTotal = total
			t.Message = "Downloading chapter content"
		})
	})
	if err != nil {
		return "", err
	}
	if item.Format == "raw" {
		if err := os.WriteFile(filepath.Join(staging, ".novel"), []byte("1"), 0644); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(staging, ".scene"), []byte(strconv.FormatInt(item.Scene, 10)), 0644); err != nil {
			return "", err
		}
		if err := saveJSONAtomic(filepath.Join(staging, ".chapter.json"), item.bookMetadata()); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(seriesDir, ".title"), []byte(item.SeriesTitle), 0644); err != nil {
			return "", err
		}
		book := item.bookMetadata()
		book.Title = item.SeriesTitle
		book.EpisodeID = 0
		if err := saveJSONAtomic(filepath.Join(seriesDir, ".series.json"), book); err != nil {
			return "", err
		}
		for suffix := 1; suffix <= 10000; suffix++ {
			target := folder
			if suffix > 1 {
				target = fmt.Sprintf("%s (%d)", folder, suffix)
			}
			if _, err := os.Stat(target); err == nil {
				continue
			} else if !os.IsNotExist(err) {
				return "", err
			}
			if err := os.Rename(staging, target); err != nil {
				return "", err
			}
			return target, nil
		}
		return "", errors.New("too many files with the same name")
	}
	if err := a.updateTask(item.ID, true, func(t *task) { t.State = "converting"; t.Message = "Converting to " + strings.ToUpper(item.Format) }); err != nil {
		return "", err
	}
	return exportNovelFile(ctx, seriesDir, filepath.Base(folder), item.Format, staging, doc, exportMetadata{Book: item.bookMetadata(), Template: item.FilenameTemplate, Filename: filenameData{SeriesName: item.SeriesTitle, SeriesID: item.SeriesID, ChapterNumber: item.Scene, ChapterID: item.EpisodeID, ChapterTitle: item.EpisodeTitle, ChapterName: filepath.Base(folder), CreatorName: item.Creator}})
}

func exportNovelFile(ctx context.Context, target, name, format, source string, doc novelDocument, metadata exportMetadata) (string, error) {
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
	return exportNovelChapters(ctx, target, base, format, []novelExportChapter{{Title: metadata.Book.Title, Dir: source, Doc: doc}}, metadata.Book)
}

func exportNovelChapters(ctx context.Context, target, base, format string, chapters []novelExportChapter, book bookMetadata) (string, error) {
	if len(chapters) == 0 || (format != "pdf" && format != "epub") {
		return "", errors.New("choose chapters and PDF or EPUB")
	}
	tmp, err := os.CreateTemp(target, ".export-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	writer := contextWriter{ctx: ctx, writer: &limitedWriter{writer: tmp, remaining: maxExportBytes}}
	if format == "pdf" {
		err = writeNovelPDFChapters(writer, chapters, book)
	} else {
		var cover *coverImage
		for _, coverURL := range []string{book.CoverURL, book.FallbackCoverURL} {
			if coverURL != "" && cover == nil {
				cover, _ = fetchCover(ctx, coverURL, nil)
			}
		}
		err = writeNovelEPUBChapters(writer, chapters, book, cover)
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
		if err := os.Link(tmp.Name(), final); err == nil {
			return final, nil
		} else if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", errors.New("too many files with the same name")
}

func writeNovelPDF(dst io.Writer, dir string, doc novelDocument, book bookMetadata) error {
	return writeNovelPDFChapters(dst, []novelExportChapter{{Title: book.Title, Dir: dir, Doc: doc}}, book)
}

func writeNovelPDFChapters(dst io.Writer, chapters []novelExportChapter, book bookMetadata) error {
	regular, err := novelFonts.ReadFile("fonts/DejaVuSerif.ttf")
	if err != nil {
		return err
	}
	bold, err := novelFonts.ReadFile("fonts/DejaVuSerif-Bold.ttf")
	if err != nil {
		return err
	}
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(20, 20, 20)
	pdf.SetAutoPageBreak(true, 20)
	pdf.AddUTF8FontFromBytes("DejaVu", "", regular)
	pdf.AddUTF8FontFromBytes("DejaVu", "B", bold)
	pdf.SetTitle(book.Title, true)
	pdf.SetAuthor(book.Creator, true)
	pdf.SetSubject(book.Description, true)
	pdf.SetKeywords(strings.Join(book.Keywords, ", "), true)
	for _, chapter := range chapters {
		if chapter.Doc.HTML == "" {
			chapter.Doc, err = readNovelDocument(chapter.Dir)
			if err != nil {
				return err
			}
		}
		pdf.AddPage()
		pdf.Bookmark(chapter.Title, 0, pdf.GetY())
		pdf.SetFont("DejaVu", "B", 17)
		pdf.MultiCell(170, 9, chapter.Title, "", "L", false)
		pdf.Ln(5)
		for _, block := range chapter.Doc.Blocks {
			if block.Image != "" {
				path := filepath.Join(chapter.Dir, filepath.Base(block.Image))
				file, err := os.Open(path)
				if err != nil {
					return err
				}
				config, _, err := image.DecodeConfig(file)
				file.Close()
				if err != nil {
					return err
				}
				width, height := 160.0, 160.0*float64(config.Height)/float64(config.Width)
				if height > 240 {
					width *= 240 / height
					height = 240
				}
				if pdf.GetY()+height > 275 {
					pdf.AddPage()
				}
				pdf.ImageOptions(path, 20, pdf.GetY(), width, height, false, fpdf.ImageOptions{}, 0, "")
				pdf.SetY(pdf.GetY() + height + 5)
				continue
			}
			if block.Text == "" {
				continue
			}
			size, spacing, style := 11.0, 6.5, ""
			if block.Level > 0 {
				size, spacing, style = 15.0, 8.0, "B"
			}
			pdf.SetFont("DejaVu", style, size)
			pdf.MultiCell(170, spacing, block.Text, "", "L", false)
			pdf.Ln(3)
		}
	}
	return pdf.Output(dst)
}

func writeNovelEPUB(dst io.Writer, dir string, doc novelDocument, book bookMetadata, cover *coverImage) error {
	return writeNovelEPUBChapters(dst, []novelExportChapter{{Title: book.Title, Dir: dir, Doc: doc}}, book, cover)
}

func writeNovelEPUBChapters(dst io.Writer, chapters []novelExportChapter, book bookMetadata, cover *coverImage) error {
	z := zip.NewWriter(dst)
	mime, err := z.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store})
	if err != nil {
		return err
	}
	if _, err := io.WriteString(mime, "application/epub+zip"); err != nil {
		return err
	}
	if err := zipText(z, "META-INF/container.xml", `<?xml version="1.0" encoding="UTF-8"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`); err != nil {
		return err
	}
	var manifest, spine, nav strings.Builder
	manifest.WriteString(`<item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>`)
	nav.WriteString(`<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>Contents</title></head><body><nav epub:type="toc" id="toc"><ol>`)
	for i, chapter := range chapters {
		if chapter.Doc.HTML == "" {
			chapter.Doc, err = readNovelDocument(chapter.Dir)
			if err != nil {
				return err
			}
		}
		name := fmt.Sprintf("chapter-%04d.xhtml", i+1)
		imagePrefix := fmt.Sprintf("chapter-%04d/", i+1)
		parsed, err := htmlparse.Parse(strings.NewReader(chapter.Doc.HTML))
		if err != nil {
			return err
		}
		viewport := findViewport(parsed)
		if viewport == nil {
			return errors.New("novel chapter is invalid")
		}
		var body strings.Builder
		for child := viewport.FirstChild; child != nil; child = child.NextSibling {
			if err := renderSavedXHTML(child, &body, imagePrefix); err != nil {
				return err
			}
		}
		xhtml := `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>` + xmlText(chapter.Title) + `</title><style>body{line-height:1.5}img{max-width:100%;height:auto}</style></head><body><h1>` + xmlText(chapter.Title) + `</h1>` + body.String() + `</body></html>`
		if err := zipText(z, "OEBPS/"+name, xhtml); err != nil {
			return err
		}
		fmt.Fprintf(&manifest, `<item id="chapter%d" href="%s" media-type="application/xhtml+xml"/>`, i, name)
		fmt.Fprintf(&spine, `<itemref idref="chapter%d"/>`, i)
		fmt.Fprintf(&nav, `<li><a href="%s">%s</a></li>`, name, xmlText(chapter.Title))
		for imageIndex, imageName := range chapter.Doc.Images {
			data, err := os.ReadFile(filepath.Join(chapter.Dir, filepath.Base(imageName)))
			if err != nil {
				return err
			}
			mime := map[string]string{".jpg": "image/jpeg", ".png": "image/png", ".gif": "image/gif"}[strings.ToLower(filepath.Ext(imageName))]
			if mime == "" {
				return errors.New("unsupported saved novel image")
			}
			resource := imagePrefix + filepath.Base(imageName)
			writer, err := z.Create("OEBPS/" + resource)
			if err != nil {
				return err
			}
			if _, err := writer.Write(data); err != nil {
				return err
			}
			fmt.Fprintf(&manifest, `<item id="image%d_%d" href="%s" media-type="%s"/>`, i, imageIndex, resource, mime)
		}
	}
	nav.WriteString(`</ol></nav></body></html>`)
	if err := zipText(z, "OEBPS/nav.xhtml", nav.String()); err != nil {
		return err
	}
	if cover != nil {
		name := "cover" + cover.Extension
		writer, err := z.Create("OEBPS/" + name)
		if err != nil {
			return err
		}
		if _, err := writer.Write(cover.Data); err != nil {
			return err
		}
		fmt.Fprintf(&manifest, `<item id="cover-image" href="%s" media-type="%s" properties="cover-image"/>`, name, cover.MediaType)
	}
	var identifier strings.Builder
	fmt.Fprintf(&identifier, "%d:%d", book.SeriesID, book.EpisodeID)
	for _, chapter := range chapters {
		identifier.WriteByte(0)
		identifier.WriteString(chapter.Title)
	}
	id := sha256.Sum256([]byte(identifier.String()))
	var fields strings.Builder
	fmt.Fprintf(&fields, `<dc:identifier id="bookid">urn:sha256:%s</dc:identifier><dc:title>%s</dc:title>`, hex.EncodeToString(id[:]), xmlText(book.Title))
	for _, field := range []struct{ tag, value string }{{"creator", book.Creator}, {"language", book.epubLanguage()}, {"description", book.Description}, {"publisher", book.Publisher}, {"date", book.Date}, {"source", book.Source}} {
		if field.value != "" {
			fmt.Fprintf(&fields, "<dc:%s>%s</dc:%s>", field.tag, xmlText(field.value), field.tag)
		}
	}
	for _, keyword := range book.Keywords {
		if strings.TrimSpace(keyword) != "" {
			fmt.Fprintf(&fields, `<dc:subject>%s</dc:subject>`, xmlText(keyword))
		}
	}
	if book.SeriesTitle != "" {
		fmt.Fprintf(&fields, `<meta property="belongs-to-collection" id="series">%s</meta><meta refines="#series" property="collection-type">series</meta>`, xmlText(book.SeriesTitle))
	}
	if cover != nil {
		fields.WriteString(`<meta name="cover" content="cover-image"/>`)
	}
	fmt.Fprintf(&fields, `<meta property="dcterms:modified">%s</meta>`, time.Now().UTC().Format("2006-01-02T15:04:05Z"))
	opf := `<?xml version="1.0" encoding="UTF-8"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="bookid"><metadata xmlns:dc="http://purl.org/dc/elements/1.1/">` + fields.String() + `</metadata><manifest>` + manifest.String() + `</manifest><spine>` + spine.String() + `</spine></package>`
	if err := zipText(z, "OEBPS/content.opf", opf); err != nil {
		return err
	}
	return z.Close()
}

func renderSavedXHTML(node *htmlparse.Node, out *strings.Builder, imagePrefix string) error {
	if node.Type == htmlparse.TextNode {
		out.WriteString(html.EscapeString(node.Data))
		return nil
	}
	if node.Type != htmlparse.ElementNode {
		return nil
	}
	tag := strings.ToLower(node.Data)
	if !novelTags[tag] {
		return nil
	}
	if tag == "img" {
		var name, alt string
		for _, attr := range node.Attr {
			if attr.Key == "src" {
				name = filepath.Base(attr.Val)
			}
			if attr.Key == "alt" {
				alt = attr.Val
			}
		}
		if name == "" || name == "." {
			return errors.New("saved novel image is invalid")
		}
		fmt.Fprintf(out, `<img src="%s" alt="%s"/>`, html.EscapeString(imagePrefix+name), html.EscapeString(alt))
		return nil
	}
	if tag == "br" || tag == "hr" {
		fmt.Fprintf(out, "<%s/>", tag)
		return nil
	}
	fmt.Fprintf(out, "<%s>", tag)
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if err := renderSavedXHTML(child, out, imagePrefix); err != nil {
			return err
		}
	}
	fmt.Fprintf(out, "</%s>", tag)
	return nil
}
