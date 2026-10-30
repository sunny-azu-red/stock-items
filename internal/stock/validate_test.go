package stock

import (
	"math"
	"testing"
)

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

func TestValidGTIN(t *testing.T) {
	tests := []struct {
		code string
		want bool
	}{
		{"7611197108900", true},  // EAN-13
		{"7611197115052", true},  // EAN-13
		{"7611197108901", false}, // wrong check digit
		{"036000291452", true},   // UPC-A
		{"96385074", true},       // EAN-8
		{"10614141000415", true}, // GTIN-14
		{"036000291453", false},  // UPC-A, wrong check digit
		{"761119710890X", false},
		{"12345", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := ValidGTIN(tt.code); got != tt.want {
			t.Errorf("ValidGTIN(%q) = %v, want %v", tt.code, got, tt.want)
		}
	}
}

func TestItemInputBarcodeSKU(t *testing.T) {
	in := ItemInput{
		Name: "Coolant", Unit: UnitML, Quantity: 1000,
		SKUs: []SKU{{Code: " 7 611197 108900 ", Kind: SKUBarcode}, {Code: "7611197108901", Kind: SKUBarcode}},
	}
	in.Normalize()
	if in.SKUs[0].Code != "7611197108900" {
		t.Errorf("barcode spaces not removed: %q", in.SKUs[0].Code)
	}
	errs := in.Validate()
	if _, ok := errs["sku_0"]; ok {
		t.Errorf("valid barcode rejected: %v", errs)
	}
	if _, ok := errs["sku_1"]; !ok {
		t.Errorf("bad check digit accepted: %v", errs)
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
		{"NaN price", MovementInput{Kind: MovePurchase, Delta: 1, TotalPrice: ptr(math.NaN())}, false},
		{"huge price", MovementInput{Kind: MovePurchase, Delta: 1, TotalPrice: ptr(1e12)}, false},
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
