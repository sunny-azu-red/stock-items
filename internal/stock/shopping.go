package stock

import (
	"sort"
	"strings"
)

// Suggestion is an item worth buying, with the reasons why.
type Suggestion struct {
	Item     Item
	Quantity float64
	Reasons  []string
}

// SuggestPurchases lists items that are below their minimum stock or short
// for a service kit, skipping items already on the shopping list. The
// suggested quantity covers the largest single need.
func SuggestPurchases(items []Item, kits []Kit, onList map[int64]bool) []Suggestion {
	byID := make(map[int64]Item, len(items))
	for _, it := range items {
		byID[it.ID] = it
	}
	sugg := map[int64]*Suggestion{}
	add := func(it Item, qty float64, reason string) {
		if onList[it.ID] || qty <= 0 {
			return
		}
		s, ok := sugg[it.ID]
		if !ok {
			s = &Suggestion{Item: it}
			sugg[it.ID] = s
		}
		s.Quantity = max(s.Quantity, round3(qty))
		s.Reasons = append(s.Reasons, reason)
	}
	for _, it := range items {
		if it.IsLow() {
			add(it, *it.MinQuantity-it.Quantity, "Below minimum of "+FormatQuantity(*it.MinQuantity, it.Unit))
		}
	}
	for _, k := range kits {
		for _, l := range k.Lines {
			if l.Archived {
				continue // no longer kept, so not bought again
			}
			if short := l.Short(); short > 0 {
				it, ok := byID[l.ItemID]
				if !ok {
					it = Item{ID: l.ItemID, Name: l.ItemName, Unit: l.Unit, Quantity: l.Have}
				}
				add(it, short, "Short for "+k.VehicleName+": "+k.Name)
			}
		}
	}
	out := make([]Suggestion, 0, len(sugg))
	for _, s := range sugg {
		out = append(out, *s)
	}
	sort.Slice(out, func(a, b int) bool {
		na, nb := strings.ToLower(out[a].Item.DisplayName()), strings.ToLower(out[b].Item.DisplayName())
		if na != nb {
			return na < nb
		}
		return out[a].Item.ID < out[b].Item.ID
	})
	return out
}
