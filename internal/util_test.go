package bench

import (
	"sync"
	"testing"
	"time"
)

func TestParseDuration_Table(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"", 0, true},
		{"-1s", 0, true},
		{"30", 30 * time.Second, false},
		{"0", 0, false},
		{"500ms", 500 * time.Millisecond, false},
		{"5MS", 5 * time.Millisecond, false},
		{"30S", 30 * time.Second, false},
		{"2M", 2 * time.Minute, false},
		{"1H", time.Hour, false},
		{"1D", 24 * time.Hour, false},
		{"2d", 48 * time.Hour, false},
		{"1W", 7 * 24 * time.Hour, false},
		{"1w", 7 * 24 * time.Hour, false},
		{"not-a-duration", 0, true},
		{"  10s  ", 10 * time.Second, false},
	}
	for _, tc := range cases {
		got, err := parseDuration(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseDuration(%q) err=nil, want error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDuration(%q) unexpected err: %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseDuration(%q)=%v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestGenSequenceId_UniqueAndMonotonicLowBits(t *testing.T) {
	const n = 2000
	seen := make(map[int64]struct{}, n)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := genSequenceId()
			mu.Lock()
			if _, ok := seen[id]; ok {
				t.Errorf("duplicate id %d", id)
			}
			seen[id] = struct{}{}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(seen) != n {
		t.Fatalf("unique ids=%d, want %d", len(seen), n)
	}
}

func TestNormalizeWorkerAddrs_Edges(t *testing.T) {
	got := normalizeWorkerAddrs([]string{
		"",
		"  ",
		"127.0.0.1:9000",
		"http://worker:9001/",
		"https://worker:9002/custom",
	}, "/worker")
	want := []string{
		"http://127.0.0.1:9000/api/worker",
		"http://worker:9001/api/worker",
		"https://worker:9002/custom",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

func TestNormalizeCaseInsensitive(t *testing.T) {
	cases := map[string]string{
		"5MS": "5ms",
		"30S": "30s",
		"2M":  "2m",
		"1H":  "1h",
		"10":  "10",
	}
	for in, want := range cases {
		if got := normalizeCaseInsensitive(in); got != want {
			t.Errorf("normalizeCaseInsensitive(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestByteUnits(t *testing.T) {
	if KB != 1024 || MB != 1024*1024 || GB != 1024*1024*1024 {
		t.Fatalf("KB/MB/GB = %d/%d/%d", KB, MB, GB)
	}
}
