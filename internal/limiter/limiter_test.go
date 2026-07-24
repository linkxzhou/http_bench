package limiter

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewLimiter_Unlimited(t *testing.T) {
	l := NewLimiter(0)
	if err := l.Wait(context.Background()); err != nil {
		t.Errorf("unlimited Wait should return nil, got %v", err)
	}
	l.Stop()
}

func TestNewLimiter_NegativeIsUnlimited(t *testing.T) {
	l := NewLimiter(-1)
	if err := l.Wait(context.Background()); err != nil {
		t.Errorf("negative rate Wait should return nil, got %v", err)
	}
}

func TestWait_AcquiresWithinBurst(t *testing.T) {
	l := NewLimiter(100)
	defer l.Stop()
	// With bucket size 100, the first 100 Waits should succeed instantly
	// (or one tick each). We just check no spurious errors within a
	// short window.
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	for i := 0; i < 50; i++ {
		if err := l.Wait(ctx); err != nil {
			t.Errorf("Wait %d: %v", i, err)
		}
	}
}

func TestWait_RespectsCancellation(t *testing.T) {
	l := NewLimiter(1) // slow
	defer l.Stop()
	// Pre-drain so next wait blocks on the empty bucket.
	<-l.tokens
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := l.Wait(ctx)
	if err == nil {
		t.Errorf("expected error for canceled wait")
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Errorf("Wait did not return promptly on cancel")
	}
}

func TestWait_StopReturns(t *testing.T) {
	l := NewLimiter(1)
	<-l.tokens
	l.Stop()
	err := l.Wait(context.Background())
	if err == nil {
		t.Errorf("expected error after Stop, got nil")
	}
}

func TestRate_LongRunApproximatesConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("long-run test")
	}
	const rate = 50
	l := NewLimiter(rate)
	defer l.Stop()
	const workers = 10
	const duration = 600 * time.Millisecond
	var count atomic.Int64
	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for l.Wait(ctx) == nil {
				count.Add(1)
			}
		}()
	}
	wg.Wait()
	got := count.Load()
	// Use float arithmetic so sub-second durations produce a non-zero
	// expected count. Allow generous slack (0.5x–2x) for scheduling jitter.
	expected := int64(float64(rate) * duration.Seconds())
	if expected < 1 {
		expected = 1
	}
	if got < expected/2 || got > expected*2 {
		t.Errorf("rate mismatch: got %d, expected ~%d", got, expected)
	}
}
