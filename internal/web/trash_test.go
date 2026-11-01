package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/sunny-azu-red/stock-items/internal/stock"
)

func TestUndoAndRecentlyDeleted(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	ctx := context.Background()
	oil := itemID(t, a.store, "Silkolene")
	rec := a.post("/services", url.Values{"vehicle_id": {itoa(k390)}, "title": {"Top up"}, "performed_on": {"2026-10-29"},
		"part_item": {itoa(oil)}, "part_qty": {"25"}})
	expect(t, rec, http.StatusSeeOther)
	svc := rec.Header().Get("Location")

	// Deleting offers Undo in its message.
	rec = a.postV(svc+"/delete", nil)
	f := flashOf(t, rec)
	if f.Undo == 0 || !strings.Contains(f.Message, "Deleted Top up") {
		t.Fatalf("flash = %+v", f)
	}
	expect(t, a.get("/"), http.StatusOK, `href="/trash"`, `Trash<span class="count count-muted">1</span>`)
	expect(t, a.get("/trash"), http.StatusOK, `href="/trash" aria-current="page"`, "Service: Top up (KTM 390, 29 Oct 2026)", `action="/trash/`+itoa(f.Undo)+`/restore"`)

	// Undo brings the service back and takes the oil again.
	rec = a.post("/trash/"+itoa(f.Undo)+"/restore", nil)
	expect(t, rec, http.StatusSeeOther)
	if l := rec.Header().Get("Location"); l != svc {
		t.Errorf("restore went to %q, want %q", l, svc)
	}
	if it, _ := a.store.GetItem(ctx, oil); it.Quantity != 500 {
		t.Errorf("oil = %v, want 500", it.Quantity)
	}
	expect(t, a.post("/trash/"+itoa(f.Undo)+"/restore", nil), http.StatusSeeOther) // twice: a message, not a 500

	// A restore that can't work explains why on the trash page.
	f = flashOf(t, a.postV(svc+"/delete", nil))
	expect(t, a.post("/items/"+itoa(oil)+"/movements", url.Values{"kind": {"use"}, "amount": {"525"}}), http.StatusSeeOther)
	rec = a.post("/trash/"+itoa(f.Undo)+"/restore", nil)
	if l := rec.Header().Get("Location"); l != "/trash" {
		t.Errorf("failed restore went to %q", l)
	}
	if msg := flashOf(t, rec); msg.Kind != "error" || !strings.Contains(msg.Message, "Can't restore") {
		t.Errorf("flash = %+v", msg)
	}

	// Ticking off and Bought offer Undo too; Empty clears the page.
	pads := itemID(t, a.store, "Rear Brake")
	entry, _ := a.store.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads, Quantity: ptr(2.0)})
	f = flashOf(t, a.postV("/shopping/"+itoa(entry)+"/bought", url.Values{"amount": {"2"}}))
	if f.Undo == 0 {
		t.Fatalf("bought flash = %+v", f)
	}
	expect(t, a.post("/trash/"+itoa(f.Undo)+"/restore", nil), http.StatusSeeOther)
	if it, _ := a.store.GetItem(ctx, pads); it.Quantity != 1 {
		t.Errorf("pads = %v, want 1", it.Quantity)
	}
	expect(t, a.post("/trash/empty", nil), http.StatusSeeOther)
	expect(t, a.get("/trash"), http.StatusOK, "Nothing deleted")
	expectNot(t, a.get("/"), `href="/trash"`)
}

func TestPurgeSaysWhatWentAlong(t *testing.T) {
	a := newApp(t)
	rec := a.post("/vehicles", url.Values{"name": {"Old bike"}, "kind": {"motorcycle"}})
	expect(t, rec, http.StatusSeeOther)
	v := rec.Header().Get("Location")
	rec = a.post("/kits", url.Values{"vehicle_id": {itoa(mustID(t, v))}, "name": {"Chain"}})
	expect(t, rec, http.StatusSeeOther)
	expect(t, a.postV(rec.Header().Get("Location")+"/delete", nil), http.StatusSeeOther)
	f := flashOf(t, a.postV(v+"/delete", nil))
	if f.Undo == 0 {
		t.Fatalf("vehicle delete flash = %+v", f)
	}
	expect(t, a.get("/trash"), http.StatusOK, "Its deleted services and kits go too.")
	rec = a.post("/trash/"+itoa(f.Undo)+"/purge", nil)
	if msg := flashOf(t, rec).Message; msg != "Permanently deleted, with 1 more entry that needed it" {
		t.Errorf("flash = %q", msg)
	}
	if n, _ := a.store.CountTrash(context.Background()); n != 0 {
		t.Errorf("trash = %d", n)
	}
}

// A restore that had to drop links to things permanently deleted says so.
func TestRestoreSaysWhatItLeftOut(t *testing.T) {
	a := newApp(t)
	k390, _ := a.seed()
	ring := itemID(t, a.store, "O-Ring")
	pads := itemID(t, a.store, "Rear Brake")
	rec := a.post("/kits", url.Values{"vehicle_id": {itoa(k390)}, "name": {"Brakes"},
		"line_item": {itoa(pads), itoa(ring)}, "line_qty": {"1", "2"}})
	expect(t, rec, http.StatusSeeOther)
	kitTrash := flashOf(t, a.postV(rec.Header().Get("Location")+"/delete", nil)).Undo
	ringTrash := flashOf(t, a.postV("/items/"+itoa(ring)+"/delete", nil)).Undo
	if kitTrash == 0 || ringTrash == 0 {
		t.Fatalf("trash ids %d, %d", kitTrash, ringTrash)
	}
	expect(t, a.post("/trash/"+itoa(ringTrash)+"/purge", nil), http.StatusSeeOther)
	msg := flashOf(t, a.post("/trash/"+itoa(kitTrash)+"/restore", nil)).Message
	if msg != "Restored Kit: Brakes (KTM 390). Left out 1 link to things permanently deleted." {
		t.Errorf("flash = %q", msg)
	}
}
