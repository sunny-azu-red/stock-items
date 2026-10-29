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
	got := SuggestPurchases([]Item{filter, oil, pads, onList}, kits, map[int64]bool{4: true})
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
