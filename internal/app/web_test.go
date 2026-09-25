package app

import (
	"regexp"
	"strings"
	"testing"
)

func TestWebControlsReferencedByScriptExist(t *testing.T) {
	html, err := webFiles.ReadFile("web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	script, err := webFiles.ReadFile("web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	selectors := regexp.MustCompile(`\$\('#[a-zA-Z0-9_-]+`)
	for _, match := range selectors.FindAllString(string(script), -1) {
		id := strings.TrimPrefix(match, "$('#")
		if !strings.Contains(string(html), `id="`+id+`"`) {
			t.Errorf("script binds missing control #%s", id)
		}
	}
}
