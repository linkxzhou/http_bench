package templatefn

// Time-related template functions (plan.md §C-5: "timeFuncs"): current
// timestamps in various units, formatted dates, and random date generation.

import "time"

// Clock abstracts the source of "now" so tests can inject a deterministic
// clock (plan.md §C-6). Production code uses the real clock (time.Now);
// tests may call SetClock to install a fake. The package-level now variable
// is the indirection point — template functions read it instead of calling
// time.Now directly, which keeps the FuncMap signatures stable (templates
// cannot pass a clock argument).
//
// now is safe for concurrent reads; SetClock is intended for test setup
// (serial) only.
var now = func() time.Time { return time.Now() }

// SetClock replaces the package clock. Intended for testing only; calling
// it concurrently with template execution is not safe. Pass nil to restore
// the real clock.
func SetClock(c func() time.Time) {
	if c == nil {
		now = func() time.Time { return time.Now() }
		return
	}
	now = c
}

// timeFuncs returns the time subset of the template FuncMap.
func timeFuncs() map[string]interface{} {
	return map[string]interface{}{
		"timestamp":     timestamp,
		"timestampMs":   timestampMs,
		"timestampNano": timestampNano,
		"date":          date,
		"randomDate":    randomDate,
	}
}

func timestamp() int64 {
	return now().Unix()
}

func timestampMs() int64 {
	return now().UnixMilli()
}

func timestampNano() int64 {
	return now().UnixNano()
}

func date(fmt string) string {
	return formatTime(now(), fmt)
}

func randomDate(fmt string) string {
	randomTime := time.Unix(randInt63n(now().Unix()-94608000)+94608000, 0)
	return formatTime(randomTime, fmt)
}

// formatTime returns formatted time string. Supported keys:
//
//	YMD      => yyyyMMdd
//	HMS      => HHmmss
//	YMDHMS   => yyyyMMdd-HHmmss
//	YMDHMSMS => yyyyMMdd-HHmmss.fff
//	RFC3339  => ISO 8601 format
//	RFC822   => RFC822 format
//
// Any other key defaults to YMDHMS.
func formatTime(now time.Time, key string) string {
	switch key {
	case "YMD":
		return now.Format("20060102")
	case "HMS":
		return now.Format("150405")
	case "YMDHMS":
		return now.Format("20060102-150405")
	case "YMDHMSMS":
		return now.Format("20060102-150405.000")
	case "RFC3339":
		return now.Format(time.RFC3339)
	case "RFC822":
		return now.Format(time.RFC822)
	default:
		return now.Format("2006-01-02 15:04:05")
	}
}
