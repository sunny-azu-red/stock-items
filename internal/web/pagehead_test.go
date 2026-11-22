package web

import (
	"context"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"testing"

	"github.com/sunny-azu-red/stock-items/internal/store"
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
	// A button of the block: a link or a button, or the form holding one
	// (its button left out: the form's tag holds data-more), wherever it
	// starts, after an {{if}} on the same line too.
	inForm := regexp.MustCompile(`(?s)(<form\b[^>]*>).*?</form>`)
	button := regexp.MustCompile(`<(?:a class="btn|button class="btn|form class="inline-form)[^>]*>(?: btn-primary| command="show-modal")*`)
	for _, p := range pages {
		b, err := fs.ReadFile(templateFS, p)
		if err != nil {
			t.Fatal(err)
		}
		m := block.FindSubmatch(b)
		if m == nil {
			continue
		}
		// A form keeps whether its button is the main one or opens a dialog.
		src := inForm.ReplaceAllStringFunc(string(m[1]), func(f string) string {
			tag := inForm.FindStringSubmatch(f)[1]
			for _, mark := range []string{"btn-primary", `command="show-modal"`} {
				if strings.Contains(f, mark) {
					tag += " " + mark
				}
			}
			return tag
		})
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
// into it, one main button at most, and Archive, Delete or Restore after
// every other button.
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
		// The row's buttons in order (⋯ and its menu after them), each
		// lifecycle (archive, delete, restore) or not.
		row, _, _ := strings.Cut(actions, `class="btn more-btn"`)
		lifecycle := false
		for _, b := range headButton.FindAllString(row, -1) {
			isEnd := lifecycleAction.MatchString(b)
			if lifecycle && !isEnd {
				t.Errorf("%s: %s after Archive, Delete or Restore:\n%s", c.path, b, actions)
			}
			lifecycle = lifecycle || isEnd
		}
	}
	// An archived item no longer in use: Restore, then Delete.
	pads := itemID(t, a.store, "Rear Brake Pads")
	if _, err := a.store.ArchiveItem(context.Background(), pads, cur(t, a.store, store.WhatItem, pads)); err != nil {
		t.Fatal(err)
	}
	actions := headActions(t, a.get(item))
	if r, d := strings.Index(actions, `/restore"`), strings.Index(actions, `/delete"`); r < 0 || d < r || strings.Contains(actions[d:], `<a class="btn`) {
		t.Errorf("archived item: want Edit, Duplicate, Restore, Delete:\n%s", actions)
	}
}

var (
	headButton      = regexp.MustCompile(`<(?:a class="btn[^"]*"|form class="inline-form")[^>]*>`)
	lifecycleAction = regexp.MustCompile(`action="[^"]*/(?:archive|delete|restore)"`)
)
