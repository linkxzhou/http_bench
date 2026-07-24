package metrics

import "sync"

// CircuitBreakerPolicy decides whether the circuit breaker should open based
// on the current aggregate state. Abstracting this (plan.md §F-4) lets us:
//   - require a minimum sample size before tripping, so a single cold-start
//     failure does not abort the whole run;
//   - tune the error-rate threshold without touching call sites;
//   - substitute alternative rules (e.g. counting non-2xx status codes as
//     failures, or only tripping on network errors) in tests or future
//     configurations.
//
// Implementations must be safe for concurrent use: ShouldOpen is invoked from
// worker goroutines via AppendResult while the collector goroutine mutates
// the CollectResult under its own lock. The caller (CircuitBroken) already
// holds the result's RLock when invoking ShouldOpen, so implementations must
// NOT attempt to re-lock the result.
type CircuitBreakerPolicy interface {
	// ShouldOpen returns true when the breaker should trip and stop the run.
	// It receives the live CollectResult (caller holds Mu.RLock) and must
	// treat it as read-only.
	ShouldOpen(r *CollectResult) bool
}

// DefaultCircuitBreakerPolicy matches the legacy behavior — trip when the
// network-error rate (FailedRequests / TotalRequests) exceeds ThresholdPercent — but
// adds a MinSamples floor so the breaker cannot fire until enough requests
// have been observed. This prevents a few cold-start failures (TLS handshake
// on first connection, slow DNS, etc.) from immediately aborting a run that
// would otherwise succeed (plan.md §F-4).
//
// Failure semantics: FailedRequests counts only requests where res.Err != nil
// (network/protocol errors). HTTP status codes, including 5xx, do NOT count
// as failures. A future policy could extend this by tracking unexpected
// status codes; such a change would need Record() to populate the extra
// counter and is intentionally out of scope here.
type DefaultCircuitBreakerPolicy struct {
	// MinSamples is the minimum number of recorded requests before the
	// breaker is eligible to trip. Set to 0 to restore the legacy behavior
	// of tripping on the first failing request.
	MinSamples int64

	// ThresholdPercent is the error-rate percentage (0–100) above which the
	// breaker opens. errorRate = FailedRequests * 100 / TotalRequests.
	ThresholdPercent int
}

// ShouldOpen implements CircuitBreakerPolicy.
func (p DefaultCircuitBreakerPolicy) ShouldOpen(r *CollectResult) bool {
	// Not enough samples yet — give the run time to warm up (plan.md §F-4).
	if r.TotalRequests < p.MinSamples {
		return false
	}
	if r.TotalRequests == 0 {
		return false
	}
	errorRate := (r.FailedRequests * 100) / r.TotalRequests
	return errorRate > int64(p.ThresholdPercent)
}

// defaultCircuitBreakerPolicy is the package-level policy used by
// CircuitBroken(). It is protected by circuitBreakerMu so that tests can
// swap it via SetCircuitBreakerPolicy without racing with concurrent
// CircuitBroken() calls from worker goroutines.
//
// Lock ordering: CircuitBroken() acquires result.Mu.RLock first, then
// circuitBreakerMu.RLock. SetCircuitBreakerPolicy acquires only
// circuitBreakerMu.Lock. No code path acquires these in the reverse order,
// so there is no deadlock risk.
var (
	circuitBreakerMu   sync.RWMutex
	circuitBreakerInst CircuitBreakerPolicy = DefaultCircuitBreakerPolicy{
		MinSamples:       10,
		ThresholdPercent: CircuitBreakerPercent,
	}
)

// SetCircuitBreakerPolicy replaces the package-level circuit-breaker policy.
// Pass nil to restore the default. Intended for use in tests; production code
// should call this only during startup, before workers begin recording.
func SetCircuitBreakerPolicy(p CircuitBreakerPolicy) {
	if p == nil {
		p = DefaultCircuitBreakerPolicy{
			MinSamples:       10,
			ThresholdPercent: CircuitBreakerPercent,
		}
	}
	circuitBreakerMu.Lock()
	circuitBreakerInst = p
	circuitBreakerMu.Unlock()
}

// currentCircuitBreakerPolicy returns the active policy under the read lock.
func currentCircuitBreakerPolicy() CircuitBreakerPolicy {
	circuitBreakerMu.RLock()
	defer circuitBreakerMu.RUnlock()
	return circuitBreakerInst
}
