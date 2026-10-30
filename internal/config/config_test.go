package config

import "testing"

func TestSchema(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"public", ""},
		{" public ", ""},
		{"e2e_desktop", "e2e_desktop"},
		{" test_ab12 ", "test_ab12"},
	}
	for _, tt := range tests {
		if got := schema(tt.in); got != tt.want {
			t.Errorf("schema(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
