package templatefn

import (
	"testing"
	"time"
)

// fakeClock returns a fixed time, making timestamp/date functions deterministic.
type fakeClock struct{ t time.Time }

func (f fakeClock) now() time.Time { return f.t }

// fakeRandom is a deterministic RandomSource: Int63n always returns the
// configured value, Float64 likewise.
type fakeRandom struct {
	intVal   int64
	floatVal float64
}

func (f fakeRandom) Int63n(n int64) int64 {
	if f.intVal < n {
		return f.intVal
	}
	return 0
}

func (f fakeRandom) Float64() float64 { return f.floatVal }

func TestSetClock_MakesTimestampDeterministic(t *testing.T) {
	fixed := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	SetClock(func() time.Time { return fixed })
	defer SetClock(nil)

	if got := timestamp(); got != fixed.Unix() {
		t.Errorf("timestamp = %d, want %d", got, fixed.Unix())
	}
	if got := timestampMs(); got != fixed.UnixMilli() {
		t.Errorf("timestampMs = %d, want %d", got, fixed.UnixMilli())
	}
	// date format should reflect the fixed clock.
	if got := date("YMD"); got != "20240102" {
		t.Errorf("date(YMD) = %s, want 20240102", got)
	}
}

func TestSetRandomSource_MakesRandomDeterministic(t *testing.T) {
	SetRandomSource(fakeRandom{intVal: 5, floatVal: 0.5})
	defer SetRandomSource(nil)

	// random(0, 10) -> randInt63n(10) returns 5 (configured) -> 5.
	if got := random(0, 10); got != 5 {
		t.Errorf("random(0,10) = %d, want 5", got)
	}
	// randomFloat(0, 100) -> 0 + 0.5*(100-0) = 50.
	if got := randomFloat(0, 100); got != 50 {
		t.Errorf("randomFloat(0,100) = %v, want 50", got)
	}
}

// TestSetClock_RestoresRealClock verifies that passing nil restores the
// production clock (calls succeed and return a value close to time.Now).
func TestSetClock_RestoresRealClock(t *testing.T) {
	SetClock(func() time.Time { return time.Time{} })
	SetClock(nil)
	before := time.Now().Unix()
	got := timestamp()
	after := time.Now().Unix()
	if got < before-1 || got > after+1 {
		t.Errorf("after restore, timestamp = %d not in [%d, %d]", got, before-1, after+1)
	}
}
