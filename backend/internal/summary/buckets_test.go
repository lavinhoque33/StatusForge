package summary

import (
	"testing"
	"time"
)

func TestRollingBucketsAlignedAndClipped(t *testing.T) {
	to := time.Date(2026, 9, 28, 12, 30, 0, 0, time.UTC)
	for _, tc := range []struct {
		window string
		size   time.Duration
		count  int
	}{{"24h", time.Hour, 25}, {"7d", 6 * time.Hour, 29}} {
		t.Run(tc.window, func(t *testing.T) {
			r := Compute(Input{Window: tc.window, To: to})
			if len(r.Buckets) != tc.count || r.Buckets[0].From != r.From ||
				r.Buckets[len(r.Buckets)-1].To != r.To {
				t.Fatalf(
					"clipped bounds: %d first=%+v last=%+v",
					len(r.Buckets),
					r.Buckets[0],
					r.Buckets[len(r.Buckets)-1],
				)
			}
			for i, b := range r.Buckets {
				if i > 0 && r.Buckets[i-1].To != b.From {
					t.Fatalf("gap at bucket %d", i)
				}
				if i > 0 && i < len(r.Buckets)-1 &&
					!parse(b.From).Equal(parse(b.From).Truncate(tc.size)) {
					t.Fatalf("not UTC aligned: %+v", b)
				}
			}
		})
	}
}
