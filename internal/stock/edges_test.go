package stock

import "testing"

func TestPricePer(t *testing.T) {
	tests := []struct {
		p    float64
		unit Unit
		want float64
		per  string
	}{
		{0.067, UnitML, 67, "L"},
		{0.0125, UnitGram, 12.5, "kg"},
		{12.5, UnitPiece, 12.5, "piece"},
		{3, "", 3, "piece"},
		{0, UnitML, 0, "L"},
	}
	for _, tt := range tests {
		got, per := PricePer(tt.p, tt.unit)
		if FormatMoney(got) != FormatMoney(tt.want) || per != tt.per {
			t.Errorf("PricePer(%v, %q) = %v, %q; want %v, %q", tt.p, tt.unit, got, per, tt.want, tt.per)
		}
	}
}

// csvSafe quotes what a spreadsheet would run as a formula, including
// after leading blanks, which some apps trim on import.
func TestCSVSafe(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"Oil filter", "Oil filter"},
		{"=1+1", "'=1+1"},
		{"+1 L", "'+1 L"},
		{"-promo", "'-promo"},
		{"@SUM(A1)", "'@SUM(A1)"},
		{"\tcmd", "'\tcmd"},
		{"\rcmd", "'\rcmd"},
		{"\t", "'\t"},
		{" =1+1", "' =1+1"},
		{"  @SUM(A1)", "'  @SUM(A1)"},
		{" -2+3", "' -2+3"},
		{"\n=1+1", "'\n=1+1"},
		{"   ", "   "},
		{"a=b", "a=b"},
		{"5W-40", "5W-40"},
		{"−500 ml", "−500 ml"}, // a minus sign, not a hyphen
		{"'quoted", "'quoted"},
	}
	for _, tt := range tests {
		if got := csvSafe(tt.in); got != tt.want {
			t.Errorf("csvSafe(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// Negative amounts read like positive ones with a sign, and never as -0.
func TestNegativeAmounts(t *testing.T) {
	format := []struct {
		q    float64
		unit Unit
		want string
	}{
		{-1500, UnitML, "-1.5 L"},
		{-250, UnitML, "-250 ml"},
		{-1475, UnitML, "-1\u202f475 ml"},
		{-1234.5, UnitGram, "-1\u202f234.5 g"},
		{-2000, UnitGram, "-2 kg"},
		{-3, UnitPiece, "-3x"},
		{-1500, UnitPiece, "-1\u202f500x"},
		{-0.5, UnitML, "-0.5 ml"},
		{-0.004, UnitML, "0 ml"},
		{-0.0001, UnitPiece, "0x"},
	}
	for _, tt := range format {
		if got := FormatQuantity(tt.q, tt.unit); got != tt.want {
			t.Errorf("FormatQuantity(%v, %s) = %q, want %q", tt.q, tt.unit, got, tt.want)
		}
	}
	split := []struct {
		q          float64
		unit       Unit
		value, sub string
	}{
		{-1500, UnitML, "-1.5", "l"},
		{-525, UnitML, "-525", "ml"},
		{-2500, UnitGram, "-2.5", "kg"},
		{-1475, UnitGram, "-1475", "g"},
		{-3, UnitPiece, "-3", "pcs"},
		{-0.0001, UnitML, "0", "ml"},
	}
	for _, tt := range split {
		if v, s := SplitForInput(tt.q, tt.unit); v != tt.value || s != tt.sub {
			t.Errorf("SplitForInput(%v, %s) = %q,%q want %q,%q", tt.q, tt.unit, v, s, tt.value, tt.sub)
		}
	}
	if got := FormatNumber(-0.0004); got != "0" {
		t.Errorf("FormatNumber(-0.0004) = %q, want 0", got)
	}
}
