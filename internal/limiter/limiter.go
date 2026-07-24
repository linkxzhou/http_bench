// Package limiter provides a simple global token-bucket rate limiter built on
// the standard library. It distributes tokens at a fixed rate (QPS) shared
// across all concurrent workers, so the aggregate request rate is bounded
// regardless of the concurrency level C.
//
// This replaces the previous per-goroutine time.Sleep approach whose formula
// (1e6 / (C * QPS) µs per request) actually produced C*QPS requests per second
// per goroutine, i.e. C^2*QPS in total — a quadratic amplification of the
// intended rate. The shared limiter guarantees the total rate is exactly QPS.
//
// A zero-value Limiter with rate <= 0 is a no-op pass-through (unlimited).
package limiter

import (
	"context"
	"sync/atomic"
	"time"
)

// Limiter is a simple global token-bucket rate limiter.
//
// A zero-value Limiter with rate <= 0 is a no-op pass-through (unlimited).
type Limiter struct {
	rate     int64 // configured QPS; read atomically
	tokens   chan struct{}
	stopChan chan struct{}
	stopped  atomic.Bool
}

// NewLimiter creates a Limiter that emits `rate` tokens per second.
// If rate <= 0, it returns a disabled limiter whose Wait() is a no-op.
func NewLimiter(rate int) *Limiter {
	if rate <= 0 {
		return &Limiter{rate: 0}
	}

	// Token bucket sized to one tick; a buffered channel smooths sub-second
	// bursts up to rate tokens without exceeding the long-run average.
	l := &Limiter{
		rate:     int64(rate),
		tokens:   make(chan struct{}, rate),
		stopChan: make(chan struct{}),
	}

	// Single producer goroutine feeds tokens at a steady interval.
	interval := time.Second / time.Duration(rate)
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-l.stopChan:
				return
			case <-ticker.C:
				select {
				case l.tokens <- struct{}{}:
				default:
					// Bucket full; drop the token to avoid blocking the
					// producer and to keep the long-run rate accurate.
				}
			}
		}
	}()

	return l
}

// Wait blocks until a token is available, the limiter is stopped, or ctx is
// canceled. For an unlimited limiter (rate <= 0) it returns immediately.
// Returns the ctx error (nil if a token was acquired) so callers can bail out
// promptly on cancellation rather than completing a request after stop.
func (l *Limiter) Wait(ctx context.Context) error {
	if l.rate <= 0 {
		return nil
	}
	select {
	case <-l.tokens:
		return nil
	case <-l.stopChan:
		return context.Canceled
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stop releases the producer goroutine. Safe to call multiple times.
func (l *Limiter) Stop() {
	if l.rate <= 0 {
		return
	}
	if l.stopped.Swap(true) {
		return
	}
	close(l.stopChan)
}
