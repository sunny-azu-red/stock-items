package store_test

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
	"github.com/sunny-azu-red/stock-items/internal/testdb"
)

func ptr[T any](v T) *T { return &v }

func newStore(t *testing.T) (*store.Store, context.Context) {
	t.Helper()
	return store.New(testdb.New(t)), context.Background()
}

func mustVehicle(t *testing.T, s *store.Store, name string) int64 {
	t.Helper()
	in := stock.VehicleInput{Name: name}
	in.Normalize()
	id, err := s.CreateVehicle(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustItem(t *testing.T, s *store.Store, in stock.ItemInput) int64 {
	t.Helper()
	in.Normalize()
	if errs := in.Validate(); errs.Any() {
		t.Fatal(errs)
	}
	id, err := s.CreateItem(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func mustGet(t *testing.T, s *store.Store, id int64) stock.Item {
	t.Helper()
	it, err := s.GetItem(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

// assertLedger checks that every item's quantity equals the sum of its
// stock movements.
func assertLedger(t *testing.T, s *store.Store) {
	t.Helper()
	ctx := context.Background()
	items, err := s.ListItems(ctx, store.ItemFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		moves, err := s.ListMovements(ctx, store.MovementFilter{ItemID: it.ID})
		if err != nil {
			t.Fatal(err)
		}
		sum := 0.0
		for _, m := range moves {
			sum += m.Delta
		}
		// Compare in thousandths: float sums of 0.1 + 0.2 are not exact.
		if math.Round(sum*1000) != math.Round(it.Quantity*1000) {
			t.Errorf("ledger mismatch for %s: quantity %v, movements sum %v", it.Name, it.Quantity, sum)
		}
	}
}

func ids(items []stock.Item) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Name
	}
	return out
}

func TestVehicles(t *testing.T) {
	s, ctx := newStore(t)
	id := mustVehicle(t, s, "KTM 390")

	dup := stock.VehicleInput{Name: "ktm 390"}
	dup.Normalize()
	var conflict *store.ConflictError
	if _, err := s.CreateVehicle(ctx, dup); !errors.As(err, &conflict) || conflict.Field != "name" {
		t.Fatalf("duplicate name: want ConflictError, got %v", err)
	}

	upd := stock.VehicleInput{Name: "KTM 390 Duke", Kind: stock.KindMotorcycle, Make: "KTM", Year: ptr(2021), Color: "#ff6600", OdometerKm: ptr(12000)}
	if err := s.UpdateVehicle(ctx, id, upd); err != nil {
		t.Fatal(err)
	}
	v, err := s.GetVehicle(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "KTM 390 Duke" || *v.Year != 2021 || *v.OdometerKm != 12000 || v.Subtitle() != "KTM (2021)" {
		t.Errorf("vehicle = %+v", v)
	}
	if err := s.SetOdometer(ctx, id, 12500); err != nil {
		t.Fatal(err)
	}

	itemID := mustItem(t, s, stock.ItemInput{Name: "Oil Filter", Unit: stock.UnitPiece, VehicleIDs: []int64{id}})
	list, _ := s.ListVehicles(ctx)
	if len(list) != 1 || list[0].ItemCount != 1 || *list[0].OdometerKm != 12500 {
		t.Errorf("list = %+v", list)
	}

	if err := s.DeleteVehicle(ctx, id); err != nil {
		t.Fatal(err)
	}
	if it := mustGet(t, s, itemID); len(it.VehicleIDs) != 0 {
		t.Errorf("item should be untagged after vehicle delete, got %v", it.VehicleIDs)
	}
	if _, err := s.GetVehicle(ctx, id); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	if err := s.DeleteVehicle(ctx, id); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("double delete: want ErrNotFound, got %v", err)
	}
}

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
	if err := s.UpdateItem(ctx, id, upd); err != nil {
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
	if err := s.UpdateItem(ctx, id, upd); !errors.As(err, &conflict) || conflict.Field != "unit" {
		t.Errorf("unit change with history: want conflict, got %v", err)
	}
	assertLedger(t, s)

	dupID, err := s.DuplicateItem(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	dup := mustGet(t, s, dupID)
	if dup.Name != "15W50 PRO 4 XP (copy)" || dup.Quantity != 0 || len(dup.SKUs) != 1 || len(dup.VehicleIDs) != 1 {
		t.Errorf("duplicate = %+v", dup)
	}

	if err := s.DeleteItem(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetItem(ctx, id); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	if err := s.UpdateItem(ctx, id, upd); !errors.Is(err, store.ErrNotFound) {
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

func TestMovements(t *testing.T) {
	s, ctx := newStore(t)
	id := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML, Quantity: 1000})

	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: id, Kind: stock.MovePurchase, Delta: 4000, TotalPrice: ptr(60.0), Supplier: "Shop"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: id, Kind: stock.MoveUse, Delta: -1700}); err != nil {
		t.Fatal(err)
	}
	var short *store.InsufficientStockError
	err := s.AddMovement(ctx, stock.MovementInput{ItemID: id, Kind: stock.MoveUse, Delta: -5000})
	if !errors.As(err, &short) || short.Have != 3300 || short.Need != 5000 {
		t.Fatalf("want InsufficientStockError, got %v", err)
	}
	if it := mustGet(t, s, id); it.Quantity != 3300 {
		t.Errorf("quantity = %v, want 3300", it.Quantity)
	}
	var verrs stock.Errors
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: id, Kind: stock.MovePurchase, Delta: -1}); !errors.As(err, &verrs) {
		t.Errorf("want validation errors, got %v", err)
	}
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: 999999, Kind: stock.MovePurchase, Delta: 1}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	assertLedger(t, s)

	price, err := s.LastUnitPrice(ctx, id)
	if err != nil || price == nil || *price != 0.015 {
		t.Errorf("last unit price = %v, %v", price, err)
	}
	st, err := s.GetStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.StockValue != 49.5 || st.Spent12m != 60 {
		t.Errorf("stats = %+v", st)
	}
	all, _ := s.ListMovements(ctx, store.MovementFilter{Limit: 2})
	if len(all) != 2 || all[0].ItemName != "Oil" {
		t.Errorf("recent movements = %+v", all)
	}
}

func TestKitsAndServices(t *testing.T) {
	s, ctx := newStore(t)
	k390, k1290 := mustVehicle(t, s, "KTM 390"), mustVehicle(t, s, "KTM 1290")
	filter := mustItem(t, s, stock.ItemInput{Name: "Oil Filter", Brand: "OEM", Unit: stock.UnitPiece, Quantity: 2, VehicleIDs: []int64{k390}})
	oil := mustItem(t, s, stock.ItemInput{Name: "15W50", Brand: "Silkolene", Unit: stock.UnitML, Quantity: 525, VehicleIDs: []int64{k390}})

	kitIn := stock.KitInput{VehicleID: k390, Name: "Oil service", IntervalKm: ptr(7500), IntervalMonths: ptr(12),
		Lines: []stock.KitLineInput{{ItemID: filter, Quantity: 1}, {ItemID: oil, Quantity: 1700}}}
	kitIn.Normalize()
	kitID, err := s.CreateKit(ctx, kitIn)
	if err != nil {
		t.Fatal(err)
	}
	kit, err := s.GetKit(ctx, kitID)
	if err != nil {
		t.Fatal(err)
	}
	if r := kit.Readiness(); r.Lines != 2 || r.ShortLines != 1 || kit.VehicleName != "KTM 390" || kit.LastService != nil {
		t.Errorf("kit = %+v readiness %+v", kit, r)
	}
	if got := kit.Due(nil, time.Now()).State; got != stock.DueNever {
		t.Errorf("due state = %s", got)
	}

	// Not enough oil: the whole service must roll back.
	svc := stock.ServiceInput{VehicleID: k390, KitID: &kitID, PerformedOn: time.Now(), OdometerKm: ptr(15000), Title: "Oil service",
		Parts: []stock.KitLineInput{{ItemID: filter, Quantity: 1}, {ItemID: oil, Quantity: 1700}}}
	var short *store.InsufficientStockError
	if _, err := s.CreateService(ctx, svc); !errors.As(err, &short) || short.ItemName != "Silkolene 15W50" {
		t.Fatalf("want InsufficientStockError, got %v", err)
	}
	if it := mustGet(t, s, filter); it.Quantity != 2 {
		t.Errorf("filter quantity after rollback = %v", it.Quantity)
	}
	if list, _ := s.ListServices(ctx, store.ServiceFilter{}); len(list) != 0 {
		t.Errorf("service should not exist after rollback: %+v", list)
	}

	// Shortfall goes to the shopping list.
	for _, l := range kit.Lines {
		if short := l.Short(); short > 0 {
			if err := s.EnsureShopping(ctx, l.ItemID, short, "For "+kit.Name); err != nil {
				t.Fatal(err)
			}
		}
	}
	list, _ := s.ListShopping(ctx)
	if len(list) != 1 || *list[0].Quantity != 1175 || list[0].ItemName != "Silkolene 15W50" {
		t.Fatalf("shopping = %+v", list)
	}
	if _, err := s.MarkBought(ctx, list[0].ID, &stock.MovementInput{Delta: 4000, TotalPrice: ptr(55.0)}); err != nil {
		t.Fatal(err)
	}

	// Kit from another vehicle is rejected.
	wrong := svc
	wrong.VehicleID = k1290
	var conflict *store.ConflictError
	if _, err := s.CreateService(ctx, wrong); !errors.As(err, &conflict) {
		t.Errorf("want ConflictError for foreign kit, got %v", err)
	}

	svcID, err := s.CreateService(ctx, svc)
	if err != nil {
		t.Fatal(err)
	}
	if it := mustGet(t, s, oil); it.Quantity != 2825 {
		t.Errorf("oil after service = %v, want 2825", it.Quantity)
	}
	v, _ := s.GetVehicle(ctx, k390)
	if v.OdometerKm == nil || *v.OdometerKm != 15000 {
		t.Errorf("odometer = %v", v.OdometerKm)
	}
	rec, err := s.GetService(ctx, svcID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Parts) != 2 || rec.Parts[1].Quantity != 1700 || rec.KitName != "Oil service" || rec.PartCount != 2 {
		t.Errorf("service = %+v", rec)
	}
	kit, _ = s.GetKit(ctx, kitID)
	if kit.LastService == nil || kit.LastService.ID != svcID {
		t.Errorf("last service = %+v", kit.LastService)
	}
	if got := kit.Due(v.OdometerKm, time.Now()).State; got != stock.DueOK {
		t.Errorf("due after service = %s", got)
	}

	// A lower odometer reading never moves the odometer backwards.
	older := stock.ServiceInput{VehicleID: k390, PerformedOn: time.Now().AddDate(-1, 0, 0), OdometerKm: ptr(9000), Title: "Chain"}
	if _, err := s.CreateService(ctx, older); err != nil {
		t.Fatal(err)
	}
	v, _ = s.GetVehicle(ctx, k390)
	if *v.OdometerKm != 15000 {
		t.Errorf("odometer moved backwards to %d", *v.OdometerKm)
	}
	assertLedger(t, s)

	if err := s.DeleteService(ctx, svcID); err != nil {
		t.Fatal(err)
	}
	if it := mustGet(t, s, oil); it.Quantity != 4525 {
		t.Errorf("oil after deleting service = %v, want 4525", it.Quantity)
	}
	if it := mustGet(t, s, filter); it.Quantity != 2 {
		t.Errorf("filter after deleting service = %v, want 2", it.Quantity)
	}
	assertLedger(t, s)

	using, _ := s.ListKits(ctx, store.KitFilter{ItemID: oil})
	if len(using) != 1 {
		t.Errorf("kits using oil = %d", len(using))
	}
	if err := s.DeleteKit(ctx, kitID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetKit(ctx, kitID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestShoppingList(t *testing.T) {
	s, ctx := newStore(t)
	pads := mustItem(t, s, stock.ItemInput{Name: "Brake Pads", Unit: stock.UnitPiece})

	if _, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads, Quantity: ptr(1.0)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads, Quantity: ptr(2.0), Note: "sintered"}); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureShopping(ctx, pads, 2, ""); err != nil {
		t.Fatal(err)
	}
	freeID, err := s.AddShopping(ctx, stock.ShoppingInput{FreeText: "Chain lube"})
	if err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListShopping(ctx)
	if len(list) != 2 || *list[0].Quantity != 3 || list[0].Note != "sintered" || list[1].Title() != "Chain lube" {
		t.Fatalf("list = %+v", list)
	}
	if _, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: ptr(int64(424242))}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("unknown item: want ErrNotFound, got %v", err)
	}

	if _, err := s.MarkBought(ctx, list[0].ID, &stock.MovementInput{Delta: 3, TotalPrice: ptr(42.0), Supplier: "KTM dealer"}); err != nil {
		t.Fatal(err)
	}
	if it := mustGet(t, s, pads); it.Quantity != 3 {
		t.Errorf("pads after purchase = %v", it.Quantity)
	}
	if _, err := s.MarkBought(ctx, freeID, nil); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListShopping(ctx); len(list) != 0 {
		t.Errorf("list should be empty: %+v", list)
	}
	if err := s.DeleteShopping(ctx, freeID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	assertLedger(t, s)
}

func TestPhotos(t *testing.T) {
	s, ctx := newStore(t)
	id := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece})
	if err := s.SetPhoto(ctx, id, "image/jpeg", []byte("full"), []byte("thumb")); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPhoto(ctx, id, "image/jpeg", []byte("full2"), []byte("thumb2")); err != nil {
		t.Fatal(err)
	}
	p, err := s.GetPhoto(ctx, id, true)
	if err != nil || string(p.Data) != "thumb2" {
		t.Errorf("thumb = %q, %v", p.Data, err)
	}
	if !mustGet(t, s, id).HasPhoto {
		t.Error("HasPhoto should be true")
	}
	if err := s.SetPhoto(ctx, 987654, "image/jpeg", []byte("x"), []byte("x")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
	if err := s.DeletePhoto(ctx, id); err != nil {
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
	if err := s.UpdateItem(ctx, id, form); err != nil {
		t.Fatal(err)
	}
	if it := mustGet(t, s, id); it.Quantity != 3 || it.Notes != "NGK" {
		t.Errorf("after untouched quantity: %v (%q), want 3", it.Quantity, it.Notes)
	}
	form.Quantity = 8
	if err := s.UpdateItem(ctx, id, form); err != nil {
		t.Fatal(err)
	}
	if it := mustGet(t, s, id); it.Quantity != 8 {
		t.Errorf("after edited quantity: %v, want 8", it.Quantity)
	}

	if d, err := s.CorrectStock(ctx, id, 8, "count"); err != nil || d != 0 {
		t.Errorf("CorrectStock to same = %v, %v", d, err)
	}
	if d, err := s.CorrectStock(ctx, id, 6, "count"); err != nil || d != -2 {
		t.Errorf("CorrectStock to 6 = %v, %v", d, err)
	}
	// Deltas are rounded to the three decimals the ledger keeps.
	var short *store.InsufficientStockError
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: id, Kind: stock.MoveUse, Delta: -6.0005}); !errors.As(err, &short) {
		t.Errorf("use 6.0005 of 6: want InsufficientStockError, got %v", err)
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
	if err := s.UpdateItem(ctx, id, in); !errors.As(err, &conflict) || conflict.Field != "unit" {
		t.Errorf("unit change of an item in a kit: want conflict, got %v", err)
	}
	other := mustItem(t, s, stock.ItemInput{Name: "Grease", Unit: stock.UnitGram})
	if _, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &other, Quantity: ptr(400.0)}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateItem(ctx, other, stock.ItemInput{Name: "Grease", Unit: stock.UnitPiece}); !errors.As(err, &conflict) {
		t.Errorf("unit change of an item on the list: want conflict, got %v", err)
	}
}

// Two services taking the same parts in opposite order must not deadlock.
func TestConcurrentServicesDoNotDeadlock(t *testing.T) {
	s, ctx := newStore(t)
	v := mustVehicle(t, s, "KTM 390")
	a := mustItem(t, s, stock.ItemInput{Name: "A", Unit: stock.UnitPiece, Quantity: 1000})
	b := mustItem(t, s, stock.ItemInput{Name: "B", Unit: stock.UnitPiece, Quantity: 1000})
	var wg sync.WaitGroup
	errs := make(chan error, 80)
	for g := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			parts := []stock.KitLineInput{{ItemID: a, Quantity: 1}, {ItemID: b, Quantity: 1}}
			if g%2 == 1 {
				parts[0], parts[1] = parts[1], parts[0]
			}
			for range 20 {
				_, err := s.CreateService(ctx, stock.ServiceInput{VehicleID: v, Title: "x", PerformedOn: time.Now(), Parts: parts})
				if err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if got := mustGet(t, s, a).Quantity; got != 920 {
		t.Errorf("A = %v, want 920", got)
	}
	assertLedger(t, s)
}

// A kit moved to another vehicle starts without the old vehicle's history.
func TestKitMovedToAnotherVehicle(t *testing.T) {
	s, ctx := newStore(t)
	v1, v2 := mustVehicle(t, s, "KTM 390"), mustVehicle(t, s, "KTM 1290")
	in := stock.KitInput{VehicleID: v1, Name: "Oil", IntervalKm: ptr(6000)}
	kit, err := s.CreateKit(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateService(ctx, stock.ServiceInput{VehicleID: v1, KitID: &kit, Title: "Oil", PerformedOn: time.Now(), OdometerKm: ptr(20000)}); err != nil {
		t.Fatal(err)
	}
	in.VehicleID = v2
	if err := s.UpdateKit(ctx, kit, in); err != nil {
		t.Fatal(err)
	}
	k, err := s.GetKit(ctx, kit)
	if err != nil {
		t.Fatal(err)
	}
	if k.LastService != nil {
		t.Errorf("moved kit kept the other vehicle's last service: %+v", k.LastService)
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
	st, err := s.GetStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(st.StockValue-77) > 0.001 || math.Abs(st.Spent12m-67) > 0.001 {
		t.Errorf("stats = %+v, want value 77 and spent 67", st)
	}
	assertLedger(t, s)
}

func TestCreateItemFromShopping(t *testing.T) {
	s, ctx := newStore(t)
	pads := mustItem(t, s, stock.ItemInput{Name: "Brake Pads", Unit: stock.UnitPiece})
	linked, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads, Quantity: ptr(2.0)})
	if err != nil {
		t.Fatal(err)
	}
	free, err := s.AddShopping(ctx, stock.ShoppingInput{FreeText: "Chain lube", Quantity: ptr(1.0)})
	if err != nil {
		t.Fatal(err)
	}

	in := stock.ItemInput{Name: "Chain lube", Unit: stock.UnitPiece, Quantity: 1, TotalPrice: ptr(45.0), Supplier: "Shop"}
	in.Normalize()
	id, err := s.CreateItemFromShopping(ctx, free, in)
	if err != nil {
		t.Fatal(err)
	}
	if it := mustGet(t, s, id); it.Name != "Chain lube" || it.Quantity != 1 {
		t.Errorf("created item = %+v", it)
	}
	if n, _ := s.CountPurchases(ctx, id); n != 1 {
		t.Errorf("purchases = %d, want 1", n)
	}
	if list, _ := s.ListShopping(ctx); len(list) != 1 || list[0].ID != linked {
		t.Errorf("only the linked entry should be left: %+v", list)
	}

	// A second submit of the same form, or an entry that is already an
	// item, creates nothing.
	for _, entry := range []int64{free, linked} {
		if _, err := s.CreateItemFromShopping(ctx, entry, in); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("entry %d: want ErrNotFound, got %v", entry, err)
		}
	}
	if items, _ := s.ListItems(ctx, store.ItemFilter{}); len(items) != 2 {
		t.Errorf("items = %v, want Brake Pads and one Chain lube", ids(items))
	}
	assertLedger(t, s)
}

func TestMarkBoughtKeepsTheRest(t *testing.T) {
	s, ctx := newStore(t)
	pads := mustItem(t, s, stock.ItemInput{Name: "Brake Pads", Unit: stock.UnitPiece})
	oil := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML})
	padsEntry, _ := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &pads, Quantity: ptr(3.0)})
	oilEntry, _ := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &oil})

	tests := []struct {
		name  string
		entry int64
		delta float64
		left  float64
		count int // entries left on the list
	}{
		{"less than listed", padsEntry, 1, 2, 2},
		{"the rest", padsEntry, 2, 0, 1},
		{"no quantity listed", oilEntry, 4000, 0, 0},
	}
	for _, tt := range tests {
		left, err := s.MarkBought(ctx, tt.entry, &stock.MovementInput{Delta: tt.delta})
		if err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		list, _ := s.ListShopping(ctx)
		if left != tt.left || len(list) != tt.count {
			t.Errorf("%s: left %v with %d entries, want %v with %d", tt.name, left, len(list), tt.left, tt.count)
		}
		i := slices.IndexFunc(list, func(e stock.ShoppingEntry) bool { return e.ID == tt.entry })
		if (i >= 0) != (tt.left > 0) || (i >= 0 && *list[i].Quantity != tt.left) {
			t.Errorf("%s: list = %+v", tt.name, list)
		}
	}
	if it := mustGet(t, s, pads); it.Quantity != 3 {
		t.Errorf("pads = %v, want 3", it.Quantity)
	}
	assertLedger(t, s)
}

func TestPurchaseSetsBestBefore(t *testing.T) {
	s, ctx := newStore(t)
	expired := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	fresh := time.Date(2029, 6, 30, 0, 0, 0, 0, time.UTC)
	id := mustItem(t, s, stock.ItemInput{Name: "Coolant", Unit: stock.UnitML, Quantity: 500, ExpiresOn: &expired})

	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: id, Kind: stock.MovePurchase, Delta: 1000}); err != nil {
		t.Fatal(err)
	}
	if it := mustGet(t, s, id); !it.ExpiresOn.Equal(expired) {
		t.Errorf("a purchase without a date should keep the expiry, got %v", it.ExpiresOn)
	}
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: id, Kind: stock.MovePurchase, Delta: 1000, ExpiresOn: &fresh}); err != nil {
		t.Fatal(err)
	}
	if it := mustGet(t, s, id); !it.ExpiresOn.Equal(fresh) {
		t.Errorf("expiry = %v, want %v", it.ExpiresOn, fresh)
	}
	var verrs stock.Errors
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: id, Kind: stock.MoveUse, Delta: -100, ExpiresOn: &expired}); !errors.As(err, &verrs) {
		t.Errorf("use with a best-before date: want validation error, got %v", err)
	}

	entry, _ := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &id})
	later := fresh.AddDate(1, 0, 0)
	if _, err := s.MarkBought(ctx, entry, &stock.MovementInput{Delta: 1000, ExpiresOn: &later}); err != nil {
		t.Fatal(err)
	}
	if it := mustGet(t, s, id); !it.ExpiresOn.Equal(later) {
		t.Errorf("expiry after shopping = %v, want %v", it.ExpiresOn, later)
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

func TestFreeTextShoppingKeepsItsUnit(t *testing.T) {
	s, ctx := newStore(t)
	in := stock.ShoppingInput{FreeText: "Coolant", Quantity: ptr(4000.0), Unit: stock.UnitML}
	in.Normalize()
	id, err := s.AddShopping(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	e, err := s.GetShopping(ctx, id)
	if err != nil || e.ItemUnit != stock.UnitML || *e.Quantity != 4000 {
		t.Errorf("entry = %+v, %v", e, err)
	}
	if got := stock.ExportShoppingText([]stock.ShoppingEntry{e}); !strings.Contains(got, "4 L") {
		t.Errorf("export = %q, want 4 L", got)
	}
}

func TestDeleteServicePutsOdometerBack(t *testing.T) {
	pool := testdb.New(t)
	s, ctx := store.New(pool), context.Background()
	odometer := func(id int64) int {
		t.Helper()
		v, err := s.GetVehicle(ctx, id)
		if err != nil || v.OdometerKm == nil {
			t.Fatalf("vehicle %d: %+v, %v", id, v, err)
		}
		return *v.OdometerKm
	}
	vehicle := func(name string, km int) int64 {
		in := stock.VehicleInput{Name: name, OdometerKm: &km}
		in.Normalize()
		id, err := s.CreateVehicle(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	service := func(vehicleID int64, km int) int64 {
		t.Helper()
		in := stock.ServiceInput{VehicleID: vehicleID, Title: "Chain clean", PerformedOn: time.Now(), OdometerKm: &km}
		in.Normalize()
		id, err := s.CreateService(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	// A typo is undone.
	typo := vehicle("Typo", 10000)
	if err := s.DeleteService(ctx, service(typo, 120000)); err != nil {
		t.Fatal(err)
	}
	if km := odometer(typo); km != 10000 {
		t.Errorf("after deleting the typo: %d, want 10000", km)
	}

	// Deleting the newest of two goes back to the older service.
	two := vehicle("Two", 10000)
	service(two, 11000)
	if err := s.DeleteService(ctx, service(two, 12000)); err != nil {
		t.Fatal(err)
	}
	if km := odometer(two); km != 11000 {
		t.Errorf("after deleting the newer service: %d, want 11000", km)
	}

	// An older service that never moved the odometer leaves it alone.
	older := service(two, 10500)
	if err := s.DeleteService(ctx, older); err != nil {
		t.Fatal(err)
	}
	if km := odometer(two); km != 11000 {
		t.Errorf("after deleting an older service: %d, want 11000", km)
	}

	// A reading entered by hand since then is kept.
	manual := vehicle("Manual", 10000)
	svc := service(manual, 12000)
	if err := s.SetOdometer(ctx, manual, 12500); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteService(ctx, svc); err != nil {
		t.Fatal(err)
	}
	if km := odometer(manual); km != 12500 {
		t.Errorf("after a manual reading: %d, want 12500", km)
	}

	// Records from before odometer_before existed fall back to the other
	// services, or stay put when there are none.
	legacy := vehicle("Legacy", 10000)
	service(legacy, 11000)
	newest := service(legacy, 12000)
	lone := vehicle("Lone", 10000)
	loneSvc := service(lone, 12000)
	if _, err := pool.Exec(ctx, `UPDATE service_records SET odometer_before = NULL`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{newest, loneSvc} {
		if err := s.DeleteService(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if km := odometer(legacy); km != 11000 {
		t.Errorf("legacy fallback: %d, want 11000", km)
	}
	if km := odometer(lone); km != 12000 {
		t.Errorf("legacy without other services: %d, want 12000 (unchanged)", km)
	}
}

func TestLastPurchases(t *testing.T) {
	s, ctx := newStore(t)
	oil := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML})
	pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece, Quantity: 2})
	older := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for _, in := range []stock.MovementInput{
		{ItemID: oil, Kind: stock.MovePurchase, Delta: 1000, TotalPrice: ptr(20.0), Supplier: "A", OccurredOn: &newer},
		{ItemID: oil, Kind: stock.MovePurchase, Delta: 4000, TotalPrice: ptr(60.0), Supplier: "B", OccurredOn: &older},
		{ItemID: oil, Kind: stock.MovePurchase, Delta: 1000, OccurredOn: &newer}, // no price: ignored
	} {
		if err := s.AddMovement(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.LastPurchases(ctx, []int64{oil, pads})
	if err != nil {
		t.Fatal(err)
	}
	lp, ok := got[oil]
	if len(got) != 1 || !ok || lp.UnitPrice != 0.02 || lp.Supplier != "A" || !lp.On.Equal(newer) {
		t.Errorf("last purchases = %+v", got)
	}
	if empty, err := s.LastPurchases(ctx, nil); err != nil || len(empty) != 0 {
		t.Errorf("no ids: %v, %v", empty, err)
	}
}

func TestUpdateAndDeleteMovements(t *testing.T) {
	s, ctx := newStore(t)
	oil := mustItem(t, s, stock.ItemInput{Name: "Oil", Unit: stock.UnitML, Quantity: 500})
	day := stock.DateOnly(time.Now())
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: oil, Kind: stock.MovePurchase, Delta: 1000, TotalPrice: ptr(670.0), OccurredOn: &day}); err != nil {
		t.Fatal(err)
	}
	moves, _ := s.ListMovements(ctx, store.MovementFilter{ItemID: oil, Kind: stock.MovePurchase})
	purchase := moves[0].ID

	// The price typo is fixed, and every money figure follows.
	if err := s.UpdateMovement(ctx, purchase, stock.MovementEdit{Amount: 1000, TotalPrice: ptr(67.0), Supplier: "moto24"}); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.LastUnitPrice(ctx, oil); p == nil || *p != 0.067 {
		t.Errorf("last unit price = %v, want 0.067", p)
	}
	if st, _ := s.GetStats(ctx); math.Abs(st.StockValue-100.5) > 0.001 || math.Abs(st.Spent12m-67) > 0.001 {
		t.Errorf("stats = %+v, want value 100.5 and spent 67", st)
	}

	// The amount moves the stock by the difference.
	if err := s.UpdateMovement(ctx, purchase, stock.MovementEdit{Amount: 4000, TotalPrice: ptr(67.0)}); err != nil {
		t.Fatal(err)
	}
	if it := mustGet(t, s, oil); it.Quantity != 4500 {
		t.Errorf("quantity = %v, want 4500", it.Quantity)
	}

	// Using 4.2 L leaves 300 ml: shrinking the purchase below what was used fails.
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: oil, Kind: stock.MoveUse, Delta: -4200}); err != nil {
		t.Fatal(err)
	}
	var short *store.InsufficientStockError
	if err := s.UpdateMovement(ctx, purchase, stock.MovementEdit{Amount: 500}); !errors.As(err, &short) || short.Have != 300 || short.Need != 3500 {
		t.Errorf("shrink used-up purchase: want InsufficientStockError(300, 3500), got %v", err)
	}
	if _, err := s.DeleteMovement(ctx, purchase); !errors.As(err, &short) {
		t.Errorf("delete used-up purchase: want InsufficientStockError, got %v", err)
	}

	// Deleting the use puts it back; then the purchase can go too.
	uses, _ := s.ListMovements(ctx, store.MovementFilter{ItemID: oil, Kind: stock.MoveUse})
	if _, err := s.DeleteMovement(ctx, uses[0].ID); err != nil {
		t.Fatal(err)
	}
	m, err := s.DeleteMovement(ctx, purchase)
	if err != nil || m.ItemID != oil || m.Kind != stock.MovePurchase {
		t.Fatalf("delete purchase = %+v, %v", m, err)
	}
	if it := mustGet(t, s, oil); it.Quantity != 500 {
		t.Errorf("quantity = %v, want 500", it.Quantity)
	}
	if n, _ := s.CountPurchases(ctx, oil); n != 0 {
		t.Errorf("purchases = %d, want 0", n)
	}
	if _, err := s.GetMovement(ctx, purchase); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("deleted movement: want ErrNotFound, got %v", err)
	}
	if err := s.UpdateMovement(ctx, purchase, stock.MovementEdit{Amount: 1}); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("update deleted movement: want ErrNotFound, got %v", err)
	}
	assertLedger(t, s)
}

func TestServiceMovementsChangeOnlyThroughTheService(t *testing.T) {
	s, ctx := newStore(t)
	k390 := mustVehicle(t, s, "KTM 390")
	pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece, Quantity: 2})
	in := stock.ServiceInput{VehicleID: k390, Title: "Brakes", PerformedOn: time.Now(), Parts: []stock.KitLineInput{{ItemID: pads, Quantity: 1}}}
	in.Normalize()
	if _, err := s.CreateService(ctx, in); err != nil {
		t.Fatal(err)
	}
	moves, _ := s.ListMovements(ctx, store.MovementFilter{ItemID: pads, Kind: stock.MoveService})
	var conflict *store.ConflictError
	if err := s.UpdateMovement(ctx, moves[0].ID, stock.MovementEdit{Amount: 2}); !errors.As(err, &conflict) {
		t.Errorf("update: want ConflictError, got %v", err)
	}
	if _, err := s.DeleteMovement(ctx, moves[0].ID); !errors.As(err, &conflict) {
		t.Errorf("delete: want ConflictError, got %v", err)
	}
	if it := mustGet(t, s, pads); it.Quantity != 1 {
		t.Errorf("quantity = %v, want 1", it.Quantity)
	}
	assertLedger(t, s)
}

func TestArchiveInsteadOfDelete(t *testing.T) {
	s, ctx := newStore(t)
	k390 := mustVehicle(t, s, "KTM 390")
	filter := mustItem(t, s, stock.ItemInput{Name: "Old filter", Unit: stock.UnitPiece, Quantity: 3})
	spare := mustItem(t, s, stock.ItemInput{Name: "Spare bolt", Unit: stock.UnitPiece, Quantity: 3})
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: filter, Kind: stock.MovePurchase, Delta: 2, TotalPrice: ptr(20.0)}); err != nil {
		t.Fatal(err)
	}
	kitIn := stock.KitInput{VehicleID: k390, Name: "Oil service", Lines: []stock.KitLineInput{{ItemID: filter, Quantity: 1}}}
	kitIn.Normalize()
	kitID, err := s.CreateKit(ctx, kitIn)
	if err != nil {
		t.Fatal(err)
	}
	svcIn := stock.ServiceInput{VehicleID: k390, Title: "Oil service", PerformedOn: time.Now(), Parts: []stock.KitLineInput{{ItemID: filter, Quantity: 1}}}
	svcIn.Normalize()
	svcID, err := s.CreateService(ctx, svcIn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddShopping(ctx, stock.ShoppingInput{ItemID: &filter}); err != nil {
		t.Fatal(err)
	}
	before, _ := s.GetStats(ctx)

	u, err := s.ItemUsage(ctx, filter)
	if err != nil || u != (stock.ItemUsage{Purchases: 1, Services: 1, Kits: 1}) {
		t.Fatalf("usage = %+v, %v", u, err)
	}
	var conflict *store.ConflictError
	if err := s.DeleteItem(ctx, filter); !errors.As(err, &conflict) || !strings.Contains(conflict.Message, "Archive it instead") {
		t.Fatalf("delete used item: want ConflictError, got %v", err)
	}

	removed, err := s.ArchiveItem(ctx, filter)
	if err != nil || !removed {
		t.Fatalf("archive = %v, %v; want it taken off the shopping list", removed, err)
	}
	if got := ids(mustList(t, s, store.ItemFilter{})); !slices.Equal(got, []string{"Spare bolt"}) {
		t.Errorf("active items = %v", got)
	}
	if got := ids(mustList(t, s, store.ItemFilter{Archived: store.ArchivedOnly})); !slices.Equal(got, []string{"Old filter"}) {
		t.Errorf("archived items = %v", got)
	}
	if it := mustGet(t, s, filter); !it.Archived() {
		t.Error("GetItem should find the archived item")
	}
	if list, _ := s.ListShopping(ctx); len(list) != 0 {
		t.Errorf("shopping = %+v", list)
	}

	// Everything that referred to it is unchanged.
	if svc, _ := s.GetService(ctx, svcID); svc.PartCount != 1 || len(svc.Parts) != 1 {
		t.Errorf("service parts = %+v", svc.Parts)
	}
	if kit, _ := s.GetKit(ctx, kitID); len(kit.Lines) != 1 || !kit.Lines[0].Archived {
		t.Errorf("kit lines = %+v", kit.Lines)
	}
	after, _ := s.GetStats(ctx)
	if after.Spent12m != before.Spent12m || after.StockValue != 0 || before.StockValue != 40 {
		t.Errorf("stats before %+v, after %+v: spending kept, archived stock not valued", before, after)
	}

	if err := s.RestoreItem(ctx, filter); err != nil {
		t.Fatal(err)
	}
	if got := ids(mustList(t, s, store.ItemFilter{})); len(got) != 2 {
		t.Errorf("after restore = %v", got)
	}

	// An item nothing refers to is deleted with its own history.
	if err := s.AddMovement(ctx, stock.MovementInput{ItemID: spare, Kind: stock.MoveUse, Delta: -1}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteItem(ctx, spare); err != nil {
		t.Fatalf("delete unused item: %v", err)
	}
	if err := s.RestoreItem(ctx, spare); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("restore deleted: want ErrNotFound, got %v", err)
	}
	if _, err := s.ArchiveItem(ctx, spare); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("archive deleted: want ErrNotFound, got %v", err)
	}
	assertLedger(t, s)
}

func mustList(t *testing.T, s *store.Store, f store.ItemFilter) []stock.Item {
	t.Helper()
	items, err := s.ListItems(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	return items
}
