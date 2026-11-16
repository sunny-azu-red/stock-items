package stock

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// suggestDay is the day the suggestion tests run on.
var suggestDay = time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)

func TestSuggestPurchases(t *testing.T) {
	filter := Item{ID: 1, Brand: "OEM", Name: "Oil Filter", Unit: UnitPiece, Quantity: 0, MinQuantity: ptr(2.0)}
	oil := Item{ID: 2, Brand: "Motorex", Name: "Power Synt", Unit: UnitML, Quantity: 500}
	pads := Item{ID: 3, Brand: "OEM", Name: "Brake Pads", Unit: UnitPiece, Quantity: 4, MinQuantity: ptr(1.0)}
	onList := Item{ID: 4, Name: "Chain lube", Unit: UnitML, Quantity: 0, MinQuantity: ptr(100.0)}
	kits := []Kit{
		{VehicleName: "KTM 390", Name: "Oil service", Lines: []KitLine{
			{ItemID: 1, Need: 1, Have: 0, Unit: UnitPiece},
			{ItemID: 2, Need: 1700, Have: 500, Unit: UnitML},
			{ItemID: 3, Need: 1, Have: 4, Unit: UnitPiece},
		}},
		{VehicleName: "KTM 1290", Name: "Oil service", Lines: []KitLine{
			{ItemID: 2, Need: 3500, Have: 500, Unit: UnitML},
		}},
	}
	got := SuggestPurchases([]Item{filter, oil, pads, onList}, kits, map[int64]*float64{4: nil}, suggestDay, 0)
	if len(got) != 2 {
		t.Fatalf("got %d suggestions, want 2: %+v", len(got), got)
	}
	// Sorted by display name: Motorex Power Synt, OEM Oil Filter.
	// Both kits are called "Oil service": one reason (the chips say whose).
	if got[0].Item.ID != 2 || got[0].Quantity != 3000 || len(got[0].Reasons) != 1 {
		t.Errorf("oil suggestion = %+v", got[0])
	}
	if got[1].Item.ID != 1 || got[1].Quantity != 2 || len(got[1].Reasons) != 2 {
		t.Errorf("filter suggestion = %+v", got[1])
	}
}

func TestSuggestPurchasesSkipsArchivedKitLines(t *testing.T) {
	kits := []Kit{{VehicleName: "KTM 390", Name: "Oil service", Lines: []KitLine{
		{ItemID: 1, ItemName: "Old filter", Need: 1, Have: 0, Unit: UnitPiece, Archived: true},
		{ItemID: 2, ItemName: "New filter", Need: 1, Have: 0, Unit: UnitPiece},
	}}}
	got := SuggestPurchases(nil, kits, nil, suggestDay, 0)
	if len(got) != 1 || got[0].Item.ID != 2 {
		t.Errorf("suggestions = %+v, want only the active item", got)
	}
}

func TestSuggestPurchasesCountsWhatIsListed(t *testing.T) {
	oil := Item{ID: 2, Name: "Oil", Unit: UnitML, Quantity: 500, MinQuantity: ptr(1000.0)}
	kits := []Kit{{VehicleName: "KTM 390", Name: "Oil service", Lines: []KitLine{{ItemID: 2, Need: 3500, Have: 500, Unit: UnitML}}}}
	tests := []struct {
		name    string
		listed  map[int64]*float64
		want    int
		listedQ float64
	}{
		{"not listed", nil, 1, 0},
		{"listed without a quantity", map[int64]*float64{2: nil}, 0, 0},
		{"listed for the min only", map[int64]*float64{2: ptr(500.0)}, 1, 500},
		{"listed for everything", map[int64]*float64{2: ptr(3000.0)}, 0, 0},
	}
	for _, tt := range tests {
		got := SuggestPurchases([]Item{oil}, kits, tt.listed, suggestDay, 0)
		if len(got) != tt.want || (tt.want == 1 && (got[0].Quantity != 3000 || got[0].Listed != tt.listedQ)) {
			t.Errorf("%s: %+v", tt.name, got)
		}
	}
}

// A vehicle's suggestions: what fits it (with General) below its minimum,
// and its own kits' shortfalls, each with the vehicles it is for.
func TestSuggestPurchasesForAVehicle(t *testing.T) {
	const a, b = 1, 2
	general := Item{ID: 1, Name: "Chain lube", Unit: UnitML, Quantity: 0, MinQuantity: ptr(100.0)}
	onlyB := Item{ID: 2, Name: "B grips", Unit: UnitPiece, Quantity: 0, MinQuantity: ptr(2.0), VehicleIDs: []int64{b}}
	shared := Item{ID: 3, Name: "Oil", Unit: UnitML, Quantity: 500, VehicleIDs: []int64{a, b}}
	bFilter := Item{ID: 4, Name: "B filter", Unit: UnitPiece, Quantity: 0, VehicleIDs: []int64{b}}
	items := []Item{general, onlyB, shared, bFilter}
	kits := []Kit{
		{VehicleID: a, VehicleName: "A", Name: "Oil service", Lines: []KitLine{
			{ItemID: 3, Need: 1000, Have: 500, Unit: UnitML},
			{ItemID: 4, Need: 1, Have: 0, Unit: UnitPiece}, // tagged with B only, still A's need
		}},
		{VehicleID: b, VehicleName: "B", Name: "Oil service", Lines: []KitLine{
			{ItemID: 3, Need: 3000, Have: 500, Unit: UnitML},
		}},
	}
	type want struct {
		qty      float64
		reasons  string
		vehicles []int64
	}
	tests := []struct {
		name    string
		vehicle int64
		want    map[int64]want
	}{
		{"every vehicle", 0, map[int64]want{
			1: {100, "Below the minimum of 100 ml", nil},
			2: {2, "Below the minimum of 2 pcs", []int64{b}},
			// Both kits share a name: the reason once, both vehicles.
			3: {2500, "Short for the Oil service kit", []int64{a, b}},
			4: {1, "Short for the Oil service kit", []int64{a}},
		}},
		{"A", a, map[int64]want{
			1: {100, "Below the minimum of 100 ml", nil},
			3: {500, "Short for the Oil service kit", []int64{a}}, // B's bigger need isn't A's
			4: {1, "Short for the Oil service kit", []int64{a}},
		}},
		{"B", b, map[int64]want{
			1: {100, "Below the minimum of 100 ml", nil},
			2: {2, "Below the minimum of 2 pcs", []int64{b}},
			3: {2500, "Short for the Oil service kit", []int64{b}},
		}},
	}
	for _, tt := range tests {
		got := SuggestPurchases(items, kits, nil, suggestDay, tt.vehicle)
		if len(got) != len(tt.want) {
			t.Errorf("%s: %d suggestions, want %d: %+v", tt.name, len(got), len(tt.want), got)
			continue
		}
		for _, s := range got {
			w, ok := tt.want[s.Item.ID]
			if !ok || s.Quantity != w.qty || strings.Join(s.Reasons, "; ") != w.reasons || !slices.Equal(s.VehicleIDs, w.vehicles) {
				t.Errorf("%s: item %d = %v %q %v, want %+v", tt.name, s.Item.ID, s.Quantity, s.Reasons, s.VehicleIDs, w)
			}
		}
	}
}

func TestShortNote(t *testing.T) {
	for _, tt := range []struct{ name, noun, want string }{
		{"Air Filter", "kit", "Short for the Air Filter kit"},
		{"Oil change", "service", "Short for the Oil change service"},
		{"Oil Service", "service", "Short for the Oil Service"},
		{"Winter kit", "kit", "Short for the Winter kit"},
		{" ", "service", "Short for a service"},
	} {
		if got := ShortNote(tt.name, tt.noun); got != tt.want {
			t.Errorf("ShortNote(%q, %q) = %q, want %q", tt.name, tt.noun, got, tt.want)
		}
	}
}

// Which vehicles' parts of the shopping list an entry is on.
func TestShoppingEntryFor(t *testing.T) {
	id := ptr(int64(9))
	tests := []struct {
		name     string
		e        ShoppingEntry
		vehicles []int64
		forA     bool
		forB     bool
	}{
		{"free text", ShoppingEntry{FreeText: "Rags"}, nil, true, true},
		{"General item", ShoppingEntry{ItemID: id}, nil, true, true},
		{"tagged with A", ShoppingEntry{ItemID: id, ItemVehicleIDs: []int64{1}}, []int64{1}, true, false},
		{"tagged with B, A's kit uses it", ShoppingEntry{ItemID: id, ItemVehicleIDs: []int64{2}, KitVehicleIDs: []int64{1, 2}}, []int64{1, 2}, true, true},
		{"tagged with B, only B's kit", ShoppingEntry{ItemID: id, ItemVehicleIDs: []int64{2}, KitVehicleIDs: []int64{2}}, []int64{2}, false, true},
		// A General item in A's kit: everyone's still, and A's chip.
		{"General item in A's kit", ShoppingEntry{ItemID: id, KitVehicleIDs: []int64{1}}, []int64{1}, true, true},
	}
	for _, tt := range tests {
		if got := tt.e.VehicleIDs(); !slices.Equal(got, tt.vehicles) {
			t.Errorf("%s: VehicleIDs = %v, want %v", tt.name, got, tt.vehicles)
		}
		if tt.e.For(1) != tt.forA || tt.e.For(2) != tt.forB || !tt.e.For(0) {
			t.Errorf("%s: For(A) = %v, For(B) = %v, For(0) = %v", tt.name, tt.e.For(1), tt.e.For(2), tt.e.For(0))
		}
	}
	all := []ShoppingEntry{tests[0].e, tests[2].e, tests[4].e}
	if shown, others := ShoppingFor(all, 1); len(shown) != 2 || others != 1 {
		t.Errorf("ShoppingFor(A) = %d shown, %d others", len(shown), others)
	}
	if shown, others := ShoppingFor(all, 0); len(shown) != 3 || others != 0 {
		t.Errorf("ShoppingFor(0) = %d shown, %d others", len(shown), others)
	}
}
