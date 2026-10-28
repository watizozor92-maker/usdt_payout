package amount

import "testing"

func TestParseUSDT(t *testing.T) {
	tests := []struct {
		in         string
		units      int64
		normalized string
		ok         bool
	}{
		{"1", 1_000_000, "1", true}, {"1.230000", 1_230_000, "1.23", true}, {"0.000001", 1, "0.000001", true},
		{"0", 0, "", false}, {"1.0000001", 0, "", false}, {"-1", 0, "", false}, {"1e2", 0, "", false},
	}
	for _, tt := range tests {
		u, n, e := ParseUSDT(tt.in)
		if (e == nil) != tt.ok || u != tt.units || n != tt.normalized {
			t.Errorf("ParseUSDT(%q)=(%d,%q,%v)", tt.in, u, n, e)
		}
	}
}
