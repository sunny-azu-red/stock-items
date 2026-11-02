package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestVocabulary keeps the words Pitbox settled on (docs/conventions.md,
// "Words"): one name for each thing, in the pages, the messages, the code's
// comments and the docs. Each retired form says what to write instead.
func TestVocabulary(t *testing.T) {
	retired := []struct {
		re      string
		use     string
		uiOnly  bool   // templates only (not Go, JavaScript or docs)
		allowIn string // a file where it is right
	}{
		{re: `\b[Oo]n stock\b`, use: `"in stock"`},
		{re: `\bCorrection\b`, use: `"Counted" (the kind's label; "count" in a sentence)`},
		{re: `Initial stock`, use: `"Starting stock"`},
		{re: `\b[1-9][0-9]*x\b`, use: `"N pcs" (FormatQuantity)`},
		{re: `>\s*Everything\s*<|"Everything"|Everything →`, use: `"All types", "All activity →"`},
		{re: `\bTo buy\b`, use: `"Shopping list", or "N suggested →"`},
		{re: `\bNew (item|vehicle|service kit|kit)\b`, use: `"Add item", "Add vehicle", "Add kit"`},
		{re: `Create a kit`, use: `"Add kit"`},
		{re: `Activity log`, use: `"Activity"`},
		{re: `Save changes`, use: `"Save <thing>"`},
		{re: `placeholder="optional"|\(optional\)</h2>`, use: "a red star on required fields instead", uiOnly: true},
		{re: `data-confirm`, use: "nothing: it can be undone. Only permanent deletes ask", uiOnly: true, allowIn: "trash.html"},
		{re: `<noscript`, use: "nothing: JavaScript is assumed", uiOnly: true},
	}
	// A link's text starts with a capital, also mid-sentence.
	lowerClear := regexp.MustCompile(`\bclear filters\b`)

	var files []string
	add := func(pattern string) {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range matches {
			if !strings.HasSuffix(m, "_test.go") {
				files = append(files, m)
			}
		}
	}
	add("templates/*.html")
	add("templates/*/*.html")
	add("static/app.js")
	add("*.go")
	add("../stock/*.go")
	add("../store/*.go")
	add("../../docs/user-guide.md")
	add("../../README.md")
	if len(files) < 30 {
		t.Fatalf("found only %d files to check", len(files))
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		isTemplate := strings.HasSuffix(f, ".html")
		for _, r := range retired {
			if (r.uiOnly && !isTemplate) || filepath.Base(f) == r.allowIn {
				continue
			}
			re := regexp.MustCompile(r.re)
			for _, loc := range re.FindAllStringIndex(text, -1) {
				line := strings.Count(text[:loc[0]], "\n") + 1
				t.Errorf("%s:%d: %q: write %s", f, line, text[loc[0]:loc[1]], r.use)
			}
		}
		if isTemplate {
			for _, loc := range lowerClear.FindAllStringIndex(text, -1) {
				t.Errorf("%s:%d: %q: write \"Clear filters\"", f, strings.Count(text[:loc[0]], "\n")+1, text[loc[0]:loc[1]])
			}
		}
	}
}
