package stock

import "testing"

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
	got := SuggestPurchases([]Item{filter, oil, pads, onList}, kits, map[int64]*float64{4: nil})
	if len(got) != 2 {
		t.Fatalf("got %d suggestions, want 2: %+v", len(got), got)
	}
	// Sorted by display name: Motorex Power Synt, OEM Oil Filter.
	if got[0].Item.ID != 2 || got[0].Quantity != 3000 || len(got[0].Reasons) != 2 {
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
	got := SuggestPurchases(nil, kits, nil)
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
		got := SuggestPurchases([]Item{oil}, kits, tt.listed)
		if len(got) != tt.want || (tt.want == 1 && (got[0].Quantity != 3000 || got[0].Listed != tt.listedQ)) {
			t.Errorf("%s: %+v", tt.name, got)
		}
	}
}
