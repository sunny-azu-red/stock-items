package web

import (
	"net/http"
	"regexp"
	"strconv"
)

// The colour theme is a cookie, not a database setting: it belongs to the
// device (a phone in the garage may want dark, the desktop light). No cookie
// means "follow the system".
const themeCookie = "theme"

// themeFrom returns "light", "dark" or "" (follow the system).
func themeFrom(r *http.Request) string {
	c, err := r.Cookie(themeCookie)
	if err != nil {
		return ""
	}
	if c.Value == "light" || c.Value == "dark" {
		return c.Value
	}
	return ""
}

// setTheme stores the theme choice. app.js applies it on the spot and posts
// with HX-Request, which gets a bare 204; plain form posts go back.
func (s *Server) setTheme(w http.ResponseWriter, r *http.Request) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	// Not HttpOnly: app.js reads it to re-apply the theme on a page that
	// Back restores from the browser's cache. It holds nothing secret.
	c := &http.Cookie{Name: themeCookie, Path: "/", SameSite: http.SameSiteLaxMode}
	switch v := r.PostFormValue("theme"); v {
	case "light", "dark":
		c.Value, c.MaxAge = v, 400*24*60*60 // the longest browsers keep a cookie
	default:
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
	if isHTMX(r) {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	http.Redirect(w, r, s.back(r, "/"), http.StatusSeeOther) //nolint:gosec // back returns only local paths
	return nil
}

// The accent colour is a cookie too, for the same reason: a preference of
// the device, set in the Appearance panel and tried out freely. It holds a
// hue (0-359 degrees) and an intensity (0-100: 0 is grey, 100 the full
// colour); app.css works every accent tone out from these two, at
// lightnesses that keep its text readable whatever they are. No cookie means
// the default, Indigo.
const accentCookie = "accent"

// Accent is a chosen accent colour.
type Accent struct {
	Hue       int // degrees on the colour wheel, 0-359
	Intensity int // 0 (grey) to 100 (full colour)
}

// The cookie's form: "hue-intensity", e.g. "280-100". Plain digits and a
// dash, which a cookie holds without quotes.
var accentValue = regexp.MustCompile(`^(\d{1,3})-(\d{1,3})$`)

// String is the accent in the cookie's form, without leading zeros.
func (a Accent) String() string { return strconv.Itoa(a.Hue) + "-" + strconv.Itoa(a.Intensity) }

// parseAccent reads "hue-intensity", or reports false for anything else.
func parseAccent(v string) (Accent, bool) {
	m := accentValue.FindStringSubmatch(v)
	if m == nil {
		return Accent{}, false
	}
	h, _ := strconv.Atoi(m[1])
	i, _ := strconv.Atoi(m[2])
	if h > 359 || i > 100 {
		return Accent{}, false
	}
	return Accent{Hue: h, Intensity: i}, true
}

// accentFrom returns the accent chosen on this device, or nil for the
// default.
func accentFrom(r *http.Request) *Accent {
	c, err := r.Cookie(accentCookie)
	if err != nil {
		return nil
	}
	a, ok := parseAccent(c.Value)
	if !ok {
		return nil
	}
	return &a
}

// setAccent stores the accent chosen in the Appearance panel ("hue" and
// "intensity", or a preset's "accent"), or forgets it ("reset", the
// default itself, or values out of range), so the default comes back.
// Like setTheme: app.js shows it on the spot and posts with HX-Request (a
// bare 204); a plain form post goes back.
func (s *Server) setAccent(w http.ResponseWriter, r *http.Request) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	// Not HttpOnly: app.js reads it to re-apply the accent on a page that
	// Back restores from the browser's cache. It holds nothing secret.
	c := &http.Cookie{Name: accentCookie, Path: "/", SameSite: http.SameSiteLaxMode, MaxAge: -1}
	// A preset posts "accent" (its "hue-intensity"); the sliders post the two.
	v := r.PostFormValue("accent")
	if v == "" {
		v = r.PostFormValue("hue") + "-" + r.PostFormValue("intensity")
	}
	if r.PostFormValue("reset") == "" {
		if a, ok := parseAccent(v); ok && a != defaultAccent {
			c.Value, c.MaxAge = a.String(), 400*24*60*60 // the longest browsers keep a cookie
		}
	}
	http.SetCookie(w, c)
	if isHTMX(r) {
		w.WriteHeader(http.StatusNoContent)
		return nil
	}
	http.Redirect(w, r, s.back(r, "/"), http.StatusSeeOther) //nolint:gosec // back returns only local paths
	return nil
}

// AccentPreset is one of the Appearance panel's ready-made accents.
type AccentPreset struct {
	Name string
	Accent
}

// accentPresets are the accents offered as swatches: Indigo (the default)
// first, then those from the design's shortlist, Graphite last (no colour).
func accentPresets() []AccentPreset {
	return []AccentPreset{
		{"Indigo", defaultAccent},
		{"Cobalt", Accent{Hue: 258, Intensity: 87}},
		{"Ocean", Accent{Hue: 228, Intensity: 50}},
		{"Plum", Accent{Hue: 332, Intensity: 74}},
		{"Raspberry", Accent{Hue: 2, Intensity: 97}},
		{"Graphite", Accent{Hue: 280, Intensity: 0}},
	}
}

// defaultAccent is what app.css shows without a cookie.
var defaultAccent = Accent{Hue: 280, Intensity: 100}
