package templatefn

// String manipulation template functions (plan.md §C-5: "将模板函数按领域
// 拆为 stringFuncs ..."). All public template names remain unchanged for
// backward compatibility; only the internal Go file organization changes.

import (
	"encoding/hex"
	"fmt"
	gourl "net/url"
	"strings"

	"github.com/linkxzhou/http_bench/internal/logging"
)

// stringFuncs returns the string-manipulation subset of the template FuncMap.
func stringFuncs() map[string]interface{} {
	return map[string]interface{}{
		"substring":   substring,
		"replace":     replace,
		"upper":       upper,
		"lower":       lower,
		"trim":        trim,
		"escape":      escape,
		"hexToString": hexToString,
		"stringToHex": stringToHex,
		"toString":    toString,
		"join":        join,
		"split":       split,
		"contains":    contains,
		"startsWith":  startsWith,
		"endsWith":    endsWith,
		"repeat":      repeat,
		"reverse":     reverse,
		"len":         length,
		"default":     defaultValue,
		"ternary":     ternary,
	}
}

func substring(s string, start, length int) string {
	runes := []rune(s)
	if start < 0 || start >= len(runes) {
		return ""
	}
	// A non-positive length yields an empty substring rather than risking a
	// panic from an end index that precedes start.
	if length <= 0 {
		return ""
	}
	end := start + length
	if end > len(runes) {
		end = len(runes)
	}
	return string(runes[start:end])
}

func replace(s, old, new string) string {
	return strings.ReplaceAll(s, old, new)
}

func upper(s string) string {
	return strings.ToUpper(s)
}

func lower(s string) string {
	return strings.ToLower(s)
}

func trim(s string) string {
	return strings.TrimSpace(s)
}

func escape(u string) string {
	return gourl.QueryEscape(u)
}

func hexToString(hexStr string) string {
	data, err := hex.DecodeString(hexStr)
	if err != nil {
		logging.Error(0, "hex decode error: %v", err)
		return ""
	}
	return string(data)
}

func stringToHex(s string) string {
	return hex.EncodeToString([]byte(s))
}

func toString(args ...interface{}) string {
	return fmt.Sprintf(`"%v"`, args...)
}

func join(sep string, parts ...string) string {
	return strings.Join(parts, sep)
}

func split(s, sep string) []string {
	return strings.Split(s, sep)
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

func startsWith(s, prefix string) bool {
	return strings.HasPrefix(s, prefix)
}

func endsWith(s, suffix string) bool {
	return strings.HasSuffix(s, suffix)
}

func repeat(s string, n int) string {
	if n < 0 {
		n = 0
	}
	return strings.Repeat(s, n)
}

func reverse(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

func length(s string) int {
	return len([]rune(s))
}

func defaultValue(value, defaultVal string) string {
	if value == "" {
		return defaultVal
	}
	return value
}

func ternary(condition bool, trueVal, falseVal interface{}) interface{} {
	if condition {
		return trueVal
	}
	return falseVal
}
