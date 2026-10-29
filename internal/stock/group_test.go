package stock

import (
	"bytes"
	"strings"
	"testing"
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
	items := []Item{{ID: 7, Brand: "OEM", Name: "Oil Filter", Unit: UnitPiece, Quantity: 1, MinQuantity: &min,
		VehicleIDs: []int64{1}, SKUs: []SKU{{Code: "77038005044", Kind: SKUOEM}}, Notes: "=HYPERLINK()"}}
	var buf bytes.Buffer
	if err := WriteCSV(&buf, items, []Vehicle{{ID: 1, Name: "KTM 390"}}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want header + 1 row, got %q", buf.String())
	}
	wantRow := `7,OEM,Oil Filter,,piece,1,1x,2,,,KTM 390,OEM:77038005044,'=HYPERLINK()`
	if lines[1] != wantRow {
		t.Errorf("row = %q\nwant  %q", lines[1], wantRow)
	}
}
