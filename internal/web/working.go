package web

import (
	"net/http"
	"net/url"
	"slices"
	"strconv"

	"github.com/sunny-azu-red/stock-items/internal/stock"
)

// Working on a vehicle is a cookie, like the theme: it belongs to the
// device (the phone in the garage works on the bike on the stand, the
// desktop sees every vehicle). It is never a hidden filter. It decides
// where a list page with no vehicle in its address goes (a 303 to the
// address with the vehicle added); the address decides what a page shows,
// and v=all says "every vehicle" on purpose. See docs/design.md, "Working
// on a vehicle".
const workingCookie = "working_on"

// workingMaxAge is the longest browsers keep a cookie.
const workingMaxAge = 400 * 24 * 60 * 60

// listPages are the pages that follow the vehicle being worked on.
var listPages = []string{"/", "/stock", "/services", "/activity"}

// newPages are the add forms that start with the vehicle chosen.
var newPages = []string{"/items/new", "/services/new", "/kits/new"}

// workingCookieID is the vehicle id the cookie holds, or 0. Only the plain
// digits the server writes count ("+1" or " 1" don't): app.js compares the
// cookie with what the page was rendered for, and must read it the same.
func workingCookieID(r *http.Request) int64 {
	c, err := r.Cookie(workingCookie)
	if err != nil {
		return 0
	}
	id, err := strconv.ParseInt(c.Value, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != c.Value {
		return 0
	}
	return id
}

// workingCookieValue is the cookie's id as the page renders it for app.js,
// "" for none or one that isn't plain digits.
func workingCookieValue(r *http.Request) string {
	if id := workingCookieID(r); id != 0 {
		return strconv.FormatInt(id, 10)
	}
	return ""
}

// countActive is how many vehicles are not archived.
func countActive(vehicles []stock.Vehicle) int {
	n := 0
	for _, v := range vehicles {
		if !v.Archived() {
			n++
		}
	}
	return n
}

// activeVehicle is the active vehicle with that id, or nil.
func activeVehicle(vehicles []stock.Vehicle, id int64) *stock.Vehicle {
	for i := range vehicles {
		if vehicles[i].ID == id && !vehicles[i].Archived() {
			return &vehicles[i]
		}
	}
	return nil
}

// workingOn is the vehicle being worked on, or 0: the cookie's, when it
// names an active vehicle and there are two or more. With one, that one
// is every page's scope already (soleVehicle): there is nothing to switch.
func workingOn(r *http.Request, vehicles []stock.Vehicle) int64 {
	id := workingCookieID(r)
	if id == 0 || countActive(vehicles) < 2 || activeVehicle(vehicles, id) == nil {
		return 0
	}
	return id
}

// workingFor is the vehicle being worked on for the layout (nil for none)
// and the cookie as it stands after this response, which app.js compares
// on Back. A cookie that names no active vehicle (deleted, archived,
// garbage) is cleared; with a single active vehicle it is kept, for when a
// second one comes back.
func workingFor(w http.ResponseWriter, r *http.Request, vehicles []stock.Vehicle) (*stock.Vehicle, string) {
	if _, err := r.Cookie(workingCookie); err != nil {
		return nil, ""
	}
	id := workingCookieID(r)
	v := activeVehicle(vehicles, id)
	if v == nil {
		http.SetCookie(w, &http.Cookie{Name: workingCookie, Path: "/", MaxAge: -1, SameSite: http.SameSiteLaxMode})
		return nil, ""
	}
	value := strconv.FormatInt(id, 10)
	if countActive(vehicles) < 2 {
		return nil, value
	}
	return v, value
}

// parseVehicleParam reads a list page's ?v=: an id (chosen), "all" (every
// vehicle, chosen on purpose: id 0), or nothing (not chosen).
func parseVehicleParam(q url.Values) (id int64, chosen bool) {
	if !q.Has("v") {
		return 0, false
	}
	if q.Get("v") == "all" {
		return 0, true
	}
	if p := parseOptID(q.Get("v")); p != nil {
		return *p, true
	}
	return 0, false
}

// setVehicleParam writes a list page's vehicle into q: v=id, or for every
// vehicle v=all while one is worked on (without it the page would go to
// that one), and nothing otherwise.
func setVehicleParam(q url.Values, id, working int64) {
	switch {
	case id != 0:
		q.Set("v", strconv.FormatInt(id, 10))
	case working != 0:
		q.Set("v", "all")
	}
}

// unaddressed drops the vehicle from a list page's or an add form's
// address, so after switching it shows the vehicle now worked on (or all).
func unaddressed(path string) string {
	u, err := url.Parse(path)
	if err != nil {
		return path
	}
	q := u.Query()
	switch {
	case slices.Contains(listPages, u.Path):
		for _, k := range []string{"v", "general", "match", "page"} {
			q.Del(k)
		}
	case slices.Contains(newPages, u.Path):
		q.Del("vehicle")
	default:
		return path
	}
	return joinQuery(u.Path, q.Encode())
}

// working is the vehicle being worked on (0 for none), for a handler that
// hasn't loaded the vehicles.
func (s *Server) working(r *http.Request) (int64, error) {
	vehicles, err := s.store.ListVehicles(r.Context())
	if err != nil {
		return 0, err
	}
	return workingOn(r, vehicles), nil
}

// setWorkingOn starts working on a vehicle (vehicle=ID) or stops (empty),
// then goes back to the page, which now shows the new choice.
func (s *Server) setWorkingOn(w http.ResponseWriter, r *http.Request) error {
	if err := parseForm(w, r); err != nil {
		return err
	}
	// Not HttpOnly: app.js reads it to bring a page that Back restores up
	// to date. It holds nothing secret.
	c := &http.Cookie{Name: workingCookie, Path: "/", SameSite: http.SameSiteLaxMode, MaxAge: -1}
	if id := parseOptID(r.PostFormValue("vehicle")); id != nil {
		c.Value, c.MaxAge = strconv.FormatInt(*id, 10), workingMaxAge
	}
	http.SetCookie(w, c)
	http.Redirect(w, r, unaddressed(s.back(r, "/")), http.StatusSeeOther) //nolint:gosec // back returns only local paths
	return nil
}
