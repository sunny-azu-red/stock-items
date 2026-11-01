package store_test

import (
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

func TestItemCreateUpdateDelete(t *testing.T) {
	s, ctx := newStore(t)
	a, b := mustVehicle(t, s, "KTM 390"), mustVehicle(t, s, "KTM 1290")
	exp := time.Date(2027, 5, 1, 0, 0, 0, 0, time.UTC)
	id := mustItem(t, s, stock.ItemInput{
		Name: "15W50 PRO 4 XP", Brand: "Silkolene", Category: "Oil & fluids", Unit: stock.UnitML, Quantity: 525,
		MinQuantity: ptr(1000.0), ExpiresOn: &exp, Location: "Shelf A",
		SKUs:       []stock.SKU{{Code: "80085", Kind: stock.SKUManufacturer, Label: "1 L", PackSize: ptr(1000.0)}, {Code: "80086", Kind: stock.SKUManufacturer, Label: "4 L", PackSize: ptr(4000.0)}},
		VehicleIDs: []int64{a, b},
	})
	it := mustGet(t, s, id)
	if it.Quantity != 525 || len(it.SKUs) != 2 || it.SKUs[1].Code != "80086" || *it.SKUs[1].PackSize != 4000 ||
		len(it.VehicleIDs) != 2 || !it.ExpiresOn.Equal(exp) || !it.IsLow() {
		t.Errorf("created item = %+v", it)
	}

	upd := stock.ItemInput{Name: "15W50 PRO 4 XP", Brand: "Silkolene", Unit: stock.UnitML, Quantity: 1500,
		SKUs: []stock.SKU{{Code: "80086", Kind: stock.SKUManufacturer}}, VehicleIDs: []int64{a}}
	if err := s.UpdateItem(ctx, id, itemV(t, s, id, upd), time.Now()); err != nil {
		t.Fatal(err)
	}
	it = mustGet(t, s, id)
	if it.Quantity != 1500 || len(it.SKUs) != 1 || len(it.VehicleIDs) != 1 || it.MinQuantity != nil {
		t.Errorf("updated item = %+v", it)
	}
	moves, _ := s.ListMovements(ctx, store.MovementFilter{ItemID: id})
	if len(moves) != 2 || moves[0].Kind != stock.MoveCorrection || moves[0].Delta != 975 || moves[1].Kind != stock.MoveInitial {
		t.Errorf("movements = %+v", moves)
	}

	upd.Unit = stock.UnitPiece
	var conflict *store.ConflictError
	if err := s.UpdateItem(ctx, id, itemV(t, s, id, upd), time.Now()); !errors.As(err, &conflict) || conflict.Field != "unit" {
		t.Errorf("unit change with history: want conflict, got %v", err)
	}
	assertLedger(t, s)

	if _, err := s.DeleteItem(ctx, id, cur(t, s, store.WhatItem, id)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetItem(ctx, id); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	if err := s.UpdateItem(ctx, id, itemV(t, s, id, upd), time.Now()); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("update missing: want ErrNotFound, got %v", err)
	}
}

func TestListItemsFilters(t *testing.T) {
	s, ctx := newStore(t)
	k390, k1290, car := mustVehicle(t, s, "KTM 390"), mustVehicle(t, s, "KTM 1290"), mustVehicle(t, s, "Car")
	soon := time.Now().AddDate(0, 0, 10)
	mustItem(t, s, stock.ItemInput{Name: "Rear Brake Pads", Brand: "OEM", Category: "Brakes", Unit: stock.UnitPiece, Quantity: 1, VehicleIDs: []int64{k390},
		SKUs: []stock.SKU{{Code: "90313090000", Kind: stock.SKUOEM}}})
	mustItem(t, s, stock.ItemInput{Name: "Oil Filter", Brand: "Hiflofiltro", Category: "Filters", Unit: stock.UnitPiece, Quantity: 1, VehicleIDs: []int64{k1290},
		SKUs: []stock.SKU{{Code: "HF-650", Kind: stock.SKUManufacturer}}})
	mustItem(t, s, stock.ItemInput{Name: "M3.0 Ready To Use", Brand: "Motorex", Unit: stock.UnitML, Quantity: 640, VehicleIDs: []int64{k390, k1290}, ExpiresOn: &soon})
	mustItem(t, s, stock.ItemInput{Name: "Brake cleaner", Unit: stock.UnitML, Quantity: 0, MinQuantity: ptr(500.0), ExpiresOn: &soon})
	mustItem(t, s, stock.ItemInput{Name: "Wiper blades", Unit: stock.UnitPiece, Quantity: 2, VehicleIDs: []int64{car}})

	tests := []struct {
		name string
		f    store.ItemFilter
		want []string
	}{
		{"all", store.ItemFilter{}, []string{"Brake cleaner", "Oil Filter", "M3.0 Ready To Use", "Rear Brake Pads", "Wiper blades"}},
		{"any 390", store.ItemFilter{VehicleIDs: []int64{k390}}, []string{"M3.0 Ready To Use", "Rear Brake Pads"}},
		{"any 390/1290", store.ItemFilter{VehicleIDs: []int64{k390, k1290}}, []string{"Oil Filter", "M3.0 Ready To Use", "Rear Brake Pads"}},
		{"all 390+1290", store.ItemFilter{VehicleIDs: []int64{k390, k1290}, MatchAll: true}, []string{"M3.0 Ready To Use"}},
		{"general", store.ItemFilter{General: true}, []string{"Brake cleaner"}},
		{"car + general", store.ItemFilter{VehicleIDs: []int64{car}, General: true}, []string{"Brake cleaner", "Wiper blades"}},
		{"search name", store.ItemFilter{Query: "brake"}, []string{"Brake cleaner", "Rear Brake Pads"}},
		{"search brand+name", store.ItemFilter{Query: "OEM Rear"}, []string{"Rear Brake Pads"}},
		{"search sku", store.ItemFilter{Query: "90313090000"}, []string{"Rear Brake Pads"}},
		{"search sku normalized", store.ItemFilter{Query: "hf650"}, []string{"Oil Filter"}},
		{"search wildcard is literal", store.ItemFilter{Query: "%"}, []string{}},
		{"category", store.ItemFilter{Category: "filters"}, []string{"Oil Filter"}},
		{"low", store.ItemFilter{LowStock: true}, []string{"Brake cleaner"}},
		{"expiring (with stock left)", store.ItemFilter{Expiring: true}, []string{"M3.0 Ready To Use"}},
		{"in stock", store.ItemFilter{InStock: true, VehicleIDs: []int64{car}, General: true}, []string{"Wiper blades"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.ListItems(ctx, tt.f)
			if err != nil {
				t.Fatal(err)
			}
			names := ids(got)
			if len(names) != len(tt.want) {
				t.Fatalf("got %v, want %v", names, tt.want)
			}
			for i := range names {
				if names[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", names, tt.want)
				}
			}
		})
	}
	cats, err := s.Categories(ctx)
	if err != nil || len(cats) < 3 {
		t.Errorf("categories = %v, %v", cats, err)
	}
	brands, err := s.Brands(ctx)
	if want := []string{"Hiflofiltro", "Motorex", "OEM"}; err != nil || !slices.Equal(brands, want) {
		t.Errorf("brands = %v, %v; want %v", brands, err, want)
	}
}

func TestPhotos(t *testing.T) {
	s, ctx := newStore(t)
	id := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece})
	if replaced, err := s.SetPhoto(ctx, id, "image/jpeg", []byte("full"), []byte("thumb")); err != nil || replaced != 0 {
		t.Fatalf("first photo: %d, %v", replaced, err)
	}
	replaced, err := s.SetPhoto(ctx, id, "image/jpeg", []byte("full2"), []byte("thumb2"))
	if err != nil || replaced == 0 {
		t.Fatalf("replacing: %d, %v; want the old one in the trash", replaced, err)
	}
	p, err := s.GetPhoto(ctx, id, true)
	if err != nil || string(p.Data) != "thumb2" {
		t.Errorf("thumb = %q, %v", p.Data, err)
	}
	if !mustGet(t, s, id).HasPhoto {
		t.Error("HasPhoto should be true")
	}
	// Undoing the replace swaps them: the old one is back, the new one in
	// the trash.
	res := mustRestore(t, s, replaced)
	if p, _ := s.GetPhoto(ctx, id, true); string(p.Data) != "thumb" {
		t.Errorf("after undo: thumb = %q", p.Data)
	}
	entries, _ := s.ListTrash(ctx)
	if len(entries) != 1 || entries[0].Kind != store.TrashPhoto || entries[0].ID != res.Replaced || entries[0].Label != "Photo: Pads (replaced)" {
		t.Errorf("trash = %+v, restored = %+v; want the newer photo", entries, res)
	}
	if _, err := s.SetPhoto(ctx, 987654, "image/jpeg", []byte("x"), []byte("x")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	if _, err := s.DeletePhoto(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetPhoto(ctx, id, false); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

// Saving the edit form with the quantity left as it was must not undo
// stock changes made while the form was open.
func TestUpdateItemKeepsStockChangedMeanwhile(t *testing.T) {
	s, ctx := newStore(t)
	id := mustItem(t, s, stock.ItemInput{Name: "Spark plug", Unit: stock.UnitPiece, Quantity: 5})
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: id, Kind: stock.MoveUse, Delta: -2}); err != nil {
		t.Fatal(err)
	}
	form := stock.ItemInput{Name: "Spark plug", Unit: stock.UnitPiece, Quantity: 5, QuantityWas: ptr(5.0), Notes: "NGK"}
	if err := s.UpdateItem(ctx, id, itemV(t, s, id, form), time.Now()); err != nil {
		t.Fatal(err)
	}
	if it := mustGet(t, s, id); it.Quantity != 3 || it.Notes != "NGK" {
		t.Errorf("after untouched quantity: %v (%q), want 3", it.Quantity, it.Notes)
	}
	form.Quantity = 8
	if err := s.UpdateItem(ctx, id, itemV(t, s, id, form), time.Now()); err != nil {
		t.Fatal(err)
	}
	if it := mustGet(t, s, id); it.Quantity != 8 {
		t.Errorf("after edited quantity: %v, want 8", it.Quantity)
	}

	if d, err := s.CorrectStock(ctx, id, 8, "count", time.Now(), ""); err != nil || d != 0 {
		t.Errorf("CorrectStock to same = %v, %v", d, err)
	}
	if d, err := s.CorrectStock(ctx, id, 6, "count", time.Now(), ""); err != nil || d != -2 {
		t.Errorf("CorrectStock to 6 = %v, %v", d, err)
	}
	// Deltas are rounded to the two decimals the ledger keeps (the pages
	// refuse finer amounts; direct calls get them rounded).
	var short *store.InsufficientStockError
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: id, Kind: stock.MoveUse, Delta: -6.006}); !errors.As(err, &short) {
		t.Errorf("use 6.006 (6.01) of 6: want InsufficientStockError, got %v", err)
	}
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: id, Kind: stock.MoveUse, Delta: -0.0049}); err == nil ||
		!strings.Contains(err.Error(), "must not be zero") {
		t.Errorf("use 0.0049 (0): want a zero amount refused, got %v", err)
	}
	assertLedger(t, s)
}

func TestUnitLockedOnceUsedInKitOrShoppingList(t *testing.T) {
	s, ctx := newStore(t)
	v := mustVehicle(t, s, "KTM 390")
	in := stock.ItemInput{Name: "Coolant", Unit: stock.UnitML}
	id := mustItem(t, s, in)
	if _, err := s.CreateKit(ctx, stock.KitInput{VehicleID: v, Name: "Coolant", Lines: []stock.KitLineInput{{ItemID: id, Quantity: 1500}}}); err != nil {
		t.Fatal(err)
	}
	in.Unit = stock.UnitPiece
	var conflict *store.ConflictError
	if err := s.UpdateItem(ctx, id, itemV(t, s, id, in), time.Now()); !errors.As(err, &conflict) || conflict.Field != "unit" {
		t.Errorf("unit change of an item in a kit: want conflict, got %v", err)
	}
	other := mustItem(t, s, stock.ItemInput{Name: "Grease", Unit: stock.UnitGram})
	if _, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &other, Quantity: ptr(400.0)}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateItem(ctx, other, itemV(t, s, other, stock.ItemInput{Name: "Grease", Unit: stock.UnitPiece}), time.Now()); !errors.As(err, &conflict) {
		t.Errorf("unit change of an item on the list: want conflict, got %v", err)
	}
}

func TestNewItemWithPriceIsAPurchase(t *testing.T) {
	s, ctx := newStore(t)
	today := stock.DateOnly(time.Now())
	antifreeze := mustItem(t, s, stock.ItemInput{Name: "Antigel M3.0", Unit: stock.UnitML, Quantity: 1000,
		TotalPrice: ptr(67.0), Supplier: " moto24 ", BoughtOn: &today})
	longAgo := today.AddDate(-2, 0, 0)
	pads := mustItem(t, s, stock.ItemInput{Name: "Brake Pads", Unit: stock.UnitPiece, Quantity: 2,
		TotalPrice: ptr(10.0), BoughtOn: &longAgo})
	shelf := mustItem(t, s, stock.ItemInput{Name: "Old grease", Unit: stock.UnitGram, Quantity: 300})

	moves, _ := s.ListMovements(ctx, store.MovementFilter{ItemID: antifreeze})
	if len(moves) != 1 || moves[0].Kind != stock.MovePurchase || moves[0].Supplier != "moto24" ||
		moves[0].TotalPrice == nil || *moves[0].TotalPrice != 67 {
		t.Errorf("antifreeze movements = %+v", moves)
	}
	if p, err := s.LastUnitPrice(ctx, antifreeze); err != nil || p == nil || *p != 0.067 {
		t.Errorf("last unit price = %v, %v", p, err)
	}
	if moves, _ := s.ListMovements(ctx, store.MovementFilter{ItemID: shelf}); len(moves) != 1 || moves[0].Kind != stock.MoveInitial {
		t.Errorf("stock already on the shelf should be initial: %+v", moves)
	}
	for _, c := range []struct {
		id   int64
		want int
	}{{antifreeze, 1}, {pads, 1}, {shelf, 0}} {
		if n, err := s.CountPurchases(ctx, c.id); err != nil || n != c.want {
			t.Errorf("CountPurchases(%d) = %d, %v; want %d", c.id, n, err, c.want)
		}
	}

	// Both priced items count in the stock value; only the recent purchase
	// counts as spending.
	st, err := s.GetStats(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(st.StockValue-77) > 0.001 || math.Abs(st.Spent12m-67) > 0.001 {
		t.Errorf("stats = %+v, want value 77 and spent 67", st)
	}
	assertLedger(t, s)
}

func TestExpiringSkipsEmptyItems(t *testing.T) {
	s, ctx := newStore(t)
	expired := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	mustItem(t, s, stock.ItemInput{Name: "Used up", Unit: stock.UnitML, ExpiresOn: &expired})
	mustItem(t, s, stock.ItemInput{Name: "Still here", Unit: stock.UnitML, Quantity: 100, ExpiresOn: &expired})
	items, err := s.ListItems(ctx, store.ItemFilter{Expiring: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(items); !slices.Equal(got, []string{"Still here"}) {
		t.Errorf("expiring = %v", got)
	}
}

func TestSearchIgnoresCodesOfJustOneCharacter(t *testing.T) {
	s, _ := newStore(t)
	mustItem(t, s, stock.ItemInput{Name: "Bolt", Unit: stock.UnitPiece, SKUs: []stock.SKU{{Code: "M8-125", Kind: stock.SKUOther}}})
	mustItem(t, s, stock.ItemInput{Name: "Washer Ø8", Unit: stock.UnitPiece})
	for q, want := range map[string][]string{
		"Ø8":     {"Washer Ø8"}, // matched by name, not every code with an 8
		"m8 125": {"Bolt"},
		"m8.125": {"Bolt"},
	} {
		if got := ids(mustList(t, s, store.ItemFilter{Query: q})); !slices.Equal(got, want) {
			t.Errorf("search %q = %v, want %v", q, got, want)
		}
	}
}

func TestSortByQuantityGroupsUnits(t *testing.T) {
	s, _ := newStore(t)
	mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML, Quantity: 500})
	mustItem(t, s, stock.ItemInput{Name: "Bolts", Unit: stock.UnitPiece, Quantity: 20})
	mustItem(t, s, stock.ItemInput{Name: "Grease", Unit: stock.UnitGram, Quantity: 100})
	mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece, Quantity: 2})
	got := ids(mustList(t, s, store.ItemFilter{Sort: store.SortQuantity}))
	if want := []string{"Grease", "Oil", "Pads", "Bolts"}; !slices.Equal(got, want) {
		t.Errorf("sorted = %v, want %v", got, want)
	}
}

func TestArchivedItemQuantityCantBeEdited(t *testing.T) {
	s, ctx := newStore(t)
	id := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece, Quantity: 3})
	if _, err := s.ArchiveItem(ctx, id, cur(t, s, store.WhatItem, id)); err != nil {
		t.Fatal(err)
	}
	upd := stock.ItemInput{Name: "Pads (old)", Unit: stock.UnitPiece, Quantity: 3, QuantityWas: ptr(3.0)}
	if err := s.UpdateItem(ctx, id, itemV(t, s, id, upd), time.Now()); err != nil {
		t.Errorf("renaming an archived item: %v", err)
	}
	upd.Quantity = 5
	var conflict *store.ConflictError
	if err := s.UpdateItem(ctx, id, itemV(t, s, id, upd), time.Now()); !errors.As(err, &conflict) || conflict.Field != "quantity" {
		t.Errorf("changing an archived item's quantity: want conflict, got %v", err)
	}
	if it := mustGet(t, s, id); it.Quantity != 3 {
		t.Errorf("quantity = %v", it.Quantity)
	}
	assertLedger(t, s)
}

func TestFitsAllPlusGeneral(t *testing.T) {
	s, ctx := newStore(t)
	a, b := mustVehicle(t, s, "A"), mustVehicle(t, s, "B")
	mustItem(t, s, stock.ItemInput{Name: "Both", Unit: stock.UnitPiece, VehicleIDs: []int64{a, b}})
	mustItem(t, s, stock.ItemInput{Name: "Only A", Unit: stock.UnitPiece, VehicleIDs: []int64{a}})
	mustItem(t, s, stock.ItemInput{Name: "General", Unit: stock.UnitPiece})
	got := ids(mustList(t, s, store.ItemFilter{VehicleIDs: []int64{a, b}, MatchAll: true, General: true}))
	if want := []string{"Both", "General"}; !slices.Equal(got, want) {
		t.Errorf("fits all + general = %v, want %v", got, want)
	}
	_ = ctx
}
