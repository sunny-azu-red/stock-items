package stock

import (
	"testing"
	"time"
)

func TestExpiryState(t *testing.T) {
	today := time.Date(2026, 10, 30, 15, 0, 0, 0, time.UTC)
	date := func(y int, m time.Month, d int) *time.Time {
		t := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
		return &t
	}
	tests := []struct {
		name string
		it   Item
		want string
	}{
		{"no date", Item{Quantity: 1}, ""},
		{"expired", Item{Quantity: 1, ExpiresOn: date(2026, 10, 29)}, "expired"},
		{"expires today", Item{Quantity: 1, ExpiresOn: date(2026, 10, 30)}, "soon"},
		{"within the window", Item{Quantity: 1, ExpiresOn: date(2026, 12, 1)}, "soon"},
		{"later", Item{Quantity: 1, ExpiresOn: date(2027, 6, 30)}, ""},
		{"expired but used up", Item{Quantity: 0, ExpiresOn: date(2020, 1, 1)}, ""},
	}
	for _, tt := range tests {
		if got := tt.it.ExpiryState(today, 60); got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestItemUsage(t *testing.T) {
	tests := []struct {
		u     ItemUsage
		inUse bool
		want  string
	}{
		{ItemUsage{}, false, ""},
		{ItemUsage{Purchases: 1}, true, "1 purchase"},
		{ItemUsage{Services: 2, Kits: 1}, true, "2 services and 1 kit"},
		{ItemUsage{Purchases: 5, Services: 3, Kits: 2}, true, "3 services, 2 kits and 5 purchases"},
	}
	for _, tt := range tests {
		if got := tt.u.String(); got != tt.want || tt.u.InUse() != tt.inUse {
			t.Errorf("%+v: %q in use %v, want %q %v", tt.u, got, tt.u.InUse(), tt.want, tt.inUse)
		}
	}
}

func TestSortSKUs(t *testing.T) {
	p1L := 1000.0
	p4L := 4000.0

	// Silkolene, Motorex, and KTM SKUs for engine oil
	silko1LPart := SKU{Code: "SILK-1L", Kind: SKUManufacturer, Brand: "Silkolene", PackSize: &p1L}
	silko1LBar := SKU{Code: "5031131762357", Kind: SKUBarcode, Brand: "Silkolene", PackSize: &p1L}
	silko4LPart := SKU{Code: "SILK-4L", Kind: SKUManufacturer, Brand: "Silkolene", PackSize: &p4L}
	silko4LBar := SKU{Code: "5031131763378", Kind: SKUBarcode, Brand: "Silkolene", PackSize: &p4L}

	ktm1LOEM := SKU{Code: "U6920032", Kind: SKUOEM, Brand: "KTM", PackSize: &p1L}
	ktm1LBar := SKU{Code: "9010544114386", Kind: SKUBarcode, Brand: "KTM", PackSize: &p1L}

	moto1LPart := SKU{Code: "308274", Kind: SKUManufacturer, Brand: "Motorex", PackSize: &p1L}
	moto1LBar := SKU{Code: "7611197012818", Kind: SKUBarcode, Brand: "Motorex", PackSize: &p1L}
	moto4LPart := SKU{Code: "306183", Kind: SKUManufacturer, Brand: "Motorex", PackSize: &p4L}
	moto4LBar := SKU{Code: "7611197012825", Kind: SKUBarcode, Brand: "Motorex", PackSize: &p4L}

	skus := []SKU{
		moto4LBar, moto1LPart, silko4LBar, ktm1LBar,
		moto1LBar, silko1LPart, moto4LPart, silko4LPart,
		ktm1LOEM, silko1LBar,
	}

	t.Run("Silkolene brand stocked", func(t *testing.T) {
		got := SortSKUs(skus, "Silkolene")
		wantCodes := []string{
			"SILK-1L", "5031131762357", "SILK-4L", "5031131763378",
			"U6920032", "9010544114386",
			"308274", "7611197012818", "306183", "7611197012825",
		}
		if len(got) != len(wantCodes) {
			t.Fatalf("got %d SKUs, want %d", len(got), len(wantCodes))
		}
		for i, w := range wantCodes {
			if got[i].Code != w {
				t.Errorf("[%d] got code %s, want %s", i, got[i].Code, w)
			}
		}
	})

	t.Run("KTM brand stocked", func(t *testing.T) {
		got := SortSKUs(skus, "KTM")
		wantCodes := []string{
			"U6920032", "9010544114386",
			"308274", "7611197012818", "306183", "7611197012825",
			"SILK-1L", "5031131762357", "SILK-4L", "5031131763378",
		}
		for i, w := range wantCodes {
			if got[i].Code != w {
				t.Errorf("[%d] got code %s, want %s", i, got[i].Code, w)
			}
		}
	})

	t.Run("Bajaj brand stocked for filter", func(t *testing.T) {
		p1pc := 1.0
		filterSKUs := []SKU{
			{Code: "90138015000", Kind: SKUOEM, Brand: "KTM", PackSize: &p1pc},
			{Code: "9010544114386", Kind: SKUBarcode, Brand: "KTM", PackSize: &p1pc},
			{Code: "HF155", Kind: SKUManufacturer, Brand: "HifloFiltro", PackSize: &p1pc},
			{Code: "824225110289", Kind: SKUBarcode, Brand: "Hiflofiltro", PackSize: &p1pc},
			{Code: "JG571014", Kind: SKUManufacturer, Brand: "Bajaj", PackSize: &p1pc},
		}
		got := SortSKUs(filterSKUs, "Bajaj")
		wantCodes := []string{"JG571014", "90138015000", "9010544114386", "HF155", "824225110289"}
		for i, w := range wantCodes {
			if got[i].Code != w {
				t.Errorf("[%d] got code %s, want %s", i, got[i].Code, w)
			}
		}
	})

	t.Run("Full tier order: item brand, OEM, Manufacturer, Retailer, Other", func(t *testing.T) {
		p1pc := 1.0
		skus := []SKU{
			{Code: "OTHER-1", Kind: SKUOther, Brand: "Misc", PackSize: &p1pc},
			{Code: "RET-BAR", Kind: SKUBarcode, Brand: "Louis", PackSize: &p1pc},
			{Code: "RET-1", Kind: SKURetailer, Brand: "Louis", PackSize: &p1pc},
			{Code: "MFG-BAR", Kind: SKUBarcode, Brand: "HifloFiltro", PackSize: &p1pc},
			{Code: "MFG-1", Kind: SKUManufacturer, Brand: "HifloFiltro", PackSize: &p1pc},
			{Code: "OEM-BAR", Kind: SKUBarcode, Brand: "Yamaha", PackSize: &p1pc},
			{Code: "OEM-1", Kind: SKUOEM, Brand: "Yamaha", PackSize: &p1pc},
			{Code: "ITEM-BAR", Kind: SKUBarcode, Brand: "Brembo", PackSize: &p1pc},
			{Code: "ITEM-1", Kind: SKUManufacturer, Brand: "Brembo", PackSize: &p1pc},
		}
		got := SortSKUs(skus, "Brembo")
		wantCodes := []string{
			"ITEM-1", "ITEM-BAR",
			"OEM-1", "OEM-BAR",
			"MFG-1", "MFG-BAR",
			"RET-1", "RET-BAR",
			"OTHER-1",
		}
		for i, w := range wantCodes {
			if got[i].Code != w {
				t.Errorf("[%d] got code %s, want %s", i, got[i].Code, w)
			}
		}
	})

	t.Run("Empty and single SKU", func(t *testing.T) {
		if got := SortSKUs(nil, "KTM"); len(got) != 0 {
			t.Errorf("expected empty, got %v", got)
		}
		single := []SKU{{Code: "123"}}
		if got := SortSKUs(single, "KTM"); len(got) != 1 || got[0].Code != "123" {
			t.Errorf("expected single, got %v", got)
		}
	})
}

func TestItemDisplaySKUs(t *testing.T) {
	it := Item{
		Brand: "Bajaj",
		SKUs: []SKU{
			{Code: "KTM1", Kind: SKUOEM, Brand: "KTM"},
			{Code: "BAJAJ1", Kind: SKUManufacturer, Brand: "Bajaj"},
		},
	}
	got := it.DisplaySKUs()
	if len(got) != 2 || got[0].Code != "BAJAJ1" || got[1].Code != "KTM1" {
		t.Errorf("DisplaySKUs() = %v, want BAJAJ1 before KTM1", got)
	}
}
