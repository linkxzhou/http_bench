package metrics

import (
	"testing"
)

// TestDefaultCircuitBreakerPolicy_MinSamples verifies the breaker does not
// trip until at least MinSamples requests have been recorded, even if every
// one of the first few is a failure (plan.md §F-4). This prevents cold-start
// failures from aborting the whole run.
func TestDefaultCircuitBreakerPolicy_MinSamples(t *testing.T) {
	p := DefaultCircuitBreakerPolicy{MinSamples: 10, ThresholdPercent: 50}

	// 1 failure out of 1 request = 100% error rate, but below MinSamples.
	r := NewCollectResult()
	r.TotalRequests = 1
	r.FailedRequests = 1
	if p.ShouldOpen(r) {
		t.Errorf("breaker tripped with %d samples (MinSamples=%d)", r.TotalRequests, p.MinSamples)
	}

	// At MinSamples with 100% failures it should trip.
	r.TotalRequests = 10
	r.FailedRequests = 10
	if !p.ShouldOpen(r) {
		t.Errorf("breaker did not trip at MinSamples with 100%% error rate")
	}
}

// TestDefaultCircuitBreakerPolicy_Threshold verifies the error-rate
// comparison boundary: errorRate > ThresholdPercent trips, == does not.
func TestDefaultCircuitBreakerPolicy_Threshold(t *testing.T) {
	p := DefaultCircuitBreakerPolicy{MinSamples: 0, ThresholdPercent: 50}

	// 50% error rate: exactly at threshold, should NOT trip (> not >=).
	r := NewCollectResult()
	r.TotalRequests = 100
	r.FailedRequests = 50
	if p.ShouldOpen(r) {
		t.Errorf("breaker tripped at exactly threshold (50%%); want > only")
	}

	// 51% error rate: above threshold, should trip.
	r.FailedRequests = 51
	if !p.ShouldOpen(r) {
		t.Errorf("breaker did not trip above threshold (51%%)")
	}
}

// TestDefaultCircuitBreakerPolicy_ZeroSamples verifies safety when no
// requests have been recorded yet.
func TestDefaultCircuitBreakerPolicy_ZeroSamples(t *testing.T) {
	p := DefaultCircuitBreakerPolicy{MinSamples: 0, ThresholdPercent: 50}
	r := NewCollectResult()
	if p.ShouldOpen(r) {
		t.Errorf("breaker tripped on empty result")
	}
}

// TestCircuitBroken_UsesInjectedPolicy verifies CircuitBroken() consults the
// package-level policy and that SetCircuitBreakerPolicy(nil) restores the
// default. This guards the wiring between CircuitBroken and the policy.
func TestCircuitBroken_UsesInjectedPolicy(t *testing.T) {
	// Save and restore default policy so other tests are unaffected.
	defer SetCircuitBreakerPolicy(nil)

	// Inject a policy that always trips.
	SetCircuitBreakerPolicy(stubPolicy{open: true})
	r := NewCollectResult()
	if !r.CircuitBroken() {
		t.Errorf("CircuitBroken did not honor injected always-open policy")
	}

	// Inject a policy that never trips.
	SetCircuitBreakerPolicy(stubPolicy{open: false})
	r2 := NewCollectResult()
	r2.TotalRequests = 100
	r2.FailedRequests = 100
	if r2.CircuitBroken() {
		t.Errorf("CircuitBroken tripped despite injected never-open policy")
	}

	// nil restores default: MinSamples=10, ThresholdPercent=50.
	SetCircuitBreakerPolicy(nil)
	r3 := NewCollectResult()
	r3.TotalRequests = 9
	r3.FailedRequests = 9
	if r3.CircuitBroken() {
		t.Errorf("default policy tripped below MinSamples=10")
	}
}

type stubPolicy struct{ open bool }

func (s stubPolicy) ShouldOpen(_ *CollectResult) bool { return s.open }

// TestSuccessfulRequestsAndErrorRate verifies the F-3 derived metrics:
// SuccessfulRequests = TotalRequests - FailedRequests, and ErrorRate is the
// integer percentage. Both must be safe to call concurrently with Record().
func TestSuccessfulRequestsAndErrorRate(t *testing.T) {
	r := NewCollectResult()
	r.TotalRequests = 100
	r.FailedRequests = 25

	if got := r.SuccessfulRequests(); got != 75 {
		t.Errorf("SuccessfulRequests = %d, want 75", got)
	}
	if got := r.ErrorRate(); got != 25 {
		t.Errorf("ErrorRate = %d, want 25", got)
	}

	// Empty result: zero-safe.
	r2 := NewCollectResult()
	if got := r2.SuccessfulRequests(); got != 0 {
		t.Errorf("SuccessfulRequests on empty = %d, want 0", got)
	}
	if got := r2.ErrorRate(); got != 0 {
		t.Errorf("ErrorRate on empty = %d, want 0", got)
	}
}
