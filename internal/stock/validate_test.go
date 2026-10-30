package stock

import (
	"math"
	"testing"
	"time"
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

func TestItemInputStartMovement(t *testing.T) {
	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		in       ItemInput
		kind     MovementKind
		errField string // expected validation error, if any
	}{
		{"nothing entered: already on the shelf", ItemInput{Quantity: 2}, MoveInitial, ""},
		{"price: bought", ItemInput{Quantity: 2, TotalPrice: ptr(30.0)}, MovePurchase, ""},
		{"shop only: bought", ItemInput{Quantity: 2, Supplier: "moto24"}, MovePurchase, ""},
		{"blank shop: not bought", ItemInput{Quantity: 2, Supplier: "   "}, MoveInitial, ""},
		{"date only: not bought", ItemInput{Quantity: 2, BoughtOn: &day}, MoveInitial, ""},
		{"bought but no amount", ItemInput{TotalPrice: ptr(30.0)}, MovePurchase, "quantity"},
		{"bad price", ItemInput{Quantity: 2, TotalPrice: ptr(-1.0)}, MovePurchase, "total_price"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := tt.in
			in.Name, in.Unit = "Coolant", UnitML
			in.Normalize()
			errs := in.Validate()
			if _, ok := errs[tt.errField]; tt.errField != "" && !ok {
				t.Errorf("want error on %s, got %v", tt.errField, errs)
			} else if tt.errField == "" && errs.Any() {
				t.Errorf("unexpected errors: %v", errs)
			}
			m := in.StartMovement(7)
			if m.Kind != tt.kind || m.ItemID != 7 || m.Delta != in.Quantity {
				t.Errorf("movement = %+v, want kind %s", m, tt.kind)
			}
			if m.Kind == MoveInitial && (m.TotalPrice != nil || m.Supplier != "" || m.OccurredOn != in.BoughtOn) {
				t.Errorf("initial stock should carry only its date: %+v", m)
			}
			if m.Kind == MovePurchase && (m.TotalPrice != in.TotalPrice || m.Supplier != in.Supplier || m.OccurredOn != in.BoughtOn) {
				t.Errorf("purchase details not carried over: %+v", m)
			}
		})
	}
}

func TestCheckEventDate(t *testing.T) {
	today := time.Date(2026, 10, 30, 15, 0, 0, 0, time.UTC)
	date := func(y int, m time.Month, d int) *time.Time {
		t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
		return &t
	}
	tests := []struct {
		name string
		d    *time.Time
		ok   bool
	}{
		{"not given", nil, true},
		{"today", date(2026, 10, 30), true},
		{"tomorrow (time zones)", date(2026, 10, 31), true},
		{"day after tomorrow", date(2026, 11, 1), false},
		{"year typo", date(2062, 10, 30), false},
		{"years ago", date(2019, 3, 1), true},
		{"first accepted day", date(1990, 1, 1), true},
		{"too old", date(1989, 12, 31), false},
	}
	for _, tt := range tests {
		if got := CheckEventDate(tt.d, today) == ""; got != tt.ok {
			t.Errorf("%s: ok = %v, want %v", tt.name, got, tt.ok)
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
		{"purchase sets best before", MovementInput{Kind: MovePurchase, Delta: 1, ExpiresOn: &time.Time{}}, true},
		{"use cannot set best before", MovementInput{Kind: MoveUse, Delta: -1, ExpiresOn: &time.Time{}}, false},
		{"correction cannot set best before", MovementInput{Kind: MoveCorrection, Delta: 1, ExpiresOn: &time.Time{}}, false},
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

func TestApplyEdit(t *testing.T) {
	day := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	other := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		m        Movement
		e        MovementEdit
		kind     MovementKind
		delta    float64
		price    *float64
		errField string
	}{
		{"fix a purchase price", Movement{Kind: MovePurchase, Delta: 1000, TotalPrice: ptr(670.0)},
			MovementEdit{Amount: 1000, TotalPrice: ptr(67.0)}, MovePurchase, 1000, ptr(67.0), ""},
		{"fix a purchase amount", Movement{Kind: MovePurchase, Delta: 1000},
			MovementEdit{Amount: 4000}, MovePurchase, 4000, nil, ""},
		{"a use stays negative", Movement{Kind: MoveUse, Delta: -100},
			MovementEdit{Amount: 250}, MoveUse, -250, nil, ""},
		{"a use takes no price", Movement{Kind: MoveUse, Delta: -100},
			MovementEdit{Amount: 100, TotalPrice: ptr(5.0)}, MoveUse, -100, nil, ""},
		{"priced initial stock becomes a purchase", Movement{Kind: MoveInitial, Delta: 2},
			MovementEdit{Amount: 2, TotalPrice: ptr(30.0)}, MovePurchase, 2, ptr(30.0), ""},
		{"initial stock with a shop becomes a purchase", Movement{Kind: MoveInitial, Delta: 2},
			MovementEdit{Amount: 2, Supplier: "moto24"}, MovePurchase, 2, nil, ""},
		{"a count that took stock away", Movement{Kind: MoveCorrection, Delta: -75},
			MovementEdit{Amount: 50, Removed: true, Note: "recount"}, MoveCorrection, -50, nil, ""},
		{"a count turned around", Movement{Kind: MoveCorrection, Delta: -75},
			MovementEdit{Amount: 100}, MoveCorrection, 100, nil, ""},
		{"zero amount", Movement{Kind: MovePurchase, Delta: 1},
			MovementEdit{}, MovePurchase, 0, nil, "amount"},
		{"bad price", Movement{Kind: MovePurchase, Delta: 1},
			MovementEdit{Amount: 1, TotalPrice: ptr(-1.0)}, MovePurchase, 1, ptr(-1.0), "total_price"},
		{"service parts are not edited here", Movement{Kind: MoveService, Delta: -1, ServiceRecordID: ptr(int64(3))},
			MovementEdit{Amount: 1}, "", 0, nil, "form"},
		{"parts of a deleted vehicle's service can be fixed", Movement{Kind: MoveService, Delta: -1},
			MovementEdit{Amount: 2}, MoveService, -2, nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.m.ItemID, tt.m.OccurredOn = 7, day
			in, errs := ApplyEdit(tt.m, tt.e)
			if _, ok := errs[tt.errField]; tt.errField != "" && !ok {
				t.Fatalf("want error on %s, got %v", tt.errField, errs)
			} else if tt.errField == "" && errs.Any() {
				t.Fatalf("unexpected errors: %v", errs)
			}
			if tt.errField == "form" {
				return
			}
			if in.Kind != tt.kind || in.Delta != tt.delta || in.ItemID != 7 {
				t.Errorf("got %s %v for item %d, want %s %v", in.Kind, in.Delta, in.ItemID, tt.kind, tt.delta)
			}
			if (in.TotalPrice == nil) != (tt.price == nil) || (in.TotalPrice != nil && *in.TotalPrice != *tt.price) {
				t.Errorf("price = %v, want %v", in.TotalPrice, tt.price)
			}
			if !in.OccurredOn.Equal(day) {
				t.Errorf("date = %v, want it kept", in.OccurredOn)
			}
		})
	}
	m := Movement{Kind: MovePurchase, Delta: 1, OccurredOn: day}
	if in, _ := ApplyEdit(m, MovementEdit{Amount: 1, OccurredOn: &other}); !in.OccurredOn.Equal(other) {
		t.Errorf("date = %v, want %v", in.OccurredOn, other)
	}
}
