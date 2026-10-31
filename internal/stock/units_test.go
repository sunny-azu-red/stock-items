package stock

import (
	"errors"
	"testing"
)

func TestParseAmount(t *testing.T) {
	tests := []struct {
		in   string
		unit Unit
		sub  string
		want float64
	}{
		{"2", UnitPiece, "", 2},
		{"6x", UnitPiece, "", 6},
		{"3 pcs", UnitPiece, "pcs", 3},
		{"525", UnitML, "ml", 525},
		{"525 ml", UnitML, "", 525},
		{"1.5", UnitML, "l", 1500},
		{"1,5 L", UnitML, "", 1500},
		{"1.5L", UnitML, "ml", 1500}, // suffix wins over selector
		{".5 l", UnitML, "", 500},
		{"0.0005 l", UnitML, "", 0.5},
		{"400g", UnitGram, "", 400},
		{"1.2 kg", UnitGram, "", 1200},
		{"0", UnitPiece, "", 0},
		{"  4 litres ", UnitML, "", 4000},
		{"1,500 L", UnitML, "", 1500}, // in L "1,500" can only be 1.5
		{"2.500", UnitGram, "kg", 2500},
		{"0,250 ml", UnitML, "", 0.25}, // a leading zero is no thousands group
		{"12.25 ml", UnitML, "", 12.25},
		{"1500.5 g", UnitGram, "", 1500.5},
		{"1000", UnitPiece, "", 1000},
		{"0.23 g", UnitGram, "", 0.23},
		{"1.2345 kg", UnitGram, "", 1234.5}, // two decimals of g once in g
		{"1.23456 l", UnitML, "", 1234.56},  // the most L can take
		{"99999999.99 ml", UnitML, "", 99999999.99},
		{"2.00", UnitPiece, "", 2}, // zero decimals are whole
		{"0.00001 l", UnitML, "", 0.01},
	}
	for _, tt := range tests {
		got, err := ParseAmount(tt.in, tt.unit, tt.sub)
		if err != nil {
			t.Errorf("ParseAmount(%q, %s, %q) error: %v", tt.in, tt.unit, tt.sub, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseAmount(%q, %s, %q) = %v, want %v", tt.in, tt.unit, tt.sub, got, tt.want)
		}
	}
}

func TestParseAmountErrors(t *testing.T) {
	tests := []struct {
		in   string
		unit Unit
		sub  string
	}{
		{"abc", UnitPiece, ""},
		{"-1", UnitPiece, ""},
		{"1.5", UnitPiece, ""},   // fractional pieces
		{"2 L", UnitPiece, ""},   // wrong dimension
		{"2 kg", UnitML, ""},     // wrong dimension
		{"2", UnitML, "kg"},      // selector of the wrong dimension
		{"2 cups", UnitML, ""},   // unknown suffix
		{"1.2.3", UnitML, "ml"},  // malformed
		{"100001 l", UnitML, ""}, // above MaxQuantity
		{"NaN", UnitML, ""},
		{"1e3", UnitML, ""},
		{"1,500 ml", UnitML, ""}, // 1500 or 1.5?
		{"1.500", UnitML, "ml"},
		{"1,500", UnitML, ""},
		{"2.500 g", UnitGram, ""},
		{"1.000", UnitPiece, ""},
		{"12,000 pcs", UnitPiece, ""},
		{"12.125 ml", UnitML, ""},
		{"0.234", UnitGram, ""},       // three decimals of g
		{"0.001 ml", UnitML, ""},      // finer than 0.01 ml
		{"1.234567 kg", UnitGram, ""}, // 1234.567 g
		{"0.000001 l", UnitML, ""},    // 0.001 ml
		{"1.0004", UnitPiece, ""},     // not rounded to 1 first
		{"2.0000001", UnitPiece, ""},
	}
	for _, tt := range tests {
		if _, err := ParseAmount(tt.in, tt.unit, tt.sub); err == nil {
			t.Errorf("ParseAmount(%q, %s, %q) expected an error", tt.in, tt.unit, tt.sub)
		}
	}
	if _, err := ParseAmount("  ", UnitML, ""); !errors.Is(err, ErrEmpty) {
		t.Errorf("blank input: want ErrEmpty, got %v", err)
	}
}

// The reasons say what to type instead; one typed in L or kg says what
// it comes to in ml or g.
func TestParseAmountFinerThanTwoDecimals(t *testing.T) {
	tests := []struct {
		in   string
		unit Unit
		sub  string
		want string
	}{
		{"0.234", UnitGram, "g", "use at most two decimals of g"},
		{"0,001 ml", UnitML, "", "use at most two decimals of ml"},
		{"1.234567 kg", UnitGram, "", "1.234567 kg is 1234.567 g: use at most two decimals of g"},
		{"0.000001", UnitML, "l", "0.000001 L is 0.001 ml: use at most two decimals of ml"},
		{".0000015 l", UnitML, "", ".0000015 L is 0.0015 ml: use at most two decimals of ml"},
		{"1.0004", UnitPiece, "", "pieces must be a whole number"},
	}
	for _, tt := range tests {
		_, err := ParseAmount(tt.in, tt.unit, tt.sub)
		if err == nil || err.Error() != tt.want {
			t.Errorf("ParseAmount(%q, %s, %q) = %v, want %q", tt.in, tt.unit, tt.sub, err, tt.want)
		}
	}
}

// sum adds at run time, with float noise: 0.1 + 0.2 is 0.30000000000000004
// (a constant expression would be exact).
func sum(a, b float64) float64 { return a + b }

func TestCheckQuantity(t *testing.T) {
	tests := []struct {
		q    float64
		unit Unit
		ok   bool
	}{
		{2, UnitPiece, true},
		{1.0004, UnitPiece, false},
		{0.5, UnitPiece, false},
		{0.23, UnitGram, true},
		{sum(0.1, 0.2), UnitML, true}, // float noise is no third decimal
		{sum(1234.56, 0.01), UnitML, true},
		{0.234, UnitGram, false},
		{0.005, UnitML, false},
		{99999999.99, UnitML, true},
		{99999999.995, UnitML, false},
	}
	for _, tt := range tests {
		if err := CheckQuantity(tt.q, tt.unit); (err == nil) != tt.ok {
			t.Errorf("CheckQuantity(%v, %s) = %v, want ok %v", tt.q, tt.unit, err, tt.ok)
		}
	}
}

func TestRoundQty(t *testing.T) {
	for in, want := range map[float64]float64{sum(0.1, 0.2): 0.3, sum(1234.56, 0.01): 1234.57, -6.006: -6.01, 0.004: 0, 1234.56: 1234.56} {
		if got := RoundQty(in); got != want {
			t.Errorf("RoundQty(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestCheckAmbiguous(t *testing.T) {
	tests := map[string]string{
		"1,500":    "could mean 1500 or 1.5: type one of those",
		"12.000":   "could mean 12000 or 12: type one of those",
		"999,999":  "could mean 999999 or 999.999: type one of those",
		"1.5":      "",
		"1.50":     "",
		"0.500":    "",
		"1500":     "",
		"1234.567": "",
		"1.5000":   "",
	}
	for in, want := range tests {
		got := ""
		if err := CheckAmbiguous(in); err != nil {
			got = err.Error()
		}
		if got != want {
			t.Errorf("CheckAmbiguous(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatQuantity(t *testing.T) {
	tests := []struct {
		q    float64
		unit Unit
		want string
	}{
		{1, UnitPiece, "1x"},
		{6, UnitPiece, "6x"},
		{525, UnitML, "525 ml"},
		{640, UnitML, "640 ml"},
		{1500, UnitML, "1.5 L"},
		{1000, UnitML, "1 L"},
		{4000, UnitML, "4 L"},
		{1750, UnitML, "1.75 L"},
		{1234.5, UnitML, "1 234.5 ml"},
		{1475, UnitML, "1 475 ml"}, // not "1.475 L", which reads as 1475 L
		{12345, UnitML, "12 345 ml"},
		{0, UnitML, "0 ml"},
		{400, UnitGram, "400 g"},
		{1200, UnitGram, "1.2 kg"},
		{2125, UnitGram, "2 125 g"},
		{0.5, UnitML, "0.5 ml"},
		{1.234, UnitGram, "1.23 g"}, // not "1.234 g", which reads as 1234 g
		{1234.567, UnitML, "1 234.57 ml"},
		{0.004, UnitML, "<0.01 ml"},
		{-0.004, UnitML, "0 ml"},
		{999.996, UnitML, "1 L"}, // rounds to 1000, shown like it
		{1500, UnitPiece, "1 500x"},
	}
	for _, tt := range tests {
		if got := FormatQuantity(tt.q, tt.unit); got != tt.want {
			t.Errorf("FormatQuantity(%v, %s) = %q, want %q", tt.q, tt.unit, got, tt.want)
		}
	}
}

func TestFormatDelta(t *testing.T) {
	if got := FormatDelta(-500, UnitML); got != "−500 ml" {
		t.Errorf("got %q", got)
	}
	if got := FormatDelta(-1475, UnitML); got != "−1 475 ml" {
		t.Errorf("got %q", got)
	}
	if got := FormatDelta(2, UnitPiece); got != "+2x" {
		t.Errorf("got %q", got)
	}
}

func TestSplitForInput(t *testing.T) {
	tests := []struct {
		q          float64
		unit       Unit
		value, sub string
	}{
		{1500, UnitML, "1.5", "l"},
		{525, UnitML, "525", "ml"},
		{3, UnitPiece, "3", "pcs"},
		{2500, UnitGram, "2.5", "kg"},
		{1475, UnitML, "1475", "ml"}, // ungrouped, so it parses back
		{1.23, UnitML, "1.23", "ml"}, // two decimals never read as a thousands group
		{12.34, UnitGram, "12.34", "g"},
		{1234.5, UnitGram, "1234.5", "g"},
		{1230, UnitML, "1.23", "l"},
		{99999999.99, UnitML, "99999999.99", "ml"},
		{0.5, UnitGram, "0.5", "g"},
		{0.01, UnitML, "0.01", "ml"},
	}
	for _, tt := range tests {
		v, s := SplitForInput(tt.q, tt.unit)
		if v != tt.value || s != tt.sub {
			t.Errorf("SplitForInput(%v, %s) = %q,%q want %q,%q", tt.q, tt.unit, v, s, tt.value, tt.sub)
		}
		// Round trip back through the parser.
		back, err := ParseAmount(v, tt.unit, s)
		if err != nil || back != tt.q {
			t.Errorf("round trip of %v %s = %v, %v", tt.q, tt.unit, back, err)
		}
	}
}

func TestSubUnitsFor(t *testing.T) {
	if got := SubUnitsFor(UnitML); len(got) != 2 || got[0].Value != "ml" || got[1].Value != "l" {
		t.Errorf("SubUnitsFor(ml) = %+v", got)
	}
	if got := SubUnitsFor(UnitPiece); len(got) != 1 {
		t.Errorf("SubUnitsFor(piece) = %+v", got)
	}
}

func TestParseAmountAnyUnitWithSelect(t *testing.T) {
	tests := []struct {
		in, sub string
		want    float64
		unit    Unit
	}{
		{"4", "l", 4000, UnitML},
		{"500", "g", 500, UnitGram},
		{"500 g", "l", 500, UnitGram}, // the typed suffix wins
		{"2", "pcs", 2, UnitPiece},
	}
	for _, tt := range tests {
		got, u, err := ParseAmountAnyUnit(tt.in, tt.sub)
		if err != nil || got != tt.want || u != tt.unit {
			t.Errorf("%q with %q = %v %s, %v; want %v %s", tt.in, tt.sub, got, u, err, tt.want, tt.unit)
		}
	}
}

func TestParseAmountAnyUnit(t *testing.T) {
	tests := []struct {
		in      string
		want    float64
		unit    Unit
		wantErr bool
	}{
		{"2", 2, UnitPiece, false},
		{"3x", 3, UnitPiece, false},
		{"4 L", 4000, UnitML, false},
		{"1,5 l", 1500, UnitML, false},
		{"250 ml", 250, UnitML, false},
		{"500g", 500, UnitGram, false},
		{"2 kg", 2000, UnitGram, false},
		{"1.5", 0, UnitPiece, true}, // pieces are whole
		{"3 bananas", 0, UnitPiece, true},
		{"", 0, UnitPiece, true},
	}
	for _, tt := range tests {
		got, u, err := ParseAmountAnyUnit(tt.in, "")
		if (err != nil) != tt.wantErr {
			t.Errorf("%q: err = %v, want error %v", tt.in, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && (got != tt.want || u != tt.unit) {
			t.Errorf("%q = %v %s, want %v %s", tt.in, got, u, tt.want, tt.unit)
		}
	}
}
