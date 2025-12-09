package main

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash"
	"math"
	"math/rand"
	gourl "net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"
)

func usageAndExit(msg string) {
	if msg != "" {
		fmt.Println(msg)
	}
	flag.Usage()
	fmt.Println("")
	os.Exit(1)
}

type flagSlice []string

func (h *flagSlice) String() string {
	return fmt.Sprintf("%s", *h)
}

func (h *flagSlice) Set(value string) error {
	*h = append(*h, value)
	return nil
}

const (
	IntMax = int(^uint(0) >> 1)
	IntMin = ^IntMax

	// String generation constants
	letterBytes    = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	letterNumBytes = "0123456789"
)

var (
	ErrInitWsClient   = errors.New("init ws client error")
	ErrInitHttpClient = errors.New("init http client error")
	ErrInitTcpClient  = errors.New("init tcp client error")
	ErrUrl            = errors.New("check url error")
)

var (
	HeaderRegexp = regexp.MustCompile(`^([\w-]+):\s*(.+)`)
	AuthRegexp   = regexp.MustCompile(`^(.+):([^\s].+)`)
)

var (
	fnMap = template.FuncMap{
		"intSum":        intSum,
		"random":        random,
		"randomDate":    randomDate,
		"randomString":  randomString,
		"randomNum":     randomNum,
		"date":          date,
		"UUID":          uuid,
		"escape":        escape,
		"getEnv":        getEnv,
		"hexToString":   hexToString,
		"stringToHex":   stringToHex,
		"toString":      toString,
		"max":           max,
		"min":           min,
		"base64Encode":  base64Encode,
		"base64Decode":  base64Decode,
		"md5":           md5Hash,
		"sha1":          sha1Hash,
		"sha256":        sha256Hash,
		"hmac":          hmacSign,
		"randomIP":      randomIP,
		"substring":     substring,
		"replace":       replace,
		"upper":         upper,
		"lower":         lower,
		"trim":          trim,
		"randomChoice":  randomChoice,
		"randomFloat":   randomFloat,
		"randomBoolean": randomBoolean,
		// JSON functions
		"jsonEncode": jsonEncode,
		"jsonDecode": jsonDecode,
		"jsonGet":    jsonGet,
		// URL functions
		"urlEncode":  urlEncode,
		"urlDecode":  urlDecode,
		"urlParse":   urlParse,
		"queryBuild": queryBuild,
		// Timestamp functions
		"timestamp":     timestamp,
		"timestampMs":   timestampMs,
		"timestampNano": timestampNano,
		// Array/String functions
		"join":       join,
		"split":      split,
		"contains":   contains,
		"startsWith": startsWith,
		"endsWith":   endsWith,
		"repeat":     repeat,
		"reverse":    reverse,
		// Math functions
		"round": round,
		"ceil":  ceil,
		"floor": floor,
		"abs":   abs,
		"pow":   pow,
		// Random data generators
		"randomEmail":      randomEmail,
		"randomPhone":      randomPhone,
		"randomUsername":   randomUsername,
		"randomUserAgent":  randomUserAgent,
		"randomHTTPMethod": randomHTTPMethod,
		"randomMAC":        randomMAC,
		"randomPort":       randomPort,
		// Utility functions
		"len":       length,
		"default":   defaultValue,
		"ternary":   ternary,
		"increment": increment,
		"decrement": decrement,
	}
	fnUUID = randomString(10)
)

// template functions
func intSum(v ...int64) int64 {
	var r int64
	for _, r1 := range v {
		r += int64(r1)
	}
	return r
}

// rnd is a thread-safe random generator
var rnd = rand.New(rand.NewSource(time.Now().UnixNano()))

// helper for locked random
func randInt63n(n int64) int64 {
	return rnd.Int63n(n)
}

func random(min, max int64) int64 {
	return randInt63n(max-min) + min
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

func date(fmt string) string {
	return formatTime(time.Now(), fmt)
}

func randomDate(fmt string) string {
	randomTime := time.Unix(randInt63n(time.Now().Unix()-94608000)+94608000, 0)
	return formatTime(randomTime, fmt)
}

func escape(u string) string {
	return gourl.QueryEscape(u)
}

func randomN(n int, letter string) string {
	if n <= 0 {
		return ""
	}

	b := make([]byte, n)
	letterLen := int64(len(letter))
	for i := 0; i < n; i++ {
		b[i] = letter[randInt63n(letterLen)]
	}

	return string(b)
}

// randomString generates a random string of length n
// using alphanumeric characters
func randomString(n int) string {
	return randomN(n, letterBytes)
}

// randomNum generates a random numeric string of length n
func randomNum(n int) string {
	return randomN(n, letterNumBytes)
}

// uuid returns a unique identifier string
func uuid() string {
	return fnUUID
}

func getEnv(key string) string {
	return os.Getenv(key)
}

// Optimize hexToString function with error handling
func hexToString(hexStr string) string {
	data, err := hex.DecodeString(hexStr)
	if err != nil {
		logError(0, "hex decode error: %v", err)
		return ""
	}
	return string(data)
}

func stringToHex(s string) string {
	data := []byte(s)
	return hex.EncodeToString(data)
}

func toString(args ...interface{}) string {
	return fmt.Sprintf(`"%v"`, args...)
}

// parseTime converts a duration string into milliseconds.
// Supported units (case-insensitive):
//
//	ms: milliseconds
//	s: seconds (default)
//	m: minutes
//	h: hours
//	d: days
//	w: weeks
//
// Examples: "100ms", "10s", "5m", "2h", "1d", "1w", or just "30" for seconds.
func parseTimeToDuration(timeStr string) time.Duration {
	s := strings.TrimSpace(timeStr)
	if s == "" {
		usageAndExit("empty duration string")
	}

	n := len(s)
	var valueStr string
	var unitDuration time.Duration

	// Check for "ms" suffix first (2 chars)
	if n > 2 && strings.ToLower(s[n-2:]) == "ms" {
		valueStr = s[:n-2]
		unitDuration = time.Millisecond
	} else {
		// Check for 1 char suffixes
		unit := strings.ToLower(s[n-1:])
		switch unit {
		case "s":
			unitDuration = time.Second
			valueStr = s[:n-1]
		case "m":
			unitDuration = time.Minute
			valueStr = s[:n-1]
		case "h":
			unitDuration = time.Hour
			valueStr = s[:n-1]
		case "d":
			unitDuration = 24 * time.Hour
			valueStr = s[:n-1]
		case "w":
			unitDuration = 7 * 24 * time.Hour
			valueStr = s[:n-1]
		default:
			// No unit suffix, default to seconds
			unitDuration = time.Second
			valueStr = s
		}
	}

	t, err := strconv.ParseInt(valueStr, 10, 64)
	if err != nil || t < 0 {
		usageAndExit(fmt.Sprintf("invalid duration: %s", timeStr))
	}

	return time.Duration(t) * unitDuration
}

func parseInputWithRegexp(input string, re *regexp.Regexp) ([]string, error) {
	matches := re.FindStringSubmatch(input)
	if len(matches) < 1 {
		return nil, fmt.Errorf("could not parse the provided input; input = %v", input)
	}
	return matches, nil
}

func genSequenceId(i int) int64 {
	return time.Now().Unix()*100 + int64(i)
}

// Helper functions
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

// Base64 encoding and decoding
func base64Encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func base64Decode(s string) string {
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		logError(0, "base64 decode error: %v", err)
		return ""
	}
	return string(data)
}

// MD5 hash
func md5Hash(s string) string {
	h := md5.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

// SHA1 hash
func sha1Hash(s string) string {
	h := sha1.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

// SHA256 hash
func sha256Hash(s string) string {
	h := sha256.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

// HMAC signature
func hmacSign(key, message, hashType string) string {
	var h func() hash.Hash
	switch strings.ToLower(hashType) {
	case "md5":
		h = md5.New
	case "sha1":
		h = sha1.New
	case "sha256":
		h = sha256.New
	default:
		h = sha256.New // Default to SHA256
	}

	mac := hmac.New(h, []byte(key))
	mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

// Random IP address
func randomIP() string {
	return fmt.Sprintf("%d.%d.%d.%d",
		randInt63n(256),
		randInt63n(256),
		randInt63n(256),
		randInt63n(256))
}

// String substring
func substring(s string, start, length int) string {
	runes := []rune(s)
	if start < 0 || start >= len(runes) {
		return ""
	}
	end := start + length
	if end > len(runes) {
		end = len(runes)
	}
	return string(runes[start:end])
}

// String replacement
func replace(s, old, new string) string {
	return strings.ReplaceAll(s, old, new)
}

// Convert to uppercase
func upper(s string) string {
	return strings.ToUpper(s)
}

// Convert to lowercase
func lower(s string) string {
	return strings.ToLower(s)
}

// Trim whitespace
func trim(s string) string {
	return strings.TrimSpace(s)
}

// Random choice from array
func randomChoice(choices ...string) string {
	if len(choices) == 0 {
		return ""
	}
	return choices[randInt63n(int64(len(choices)))]
}

// Random float number
func randomFloat(min, max float64) float64 {
	return min + rnd.Float64()*(max-min)
}

// Random boolean value
func randomBoolean() bool {
	return rnd.Intn(2) == 1
}

// ============================================================================
// JSON Functions
// ============================================================================

// jsonEncode converts a value to JSON string
func jsonEncode(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		logError(0, "json encode error: %v", err)
		return ""
	}
	return string(data)
}

// jsonDecode parses JSON string to map
func jsonDecode(s string) map[string]interface{} {
	var result map[string]interface{}
	err := json.Unmarshal([]byte(s), &result)
	if err != nil {
		logError(0, "json decode error: %v", err)
		return make(map[string]interface{})
	}
	return result
}

// jsonGet extracts value from JSON string by key path (e.g., "user.name")
func jsonGet(jsonStr, keyPath string) string {
	var data map[string]interface{}
	err := json.Unmarshal([]byte(jsonStr), &data)
	if err != nil {
		logError(0, "json parse error: %v", err)
		return ""
	}

	keys := strings.Split(keyPath, ".")
	var current interface{} = data

	for _, key := range keys {
		if m, ok := current.(map[string]interface{}); ok {
			current = m[key]
			if current == nil {
				return ""
			}
		} else {
			return ""
		}
	}

	return fmt.Sprintf("%v", current)
}

// ============================================================================
// URL Functions
// ============================================================================

// urlEncode encodes a string for use in URL
func urlEncode(s string) string {
	return gourl.QueryEscape(s)
}

// urlDecode decodes a URL-encoded string
func urlDecode(s string) string {
	decoded, err := gourl.QueryUnescape(s)
	if err != nil {
		logError(0, "url decode error: %v", err)
		return s
	}
	return decoded
}

// urlParse extracts component from URL (scheme, host, path, query, fragment)
func urlParse(urlStr, component string) string {
	u, err := gourl.Parse(urlStr)
	if err != nil {
		logError(0, "url parse error: %v", err)
		return ""
	}

	switch strings.ToLower(component) {
	case "scheme":
		return u.Scheme
	case "host":
		return u.Host
	case "hostname":
		return u.Hostname()
	case "port":
		return u.Port()
	case "path":
		return u.Path
	case "query":
		return u.RawQuery
	case "fragment":
		return u.Fragment
	default:
		return urlStr
	}
}

// queryBuild builds query string from key-value pairs
// Usage: queryBuild("key1", "value1", "key2", "value2")
func queryBuild(pairs ...string) string {
	if len(pairs)%2 != 0 {
		logError(0, "queryBuild requires even number of arguments")
		return ""
	}

	values := gourl.Values{}
	for i := 0; i < len(pairs); i += 2 {
		values.Add(pairs[i], pairs[i+1])
	}
	return values.Encode()
}

// ============================================================================
// Timestamp Functions
// ============================================================================

// timestamp returns current Unix timestamp in seconds
func timestamp() int64 {
	return time.Now().Unix()
}

// timestampMs returns current Unix timestamp in milliseconds
func timestampMs() int64 {
	return time.Now().UnixMilli()
}

// timestampNano returns current Unix timestamp in nanoseconds
func timestampNano() int64 {
	return time.Now().UnixNano()
}

// ============================================================================
// Array/String Functions
// ============================================================================

// join concatenates strings with separator
func join(sep string, parts ...string) string {
	return strings.Join(parts, sep)
}

// split splits string by separator
func split(s, sep string) []string {
	return strings.Split(s, sep)
}

// contains checks if string contains substring
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}

// startsWith checks if string starts with prefix
func startsWith(s, prefix string) bool {
	return strings.HasPrefix(s, prefix)
}

// endsWith checks if string ends with suffix
func endsWith(s, suffix string) bool {
	return strings.HasSuffix(s, suffix)
}

// repeat repeats string n times
func repeat(s string, n int) string {
	if n < 0 {
		n = 0
	}
	return strings.Repeat(s, n)
}

// reverse reverses a string
func reverse(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

// ============================================================================
// Math Functions
// ============================================================================

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

// ============================================================================
// Random Data Generators
// ============================================================================

// randomEmail generates a random email address
func randomEmail() string {
	domains := []string{"gmail.com", "yahoo.com", "outlook.com", "hotmail.com", "example.com"}
	username := randomString(8)
	domain := domains[randInt63n(int64(len(domains)))]
	return fmt.Sprintf("%s@%s", username, domain)
}

// randomPhone generates a random phone number (format: +1-XXX-XXX-XXXX)
func randomPhone() string {
	return fmt.Sprintf("+1-%s-%s-%s",
		randomNum(3),
		randomNum(3),
		randomNum(4))
}

// randomUsername generates a random username
func randomUsername() string {
	prefixes := []string{"user", "test", "demo", "admin", "guest"}
	prefix := prefixes[randInt63n(int64(len(prefixes)))]
	return fmt.Sprintf("%s_%s", prefix, randomNum(6))
}

// randomUserAgent generates a random User-Agent string
func randomUserAgent() string {
	userAgents := []string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Safari/605.1.15",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.1 Mobile/15E148 Safari/604.1",
	}
	return userAgents[randInt63n(int64(len(userAgents)))]
}

// randomHTTPMethod generates a random HTTP method
func randomHTTPMethod() string {
	methods := []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"}
	return methods[randInt63n(int64(len(methods)))]
}

// randomMAC generates a random MAC address
func randomMAC() string {
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x",
		randInt63n(256), randInt63n(256), randInt63n(256),
		randInt63n(256), randInt63n(256), randInt63n(256))
}

// randomPort generates a random port number (1024-65535)
func randomPort() int64 {
	return randInt63n(64512) + 1024
}

// ============================================================================
// Utility Functions
// ============================================================================

// length returns the length of a string
func length(s string) int {
	return len([]rune(s))
}

// defaultValue returns default if value is empty
func defaultValue(value, defaultVal string) string {
	if value == "" {
		return defaultVal
	}
	return value
}

// ternary returns trueVal if condition is true, otherwise falseVal
func ternary(condition bool, trueVal, falseVal interface{}) interface{} {
	if condition {
		return trueVal
	}
	return falseVal
}

// increment adds 1 to a number
func increment(n int64) int64 {
	return n + 1
}

// decrement subtracts 1 from a number
func decrement(n int64) int64 {
	return n - 1
}

const (
	KB = 1 << 10
	MB = 1 << 20
	GB = 1 << 30
)

// toByteSizeStr converts bytes to human-readable string
func toByteSizeStr(size float64) string {
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
