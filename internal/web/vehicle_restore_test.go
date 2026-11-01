package web

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"testing"
)

// Putting a deleted vehicle back restores its item tags, which belong to
// the items' version: an item edit form opened before (or while the
// vehicle was in the trash, when it doesn't show the tag) is stale, so it
// can't save the tag away.
func TestVehicleUndoMakesItemFormsStale(t *testing.T) {
	for _, opened := range []string{"before the delete", "while it was deleted"} {
		t.Run(opened, func(t *testing.T) {
			a := newApp(t)
			k390, _ := a.seed()
			oil := itemID(t, a.store, "Silkolene")
			action := itemPath(oil)
			vehicle := "/vehicles/" + itoa(k390)
			fields := url.Values{"name": {"15W50 PRO 4 XP"}, "brand": {"Silkolene"}, "unit": {"ml"}, "quantity": {"525"},
				"quantity_unit": {"ml"}, "quantity_was": {"525"}, "notes": {"Typed meanwhile"}}

			var mine string
			if opened == "before the delete" {
				mine = openForm(t, a, action+"/edit", action)
				fields.Set("vehicle", itoa(k390))
			}
			undo := flashOf(t, saveFresh(t, a, vehicle, vehicle+"/delete", nil)).Undo
			if undo == 0 {
				t.Fatal("no Undo")
			}
			if mine == "" {
				mine = openForm(t, a, action+"/edit", action)
			}
			rec := a.post("/trash/"+itoa(undo)+"/restore", nil)
			expect(t, rec, http.StatusSeeOther)
			if f := flashOf(t, rec); f.Kind == "error" {
				t.Fatalf("undo: %s", f.Message)
			}

			expect(t, submit(a, action, mine, fields), http.StatusUnprocessableEntity,
				"This item was changed since you opened this", "Typed meanwhile")
			it, err := a.store.GetItem(context.Background(), oil)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(it.VehicleIDs, k390) || it.Notes == "Typed meanwhile" {
				t.Errorf("item after the stale save: tags %v, notes %q", it.VehicleIDs, it.Notes)
			}
		})
	}
}
