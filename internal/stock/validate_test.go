package stock

import "testing"

func TestItemInputValidate(t *testing.T) {
	in := ItemInput{
		Name: "  Oil Filter ", Unit: UnitPiece, Quantity: 1,
		SKUs: []SKU{
			{Code: " 77038005044 ", Kind: SKUOEM},
			{}, // empty row is dropped
			{Code: "HF155", Kind: ""},
		},
		VehicleIDs: []int64{2, 1, 2, 0},
	}
	in.Normalize()
	if errs := in.Validate(); errs.Any() {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if in.Name != "Oil Filter" || len(in.SKUs) != 2 || in.SKUs[0].Code != "77038005044" || in.SKUs[1].Kind != SKUOEM {
		t.Errorf("normalize failed: %+v", in)
	}
	if len(in.VehicleIDs) != 2 {
		t.Errorf("vehicle ids not deduplicated: %v", in.VehicleIDs)
	}
}

func TestItemInputValidateErrors(t *testing.T) {
	neg := -1.0
	in := ItemInput{
		Unit: "litre", Quantity: -1, MinQuantity: &neg,
		SKUs: []SKU{{Code: "A", Kind: SKUOEM}, {Code: "a", Kind: SKUOEM}, {Label: "1 L", Kind: "bogus"}},
	}
	in.Normalize()
	errs := in.Validate()
	for _, f := range []string{"name", "unit", "quantity", "min_quantity", "sku_1", "sku_2"} {
		if _, ok := errs[f]; !ok {
			t.Errorf("expected error for %s, got %v", f, errs)
		}
	}
}

func TestVehicleInputValidate(t *testing.T) {
	in := VehicleInput{Name: " KTM 390 ", VIN: " vbk123 "}
	in.Normalize()
	if errs := in.Validate(); errs.Any() {
		t.Fatalf("unexpected errors: %v", errs)
	}
	if in.Kind != KindMotorcycle || in.Color != "#f97316" || in.VIN != "VBK123" {
		t.Errorf("defaults not applied: %+v", in)
	}
	year := 1800
	bad := VehicleInput{Kind: "boat", Color: "red", Year: &year}
	errs := bad.Validate()
	for _, f := range []string{"name", "kind", "color", "year"} {
		if _, ok := errs[f]; !ok {
			t.Errorf("expected error for %s, got %v", f, errs)
		}
	}
}

func TestMovementInputValidate(t *testing.T) {
	cases := []struct {
		name string
		in   MovementInput
		ok   bool
	}{
		{"purchase adds", MovementInput{Kind: MovePurchase, Delta: 2}, true},
		{"purchase cannot remove", MovementInput{Kind: MovePurchase, Delta: -2}, false},
		{"use removes", MovementInput{Kind: MoveUse, Delta: -500}, true},
		{"use cannot add", MovementInput{Kind: MoveUse, Delta: 1}, false},
		{"correction either way", MovementInput{Kind: MoveCorrection, Delta: -1}, true},
		{"zero", MovementInput{Kind: MoveCorrection, Delta: 0}, false},
		{"unknown kind", MovementInput{Kind: "theft", Delta: -1}, false},
		{"negative price", MovementInput{Kind: MovePurchase, Delta: 1, TotalPrice: ptr(-1.0)}, false},
	}
	for _, c := range cases {
		if got := !c.in.Validate().Any(); got != c.ok {
			t.Errorf("%s: valid = %v, want %v", c.name, got, c.ok)
		}
	}
}

func TestKitAndServiceMergeDuplicateLines(t *testing.T) {
	in := KitInput{VehicleID: 1, Name: "Oil service", Lines: []KitLineInput{
		{ItemID: 1, Quantity: 1}, {ItemID: 0, Quantity: 5}, {ItemID: 1, Quantity: 1}, {ItemID: 2, Quantity: 1700},
	}}
	in.Normalize()
	if errs := in.Validate(); errs.Any() {
		t.Fatal(errs)
	}
	if len(in.Lines) != 2 || in.Lines[0].Quantity != 2 {
		t.Errorf("lines = %+v", in.Lines)
	}
	svc := ServiceInput{VehicleID: 1}
	svc.Normalize()
	errs := svc.Validate()
	if _, ok := errs["title"]; !ok {
		t.Errorf("title required, got %v", errs)
	}
	if _, ok := errs["performed_on"]; !ok {
		t.Errorf("date required, got %v", errs)
	}
}

func TestShoppingInputValidate(t *testing.T) {
	in := ShoppingInput{FreeText: "  "}
	in.Normalize()
	if !in.Validate().Any() {
		t.Error("empty entry must be invalid")
	}
	in = ShoppingInput{FreeText: "Chain lube"}
	if in.Validate().Any() {
		t.Error("free text entry should be valid")
	}
}
