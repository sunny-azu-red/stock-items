package stock

import (
	"encoding/csv"
	"io"
	"sort"
	"strconv"
	"strings"
)

// Section is a group of items that share exactly the same vehicle tags,
// e.g. "KTM 390", "KTM 390 + KTM 1290" or "General" (no vehicle).
type Section struct {
	Key      string
	Title    string
	Vehicles []Vehicle
	Items    []Item
}

// Shared reports whether the section is for items fitting several vehicles.
func (s Section) Shared() bool { return len(s.Vehicles) > 1 }

// General reports whether the section holds untagged items.
func (s Section) General() bool { return len(s.Vehicles) == 0 }

// GroupByVehicles groups items by their exact vehicle tag set. Single
// vehicle sections come first (in the order of vehicles), then shared
// combinations (fewest vehicles first), then "General". Items keep their
// relative order.
func GroupByVehicles(items []Item, vehicles []Vehicle) []Section {
	pos := make(map[int64]int, len(vehicles))
	for i, v := range vehicles {
		pos[v.ID] = i
	}
	byKey := map[string]*Section{}
	var order []*Section
	for _, it := range items {
		var ids []int64
		for _, id := range it.VehicleIDs {
			if _, ok := pos[id]; ok {
				ids = append(ids, id)
			}
		}
		sort.Slice(ids, func(a, b int) bool { return pos[ids[a]] < pos[ids[b]] })
		key := sectionKey(ids)
		sec, ok := byKey[key]
		if !ok {
			sec = &Section{Key: key}
			names := make([]string, len(ids))
			for i, id := range ids {
				v := vehicles[pos[id]]
				sec.Vehicles = append(sec.Vehicles, v)
				names[i] = v.Name
			}
			if len(ids) == 0 {
				sec.Title = "General"
			} else {
				sec.Title = strings.Join(names, " + ")
			}
			byKey[key] = sec
			order = append(order, sec)
		}
		sec.Items = append(sec.Items, it)
	}
	sort.SliceStable(order, func(a, b int) bool {
		sa, sb := order[a], order[b]
		ra, rb := sectionRank(sa), sectionRank(sb)
		if ra != rb {
			return ra < rb
		}
		for i := 0; i < len(sa.Vehicles) && i < len(sb.Vehicles); i++ {
			if pa, pb := pos[sa.Vehicles[i].ID], pos[sb.Vehicles[i].ID]; pa != pb {
				return pa < pb
			}
		}
		return false
	})
	out := make([]Section, len(order))
	for i, s := range order {
		out[i] = *s
	}
	return out
}

// sectionRank orders sections: singles (1), then combos by size, General last.
func sectionRank(s *Section) int {
	if len(s.Vehicles) == 0 {
		return 1 << 20
	}
	return len(s.Vehicles)
}

func sectionKey(ids []int64) string {
	if len(ids) == 0 {
		return "general"
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return "v-" + strings.Join(parts, "-")
}

// ExportText renders sections in a plain text format that is easy to paste
// into notes or a chat:
//
//	KTM 390
//	------------
//	1x - OEM Rear Brake Pads
//	525 ml - Silkolene 15W50 PRO 4 XP
func ExportText(sections []Section) string {
	var b strings.Builder
	for i, s := range sections {
		if i > 0 {
			b.WriteString("\n\n")
		}
		writeHeading(&b, s.Title)
		for _, it := range s.Items {
			b.WriteString(FormatQuantity(it.Quantity, it.Unit))
			b.WriteString(" - ")
			b.WriteString(it.DisplayName())
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// ExportShoppingText renders the shopping list in the same plain format.
func ExportShoppingText(entries []ShoppingEntry) string {
	var b strings.Builder
	writeHeading(&b, "Shopping list")
	for _, e := range entries {
		if e.Quantity != nil {
			b.WriteString(FormatQuantity(*e.Quantity, e.ItemUnit))
			b.WriteString(" - ")
		} else {
			b.WriteString("- ")
		}
		b.WriteString(e.Title())
		if e.Note != "" {
			b.WriteString(" (" + e.Note + ")")
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func writeHeading(b *strings.Builder, title string) {
	b.WriteString(title)
	b.WriteByte('\n')
	b.WriteString(strings.Repeat("-", max(12, len([]rune(title)))))
	b.WriteByte('\n')
}

// WriteCSV writes items as CSV with one row per item.
func WriteCSV(w io.Writer, items []Item, vehicles []Vehicle) error {
	names := make(map[int64]string, len(vehicles))
	for _, v := range vehicles {
		names[v.ID] = v.Name
	}
	cw := csv.NewWriter(w)
	header := []string{"id", "brand", "name", "category", "unit", "quantity", "quantity_display",
		"min_quantity", "expires_on", "location", "vehicles", "skus", "notes"}
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, it := range items {
		var vs []string
		for _, id := range it.VehicleIDs {
			if n, ok := names[id]; ok {
				vs = append(vs, n)
			}
		}
		var skus []string
		for _, s := range it.SKUs {
			skus = append(skus, s.Kind.Label()+":"+s.Code)
		}
		minQ, exp := "", ""
		if it.MinQuantity != nil {
			minQ = FormatNumber(*it.MinQuantity)
		}
		if it.ExpiresOn != nil {
			exp = it.ExpiresOn.Format("2006-01-02")
		}
		row := []string{
			strconv.FormatInt(it.ID, 10), csvSafe(it.Brand), csvSafe(it.Name), csvSafe(it.Category), string(it.Unit),
			FormatNumber(it.Quantity), FormatQuantity(it.Quantity, it.Unit), minQ, exp,
			csvSafe(it.Location), csvSafe(strings.Join(vs, "; ")), csvSafe(strings.Join(skus, "; ")), csvSafe(it.Notes),
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// csvSafe stops spreadsheet apps from treating user text as a formula.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}
