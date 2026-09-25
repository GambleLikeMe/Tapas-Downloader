package app

import (
	download "api-scraper/internal/download"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

const defaultFilenameTemplate = "Episode {chapter_number}[{chapter_id}]"

type filenameData struct {
	SeriesName    string
	SeriesID      int64
	ChapterNumber int64
	ChapterID     int64
	ChapterTitle  string
	ChapterName   string
	CreatorName   string
}

func filenameBase(template string, data filenameData) (string, error) {
	if template == "" {
		template = defaultFilenameTemplate
	}
	if len(template) > 512 {
		return "", errors.New("filename template is too long")
	}
	values := map[string]string{
		"series_name":    data.SeriesName,
		"series_id":      numberOrEmpty(data.SeriesID),
		"chapter_number": numberOrEmpty(data.ChapterNumber),
		"chapter_id":     numberOrEmpty(data.ChapterID),
		"chapter_title":  data.ChapterTitle,
		"chapter_name":   data.ChapterName,
		"creator_name":   data.CreatorName,
	}
	var name strings.Builder
	for i := 0; i < len(template); {
		switch template[i] {
		case '{':
			end := strings.IndexByte(template[i+1:], '}')
			if end < 0 {
				return "", errors.New("missing } in filename template")
			}
			key := template[i+1 : i+1+end]
			value, ok := values[key]
			if !ok {
				return "", fmt.Errorf("unknown variable: {%s}", key)
			}
			name.WriteString(value)
			i += end + 2
		case '}':
			return "", errors.New("unexpected } in filename template")
		default:
			name.WriteByte(template[i])
			i++
		}
	}
	return download.SafeName(name.String()), nil
}

func numberOrEmpty(number int64) string {
	if number <= 0 {
		return ""
	}
	return strconv.FormatInt(number, 10)
}

func validateFilenameTemplate(template string) error {
	if strings.TrimSpace(template) == "" {
		return errors.New("enter a filename template")
	}
	_, err := filenameBase(template, filenameData{})
	return err
}

func (a *app) filenamePreview(w http.ResponseWriter, r *http.Request) {
	template := r.URL.Query().Get("template")
	if err := validateFilenameTemplate(template); err != nil {
		fail(w, 400, err)
		return
	}
	base, err := filenameBase(template, filenameData{SeriesName: "My Comic", SeriesID: 12345, ChapterNumber: 10, ChapterID: 123456, ChapterTitle: "10", ChapterName: "Episode 10 [123456]", CreatorName: "Creator"})
	if err != nil {
		fail(w, 400, err)
		return
	}
	send(w, map[string]string{"preview": base + ".pdf"})
}
