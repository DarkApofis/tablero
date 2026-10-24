package domain

// Fractional positions for ordered lists (columns in a board, tasks in a
// column). See docs/DECISIONS.md #2.

// PositionStep is the gap used when appending, prepending and rebalancing.
const PositionStep = 1024.0

// minGap is the smallest gap we are willing to split. Below it, precision
// is about to run out and the list should be rebalanced.
const minGap = 1e-9

// Between returns a position strictly between prev and next. A nil prev means
// "at the start", a nil next means "at the end". ok is false when there is no
// usable room left and the caller must rebalance and try again.
func PositionBetween(prev, next *float64) (pos float64, ok bool) {
	switch {
	case prev == nil && next == nil:
		return PositionStep, true
	case prev == nil:
		return *next - PositionStep, true
	case next == nil:
		return *prev + PositionStep, true
	}

	lo, hi := *prev, *next
	if hi-lo < minGap {
		return 0, false
	}
	mid := lo + (hi-lo)/2
	if mid <= lo || mid >= hi {
		return 0, false
	}
	return mid, true
}
