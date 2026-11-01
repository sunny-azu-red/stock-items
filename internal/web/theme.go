package web

import "net/http"

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
