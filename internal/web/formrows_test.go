package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/sunny-azu-red/stock-items/internal/store"
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
	// The same for an item (no name), whose empty SKU row once came back
	// along with the new one: a row more after every failed save.
	badItem := a.post("/items", url.Values{
		"unit": {"piece"}, "quantity": {"1"},
		"sku_code": {"C3", ""}, "sku_kind": {"oem", "oem"}, "sku_brand": {"", ""}, "sku_pack": {"", ""}, "sku_pack_unit": {"pcs", "pcs"},
	})
	expect(t, badItem, http.StatusUnprocessableEntity)

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
		{"item sent back", func() string { return badItem.Body.String() }, "sku_code", 2},
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
		"vehicle_id": {itoa(k390)}, "name": {"Oil service"}, "interval_distance": {"7500"},
		"line_item": {itoa(oil)}, "line_qty": {"100 ml"},
	})
	expect(t, rec, http.StatusSeeOther)
	kit := rec.Header().Get("Location")
	kitID := kit[len("/kits/"):]
	start := `href="/services/new?kit=` + kitID + `" data-kit-start>`

	// From the vehicle page: its kits show at once, each ending in its due badge.
	// The odometer starts empty, the last reading as its placeholder.
	expect(t, a.get("/services/new?vehicle="+itoa(k390)), http.StatusOK,
		`<div class="field" id="kit-starts">`, `data-kit-vehicle="`+itoa(k390)+`"><a`, start, "Start from a kit",
		`Oil service<span class="badge badge-muted">Not done yet</span></a>`,
		`name="odometer" value="" inputmode="numeric" placeholder="Last reading 10`+"\u202f"+`000 km"`,
		`data-odometer="10`+"\u202f"+`000 km"`)
	expect(t, a.get("/services/new?kit="+kitID), http.StatusOK, `name="odometer" value="" inputmode="numeric" placeholder="Last reading 10`)
	// No vehicle yet, or one without kits: the section waits, hidden.
	expect(t, a.get("/services/new"), http.StatusOK, `id="kit-starts" hidden`, `data-kit-vehicle="`+itoa(k390)+`" hidden`)
	expect(t, a.get("/services/new?vehicle="+itoa(k1290)), http.StatusOK, `id="kit-starts" hidden`)
	// Already from a kit, or editing: no section.
	expectNot(t, a.get("/services/new?kit="+kitID), "kit-starts")
	rec = a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Chain clean"}, "performed_on": {"2026-10-20"}})
	expect(t, rec, http.StatusSeeOther)
	expectNot(t, a.get(rec.Header().Get("Location")+"/edit"), "kit-starts")

	// Picking the kit keeps the date, odometer and labour typed so far.
	expect(t, a.get("/services/new?kit="+kitID+"&performed_on=2026-10-01&odometer=12345&labor_cost=80"), http.StatusOK,
		`id="performed_on" name="performed_on" value="2026-10-01"`, `name="odometer" value="12345"`, `name="labor_cost" value="80"`,
		`value="Oil service"`)

	// Every scheduled kit ends in its due badge, on track too, most urgent
	// first, with how far off it is as the button's title.
	logKit := func(name, months, on string) string {
		t.Helper()
		rec := a.post("/kits", url.Values{"vehicle_id": {itoa(k390)}, "name": {name}, "interval_months": {months}})
		expect(t, rec, http.StatusSeeOther)
		id := rec.Header().Get("Location")[len("/kits/"):]
		expect(t, a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "kit_id": {id}, "title": {name}, "performed_on": {on}}), http.StatusSeeOther)
		return id
	}
	coolant := logKit("Coolant", "24", "2026-10-01")
	brakes := logKit("Brake fluid", "12", "2025-10-01") // due 2026-10-01, 28 days ago
	page := a.get("/services/new?vehicle=" + itoa(k390))
	expect(t, page, http.StatusOK,
		`title="28 days overdue" href="/services/new?kit=`+brakes+`" data-kit-start>`,
		`Brake fluid<span class="badge badge-danger">`,
		`title="in 703 days" href="/services/new?kit=`+coolant+`" data-kit-start>`, // due 2028-10-01
		`Coolant<span class="badge badge-ok">On track</span></a>`)
	body := page.Body.String()
	if strings.Index(body, "Brake fluid<span") > strings.Index(body, "Oil service<span") {
		t.Error("the overdue kit should come before the one not done yet")
	}
}

// A part row's item is picked from the list or typed: the hidden id while
// the text names its item, else the one item the text names (lineItem).
// A part must be an item, so other text is refused at the row, kept as
// typed.
func TestPartRowsTakeTypedNames(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	ctx := context.Background()
	oil := itemID(t, a.store, "Silkolene")
	kitLines := func(t *testing.T, rec *httptest.ResponseRecorder) []int64 {
		t.Helper()
		expect(t, rec, http.StatusSeeOther)
		kit, err := a.store.GetKit(ctx, mustID(t, rec.Header().Get("Location")))
		if err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for _, l := range kit.Lines {
			ids = append(ids, l.ItemID)
		}
		return ids
	}
	kit := func(name string, rows ...string) url.Values {
		f := url.Values{"vehicle_id": {itoa(k390)}, "name": {name}}
		for i := 0; i < len(rows); i += 2 {
			f.Add("line_item", rows[i])
			f.Add("line_item_text", rows[i+1])
			f.Add("line_qty", "1")
		}
		return f
	}

	t.Run("picked", func(t *testing.T) {
		if got := kitLines(t, a.post("/kits", kit("Picked", itoa(oil), "Silkolene 15W50 PRO 4 XP"))); !slices.Equal(got, []int64{oil}) {
			t.Errorf("lines = %v, want the oil", got)
		}
	})
	t.Run("typed exactly, not picked", func(t *testing.T) {
		if got := kitLines(t, a.post("/kits", kit("Typed", "", " silkolene 15w50 pro 4 xp"))); !slices.Equal(got, []int64{oil}) {
			t.Errorf("lines = %v, want the oil", got)
		}
	})
	t.Run("picked, then renamed in another tab", func(t *testing.T) {
		// The page clears the id once the text no longer names the item, so
		// a posted one stands: an open form's row keeps its renamed item.
		if got := kitLines(t, a.post("/kits", kit("Renamed", itoa(oil), "Silkolene (old name)"))); !slices.Equal(got, []int64{oil}) {
			t.Errorf("lines = %v, want the oil", got)
		}
	})
	t.Run("a name that is no item", func(t *testing.T) {
		rec := a.post("/kits", kit("Unknown", "", "Silkolene 10W40"))
		expect(t, rec, http.StatusUnprocessableEntity, "No item is called “Silkolene 10W40”: pick one from the list",
			`name="line_item_text" value="Silkolene 10W40"`, `<input type="hidden" name="line_item" value="" data-item-id>`)
	})
	t.Run("a name two items have", func(t *testing.T) {
		expect(t, a.post("/items", url.Values{"brand": {"KTM"}, "name": {"Oil Filter"}, "unit": {"piece"}}), http.StatusSeeOther)
		expect(t, a.post("/kits", kit("Two", "", "oil filter")), http.StatusUnprocessableEntity,
			"2 items are called “oil filter”: pick one from the list")
	})
	t.Run("a service's part, and its edit form names it", func(t *testing.T) {
		rec := a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Top up"}, "performed_on": {"2026-10-20"},
			"part_item": {""}, "part_item_text": {"Silkolene 15W50 PRO 4 XP"}, "part_qty": {"100 ml"}})
		expect(t, rec, http.StatusSeeOther)
		svc, err := a.store.GetService(ctx, mustID(t, rec.Header().Get("Location")))
		if err != nil || len(svc.Parts) != 1 || svc.Parts[0].ItemID != oil {
			t.Fatalf("service = %+v, %v", svc, err)
		}
		expect(t, a.get(rec.Header().Get("Location")+"/edit"), http.StatusOK,
			`name="part_item_text" value="Silkolene 15W50 PRO 4 XP"`, `name="part_item" value="`+itoa(oil)+`" data-item-id>`)
	})
	t.Run("an archived line stays when the kit is saved again", func(t *testing.T) {
		pads := itemID(t, a.store, "Rear Brake")
		rec := a.post("/kits", kit("Brakes", itoa(pads), "OEM Rear Brake Pads"))
		expect(t, rec, http.StatusSeeOther)
		path := rec.Header().Get("Location")
		if _, err := a.store.ArchiveItem(ctx, pads, cur(t, a.store, store.WhatItem, pads)); err != nil {
			t.Fatal(err)
		}
		expect(t, a.get(path+"/edit"), http.StatusOK, `name="line_item_text" value="OEM Rear Brake Pads"`)
		// Typed in a new row, it isn't offered: the list has no archived items.
		expect(t, a.post("/kits", kit("More brakes", "", "OEM Rear Brake Pads")), http.StatusUnprocessableEntity,
			"No item is called “OEM Rear Brake Pads”: pick one from the list")
		form := kit("Brakes", itoa(pads), "OEM Rear Brake Pads")
		k, err := a.store.GetKit(ctx, mustID(t, path))
		if err != nil {
			t.Fatal(err)
		}
		form.Set("version", strconv.Itoa(k.Version))
		if got := kitLines(t, a.post(path, form)); !slices.Equal(got, []int64{pads}) {
			t.Errorf("lines = %v, want the archived pads kept", got)
		}
	})
}
