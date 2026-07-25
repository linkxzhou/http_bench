package main

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/linkxzhou/http_bench/internal/templatefn"
)

var runIDSeq int64

func genSequenceId() int64 {
	seq := atomic.AddInt64(&runIDSeq, 1)
	base := time.Now().UTC().UnixNano()
	// Pack timestamp (upper bits) and monotonic sequence (lower 20 bits)
	// into a single int64. Each component occupies non-overlapping bits,
	// so the combination is collision-free as long as fewer than ~1M IDs
	// are generated in a single process. The previous XOR scheme could
	// collide when baseΔ == seq1^seq2.
	return (base &^ 0xFFFFF) | (seq & 0xFFFFF)
}

var (
	fnMap        = templatefn.FnMap
	HeaderRegexp = templatefn.HeaderRegexp
	AuthRegexp   = templatefn.AuthRegexp
)

func usageAndExit(msg string) {
	if msg != "" {
		fmt.Fprintln(os.Stderr, msg)
	}
	flag.Usage()
	os.Exit(1)
}

// parseDuration accepts strings like "30s", "500ms", "2m", "1h", "1D", "1W"
// (case-insensitive on the suffix) and the Go standard forms. "1D" is one
// day, "1W" is one week, matching the legacy CLI behavior.
func parseDuration(timeStr string) (time.Duration, error) {
	s := strings.TrimSpace(timeStr)
	if s == "" {
		return 0, errors.New("empty duration string")
	}
	if s[0] == '-' {
		return 0, fmt.Errorf("invalid duration %q: must be non-negative", timeStr)
	}
	// Bare integer defaults to seconds.
	if n, err := strconv.Atoi(s); err == nil {
		return time.Duration(n) * time.Second, nil
	}
	last := s[len(s)-1]
	// Single letter d/w extension (case-insensitive).
	switch strings.ToUpper(string(last)) {
	case "D":
		n, err := strconv.Atoi(s[:len(s)-1])
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", timeStr, err)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	case "W":
		n, err := strconv.Atoi(s[:len(s)-1])
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q: %w", timeStr, err)
		}
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	}
	// Convert "30S" / "2M" / "1H" / "5MS" into Go standard forms.
	normalized := normalizeCaseInsensitive(s)
	d, err := time.ParseDuration(normalized)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", timeStr, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("invalid duration %q: must be non-negative", timeStr)
	}
	return d, nil
}

// normalizeWorkerAddrs rewrites bare worker addresses (host:port) into full
// worker API URLs. A missing scheme defaults to "http"; a missing path
// defaults to the worker API mount point ("/api" + workerAPIPath), matching
// the route registered by the dashboard server. Without this, a bare
// "host:port" address fails in http.NewRequest ("first path segment in URL
// cannot contain colon") and the task never reaches the worker.
func normalizeWorkerAddrs(addrs []string, workerAPIPath string) []string {
	apiPath := "/api" + workerAPIPath
	out := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		if !strings.Contains(addr, "://") {
			addr = "http://" + addr
		}
		if u, err := url.Parse(addr); err == nil && (u.Path == "" || u.Path == "/") {
			u.Path = apiPath
			addr = u.String()
		}
		out = append(out, addr)
	}
	return out
}

func normalizeCaseInsensitive(s string) string {
	if !strings.ContainsAny(s, "smhdMSMHD") {
		return s
	}
	upper := strings.ToUpper(s)
	switch upper[len(upper)-1] {
	case 'S':
		// "5MS" -> "5ms"; "30S" -> "30s".
		if strings.HasSuffix(upper, "MS") {
			return strings.ToLower(s)
		}
		return s[:len(s)-1] + "s"
	case 'M':
		return s[:len(s)-1] + "m"
	case 'H':
		return s[:len(s)-1] + "h"
	case 'D', 'W':
		return s
	}
	return s
}
