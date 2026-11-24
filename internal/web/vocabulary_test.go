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
		unless  string // a match that contains it is right
	}{
		{re: `\b[Oo]n stock\b`, use: `"in stock"`},
		{re: `in stock: \{\{|"in stock: "`, use: `the stock badge ({{template "stock-badge"}}): "3 pcs in stock"`},
		{re: `\b(Correction|Counted)\b`, use: `"Adjusted" (the kind's label; "adjustment" in a sentence)`},
		{re: `Initial stock`, use: `"Starting stock"`},
		{re: `\b[1-9][0-9]*x\b`, use: `"N pcs" (FormatQuantity)`},
		{re: `>\s*Everything\s*<|"Everything"|Everything →`, use: `"All types", "All activity →"`},
		{re: `\bTo buy\b`, use: `"Shopping list", or "N suggested →"`},
		{re: `\bNew (item|vehicle|service kit|kit)\b`, use: `"Add item", "Add vehicle", "Add kit"`},
		{re: `Create a kit|Create a (<strong>)?service kit`, use: `"Add kit" ("Add a kit" in a sentence)`},
		{re: `[Dd]o this service`, use: `"Log service" (the kit page's button too)`},
		{re: `take parts from stock|Delete &amp; return parts|Delete & return parts`, use: `"Save service" and "Delete": the header line, Delete's tooltip and the message say what happens to the parts`},
		{re: `Something went wrong`, use: `what happened and what to do: "Pitbox hit an error. Try again."`},
		{re: `\bService log\b`, use: `"Service history"`},
		{re: `Running low`, use: `"Low stock"`},
		{re: `Below minimum of`, use: `"Below the minimum of" (the stock badge's words)`},
		{re: `"Short for "\s*\+`, use: `stock.ShortNote: "Short for the Air Filter kit" (what needs it; its vehicle is a chip, never text)`},
		{re: `since you opened this`, use: `"since you opened it"`},
		{re: `aria-label="Kind"`, use: `"Type"`, uiOnly: true},
		{re: `Activity log`, use: `"Activity"`},
		{re: `Save changes`, use: `"Save <thing>"`},
		{re: `\bparts? (is |are )?short\b|\b\d+ short\b|"part"\}\} short`, use: `"missing": "1 part missing", "2 parts are missing from stock" (only a shopping reason says "Short for the Air Filter kit")`},
		{re: `Add missing to (the )?shopping list`, use: `"Add missing" (a parts card's button, and the service form's; the cart icon and its tooltip say where)`},
		{re: `\bWhat was done\b`, use: `"Work performed"`},
		{re: `\b([Ii]n|[Hh]as( a)?|[Oo]ut of) warranty\b|[Ww]arranty (expired|active|valid)\b`, use: `"Under warranty", "Warranty ends soon", "Warranty ended" (the warranty badge's words)`},
		{re: `Other items`, use: `"General": an item list heads a vehicle's items "For KTM 390", then "General" (docs/design.md, Item lists)`},
		{re: `[Ff]ocus mode|My [Gg]arage|>\s*Show all\s*<|Stop working on`, use: `"Working on X" (the bar), "Work on" (the chips), "All vehicles" (to stop)`},
		{re: `placeholder="optional"|\(optional\)</h2>`, use: "a red star on required fields instead", uiOnly: true},
		{re: `data-confirm`, use: `the "confirm-dialog" partial (a <dialog> opened with commandfor)`, uiOnly: true},
		// Asking first; the photo's viewer (item_show.html) asks nothing.
		{re: `<button[^>]*command="show-modal"`, use: "nothing: it can be undone. Only permanent deletes ask", uiOnly: true, allowIn: "trash.html", unless: `commandfor="photo-view"`},
		{re: `>\s*(Something new|Item in stock)\s*<`, use: `"Item": one field that suggests your stock and takes anything else as something new`, uiOnly: true},
		{re: `<noscript`, use: "nothing: JavaScript is assumed", uiOnly: true},
		{re: `[Ii]ntermediate schedule|services in between|[Hh]alf-way|[Hh]alf service`, use: `"intermediate services" (a kit's 1 or 2 between full ones; "Intermediate" for the tier; "1, halfway" the option)`},
		{re: `style="[^"{]*[0-9](px|rem|em)\b`, use: "a class in app.css (whole pixels, no lengths inline)", uiOnly: true},
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
				if strings.HasPrefix(text[loc[0]:], "Counted in") {
					continue // the item form's unit label
				}
				if r.unless != "" && strings.Contains(text[loc[0]:loc[1]], r.unless) {
					continue
				}
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
