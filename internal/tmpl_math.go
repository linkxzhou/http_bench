/*
 * tmpl_math.go — 数学模板函数（mathFuncs）
 *
 *   intSum / max / min / round / ceil / floor / abs / pow
 *   increment / decrement
 *   toByteSizeStr — 字节 → 人类可读（KB/MB/GB 常量在 util.go）
 *
 *   const IntMax / IntMin — int 边界值（metrics.go 初始化哨兵也用）
 *
 * 依赖：util.go (KB/MB/GB)
 * 被依赖：templatefn.go → FnMap, metrics.go (IntMax/IntMin)
 */

package bench

import (
	"fmt"
	"math"
)

const (
	IntMax = int(^uint(0) >> 1)
	IntMin = ^IntMax
)

// mathFuncs returns the math subset of the template FuncMap.
func mathFuncs() map[string]interface{} {
	return map[string]interface{}{
		"max":       max,
		"min":       min,
		"intSum":    intSum,
		"round":     round,
		"ceil":      ceil,
		"floor":     floor,
		"abs":       abs,
		"pow":       pow,
		"increment": increment,
		"decrement": decrement,
	}
}

func intSum(v ...int64) int64 {
	var r int64
	for _, r1 := range v {
		r += int64(r1)
	}
	return r
}

func max(a int64, bList ...int64) int64 {
	maxValue := a
	for _, bValue := range bList {
		if maxValue < bValue {
			maxValue = bValue
		}
	}
	return maxValue
}

func min(a int64, bList ...int64) int64 {
	minValue := a
	for _, bValue := range bList {
		if minValue > bValue {
			minValue = bValue
		}
	}
	return minValue
}

// round rounds a float to nearest integer
func round(f float64) int64 {
	return int64(math.Round(f))
}

// ceil returns the smallest integer >= f
func ceil(f float64) int64 {
	return int64(math.Ceil(f))
}

// floor returns the largest integer <= f
func floor(f float64) int64 {
	return int64(math.Floor(f))
}

// abs returns absolute value
func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// pow returns base^exponent
func pow(base, exponent float64) float64 {
	return math.Pow(base, exponent)
}

// increment adds 1 to a number
func increment(n int64) int64 {
	return n + 1
}

// decrement subtracts 1 from a number
func decrement(n int64) int64 {
	return n - 1
}

// tmplToByteSizeStr converts bytes to human-readable string. Registered in
// mathFuncs when needed; currently an internal helper kept for parity with
// the legacy template surface.
func tmplToByteSizeStr(size float64) string {
	switch {
	case size >= GB:
		return fmt.Sprintf("%.3f GB", size/GB)
	case size >= MB:
		return fmt.Sprintf("%.3f MB", size/MB)
	case size >= KB:
		return fmt.Sprintf("%.3f KB", size/KB)
	default:
		return fmt.Sprintf("%.0f bytes", size)
	}
}
