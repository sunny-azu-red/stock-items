package stock

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

// Unit is the base unit an item is counted in. Quantities are always
// stored in the base unit: pieces, millilitres or grams.
type Unit string

const (
	UnitPiece Unit = "piece"
	UnitML    Unit = "ml"
	UnitGram  Unit = "g"
)

// Units lists the valid units in display order.
var Units = []Unit{UnitPiece, UnitML, UnitGram}

// Valid reports whether u is a known unit.
func (u Unit) Valid() bool {
	return u == UnitPiece || u == UnitML || u == UnitGram
}

// Label is a human readable name for the unit.
func (u Unit) Label() string {
	switch u {
	case UnitML:
		return "Volume (ml / L)"
	case UnitGram:
		return "Weight (g / kg)"
	default:
		return "Pieces"
	}
}

// SubUnit is an input unit that can be converted to a base unit.
type SubUnit struct {
	Value  string // form value, e.g. "l"
	Label  string // display, e.g. "L"
	Base   Unit
	Factor float64
}

// SubUnits are all input units, grouped by base unit.
var SubUnits = []SubUnit{
	{"pcs", "pcs", UnitPiece, 1},
	{"ml", "ml", UnitML, 1},
	{"l", "L", UnitML, 1000},
	{"g", "g", UnitGram, 1},
	{"kg", "kg", UnitGram, 1000},
}

// suffixes maps typed suffixes to sub unit values.
var suffixes = map[string]string{
	"": "", "x": "pcs", "pc": "pcs", "pcs": "pcs", "piece": "pcs", "pieces": "pcs", "buc": "pcs",
	"ml": "ml", "l": "l", "lt": "l", "ltr": "l", "litre": "l", "liter": "l", "liters": "l", "litres": "l",
	"g": "g", "gr": "g", "kg": "kg",
}

// SubUnitsFor returns the sub units valid for base unit u.
func SubUnitsFor(u Unit) []SubUnit {
	var out []SubUnit
	for _, s := range SubUnits {
		if s.Base == u {
			out = append(out, s)
		}
	}
	return out
}

func findSubUnit(value string) (SubUnit, bool) {
	for _, s := range SubUnits {
		if s.Value == value {
			return s, true
		}
	}
	return SubUnit{}, false
}

// ErrEmpty is returned by ParseAmount for blank input.
var ErrEmpty = errors.New("empty amount")

var amountRe = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?|\.[0-9]+)\s*([a-z]*)\.?$`)

// ParseAmount parses a user typed amount such as "2", "525 ml", "1,5 L" or
// "400g" into the base unit of u. A suffix in the text wins over subUnit
// (the value of a unit selector next to the input); with neither, the base
// unit is assumed. Pieces must be whole numbers.
func ParseAmount(s string, u Unit, subUnit string) (float64, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0, ErrEmpty
	}
	s = strings.ReplaceAll(s, ",", ".")
	m := amountRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("%q is not a valid amount", s)
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a valid amount", s)
	}
	sub, ok := suffixes[m[2]]
	if !ok {
		return 0, fmt.Errorf("unknown unit %q", m[2])
	}
	if sub == "" {
		sub = strings.ToLower(strings.TrimSpace(subUnit))
	}
	factor := 1.0
	if sub != "" {
		su, ok := findSubUnit(sub)
		if !ok {
			return 0, fmt.Errorf("unknown unit %q", sub)
		}
		if su.Base != u {
			return 0, fmt.Errorf("%s cannot be used for %s", su.Label, strings.ToLower(u.Label()))
		}
		factor = su.Factor
	}
	v := round3(n * factor)
	if u == UnitPiece && v != math.Trunc(v) {
		return 0, errors.New("pieces must be a whole number")
	}
	return v, nil
}

// FormatQuantity renders a base unit quantity for people:
// "6x", "525 ml", "1.5 L", "400 g", "1.2 kg".
func FormatQuantity(q float64, u Unit) string {
	switch u {
	case UnitML:
		if useLargeUnit(q) {
			return FormatNumber(q/1000) + " L"
		}
		return FormatNumber(q) + " ml"
	case UnitGram:
		if useLargeUnit(q) {
			return FormatNumber(q/1000) + " kg"
		}
		return FormatNumber(q) + " g"
	default:
		return FormatNumber(q) + "x"
	}
}

// FormatDelta renders a signed quantity change: "+2x", "−500 ml".
func FormatDelta(q float64, u Unit) string {
	if q < 0 {
		return "−" + FormatQuantity(-q, u)
	}
	return "+" + FormatQuantity(q, u)
}

// SplitForInput turns a base quantity into an input value and sub unit,
// picking the larger unit when it reads better (1500 ml -> "1.5", "l").
func SplitForInput(q float64, u Unit) (string, string) {
	switch u {
	case UnitML:
		if useLargeUnit(q) {
			return FormatNumber(q / 1000), "l"
		}
		return FormatNumber(q), "ml"
	case UnitGram:
		if useLargeUnit(q) {
			return FormatNumber(q / 1000), "kg"
		}
		return FormatNumber(q), "g"
	default:
		return FormatNumber(q), "pcs"
	}
}

// FormatNumber prints up to three decimals without trailing zeros.
func FormatNumber(f float64) string {
	return strconv.FormatFloat(round3(f), 'f', -1, 64)
}

// DefaultStep is the amount the quick +/- buttons change for a unit.
func DefaultStep(u Unit) float64 {
	switch u {
	case UnitML:
		return 100
	case UnitGram:
		return 50
	default:
		return 1
	}
}

// useLargeUnit reports whether q (ml or g) reads better as L or kg: it must
// be at least 1000 and a whole number, so no precision is lost.
func useLargeUnit(q float64) bool {
	q = math.Abs(round3(q))
	return q >= 1000 && q == math.Trunc(q)
}

func round3(f float64) float64 {
	return math.Round(f*1000) / 1000
}

func itoa(i int) string { return strconv.Itoa(i) }
