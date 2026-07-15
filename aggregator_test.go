package golens

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestAggregatorTracksWindow(t *testing.T) {
	storage := newMemoryStorage(0)
	a := newAggregator(storage, time.Millisecond)

	a.add("m", 2, nil)
	a.add("m", 4, nil)
	a.add("m", 6, nil)

	// Peek into the window directly.
	a.mu.Lock()
	w := a.windows["m"]
	a.mu.Unlock()
	if w == nil || w.count != 3 || w.sum != 12 || w.min != 2 || w.max != 6 {
		t.Errorf("window = %+v", w)
	}
}

func TestAggregatorFlushAllWritesToStorage(t *testing.T) {
	storage := newMemoryStorage(0)
	a := newAggregator(storage, time.Millisecond)
	r := &Registry{storage: storage, ctx: context.Background(), cfg: DefaultConfig()}

	a.add("flushed", 1, nil)
	a.add("flushed", 3, nil)
	a.flushAll(r)

	res, err := storage.Query(context.Background(), Query{Name: "flushed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 {
		t.Fatalf("want 1 bucket, got %d", len(res))
	}
	if res[0].Count != 2 || res[0].Sum != 4 {
		t.Errorf("stored = %+v", res[0])
	}
	if res[0].Min != 1 || res[0].Max != 3 {
		t.Errorf("min/max = %v/%v", res[0].Min, res[0].Max)
	}
}

func TestAggregatorFlushAllEmpty(t *testing.T) {
	storage := newMemoryStorage(0)
	a := newAggregator(storage, time.Millisecond)
	r := &Registry{storage: storage, ctx: context.Background(), cfg: DefaultConfig()}
	a.flushAll(r) // no windows: no-op, no panic
	res, _ := storage.Query(context.Background(), Query{})
	if len(res) != 0 {
		t.Errorf("expected empty, got %d", len(res))
	}
}

func TestAggregatorConcurrent(t *testing.T) {
	storage := newMemoryStorage(0)
	a := newAggregator(storage, time.Millisecond)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.add("c", 1, nil)
		}()
	}
	wg.Wait()
	a.mu.Lock()
	w := a.windows["c"]
	a.mu.Unlock()
	if w.count != 100 {
		t.Errorf("count = %d, want 100", w.count)
	}
}
