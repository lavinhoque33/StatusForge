package store

import (
	"sync"
	"testing"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

func TestConcurrentClaimsHaveOneWinner(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s := isolatedStore(t, now)
	ctx := t.Context()
	m := monitor.New("concurrent", monitor.Check{Method: "GET", DeadlineMs: 1000}, now)
	m.IntervalSeconds = 60
	if err := s.Create(ctx, m); err != nil {
		t.Fatal(err)
	}
	ws, err := s.Works(ctx, m, now)
	if err != nil || len(ws) != 1 {
		t.Fatalf("work %v %v", ws, err)
	}
	start := make(chan struct{})
	results := make(chan bool, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, token, e := s.Claim(ctx, ws[0], now)
			results <- e == nil && token != ""
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	for won := range results {
		if won {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("claims won=%d", winners)
	}
}
