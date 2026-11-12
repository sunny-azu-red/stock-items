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
// desktop sees every vehicle). It is never a hidden filter: while it is
// set, a list page (listPages) whose address asks for anything but that
// vehicle (with General on Stock and Activity) gets a 303 to the address
// for it, so the address always says what the page shows, and the bar and
// the page never disagree. New services and kits are for it (otherWork).
// Exports follow their address only. See docs/design.md, "Working on a
// vehicle".
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
// garbage) is cleared, and so is one while a single vehicle is active.
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
	if countActive(vehicles) < 2 {
		// Nothing to switch with one active vehicle: stop, so a choice made
		// months ago doesn't come back when a second one is added.
		http.SetCookie(w, &http.Cookie{Name: workingCookie, Path: "/", MaxAge: -1, SameSite: http.SameSiteLaxMode})
		return nil, ""
	}
	return v, strconv.FormatInt(id, 10)
}

// vehicleParam reads a list page's ?v=, 0 for none (or not an id).
func vehicleParam(q url.Values) int64 {
	if p := parseOptID(q.Get("v")); p != nil {
		return *p
	}
	return 0
}

// setVehicleParam writes a list page's vehicle into q (v=id), nothing for 0.
func setVehicleParam(q url.Values, id int64) {
	if id != 0 {
		q.Set("v", strconv.FormatInt(id, 10))
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

// otherWork is the form error for new work (a service, a kit) for another
// vehicle than the one worked on, "" when it is fine: while a vehicle is
// worked on, new work is for it (like "archived means no new work"). A
// form opened before a switch comes back with this. what is "log this
// service" or "add this kit"; fixed reports that the form can't choose the vehicle (a kit's
// service), so the only way is to work on the other one.
func (s *Server) otherWork(r *http.Request, vehicleID int64, what string, fixed bool) (string, error) {
	vehicles, err := s.store.ListVehicles(r.Context())
	if err != nil {
		return "", err
	}
	working := workingOn(r, vehicles)
	if working == 0 || vehicleID == 0 || vehicleID == working {
		return "", nil
	}
	name := func(id int64) string {
		for _, v := range vehicles {
			if v.ID == id {
				return v.Name
			}
		}
		return "that vehicle"
	}
	if fixed {
		return "You're working on " + name(working) + ": work on " + name(vehicleID) + " first to " + what, nil
	}
	return "You're working on " + name(working) + ": choose it, or work on " + name(vehicleID) + " first to " + what, nil
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
