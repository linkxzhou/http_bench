// Package logging provides leveled logging with race-free verbosity control.
//
// Levels: Trace (0) < Debug (1) < Info (2) < Warn (3) < Error (4). Lower
// numeric level = more verbose. A message at level L is printed only when
// the current threshold is <= L. The threshold is stored in an atomic.Int32
// so concurrent readers (collectors, request workers) cannot race with the
// process startup that adjusts it via -verbose.
//
// Two API surfaces are supported:
//   - legacy leveled functions: Trace/Debug/Info/Warn/Error (callers pass
//     a sequence id explicitly; output goes to os.Stdout);
//   - a *slog.Logger adapter (NewSlog) so dependents that consume the
//     standard library's structured logger can use this package transparently.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Log levels. Lower numeric value = more verbose.
const (
	LevelTrace = iota
	LevelDebug
	LevelInfo
	LevelWarn
	LevelError
)

// LevelNames maps log levels to their string representations.
var LevelNames = map[int]string{
	LevelTrace: "TRACE",
	LevelDebug: "DEBUG",
	LevelInfo:  "INFO",
	LevelWarn:  "WARN",
	LevelError: "ERROR",
}

var verboseLevel atomic.Int32

// SetLevel publishes a new verbosity level for concurrent readers.
func SetLevel(level int) { verboseLevel.Store(int32(level)) }

// GetLevel returns the current verbosity level.
func GetLevel() int { return int(verboseLevel.Load()) }

// SetOutput swaps the destination writer used by the leveled API. Defaults
// to os.Stdout. Tests can redirect to a buffer.
func SetOutput(w io.Writer) {
	if w == nil {
		w = os.Stdout
	}
	output = newSyncBuffer(w)
}

var output io.Writer = newSyncBuffer(os.Stdout)

type syncBuffer struct {
	mu  sync.Mutex
	buf io.Writer
}

// newSyncBuffer returns a *syncBuffer to ensure the mutex is shared between
// all writers. A value-receiver wrapper would not serialise writes.
func newSyncBuffer(w io.Writer) *syncBuffer { return &syncBuffer{buf: w} }

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

// print writes a single log line if the current threshold permits it.
func print(level int, seqId int64, format string, args ...interface{}) {
	if verboseLevel.Load() > int32(level) {
		return
	}
	name, ok := LevelNames[level]
	if !ok {
		name = "ERROR"
	}
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	fmt.Fprintf(output, "[%s][%s][%d] "+format+"\n", append([]interface{}{timestamp, name, seqId}, args...)...)
}

// Trace logs a trace-level message (most verbose).
func Trace(seqId int64, format string, args ...interface{}) {
	print(LevelTrace, seqId, format, args...)
}

// Debug logs a debug-level message.
func Debug(seqId int64, format string, args ...interface{}) {
	print(LevelDebug, seqId, format, args...)
}

// Info logs an info-level message.
func Info(seqId int64, format string, args ...interface{}) { print(LevelInfo, seqId, format, args...) }

// Warn logs a warning-level message.
func Warn(seqId int64, format string, args ...interface{}) { print(LevelWarn, seqId, format, args...) }

// Error logs an error message.
func Error(seqId int64, format string, args ...interface{}) {
	print(LevelError, seqId, format, args...)
}

// Slog returns a *slog.Logger that wraps the legacy leveled API. The
// returned logger emits through the same writer/level filter as the legacy
// functions, so installing it does not change runtime behavior.
func Slog() *slog.Logger { return slog.New(newSlogHandler()) }

type slogHandler struct{ level slog.Level }

func newSlogHandler() *slogHandler { return &slogHandler{level: slog.LevelInfo} }

func (h *slogHandler) Enabled(_ context.Context, l slog.Level) bool {
	threshold := GetLevel()
	if l >= slog.LevelError {
		return threshold <= LevelError
	}
	if l >= slog.LevelWarn {
		return threshold <= LevelWarn
	}
	if l >= slog.LevelInfo {
		return threshold <= LevelInfo
	}
	return threshold <= LevelDebug
}

func (h *slogHandler) Handle(_ context.Context, r slog.Record) error {
	level, ok := LevelNames[mapSlogLevel(r.Level)]
	if !ok {
		level = "INFO"
	}
	fmt.Fprintf(output, "[%s][%s][seq=0] %s\n", time.Now().Format("2006-01-02 15:04:05"), level, r.Message)
	return nil
}

func (h *slogHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }
func (h *slogHandler) WithGroup(name string) slog.Handler       { return h }

func mapSlogLevel(l slog.Level) int {
	switch {
	case l >= slog.LevelError:
		return LevelError
	case l >= slog.LevelWarn:
		return LevelWarn
	case l >= slog.LevelInfo:
		return LevelInfo
	default:
		return LevelDebug
	}
}
