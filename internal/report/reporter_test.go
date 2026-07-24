package report

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func sampleSnapshot() Snapshot {
	return Snapshot{
		TotalRequests:    100,
		FailedRequests:   2,
		RPS:              50,
		BytesReceived:    4096,
		Slowest:          100 * time.Millisecond,
		Fastest:          1 * time.Millisecond,
		Average:          5 * time.Millisecond,
		Duration:         2 * time.Second,
		LatencyHistogram: map[time.Duration]int64{1 * time.Millisecond: 1, 10 * time.Millisecond: 9, 100 * time.Millisecond: 90},
		StatusCodeCounts: map[int]int{200: 95, 500: 5},
		ErrorCounts:      map[string]int{"timeout": 3, "EOF": 1},
	}
}

func TestTextReporter_RendersAllSections(t *testing.T) {
	var buf bytes.Buffer
	rep := TextReporter{}
	if err := rep.Write(&buf, sampleSnapshot()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	s := buf.String()
	for _, want := range []string{"Summary:", "Status code distribution:", "Latency distribution:", "Error distribution:"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in output:\n%s", want, s)
		}
	}
}

func TestCSVReporter_EmitsRows(t *testing.T) {
	var buf bytes.Buffer
	rep := CSVReporter{}
	if err := rep.Write(&buf, sampleSnapshot()); err != nil {
		t.Fatalf("Write: %v", err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) < 2 || lines[0] != "Duration,Count" {
		t.Errorf("unexpected header line: %q", lines[0])
	}
}

func TestHTMLReporter_EscapesErrors(t *testing.T) {
	snap := sampleSnapshot()
	snap.ErrorCounts = map[string]int{`<script>alert(1)</script>`: 1}
	var buf bytes.Buffer
	rep := HTMLReporter{}
	if err := rep.Write(&buf, snap); err != nil {
		t.Fatalf("Write: %v", err)
	}
	s := buf.String()
	if strings.Contains(s, "<script>alert(1)</script>") {
		t.Errorf("HTML output did not escape script tag:\n%s", s)
	}
	if !strings.Contains(s, "&lt;script&gt;") {
		t.Errorf("expected escaped script tag in output:\n%s", s)
	}
}

func TestNewReporter_UnknownDefaultsToText(t *testing.T) {
	r := NewReporter("xyz")
	if _, ok := r.(TextReporter); !ok {
		t.Errorf("expected TextReporter for unknown format, got %T", r)
	}
}

func TestNewReporter_CSVAndHTML(t *testing.T) {
	if _, ok := NewReporter("csv").(CSVReporter); !ok {
		t.Errorf("expected CSVReporter for 'csv'")
	}
	if _, ok := NewReporter("html").(HTMLReporter); !ok {
		t.Errorf("expected HTMLReporter for 'html'")
	}
}

func TestTextReporter_EmptyHistogram(t *testing.T) {
	var buf bytes.Buffer
	rep := TextReporter{}
	if err := rep.Write(&buf, Snapshot{}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty output, got %q", buf.String())
	}
}
