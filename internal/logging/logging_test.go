package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestSetGetLevel(t *testing.T) {
	SetLevel(LevelInfo)
	if GetLevel() != LevelInfo {
		t.Errorf("GetLevel mismatch: %d", GetLevel())
	}
}

func TestSetOutput_RespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)
	SetLevel(LevelError)
	Info(1, "info should be suppressed")
	Trace(1, "trace should be suppressed")
	Error(1, "an error")
	s := buf.String()
	if !strings.Contains(s, "an error") {
		t.Errorf("expected error message, got %q", s)
	}
	if strings.Contains(s, "suppressed") {
		t.Errorf("lower-level messages should be filtered: %q", s)
	}
	SetOutput(nil)
}

func TestSlog_RoutesThroughLevelFilter(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)
	SetLevel(LevelWarn)
	defer SetOutput(nil)
	logger := Slog()
	logger.Info("info suppressed")
	logger.Warn("warn visible")
	s := buf.String()
	if strings.Contains(s, "info suppressed") {
		t.Errorf("info should be filtered: %q", s)
	}
	if !strings.Contains(s, "warn visible") {
		t.Errorf("warn missing: %q", s)
	}
}

func TestLoggingConcurrent(t *testing.T) {
	var buf bytes.Buffer
	SetOutput(&buf)
	SetLevel(LevelInfo)
	defer SetOutput(nil)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				Info(int64(j), "msg %d", j)
			}
		}()
	}
	wg.Wait()
	if buf.Len() == 0 {
		t.Errorf("expected output")
	}
}

func TestSlog_Enabled(t *testing.T) {
	SetLevel(LevelDebug)
	defer SetLevel(LevelError)
	h := newSlogHandler()
	if !h.Enabled(nil, slog.LevelDebug) {
		t.Errorf("Debug should be enabled at threshold LevelDebug")
	}
	if !h.Enabled(nil, slog.LevelInfo) {
		t.Errorf("Info should be enabled at threshold LevelDebug")
	}
	if !h.Enabled(nil, slog.LevelWarn) {
		t.Errorf("Warn should be enabled at threshold LevelDebug")
	}
}
