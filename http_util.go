package main

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"hash"
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

var logLevels = map[int]string{
	logLevelTrace: "TRACE",
	logLevelDebug: "DEBUG",
	logLevelInfo:  "INFO",
}

const (
	IntMax = int(^uint(0) >> 1)
	IntMin = ^IntMax

	// String generation constants
	letterIdxBits  = 6                    // 6 bits to represent a letter index
	letterIdxMask  = 1<<letterIdxBits - 1 // All 1-bits, as many as letterIdxBits
	letterIdxMax   = 63 / letterIdxBits   // # of letter indices fitting in 63 bits
	letterBytes    = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	letterNumBytes = "0123456789"

	// HTTP related constants
	httpContentTypeJSON = "application/json"
	httpWorkerApiPath   = "/api"

	// HTTP worker constants
	defaultWorkerTimeout = 10000
)

var (
	ErrInitWsClient   = errors.New("init ws client error")
	ErrInitHttpClient = errors.New("init http client error")
	ErrInitTcpClient  = errors.New("init tcp client error")
	ErrUrl            = errors.New("check url error")
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
		verbosePrint(logLevelError, "hex decode error: %v", err)
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

// parseTime converts a duration string into seconds.
// Supported units (case-insensitive):
//
//	s: seconds (default)
//	m: minutes
//	h: hours
//	d: days
//	w: weeks
//
// e.g. "10s", "5m", "2h", "1d", "1w", or just "30" for seconds.
func parseTime(timeStr string) int64 {
	s := strings.TrimSpace(timeStr)
	if s == "" {
		usageAndExit("empty duration string")
	}
	// unit multipliers in seconds
	units := map[string]int64{
		"s": 1,
		"m": 60,
		"h": 3600,
		"d": 86400,
		"w": 604800,
	}
	// split numeric part and unit suffix
	n := len(s)
	unit := strings.ToLower(s[n-1:])
	multiplier, ok := units[unit]
	valueStr := s
	if ok {
		valueStr = s[:n-1]
	} else {
		multiplier = 1
	}
	t, err := strconv.ParseInt(valueStr, 10, 64)
	if err != nil || t < 0 {
		usageAndExit(fmt.Sprintf("invalid duration: %s", timeStr))
	}
	return t * multiplier * 1000
}

func parseInputWithRegexp(input, regx string) ([]string, error) {
	re := regexp.MustCompile(regx)
	matches := re.FindStringSubmatch(input)
	if len(matches) < 1 {
		return nil, fmt.Errorf("could not parse the provided input; input = %v", input)
	}
	return matches, nil
}

// Optimize parseFile function with more efficient line splitting
func parseFile(fileName string, delimiter []rune) ([]string, error) {
	content, err := os.ReadFile(fileName)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	if delimiter == nil {
		return []string{string(content)}, nil
	}

	// Pre-allocate sufficient capacity to reduce reallocation
	contentStr := string(content)
	estimatedLines := min(int64(len(contentStr)/30), 1000) // Estimate line count
	result := make([]string, 0, estimatedLines)

	// Create delimiter set for quick lookup
	delimSet := make(map[rune]struct{}, len(delimiter))
	for _, d := range delimiter {
		delimSet[d] = struct{}{}
	}

	lines := strings.FieldsFunc(contentStr, func(r rune) bool {
		_, ok := delimSet[r]
		return ok
	})

	// Filter empty lines
	for _, line := range lines {
		if line != "" {
			result = append(result, line)
		}
	}

	return result, nil
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

func println(vfmt string, args ...interface{}) {
	fmt.Printf(vfmt+"\n", args...)
}

// Optimize verbosePrint function to avoid unnecessary formatting
func verbosePrint(level int, vfmt string, args ...interface{}) {
	if *verbose > level {
		return
	}

	ts := time.Now().Format("2006-01-02 15:04:05")
	prefix := "[%s][ERROR]"
	if l, ok := logLevels[level]; ok {
		prefix = "[%s][" + l + "]"
	}

	// Avoid unnecessary Sprintf calls when there are no arguments
	fmt.Printf(prefix+" "+vfmt+"\n", append([]interface{}{ts}, args...)...)
}

// Base64 encoding and decoding
func base64Encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func base64Decode(s string) string {
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		verbosePrint(logLevelError, "base64 decode error: %v", err)
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
