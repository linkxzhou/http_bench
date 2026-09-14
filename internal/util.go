/*
 * util.go — 通用工具函数
 *
 *   genSequenceId() int64 — 进程唯一 RunID
 *     └─ (UTC 纳秒 &^ 0xFFFFF) | (atomic 序号 & 0xFFFFF)
 *        时间戳占高位、单调序号占低 20 位，互不重叠 → 无碰撞
 *
 *   parseDuration(s) (time.Duration, error)
 *     ├─ 纯数字 → 秒
 *     ├─ "1D"/"1W" 后缀 → 天/周
 *     └─ normalizeCaseInsensitive("5MS"/"2M"...) → 标准库解析
 *
 *   normalizeWorkerAddrs(addrs, apiPath) []string
 *     └─ 裸 host:port → http://host:port/api{path} 完整 worker API URL
 *
 *   const KB / MB / GB — 字节单位（全包共享，自 metrics/tmpl_math 合并）
 *
 * 依赖：无
 * 被依赖：cli.go (parseDuration), http_bench.go (genSequenceId,
 *         normalizeWorkerAddrs), metrics.go / report.go / tmpl_math.go (KB/MB/GB)
 */

package bench

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Byte-size units for human-readable output (shared across metrics,
// report, and template formatting).
const (
	KB = 1 << 10
	MB = 1 << 20
	GB = 1 << 30
)

var runIDSeq int64

// genSequenceId returns a process-unique run ID. It packs a UTC nanosecond
// timestamp (upper bits) and a monotonic sequence (lower 20 bits) into a
// single int64. Each component occupies non-overlapping bits, so the
// combination is collision-free as long as fewer than ~1M IDs are generated
// in a single process.
func genSequenceId() int64 {
	seq := atomic.AddInt64(&runIDSeq, 1)
	base := time.Now().UTC().UnixNano()
	return (base &^ 0xFFFFF) | (seq & 0xFFFFF)
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
// the route registered by the dashboard server.
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

// normalizeCaseInsensitive converts "5MS" → "5ms", "30S" → "30s", "2M" → "2m"
// so time.ParseDuration accepts the legacy case-insensitive suffixes.
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
