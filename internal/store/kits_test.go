package store_test

import (
	"errors"
	"testing"

	"github.com/sunny-azu-red/stock-items/internal/stock"
	"github.com/sunny-azu-red/stock-items/internal/store"
)

// A kit line read in another unit than its item has now is refused, and
// the message names the unit as amounts do ("pcs"), not its code.
func TestKitLineInAnOldUnitNamesTheUnit(t *testing.T) {
	s, ctx := newStore(t)
	v := mustVehicle(t, s, "KTM 390")
	pads := mustItem(t, s, stock.ItemInput{Name: "Pads", Unit: stock.UnitPiece, Quantity: 2})
	in := stock.KitInput{VehicleID: v, Name: "Brakes", Lines: []stock.KitLineInput{{ItemID: pads, Quantity: 500, Unit: stock.UnitML}}}
	in.Normalize()
	_, err := s.CreateKit(ctx, in)
	var conflict *store.ConflictError
	if !errors.As(err, &conflict) || conflict.Message != "Pads is counted in pcs now (its unit was changed meanwhile). Check its amount and save again." {
		t.Fatalf("err = %v", err)
	}
}
