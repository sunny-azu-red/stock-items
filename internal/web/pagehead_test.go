package web

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"
)

// TestPageHeads keeps the page header in one place (docs/design.md#7-page-headers):
// layout.html writes it, and a page only defines its head-* blocks. Pages
// once wrote it by hand, and nine of them wrote the line as a <p>, whose
// margin put 30px under their header instead of 18.
func TestPageHeads(t *testing.T) {
	tmpl, err := parseTemplates((&Server{}).funcs())
	if err != nil {
		t.Fatal(err)
	}
	pages, err := fs.Glob(templateFS, "templates/pages/*.html")
	if err != nil || len(pages) == 0 {
		t.Fatalf("no page templates: %v", err)
	}
	// What only the layout writes: the header's classes (whole names, anywhere
	// in the list) and the heading.
	own := regexp.MustCompile(`class="(?:[^"]*\s)?(?:page-head|page-title|vehicle-hero|sub)(?:\s[^"]*)?"|<h1\b`)
	for _, p := range pages {
		name := strings.TrimSuffix(path.Base(p), ".html")
		if tmpl.pages[name].Lookup("head-title") == nil {
			t.Errorf("%s: no {{define \"head-title\"}} (the heading; head-line, head-actions and head-lead are optional)", p)
		}
		b, err := fs.ReadFile(templateFS, p)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range own.FindAllString(string(b), -1) {
			t.Errorf("%s: %q: the layout writes the page header; define head-title, head-line, head-actions or head-lead instead", p, m)
		}
	}
	partials, err := fs.Glob(templateFS, "templates/partials/*.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range partials {
		b, err := fs.ReadFile(templateFS, p)
		if err != nil {
			t.Fatal(err)
		}
		if m := own.FindString(string(b)); m != "" {
			t.Errorf("%s: %q: the layout writes the page header", p, m)
		}
	}
}
