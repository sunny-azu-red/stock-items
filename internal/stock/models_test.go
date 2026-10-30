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
