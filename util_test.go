package main

import (
	"sync"
	"testing"
	"time"
)

// ============================================================================
// Test parseDuration (pure function, no os.Exit)
// Remaining template-function tests live in internal/templatefn/funcs_test.go.
// ============================================================================

func TestParseDuration(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		// Bare number defaults to seconds
		{"30", 30 * time.Second},
		{"0", 0},
		// Standard units
		{"100ms", 100 * time.Millisecond},
		{"10s", 10 * time.Second},
		{"5m", 5 * time.Minute},
		{"2h", 2 * time.Hour},
		{"1d", 24 * time.Hour},
		{"1w", 7 * 24 * time.Hour},
		// Case-insensitive units
		{"5MS", 5 * time.Millisecond},
		{"3S", 3 * time.Second},
		{"2M", 2 * time.Minute},
		{"1H", time.Hour},
		{"1D", 24 * time.Hour},
		{"1W", 7 * 24 * time.Hour},
		// Whitespace trimmed
		{"  10s  ", 10 * time.Second},
	}

	for _, tt := range tests {
		got, err := parseDuration(tt.input)
		if err != nil {
			t.Errorf("parseDuration(%q) unexpected error: %v", tt.input, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseDuration(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestParseDurationErrors(t *testing.T) {
	errorCases := []string{
		"",    // empty
		"   ", // whitespace only
		"abc", // non-numeric, no unit
		"10x", // unknown unit
		"-5s", // negative value
		"-1",  // negative bare
		"ms",  // unit without value
	}

	for _, input := range errorCases {
		got, err := parseDuration(input)
		if err == nil {
			t.Errorf("parseDuration(%q) expected error, got %v", input, got)
		}
		if got != 0 {
			t.Errorf("parseDuration(%q) error case returned non-zero duration %v", input, got)
		}
	}
}

// TestGenSequenceId_Unique verifies that genSequenceId produces unique IDs
// across concurrent callers (plan.md §E-6). The old Unix()*100+i scheme could
// collide; the new nanosecond+atomic-sequence scheme must not.
func TestGenSequenceId_Unique(t *testing.T) {
	const goroutines = 32
	const perGoroutine = 64
	ids := make(chan int64, goroutines*perGoroutine)

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				ids <- genSequenceId()
			}
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[int64]struct{}, goroutines*perGoroutine)
	for id := range ids {
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate ID generated: %d", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != goroutines*perGoroutine {
		t.Errorf("expected %d unique IDs, got %d", goroutines*perGoroutine, len(seen))
	}
}

// TestGenSequenceId_MonotonicWithinNanosecond verifies that even when two
// calls fall within the same nanosecond (rare but possible), the atomic
// sequence still differentiates them.
func TestGenSequenceId_MonotonicWithinNanosecond(t *testing.T) {
	a := genSequenceId()
	b := genSequenceId()
	if a == b {
		t.Fatalf("two consecutive calls produced identical ID %d", a)
	}
}

// TestNormalizeWorkerAddrs verifies bare host:port addresses are rewritten
// into full worker API URLs while already-qualified URLs are preserved.
func TestNormalizeWorkerAddrs(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		api  string
		want []string
	}{
		{
			name: "bare host:port gets scheme and default api path",
			in:   []string{"127.0.0.1:12710"},
			api:  "",
			want: []string{"http://127.0.0.1:12710/api"},
		},
		{
			name: "custom worker api path",
			in:   []string{"127.0.0.1:12710"},
			api:  "cb9ab101f9f725cb7c3a355bd5631184",
			want: []string{"http://127.0.0.1:12710/apicb9ab101f9f725cb7c3a355bd5631184"},
		},
		{
			name: "scheme preserved, path appended",
			in:   []string{"http://192.168.1.10:12710"},
			api:  "",
			want: []string{"http://192.168.1.10:12710/api"},
		},
		{
			name: "explicit path preserved",
			in:   []string{"http://192.168.1.10:12710/custom"},
			api:  "",
			want: []string{"http://192.168.1.10:12710/custom"},
		},
		{
			name: "trailing slash treated as empty path",
			in:   []string{"192.168.1.10:12710/"},
			api:  "",
			want: []string{"http://192.168.1.10:12710/api"},
		},
		{
			name: "empty entries dropped",
			in:   []string{"", "  ", "127.0.0.1:12710"},
			api:  "",
			want: []string{"http://127.0.0.1:12710/api"},
		},
	}
	for _, tt := range tests {
		got := normalizeWorkerAddrs(tt.in, tt.api)
		if len(got) != len(tt.want) {
			t.Errorf("%s: normalizeWorkerAddrs(%v) = %v, want %v", tt.name, tt.in, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("%s: normalizeWorkerAddrs(%v)[%d] = %q, want %q", tt.name, tt.in, i, got[i], tt.want[i])
			}
		}
	}
}
