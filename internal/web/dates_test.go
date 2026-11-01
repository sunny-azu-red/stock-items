package web

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// inZone makes loc the app's zone for the test, as main does from TZ.
// Call it before opening the app, so no connection of its pool is running
// while it is set or put back.
func inZone(t *testing.T, loc *time.Location) {
	t.Helper()
	swap(t, &time.Local, loc)
}

// Just after midnight in the app's zone (UTC+3) it is already the next day
// everywhere in the app, though UTC, and the database's session, is still
// on the last one: the dates forms suggest, the day a service counts as
// logged on for its km reminder, what is expiring, and when something was
// deleted.
func TestTheAppsDayIsItsZones(t *testing.T) {
	inZone(t, time.FixedZone("UTC+3", 3*60*60))
	now := time.Date(2026, 10, 31, 21, 30, 0, 0, time.UTC) // 00:30 on 1 November in the app
	a := newAppAt(t, func() time.Time { return now })
	ctx := context.Background()
	k390, _ := a.seed() // at 10,000 km
	oil := itemID(t, a.store, "Silkolene")

	t.Run("forms suggest the local day", func(t *testing.T) {
		expect(t, a.get("/services/new?vehicle="+itoa(k390)), http.StatusOK, `name="performed_on" value="2026-11-01"`)
		expect(t, a.get("/items/new"), http.StatusOK, `name="bought_on" value="2026-11-01"`)
		expect(t, a.get(itemPath(oil)), http.StatusOK, `name="occurred_on" value="2026-11-01"`)
		// A quick change has no date field: it is dated the local day too.
		expect(t, a.post(itemPath(oil)+"/adjust", url.Values{"dir": {"down"}}), http.StatusSeeOther)
		moves, err := a.store.ListMovements(ctx, store.MovementFilter{ItemID: oil, Kind: stock.MoveUse})
		if err != nil || len(moves) != 1 {
			t.Fatalf("uses = %+v, %v", moves, err)
		}
		if got := moves[0].OccurredOn.Format(time.DateOnly); got != "2026-11-01" {
			t.Errorf("quick change dated %s, want 2026-11-01", got)
		}
	})

	t.Run("a service logged now is logged on the local day", func(t *testing.T) {
		rec := a.post("/kits", url.Values{"vehicle_id": {itoa(k390)}, "name": {"Oil service"}, "interval_km": {"5000"}})
		expect(t, rec, http.StatusSeeOther)
		kit := rec.Header().Get("Location")
		// Done a week ago, by the local day: the reading when it was logged
		// still says where it was done.
		fields := url.Values{"vehicle_id": {itoa(k390)}, "kit_id": {itoa(mustID(t, kit))}, "title": {"Oil service"}, "performed_on": {"2026-10-25"}}
		rec = a.post("/services", fields)
		expect(t, rec, http.StatusSeeOther)
		svc := rec.Header().Get("Location")
		// Logged now (the database's clock isn't the test's).
		if _, err := a.pool.Exec(ctx, `UPDATE service_records SET created_at = $1`, now); err != nil {
			t.Fatal(err)
		}
		due := "<dd>" + stock.FormatKm(15000) + "</dd>"
		expect(t, a.get(kit), http.StatusOK, due)
		// The date typed is the day kept, and it was logged today.
		expect(t, a.get(svc), http.StatusOK, "25 Oct 2026", "<dd>today</dd>")

		// Done eight days ago by the local day (seven in UTC): the reading
		// at log no longer counts.
		fields.Set("performed_on", "2026-10-24")
		expect(t, a.postV(svc, fields), http.StatusSeeOther)
		rec = a.get(kit)
		expect(t, rec, http.StatusOK, "Needs odometer")
		expectNot(t, rec, due)
	})

	t.Run("expiring counts from the local day", func(t *testing.T) {
		expiring := func(brand, on string) int64 {
			rec := a.post("/items", url.Values{"brand": {brand}, "name": {"Coolant"}, "unit": {"ml"}, "quantity": {"500"}, "expires_on": {on}})
			expect(t, rec, http.StatusSeeOther)
			return mustID(t, rec.Header().Get("Location"))
		}
		// The last day "expiring" reaches from 1 November, a day past it
		// from 31 October; and yesterday.
		last := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, store.ExpiringDays)
		soon, gone := expiring("Motul", last.Format(time.DateOnly)), expiring("Liqui Moly", "2026-10-31")
		expect(t, a.get(itemPath(soon)), http.StatusOK, `<span class="badge badge-warn">Expires soon</span>`)
		expect(t, a.get(itemPath(gone)), http.StatusOK, `<span class="badge badge-danger">Expired</span>`)
		link := func(id int64) string { return `class="item-name" href="` + itemPath(id) + `"` }
		rec := a.get("/stock?exp=1")
		expect(t, rec, http.StatusOK, link(soon), link(gone))
		expectNot(t, rec, link(oil))
		expect(t, a.get("/"), http.StatusOK, `badge-warn">`+last.Format("2 Jan 2006")+`</span>`, `badge-danger">31 Oct 2026</span>`)
	})

	t.Run("deleted today is the local day", func(t *testing.T) {
		for _, name := range []string{"Chain lube", "Brake fluid"} {
			expect(t, a.post("/shopping", url.Values{"free_text": {name}}), http.StatusSeeOther)
		}
		entries, err := a.store.ListShopping(ctx)
		if err != nil || len(entries) != 2 {
			t.Fatalf("shopping = %+v, %v", entries, err)
		}
		for _, e := range entries {
			expect(t, a.postV("/shopping/"+itoa(e.ID)+"/delete", nil), http.StatusSeeOther)
		}
		// 00:15 on 1 November, and 23:45 the evening before, in the app.
		if _, err := a.pool.Exec(ctx, `UPDATE trash SET deleted_at = CASE label
			WHEN 'Shopping list: Chain lube' THEN '2026-10-31 21:15Z'::timestamptz ELSE '2026-10-31 20:45Z'::timestamptz END`); err != nil {
			t.Fatal(err)
		}
		rec := a.get("/trash")
		expect(t, rec, http.StatusOK, "Deleted today", "Deleted yesterday")
		expectNot(t, rec, "Deleted 1 Nov 2026")
	})
}

// A date typed in a form is that calendar day in any app zone, east or
// west of UTC: kept as midnight UTC, it is checked against the app's
// today and stored as typed.
func TestATypedDateIsThatDay(t *testing.T) {
	for _, zone := range []*time.Location{time.FixedZone("UTC+3", 3*60*60), time.FixedZone("UTC-5", -5*60*60)} {
		t.Run(zone.String(), func(t *testing.T) {
			inZone(t, zone)
			d, err := parseOptDate("2026-10-31")
			if err != nil || !d.Equal(time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)) || d.Format(time.DateOnly) != "2026-10-31" {
				t.Fatalf("parseOptDate = %v, %v", d, err)
			}
			// Late on 31 October in the app: tomorrow is allowed, the day
			// after isn't.
			now := time.Date(2026, 10, 31, 23, 30, 0, 0, zone)
			if _, err := parseEventDate("2026-11-01", now); err != nil {
				t.Errorf("tomorrow: %v", err)
			}
			if _, err := parseEventDate("2026-11-02", now); err == nil {
				t.Error("the day after tomorrow was accepted")
			}
		})
	}
}
