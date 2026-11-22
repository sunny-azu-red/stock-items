package stock

import (
	"slices"
	"sort"
	"strings"
	"time"
)

// Suggestion is an item worth buying, with the reasons why.
type Suggestion struct {
	Item     Item
	Quantity float64 // the total needed
	Listed   float64 // already on the shopping list (less than Quantity)
	Reasons  []string
	// VehicleIDs are the vehicles it is for, in id order: the item's tags
	// when it is below its minimum, and each short kit's vehicle. None for
	// a General item below its minimum.
	VehicleIDs []int64
}

// NamesItem reports whether text is just an item's name or "brand name",
// ignoring case and surrounding spaces: a shopping entry's text that is
// that item (store.ItemsNamed matches the same way).
func NamesItem(text, brand, name string) bool {
	text = strings.TrimSpace(text)
	return text != "" && (strings.EqualFold(text, strings.TrimSpace(name)) || strings.EqualFold(text, strings.TrimSpace(brand+" "+name)))
}

// ShortNote is the reason a part goes on the shopping list: "Short for the
// Air Filter kit", or "Short for the Oil change service". It names what
// needs the part, never its vehicle: the list shows that as a chip. The
// noun is left out when the name ends with it already ("Short for the Oil
// service", not "… Oil service service").
func ShortNote(name, noun string) string {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		return "Short for a " + noun
	case strings.HasSuffix(strings.ToLower(name), strings.ToLower(noun)):
		return "Short for the " + name
	}
	return "Short for the " + name + " " + noun
}

// SuggestPurchases lists items that are below their minimum stock or short
// for a service kit. The suggested quantity covers the largest single need.
// listed holds the quantity of each item already on the shopping list (nil
// when the entry has none); an item is only suggested while the list asks
// for less than it needs. today picks each kit's schedule due next.
// vehicleID, unless 0, keeps to that vehicle's part: items below their
// minimum that fit it (with General), and its own kits' shortfalls. items
// holds every item still, for the kit lines to find theirs.
func SuggestPurchases(items []Item, kits []Kit, listed map[int64]*float64, today time.Time, vehicleID int64) []Suggestion {
	today = DateOnly(today)
	byID := make(map[int64]Item, len(items))
	for _, it := range items {
		byID[it.ID] = it
	}
	sugg := map[int64]*Suggestion{}
	add := func(it Item, qty float64, reason string, vehicles ...int64) {
		if qty <= 0 {
			return
		}
		s, ok := sugg[it.ID]
		if !ok {
			s = &Suggestion{Item: it}
			sugg[it.ID] = s
		}
		s.Quantity = max(s.Quantity, RoundQty(qty))
		// Two vehicles' kits often share a name ("Oil service"): say it
		// once, the chips say whose.
		if !slices.Contains(s.Reasons, reason) {
			s.Reasons = append(s.Reasons, reason)
		}
		s.VehicleIDs = append(s.VehicleIDs, vehicles...)
	}
	for _, it := range items {
		if it.IsLow() && (vehicleID == 0 || it.Fits(vehicleID)) {
			add(it, *it.MinQuantity-it.Quantity, "Below the minimum of "+FormatQuantity(*it.MinQuantity, it.Unit), it.VehicleIDs...)
		}
	}
	for _, k := range kits {
		if vehicleID != 0 && k.VehicleID != vehicleID {
			continue
		}
		for _, l := range k.ActiveLines(k.VehicleOdometer, today) {
			if l.Archived {
				continue // no longer kept, so not bought again
			}
			if short := l.Short(); short > 0 {
				it, ok := byID[l.ItemID]
				if !ok {
					it = Item{ID: l.ItemID, Name: l.ItemName, Unit: l.Unit, Quantity: l.Have}
				}
				add(it, short, ShortNote(k.Name, "kit"), k.VehicleID)
			}
		}
	}
	out := make([]Suggestion, 0, len(sugg))
	for id, s := range sugg {
		slices.Sort(s.VehicleIDs)
		s.VehicleIDs = slices.Compact(s.VehicleIDs)
		if q, ok := listed[id]; ok {
			if q == nil || *q >= s.Quantity {
				continue // the list already covers it
			}
			s.Listed = *q
		}
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

// SuggestForItem returns the purchase suggestion for a single item, checking
// its minimum stock and active kit lines first, and falling back to non-active
// kit tiers (e.g. full parts when intermediate is next) when the user explicitly
// adds the item to the shopping list from its page.
func SuggestForItem(it Item, kits []Kit, today time.Time) (Suggestion, bool) {
	if it.Archived() {
		return Suggestion{}, false
	}
	out := Suggestion{Item: it}
	add := func(qty float64, reason string) {
		if qty <= 0 {
			return
		}
		out.Quantity = max(out.Quantity, RoundQty(qty))
		if !slices.Contains(out.Reasons, reason) {
			out.Reasons = append(out.Reasons, reason)
		}
	}
	if it.IsLow() {
		add(*it.MinQuantity-it.Quantity, "Below the minimum of "+FormatQuantity(*it.MinQuantity, it.Unit))
	}
	for _, k := range kits {
		if k.Archived() {
			continue
		}
		var short float64
		for _, l := range k.ActiveLines(k.VehicleOdometer, today) {
			if l.ItemID == it.ID && !l.Archived {
				short = max(short, l.Short())
			}
		}
		if short == 0 {
			for _, l := range append(slices.Clone(k.Lines), k.InterLines...) {
				if l.ItemID == it.ID && !l.Archived {
					short = max(short, l.Short())
				}
			}
		}
		add(short, ShortNote(k.Name, "kit"))
	}
	return out, out.Quantity > 0
}
