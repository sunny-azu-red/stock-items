package web

import (
	"io/fs"
	"net/http"
	"net/url"
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

// TestHeadActions keeps the header's buttons to the rule
// (docs/design.md#header-buttons): no back button; in a page's head-actions
// with two buttons or more, each one but the main (tinted) one and one
// that opens a dialog says when it may go into ⋯ (data-more).
func TestHeadActions(t *testing.T) {
	pages, err := fs.Glob(templateFS, "templates/pages/*.html")
	if err != nil {
		t.Fatal(err)
	}
	// The block runs to the next one (its {{if}}s have {{end}}s of their own).
	block := regexp.MustCompile(`(?s)\{\{define "head-actions"\}\}(.*?)(?:\{\{define |\z)`)
	// A button at the top of the block: a link or a lone button, or the form
	// holding one; its first line is enough (the form's tag holds data-more).
	button := regexp.MustCompile(`(?m)^<(?:a class="btn|button class="btn|form class="inline-form)[^\n]*`)
	for _, p := range pages {
		b, err := fs.ReadFile(templateFS, p)
		if err != nil {
			t.Fatal(err)
		}
		m := block.FindSubmatch(b)
		if m == nil {
			continue
		}
		src := string(m[1])
		if strings.Contains(src, "btn-ghost") || strings.Contains(src, `"arrow-left"`) {
			t.Errorf("%s: a back button in the header: the nav and the line's chips lead up, the browser's Back goes back", p)
		}
		buttons := button.FindAllString(src, -1)
		if len(buttons) < 2 {
			continue
		}
		for _, btn := range buttons {
			if !strings.Contains(btn, "data-more=") && !strings.Contains(btn, "btn-primary") && !strings.Contains(btn, `command="show-modal"`) {
				t.Errorf("%s: %s…: give it data-more (1 Archive or Delete, 2 Duplicate, 3 Edit, 4 other actions)", p, truncateLine(btn))
			}
		}
	}
}

func truncateLine(s string) string {
	if len(s) > 90 {
		return s[:90]
	}
	return s
}

// TestHeadMore: the header has ⋯ and its menu only when a button may go
// into it, one main button at most, and Archive, Delete or Restore last.
func TestHeadMore(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	rec := a.post("/kits", url.Values{"vehicle_id": {itoa(k390)}, "name": {"Chain"}})
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	item := "/items/" + itoa(itemID(t, a.store, "Rear Brake Pads"))
	for _, c := range []struct {
		path string
		more bool
	}{
		{"/", true}, {"/stock", false}, {"/services", true}, {"/vehicles", false}, {"/activity", false},
		{"/shopping", false}, {"/trash", false}, {"/vehicles/" + itoa(k390), true}, {item, true}, {kit, true},
	} {
		rec := a.get(c.path)
		expect(t, rec, http.StatusOK)
		actions := headActions(t, rec)
		if got := strings.Contains(actions, `class="btn more-btn"`); got != c.more {
			t.Errorf("%s: ⋯ %v, want %v:\n%s", c.path, got, c.more, actions)
		}
		if n := strings.Count(actions, "btn-primary"); n > 1 {
			t.Errorf("%s: %d main buttons:\n%s", c.path, n, actions)
		}
		last := strings.LastIndex(actions, `<a class="btn`)
		for _, end := range []string{`/archive"`, `/delete"`, `/restore"`} {
			if i := strings.Index(actions, end); i >= 0 && i < last {
				t.Errorf("%s: %s before another button:\n%s", c.path, end, actions)
			}
		}
	}
}
