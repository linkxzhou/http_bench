package main

import (
	"fmt"
	"time"
)

// Log levels for verbose output
// Lower values indicate more verbose logging
const (
	logLevelTrace = iota // Trace level: most verbose, detailed execution flow
	logLevelDebug        // Debug level: debugging information
	logLevelInfo         // Info level: general informational messages
	logLevelWarn         // Warn level: warning messages
	logLevelError        // Error level: error messages only
)

// logLevelNames maps log levels to their string representations
var logLevelNames = map[int]string{
	logLevelTrace: "TRACE",
	logLevelDebug: "DEBUG",
	logLevelInfo:  "INFO",
	logLevelWarn:  "WARN",
	logLevelError: "ERROR",
}

// verbosePrint outputs a log message if the current verbose level permits it
// Only logs messages at or above the configured verbosity level
// Uses efficient formatting to minimize allocations
func verbosePrint(level int, format string, args ...interface{}) {
	// Skip logging if verbosity level is too low
	if *verbose < level {
		return
	}

	// Get log level name, default to ERROR if unknown
	levelName, ok := logLevelNames[level]
	if !ok {
		levelName = "ERROR"
	}

	// Format timestamp once
	timestamp := time.Now().Format("2006-01-02 15:04:05")

	// Build and print log message in one call to minimize allocations
	// Format: [timestamp][LEVEL] message
	fmt.Printf("[%s][%s] "+format+"\n", append([]interface{}{timestamp, levelName}, args...)...)
}

// logTrace logs a trace-level message (most verbose)
// Only visible when verbose level >= 0
func logTrace(format string, args ...interface{}) {
	verbosePrint(logLevelTrace, format, args...)
}

// logDebug logs a debug-level message
// Only visible when verbose level >= 1
func logDebug(format string, args ...interface{}) {
	verbosePrint(logLevelDebug, format, args...)
}

// logInfo logs an info-level message
// Only visible when verbose level >= 2
func logInfo(format string, args ...interface{}) {
	verbosePrint(logLevelInfo, format, args...)
}

// logWarn logs a warning-level message
// Only visible when verbose level >= 3
func logWarn(format string, args ...interface{}) {
	verbosePrint(logLevelWarn, format, args...)
}

// logError logs an error-level message
// Only visible when verbose level >= 4
func logError(format string, args ...interface{}) {
	verbosePrint(logLevelError, format, args...)
}
