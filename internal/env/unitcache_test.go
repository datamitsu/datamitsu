package env

import "testing"

// A malformed value must fall back to the default rather than disabling the
// verdict cache: zero is a meaningful setting ("never trust a stored verdict"),
// so a typo silently turning it off would be indistinguishable from asking for
// it. Negative values are rejected for the same reason.
func TestGetUnitCacheTTLMinutes(t *testing.T) {
	def := GetUnitCacheTTLMinutes()
	if def <= 0 {
		t.Fatalf("default TTL = %d, want a positive value", def)
	}

	tests := []struct {
		name string
		set  string
		want int
	}{
		{"unset uses the default", "", def},
		{"explicit override", "42", 42},
		{"zero disables the cache", "0", 0},
		{"negative falls back", "-5", def},
		{"garbage falls back", "soon", def},
		{"float falls back", "1.5", def},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(unitCacheTTLMinutes.Name, tt.set)
			if got := GetUnitCacheTTLMinutes(); got != tt.want {
				t.Errorf("GetUnitCacheTTLMinutes() = %d, want %d", got, tt.want)
			}
		})
	}
}
