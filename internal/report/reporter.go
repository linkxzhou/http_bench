// Package report renders benchmark snapshots to text, CSV, or HTML. It
// depends on the metrics package only through the Snapshot DTO, breaking
// the would-be import cycle (metrics → report → metrics).
package report

import (
	"fmt"
	"html"
	"io"
	"sort"
	"time"
)

// Snapshot is the read-only report view of a CollectResult. Callers copy
// the relevant fields from *metrics.CollectResult under its own lock; the
// report package does not import metrics to avoid the cycle.
type Snapshot struct {
	TotalRequests    int64
	FailedRequests   int64
	RPS              int64
	BytesReceived    int64
	Fastest          time.Duration
	Slowest          time.Duration
	Average          time.Duration
	Duration         time.Duration
	LatencyHistogram map[time.Duration]int64
	StatusCodeCounts map[int]int
	ErrorCounts      map[string]int
	StopReason       string
}

// Reporter renders a snapshot to an io.Writer in a specific format.
type Reporter interface {
	Write(w io.Writer, s Snapshot) error
}

// TextReporter renders the human-readable summary, status code distribution,
// latency percentiles, and error distribution.
type TextReporter struct{}

// CSVReporter renders latency samples as comma-separated rows.
type CSVReporter struct{}

// HTMLReporter renders a self-contained HTML document with HTML-escaped text.
type HTMLReporter struct{}

// NewReporter returns the Reporter matching the given output name, or the
// TextReporter for unknown/empty values.
func NewReporter(output string) Reporter {
	switch output {
	case "csv":
		return CSVReporter{}
	case "html":
		return HTMLReporter{}
	default:
		return TextReporter{}
	}
}

func sortedDurations(lats map[time.Duration]int64) []time.Duration {
	out := make([]time.Duration, 0, len(lats))
	for d := range lats {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func sortedStatusCodes(dist map[int]int) []int {
	out := make([]int, 0, len(dist))
	for code := range dist {
		out = append(out, code)
	}
	sort.Ints(out)
	return out
}

type errorEntry struct {
	msg   string
	count int
}

func sortedErrors(dist map[string]int) []errorEntry {
	out := make([]errorEntry, 0, len(dist))
	for msg, count := range dist {
		out = append(out, errorEntry{msg: msg, count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].count != out[j].count {
			return out[i].count > out[j].count
		}
		return out[i].msg < out[j].msg
	})
	return out
}

func (TextReporter) Write(w io.Writer, s Snapshot) error {
	if len(s.LatencyHistogram) == 0 {
		return nil
	}
	avgSize := int64(0)
	if s.TotalRequests > 0 {
		avgSize = s.BytesReceived / s.TotalRequests
	}
	fmt.Fprintf(w, "Summary:\n")
	fmt.Fprintf(w, "  Total:\t%4.4f secs\n", s.Duration.Seconds())
	fmt.Fprintf(w, "  Slowest:\t%4.4f secs\n", s.Slowest.Seconds())
	fmt.Fprintf(w, "  Fastest:\t%4.4f secs\n", s.Fastest.Seconds())
	fmt.Fprintf(w, "  Average:\t%4.4f secs\n", s.Average.Seconds())
	fmt.Fprintf(w, "  Requests/sec:\t%4.2f\n", float64(s.RPS))
	fmt.Fprintf(w, "  Total data:\t%s\n", ToByteSizeStr(float64(s.BytesReceived)))
	if s.TotalRequests > 0 {
		fmt.Fprintf(w, "  Size/request:\t%d bytes\n", avgSize)
	}
	if s.StopReason != "" {
		fmt.Fprintf(w, "  Stop reason:\t%s\n", s.StopReason)
	}
	if len(s.StatusCodeCounts) > 0 {
		fmt.Fprintf(w, "\nStatus code distribution:\n")
		for _, code := range sortedStatusCodes(s.StatusCodeCounts) {
			fmt.Fprintf(w, "  [%d]\t%d responses\n", code, s.StatusCodeCounts[code])
		}
	}
	if s.TotalRequests > 0 {
		writeLatencyPercentiles(w, s)
	}
	if len(s.ErrorCounts) > 0 {
		fmt.Fprintf(w, "\nError distribution:\n")
		for _, e := range sortedErrors(s.ErrorCounts) {
			fmt.Fprintf(w, "  [%d times] %s\n", e.count, e.msg)
		}
	}
	return nil
}

func writeLatencyPercentiles(w io.Writer, s Snapshot) {
	sorted := sortedDurations(s.LatencyHistogram)
	percentiles := []int{10, 25, 50, 75, 90, 95, 99}
	percentileData := make([]float64, len(percentiles))
	var cumulative int64
	idx := 0
	for _, d := range sorted {
		if idx >= len(percentiles) {
			break
		}
		cumulative += s.LatencyHistogram[d]
		pct := (cumulative * 100) / s.TotalRequests
		for idx < len(percentiles) && int(pct) >= percentiles[idx] {
			percentileData[idx] = float64(d.Seconds())
			idx++
		}
	}
	fmt.Fprintf(w, "\nLatency distribution:\n")
	for i, p := range percentiles {
		fmt.Fprintf(w, "  %d%% in %4.4f secs\n", p, percentileData[i])
	}
}

func (CSVReporter) Write(w io.Writer, s Snapshot) error {
	fmt.Fprintf(w, "Duration,Count\n")
	for _, d := range sortedDurations(s.LatencyHistogram) {
		fmt.Fprintf(w, "%.4f,%d\n", d.Seconds(), s.LatencyHistogram[d])
	}
	return nil
}

func (HTMLReporter) Write(w io.Writer, s Snapshot) error {
	avgSize := int64(0)
	if s.TotalRequests > 0 {
		avgSize = s.BytesReceived / s.TotalRequests
	}
	fmt.Fprintf(w, "<html><head><meta charset=\"UTF-8\"><title>Benchmark Result</title></head><body>\n")
	fmt.Fprintf(w, "<h1>Benchmark Summary</h1>\n")
	fmt.Fprintf(w, "<p>Total: %.4f secs<br>Slowest: %.4f secs<br>Fastest: %.4f secs<br>Average: %.4f secs<br>Requests/sec: %.2f<br>Total Data: %s<br>Size/request: %d bytes</p>\n",
		s.Duration.Seconds(), s.Slowest.Seconds(), s.Fastest.Seconds(),
		s.Average.Seconds(), float64(s.RPS),
		ToByteSizeStr(float64(s.BytesReceived)), avgSize)
	fmt.Fprintf(w, "<h2>Status Codes</h2><table border=\"1\"><tr><th>Code</th><th>Count</th></tr>\n")
	for _, code := range sortedStatusCodes(s.StatusCodeCounts) {
		fmt.Fprintf(w, "<tr><td>%d</td><td>%d</td></tr>\n", code, s.StatusCodeCounts[code])
	}
	fmt.Fprintf(w, "</table>\n")
	fmt.Fprintf(w, "<h2>Latency Distribution</h2><table border=\"1\"><tr><th>Duration (secs)</th><th>Count</th></tr>\n")
	for _, d := range sortedDurations(s.LatencyHistogram) {
		fmt.Fprintf(w, "<tr><td>%.4f</td><td>%d</td></tr>\n", d.Seconds(), s.LatencyHistogram[d])
	}
	fmt.Fprintf(w, "</table>\n")
	if len(s.ErrorCounts) > 0 {
		fmt.Fprintf(w, "<h2>Errors</h2><table border=\"1\"><tr><th>Error</th><th>Count</th></tr>\n")
		for _, e := range sortedErrors(s.ErrorCounts) {
			fmt.Fprintf(w, "<tr><td>%s</td><td>%d</td></tr>\n", html.EscapeString(e.msg), e.count)
		}
		fmt.Fprintf(w, "</table>\n")
	}
	fmt.Fprintf(w, "</body></html>\n")
	return nil
}

// ToByteSizeStr renders bytes as a human-readable string. Kept here so the
// report package is self-contained for the snapshot DTO.
func ToByteSizeStr(b float64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%.2f B", b)
	}
	div, exp := unit, 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
		if exp >= 5 {
			break
		}
	}
	return fmt.Sprintf("%.2f %cB", b/float64(div), "KMGTP"[exp])
}
