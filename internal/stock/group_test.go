package stock

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// exampleInventory is the inventory from the original request.
func exampleInventory() ([]Item, []Vehicle) {
	ktm390 := Vehicle{ID: 1, Name: "KTM 390"}
	ktm1290 := Vehicle{ID: 2, Name: "KTM 1290"}
	car := Vehicle{ID: 3, Name: "Car"}
	piece := func(qty float64, brand, name string, vids ...int64) Item {
		return Item{Brand: brand, Name: name, Unit: UnitPiece, Quantity: qty, VehicleIDs: vids}
	}
	ml := func(qty float64, brand, name string, vids ...int64) Item {
		return Item{Brand: brand, Name: name, Unit: UnitML, Quantity: qty, VehicleIDs: vids}
	}
	items := []Item{
		piece(1, "OEM", "Rear Brake Pads", 1),
		piece(1, "OEM", "Fuel Filter", 1),
		piece(1, "OEM", "Air Filter", 1),
		piece(1, "OEM", "Oil Filter", 1),
		piece(2, "OEM", "Oil Screen 1 O-Ring", 1),
		piece(3, "OEM", "Oil Screen 2 O-Ring", 1),
		ml(525, "Silkolene", "15W50 PRO 4 XP", 1),
		piece(1, "OEM", "Rear Brake Pads", 2),
		piece(1, "Hiflofiltro", "Oil Filter", 2),
		piece(6, "OEM", "Oil Screen 1+2 O-Ring", 2),
		ml(1500, "Motorex", "10W50 Power Synt 4T", 2),
		piece(6, "OEM", "Oil Filter O-Ring", 2, 1), // tag order must not matter
		ml(640, "Motorex", "M3.0 Ready To Use", 1, 2),
	}
	return items, []Vehicle{ktm390, ktm1290, car}
}

func TestGroupByVehicles(t *testing.T) {
	items, vehicles := exampleInventory()
	items = append(items, Item{Name: "Brake cleaner", Unit: UnitML, Quantity: 500})
	items = append(items, Item{Name: "Zip ties", Unit: UnitPiece, Quantity: 40, VehicleIDs: []int64{3, 1, 2}})

	got := GroupByVehicles(items, vehicles)
	wantTitles := []string{"KTM 390", "KTM 1290", "KTM 390 + KTM 1290", "KTM 390 + KTM 1290 + Car", "General"}
	if len(got) != len(wantTitles) {
		t.Fatalf("got %d sections, want %d: %+v", len(got), len(wantTitles), got)
	}
	for i, s := range got {
		if s.Title != wantTitles[i] {
			t.Errorf("section %d title = %q, want %q", i, s.Title, wantTitles[i])
		}
	}
	if n := len(got[0].Items); n != 7 {
		t.Errorf("KTM 390 section has %d items, want 7", n)
	}
	if !got[2].Shared() || got[0].Shared() || !got[4].General() {
		t.Errorf("Shared/General flags wrong")
	}
	if got[2].Key != "v-1-2" || got[4].Key != "general" {
		t.Errorf("keys = %q, %q", got[2].Key, got[4].Key)
	}
}

func TestGroupIgnoresUnknownVehicles(t *testing.T) {
	items := []Item{{Name: "X", VehicleIDs: []int64{99}}}
	got := GroupByVehicles(items, []Vehicle{{ID: 1, Name: "A"}})
	if len(got) != 1 || !got[0].General() {
		t.Fatalf("unknown vehicle ids should fall into General, got %+v", got)
	}
}

func TestExportTextMatchesExample(t *testing.T) {
	items, vehicles := exampleInventory()
	want := `KTM 390
------------
1x - OEM Rear Brake Pads
1x - OEM Fuel Filter
1x - OEM Air Filter
1x - OEM Oil Filter
2x - OEM Oil Screen 1 O-Ring
3x - OEM Oil Screen 2 O-Ring
525 ml - Silkolene 15W50 PRO 4 XP


KTM 1290
------------
1x - OEM Rear Brake Pads
1x - Hiflofiltro Oil Filter
6x - OEM Oil Screen 1+2 O-Ring
1.5 L - Motorex 10W50 Power Synt 4T


KTM 390 + KTM 1290
------------------
6x - OEM Oil Filter O-Ring
640 ml - Motorex M3.0 Ready To Use
`
	got := ExportText(GroupByVehicles(items, vehicles))
	if got != want {
		t.Errorf("ExportText mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestDisplayName(t *testing.T) {
	tests := []struct{ brand, name, want string }{
		{"OEM", "Oil Filter", "OEM Oil Filter"},
		{"", "Oil Filter", "Oil Filter"},
		{"Motorex", "Motorex Power Synt", "Motorex Power Synt"},
		{" Motul ", " 7100 ", "Motul 7100"},
	}
	for _, tt := range tests {
		if got := (Item{Brand: tt.brand, Name: tt.name}).DisplayName(); got != tt.want {
			t.Errorf("DisplayName(%q,%q) = %q, want %q", tt.brand, tt.name, got, tt.want)
		}
	}
}

func TestExportShoppingText(t *testing.T) {
	two, oil := 2.0, 4000.0
	got := ExportShoppingText([]ShoppingEntry{
		{ItemName: "OEM Oil Filter", ItemUnit: UnitPiece, Quantity: &two},
		{ItemName: "Motorex Power Synt", ItemUnit: UnitML, Quantity: &oil, Note: "4 L can"},
		{FreeText: "Chain lube"},
	})
	want := "Shopping list\n-------------\n2x - OEM Oil Filter\n4 L - Motorex Power Synt (4 L can)\n- Chain lube\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestWriteCSV(t *testing.T) {
	min := 2.0
	pack := 1000.0
	bought := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	items := []Item{
		{ID: 7, Brand: "OEM", Name: "Oil Filter", Unit: UnitPiece, Quantity: 1, MinQuantity: &min,
			VehicleIDs: []int64{1}, SKUs: []SKU{{Code: "77038005044", Kind: SKUOEM}}, Notes: "=HYPERLINK()"},
		{ID: 8, Brand: "Motorex", Name: "Coolant", Unit: UnitML, Quantity: 1500,
			SKUs: []SKU{{Code: "M30", Kind: SKUManufacturer, Label: "1 L bottle", PackSize: &pack}}},
	}
	last := map[int64]LastPurchase{8: {UnitPrice: 0.067, On: bought, Supplier: "moto24"}}
	var buf bytes.Buffer
	if err := WriteCSV(&buf, items, []Vehicle{{ID: 1, Name: "KTM 390"}}, last); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	want := []string{
		"id,brand,name,category,unit,quantity,quantity_display,min_quantity,expires_on,location,vehicles,skus,notes," +
			"last_price,price_per,stock_value,last_bought_on,last_shop",
		// Never bought with a price: empty money columns.
		`7,OEM,Oil Filter,,piece,1,1x,2,,,KTM 390,OEM:77038005044,'=HYPERLINK(),,,,,`,
		// Priced per litre, valued at the last price.
		`8,Motorex,Coolant,,ml,1500,1.5 L,,,,,"Manufacturer:M30 (1 L bottle, pack 1 L)",,67.00,L,100.50,2026-10-01,moto24`,
	}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(lines), len(want), buf.String())
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q\nwant     %q", i, lines[i], want[i])
		}
	}
}

func TestWriteMovementsCSV(t *testing.T) {
	day := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	svc := int64(3)
	moves := []Movement{
		{ItemID: 8, ItemName: "Motorex Coolant", ItemUnit: UnitML, Kind: MovePurchase, Delta: 1000,
			TotalPrice: ptr(67.0), Supplier: "moto24", Note: "-promo", OccurredOn: day},
		{ItemID: 8, ItemName: "Motorex Coolant", ItemUnit: UnitML, Kind: MoveService, Delta: -500,
			Note: "Coolant change", OccurredOn: day, ServiceRecordID: &svc, ServiceTitle: "Coolant change"},
		{ItemID: 7, ItemName: "OEM Oil Filter", ItemUnit: UnitPiece, Kind: MoveUse, Delta: -1, OccurredOn: day},
	}
	var buf bytes.Buffer
	if err := WriteMovementsCSV(&buf, moves); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"date,item_id,item,kind,change,change_display,unit,total_price,unit_price,price_per,shop,note,service",
		"2026-10-29,8,Motorex Coolant,purchase,1000,'+1 L,ml,67.00,67.00,L,moto24,'-promo,", // "+1 L" would be a formula
		"2026-10-29,8,Motorex Coolant,service,-500,−500 ml,ml,,,,,,Coolant change",
		"2026-10-29,7,OEM Oil Filter,use,-1,−1x,piece,,,,,,",
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(lines), len(want), buf.String())
	}
	for i := range want {
		if lines[i] != want[i] {
			t.Errorf("line %d = %q\nwant     %q", i, lines[i], want[i])
		}
	}
}
