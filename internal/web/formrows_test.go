package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// templateTags matches <template> elements: their rows are copies to add,
// not part of the form's list.
var templateTags = regexp.MustCompile(`(?s)<template\b.*?</template>`)

// listRows counts the rows of a repeating list on a page, and the empty
// ones (no amount, or no SKU code, typed).
func listRows(body, field string) (rows, empty int) {
	body = templateTags.ReplaceAllString(body, "")
	return strings.Count(body, `name="`+field+`" value="`), strings.Count(body, `name="`+field+`" value=""`)
}

func TestRepeatingListsEndWithOneEmptyRow(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	pads := itemID(t, a.store, "Rear Brake")
	oil := itemID(t, a.store, "Silkolene")

	rec := a.post("/items", url.Values{
		"name": {"Spark Plug"}, "unit": {"piece"}, "quantity": {"2"},
		"sku_code": {"A1", "B2"}, "sku_kind": {"oem", "oem"},
	})
	expect(t, rec, http.StatusSeeOther)
	plug := rec.Header().Get("Location")
	rec = a.post("/kits", url.Values{
		"vehicle_id": {itoa(k390)}, "name": {"Oil service"},
		"line_item": {itoa(oil), itoa(pads)}, "line_qty": {"100 ml", "1"},
	})
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	rec = a.post("/services", url.Values{
		"vehicle_id": {itoa(k390)}, "title": {"Brakes"}, "performed_on": {"2026-10-20"},
		"part_item": {itoa(pads)}, "part_qty": {"1"},
	})
	expect(t, rec, http.StatusSeeOther)
	svc := rec.Header().Get("Location")
	// Sent back with an error (no title), holding one typed part and one
	// empty row: the empty one isn't doubled.
	bad := a.post("/services", url.Values{
		"vehicle_id": {itoa(k390)}, "performed_on": {"2026-10-20"},
		"part_item": {itoa(oil), ""}, "part_qty": {"50 ml", ""},
	})
	expect(t, bad, http.StatusUnprocessableEntity)

	for _, tc := range []struct {
		name  string
		rec   func() string
		field string
		rows  int
	}{
		{"new item", func() string { return a.get("/items/new").Body.String() }, "sku_code", 1},
		{"edit item", func() string { return a.get(plug + "/edit").Body.String() }, "sku_code", 3},
		{"new kit", func() string { return a.get("/kits/new").Body.String() }, "line_qty", 1},
		{"new kit, intermediate", func() string { return a.get("/kits/new").Body.String() }, "inter_line_qty", 1},
		{"edit kit", func() string { return a.get(kit + "/edit").Body.String() }, "line_qty", 3},
		{"new service", func() string { return a.get("/services/new").Body.String() }, "part_qty", 1},
		{"new service from a kit", func() string { return a.get("/services/new?kit=" + kit[len("/kits/"):]).Body.String() }, "part_qty", 3},
		{"edit service", func() string { return a.get(svc + "/edit").Body.String() }, "part_qty", 2},
		{"service sent back", func() string { return bad.Body.String() }, "part_qty", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rows, empty := listRows(tc.rec(), tc.field)
			if rows != tc.rows || empty != 1 {
				t.Errorf("%s: %d rows, %d empty; want %d rows, 1 empty", tc.field, rows, empty, tc.rows)
			}
		})
	}
}

func TestLogServiceOffersTheVehiclesKits(t *testing.T) {
	a := newApp(t)
	k390, k1290 := a.seed()
	oil := itemID(t, a.store, "Silkolene")
	rec := a.post("/kits", url.Values{
		"vehicle_id": {itoa(k390)}, "name": {"Oil service"}, "interval_km": {"7500"},
		"line_item": {itoa(oil)}, "line_qty": {"100 ml"},
	})
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	kitID := kit[len("/kits/"):]
	start := `href="/services/new?kit=` + kitID + `" data-kit-start>`

	// From the vehicle page: its kits show at once, with their due badge.
	// The odometer starts empty, the last reading as its placeholder.
	expect(t, a.get("/services/new?vehicle="+itoa(k390)), http.StatusOK,
		`<div class="form-section" id="kit-starts">`, `data-kit-vehicle="`+itoa(k390)+`"><a`, start, "Start from a kit", "Not done yet",
		`name="odometer_km" value="" inputmode="numeric" placeholder="Last reading 10`+"\u202f"+`000 km"`,
		`data-odometer="10`+"\u202f"+`000 km"`)
	expect(t, a.get("/services/new?kit="+kitID), http.StatusOK, `name="odometer_km" value="" inputmode="numeric" placeholder="Last reading 10`)
	// No vehicle yet, or one without kits: the section waits, hidden.
	expect(t, a.get("/services/new"), http.StatusOK, `id="kit-starts" hidden`, `data-kit-vehicle="`+itoa(k390)+`" hidden`)
	expect(t, a.get("/services/new?vehicle="+itoa(k1290)), http.StatusOK, `id="kit-starts" hidden`)
	// Already from a kit, or editing: no section.
	expectNot(t, a.get("/services/new?kit="+kitID), "kit-starts")
	rec = a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Chain clean"}, "performed_on": {"2026-10-20"}})
	expect(t, rec, http.StatusSeeOther)
	expectNot(t, a.get(rec.Header().Get("Location")+"/edit"), "kit-starts")

	// Picking the kit keeps the date, odometer and labour typed so far.
	expect(t, a.get("/services/new?kit="+kitID+"&performed_on=2026-10-01&odometer_km=12345&labor_cost=80"), http.StatusOK,
		`id="performed_on" name="performed_on" value="2026-10-01"`, `name="odometer_km" value="12345"`, `name="labor_cost" value="80"`,
		`value="Oil service"`)
}
