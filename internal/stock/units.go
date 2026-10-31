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

// MaxQuantity is the largest amount (in base units) one item can hold or
// one input can carry. It keeps sums well inside the numeric(12,3) columns.
const MaxQuantity = 100_000_000

var amountRe = regexp.MustCompile(`^([0-9]+(?:\.[0-9]+)?|\.[0-9]+)\s*([a-z]*)\.?$`)

// ParseAmount parses a user typed amount such as "2", "525 ml", "1,5 L" or
// "400g" into the base unit of u. A suffix in the text wins over subUnit
// (the value of a unit selector next to the input); with neither, the base
// unit is assumed. Pieces must be whole numbers. In pieces, ml and g a
// number like "1,500" is refused as ambiguous (see CheckAmbiguous); in L
// and kg it is read as a decimal.
func ParseAmount(s string, u Unit, subUnit string) (float64, error) {
	typed := strings.TrimSpace(s) // quoted in errors as typed
	s = strings.ToLower(typed)
	if s == "" {
		return 0, ErrEmpty
	}
	s = strings.ReplaceAll(s, ",", ".")
	m := amountRe.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("%q is not a valid amount", typed)
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a valid amount", typed)
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
	if factor == 1 {
		if err := CheckAmbiguous(m[1]); err != nil {
			return 0, err
		}
	}
	v := round3(n * factor)
	if v > MaxQuantity {
		return 0, errors.New("that amount is too large")
	}
	if u == UnitPiece && v != math.Trunc(v) {
		return 0, errors.New("pieces must be a whole number")
	}
	return v, nil
}

// groupedRe matches a number that reads as a whole number with a thousands
// separator as well as a decimal: one to three digits without a leading
// zero, a comma or point, then exactly three digits ("1,500", "12.000").
var groupedRe = regexp.MustCompile(`^[1-9][0-9]{0,2}[.,][0-9]{3}$`)

// CheckAmbiguous returns an error when num could be a whole number written
// with a thousands separator as well as a decimal, so that "1.500" copied
// from a Romanian price is not silently read as 1.5.
func CheckAmbiguous(num string) error {
	if !groupedRe.MatchString(num) {
		return nil
	}
	whole := num[:len(num)-4] + num[len(num)-3:]
	n, _ := strconv.ParseFloat(strings.Replace(num, ",", ".", 1), 64)
	return fmt.Errorf("could mean %s or %s: type one of those", whole, FormatNumber(n))
}

// ParseAmountAnyUnit parses an amount for something that is not an item
// yet, taking the unit from the text itself ("4 L" is 4000 ml, "500g" is
// 500 g), else from subUnit (the unit select next to the input), else
// counting pieces.
func ParseAmountAnyUnit(s, subUnit string) (float64, Unit, error) {
	u := UnitPiece
	if su, ok := findSubUnit(strings.ToLower(strings.TrimSpace(subUnit))); ok {
		u = su.Base
	}
	clean := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), ",", ".")
	if m := amountRe.FindStringSubmatch(clean); m != nil {
		if su, ok := findSubUnit(suffixes[m[2]]); ok {
			u = su.Base
		}
	}
	v, err := ParseAmount(s, u, subUnit) // a typed suffix wins over subUnit
	return v, u, err
}

// FormatQuantity renders a base unit quantity for people:
// "6x", "525 ml", "1.5 L", "1 475 ml", "400 g", "1.2 kg". Like L and kg,
// ml and g show at most two decimals, as "1.234 g" reads as 1234 g.
func FormatQuantity(q float64, u Unit) string {
	switch u {
	case UnitML:
		if useLargeUnit(round2(q)) {
			return FormatNumber(round2(q)/1000) + " L"
		}
		return formatSmall(q) + " ml"
	case UnitGram:
		if useLargeUnit(round2(q)) {
			return FormatNumber(round2(q)/1000) + " kg"
		}
		return formatSmall(q) + " g"
	default:
		return formatGrouped(q) + "x"
	}
}

// formatSmall is formatGrouped to two decimals; a little that would show
// as nothing shows as "<0.01".
func formatSmall(q float64) string {
	r := round2(q)
	if r == 0 && q > 0 {
		return "<0.01"
	}
	return formatGrouped(r)
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

// formatGrouped is FormatNumber with thousands separators: "1 234.5".
func formatGrouped(f float64) string {
	s := FormatNumber(f)
	whole, frac, hasFrac := strings.Cut(s, ".")
	n, err := strconv.Atoi(whole)
	if err != nil || (n > -1000 && n < 1000) {
		return s
	}
	if hasFrac {
		return FormatInt(n) + "." + frac
	}
	return FormatInt(n)
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
// The value parses back to exactly q.
func SplitForInput(q float64, u Unit) (string, string) {
	switch u {
	case UnitML:
		if useLargeUnit(q) {
			return FormatNumber(q / 1000), "l"
		}
		return exactInput(q), "ml"
	case UnitGram:
		if useLargeUnit(q) {
			return FormatNumber(q / 1000), "kg"
		}
		return exactInput(q), "g"
	default:
		return FormatNumber(q), "pcs"
	}
}

// exactInput is FormatNumber with a trailing zero where the number would
// otherwise be refused as ambiguous on the way back: "1.2340" ml, not
// "1.234" (which could mean 1234).
func exactInput(q float64) string {
	s := FormatNumber(q)
	if CheckAmbiguous(s) != nil {
		s += "0"
	}
	return s
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
// be at least 1000 and a multiple of 10, so it takes at most two decimals.
// Three ("1.475 L") would read as 1475 L where a point groups thousands.
func useLargeUnit(q float64) bool {
	q = math.Abs(round3(q))
	return q >= 1000 && math.Mod(q, 10) == 0
}

func round3(f float64) float64 {
	return math.Round(f*1000) / 1000
}

func itoa(i int) string { return strconv.Itoa(i) }
