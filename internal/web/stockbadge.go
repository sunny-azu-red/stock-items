package web

import (
	"encoding/json"
	"strings"

	"github.com/sunny-azu-red/stock-items/internal/stock"
)

// stockBadge is what the "stock-badge" partial shows for an item: its
// stock level (stock.Item.LevelFor), worded for where it is shown.
type stockBadge struct {
	Level stock.StockLevel
	Text  string // "" shows no badge
	Title string
}

// Class is the badge's colour for its level.
func (b stockBadge) Class() string {
	switch b.Level {
	case stock.LevelOut, stock.LevelShort:
		return "badge-danger"
	case stock.LevelLow:
		return "badge-warn"
	default:
		return "badge-muted"
	}
}

// Icon is the badge's icon, if it has one.
func (b stockBadge) Icon() string {
	switch b.Level {
	case stock.LevelShort:
		return "x"
	case stock.LevelLow:
		return "triangle-alert"
	default:
		return ""
	}
}

// Stock badge wording; {have}, {need} and {min} are quantities. The
// browser fills in the same patterns (stockBadgeLevels).
const (
	stockOutText  = "Out of stock"
	stockHaveText = "{have} in stock"
	stockLowText  = "Low · min {min}"
	stockNeedTip  = "Need {need}"
	stockMinTip   = "Below the minimum of {min}"
)

// newStockBadge is the badge for it when need (base units, 0 for none) is
// asked of it. withQty puts what is in stock in the badge, for a page that
// doesn't show it otherwise; without it, an item with enough has no badge
// and a low one names its minimum.
func newStockBadge(it stock.Item, need float64, withQty bool) stockBadge {
	b := stockBadge{Level: it.LevelFor(need)}
	r := strings.NewReplacer(
		"{have}", stock.FormatQuantity(it.Quantity, it.Unit),
		"{need}", stock.FormatQuantity(need, it.Unit),
		"{min}", stock.FormatQuantity(derefFloat(it.MinQuantity), it.Unit))
	switch {
	case b.Level == stock.LevelOut:
		b.Text = stockOutText
	case b.Level == stock.LevelShort:
		b.Text, b.Title = r.Replace(stockHaveText), r.Replace(stockNeedTip)
	case b.Level == stock.LevelLow && withQty:
		b.Text, b.Title = r.Replace(stockHaveText), r.Replace(stockMinTip)
	case b.Level == stock.LevelLow:
		b.Text = r.Replace(stockLowText)
	case withQty:
		b.Text = r.Replace(stockHaveText)
	}
	return b
}

// stockBadgeLevels are the badges with quantity, their quantities left as
// patterns, for the page's <template id="stock-badges"> that app.js fills
// in for part rows.
func stockBadgeLevels() []stockBadge {
	return []stockBadge{
		{Level: stock.LevelOut, Text: stockOutText},
		{Level: stock.LevelShort, Text: stockHaveText, Title: stockNeedTip},
		{Level: stock.LevelLow, Text: stockHaveText, Title: stockMinTip},
		{Level: stock.LevelOK, Text: stockHaveText},
	}
}

// stockUnitsJSON tells app.js how amounts are read and written
// (stock.ParseAmount, stock.FormatQuantity), so a part row's badge can
// compare what the row asks for with what is in stock.
func stockUnitsJSON() (string, error) {
	type unit struct {
		Base   stock.Unit `json:"base"`
		Factor float64    `json:"factor"`
		Label  string     `json:"label"`
	}
	units := map[string]unit{}
	for _, su := range stock.SubUnits {
		units[su.Value] = unit{Base: su.Base, Factor: su.Factor, Label: su.Label}
	}
	b, err := json.Marshal(map[string]any{
		"suffixes": stock.Suffixes(),
		"units":    units,
		"sep":      strings.TrimSuffix(strings.TrimPrefix(stock.FormatInt(1000), "1"), "000"), // thousands
	})
	return string(b), err
}
