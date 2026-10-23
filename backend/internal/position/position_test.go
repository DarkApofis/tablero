package position

import (
	"math"
	"testing"
)

func p(v float64) *float64 { return new(v) }

func TestBetween(t *testing.T) {
	tests := []struct {
		name       string
		prev, next *float64
		want       float64
		wantOK     bool
	}{
		{"empty list", nil, nil, Step, true},
		{"at start", nil, p(1024), 0, true},
		{"at start, negative is fine", nil, p(-5), -5 - Step, true},
		{"at end", p(2048), nil, 3072, true},
		{"midpoint", p(1024), p(2048), 1536, true},
		{"midpoint of fractions", p(1), p(1.5), 1.25, true},
		{"gap too small", p(1), p(1 + 1e-12), 0, false},
		{"adjacent floats", p(1), p(math.Nextafter(1, 2)), 0, false},
		{"equal positions", p(7), p(7), 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Between(tt.prev, tt.next)
			if ok != tt.wantOK || (ok && got != tt.want) {
				t.Errorf("Between = %v, %v; want %v, %v", got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// Repeatedly inserting into the same gap must eventually ask for a rebalance
// instead of returning a position equal to a neighbor.
func TestBetweenRunsOutThenAsksForRebalance(t *testing.T) {
	lo, hi := 1024.0, 2048.0
	for i := range 200 {
		mid, ok := Between(&lo, &hi)
		if !ok {
			if i < 20 {
				t.Fatalf("asked for rebalance after only %d inserts", i)
			}
			return
		}
		if mid <= lo || mid >= hi {
			t.Fatalf("insert %d: %v not strictly between %v and %v", i, mid, lo, hi)
		}
		hi = mid
	}
	t.Fatal("never asked for a rebalance")
}
