package main

import "testing"

func TestCheckSchema(t *testing.T) {
	tests := []struct {
		schema, version string
		allow, wantErr  bool
	}{
		{"public", "", false, true},
		{"public", "", true, false},
		{"public", "docker", false, false},
		{"public", "v1.2.3", false, false},
		{"dev", "", false, false},
		{"e2e_desktop", "", false, false},
	}
	for _, tt := range tests {
		err := checkSchema(tt.schema, tt.version, tt.allow)
		if (err != nil) != tt.wantErr {
			t.Errorf("checkSchema(%q, %q, %v) = %v, want error %v", tt.schema, tt.version, tt.allow, err, tt.wantErr)
		}
	}
}
