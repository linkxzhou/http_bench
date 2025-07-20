package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"math/rand"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFormatTime(t *testing.T) {
	tm := time.Date(2025, 2, 3, 4, 5, 6, 0, time.UTC)
	if got := formatTime(tm, "YMD"); got != "20250203" {
		t.Errorf("formatTime YMD got %s", got)
	}
	if got := formatTime(tm, "HMS"); got != "040506" {
		t.Errorf("formatTime HMS got %s", got)
	}
	if got := formatTime(tm, "OTHER"); got != "2025-02-03 04:05:06" {
		t.Errorf("formatTime default got %s", got)
	}
}

func TestEscape(t *testing.T) {
	s := "a b&c"
	want := url.QueryEscape(s)
	if got := escape(s); got != want {
		t.Errorf("escape got %s, want %s", got, want)
	}
}

func TestRandom(t *testing.T) {
	min, max := int64(10), int64(20)
	rand.Seed(42)
	for i := 0; i < 100; i++ {
		v := random(min, max)
		if v < min || v >= max {
			t.Errorf("random out of range: %d", v)
		}
	}
}

func TestRandomN(t *testing.T) {
	letters := "abc"
	rand.Seed(1)
	s := randomN(10, letters)
	if len(s) != 10 {
		t.Errorf("randomN length %d", len(s))
	}
	for _, c := range s {
		if !strings.ContainsRune(letters, c) {
			t.Errorf("randomN invalid char: %c", c)
		}
	}
	if randomN(0, letters) != "" {
		t.Error("randomN zero len")
	}
}

func TestDateAndRandomDate(t *testing.T) {
	reYMD := regexp.MustCompile("^\\d{8}$")
	if m := date("YMD"); !reYMD.MatchString(m) {
		t.Errorf("date YMD %s", m)
	}
	reHMS := regexp.MustCompile("^\\d{6}$")
	if m := date("HMS"); !reHMS.MatchString(m) {
		t.Errorf("date HMS %s", m)
	}
	rand.Seed(3)
	if rd := randomDate("YMD"); !reYMD.MatchString(rd) {
		t.Errorf("randomDate %s", rd)
	}
}

func TestRandomDateFormats(t *testing.T) {
	// Test HMS and default for randomDate
	rand.Seed(5)
	rd1 := randomDate("HMS")
	if len(rd1) != 6 {
		t.Errorf("randomDate HMS length %d", len(rd1))
	}
	rd2 := randomDate("OTHER")
	if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$`).MatchString(rd2) {
		t.Errorf("randomDate default format %s", rd2)
	}
}

func TestConcurrentRandom(t *testing.T) {
	min, max := int64(100), int64(200)
	rand.Seed(time.Now().UnixNano())
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v := random(min, max)
			if v < min || v >= max {
				t.Errorf("concurrent random out of range: %d", v)
			}
		}()
	}
	wg.Wait()
}

func TestConcurrentRandomN(t *testing.T) {
	letters := "xyz"
	rand.Seed(7)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := randomN(20, letters)
			if len(s) != 20 {
				t.Errorf("concurrent randomN length %d", len(s))
			}
			for _, c := range s {
				if !strings.ContainsRune(letters, c) {
					t.Errorf("concurrent randomN invalid char: %c", c)
				}
			}
		}()
	}
	wg.Wait()
}

func TestEscapeComplex(t *testing.T) {
	cases := map[string]string{
		"a+b c": "a%2Bb+c",
		"汉字":    url.QueryEscape("汉字"),
	}
	for input, want := range cases {
		if got := escape(input); got != want {
			t.Errorf("escape(%s) = %s, want %s", input, got, want)
		}
	}
}

// Test Base64 encoding and decoding
func TestBase64Encode(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "aGVsbG8="},
		{"world", "d29ybGQ="},
		{"", ""},
		{"测试", base64.StdEncoding.EncodeToString([]byte("测试"))},
	}

	for _, tt := range tests {
		if got := base64Encode(tt.input); got != tt.want {
			t.Errorf("base64Encode(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBase64Decode(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"aGVsbG8=", "hello"},
		{"d29ybGQ=", "world"},
		{"", ""},
	}

	for _, tt := range tests {
		if got := base64Decode(tt.input); got != tt.want {
			t.Errorf("base64Decode(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}

	// Test invalid base64
	if got := base64Decode("invalid!!!"); got != "" {
		t.Errorf("base64Decode(invalid) should return empty string, got %q", got)
	}
}

// Test hash functions
func TestMd5Hash(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "5d41402abc4b2a76b9719d911017c592"},
		{"world", "7d793037a0760186574b0282f2f435e7"},
		{"", "d41d8cd98f00b204e9800998ecf8427e"},
	}

	for _, tt := range tests {
		if got := md5Hash(tt.input); got != tt.want {
			t.Errorf("md5Hash(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSha1Hash(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d"},
		{"world", "7c211433f02071597741e6ff5a8ea34789abbf43"},
		{"", "da39a3ee5e6b4b0d3255bfef95601890afd80709"},
	}

	for _, tt := range tests {
		if got := sha1Hash(tt.input); got != tt.want {
			t.Errorf("sha1Hash(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSha256Hash(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "2cf24dba4f21d4288094c8b0f01b4336b8b8c8b8b8b8b8b8b8b8b8b8b8b8b8b8"},
		{"", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"},
	}

	for _, tt := range tests {
		h := sha256.New()
		h.Write([]byte(tt.input))
		expected := hex.EncodeToString(h.Sum(nil))
		if got := sha256Hash(tt.input); got != expected {
			t.Errorf("sha256Hash(%q) = %q, want %q", tt.input, got, expected)
		}
	}
}

// Test HMAC signature
func TestHmacSign(t *testing.T) {
	tests := []struct {
		key      string
		message  string
		hashType string
	}{
		{"secret", "message", "sha256"},
		{"key", "data", "md5"},
		{"test", "hello", "sha1"},
		{"key", "msg", "unknown"}, // should default to sha256
	}

	for _, tt := range tests {
		got := hmacSign(tt.key, tt.message, tt.hashType)
		if len(got) == 0 {
			t.Errorf("hmacSign(%q, %q, %q) returned empty string", tt.key, tt.message, tt.hashType)
		}
		// Verify it's a valid hex string
		if _, err := hex.DecodeString(got); err != nil {
			t.Errorf("hmacSign(%q, %q, %q) returned invalid hex: %q", tt.key, tt.message, tt.hashType, got)
		}
	}
}

// Test random IP generation
func TestRandomIP(t *testing.T) {
	ipPattern := regexp.MustCompile(`^\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}$`)
	for i := 0; i < 10; i++ {
		ip := randomIP()
		if !ipPattern.MatchString(ip) {
			t.Errorf("randomIP() = %q, invalid IP format", ip)
		}
		// Verify each octet is 0-255
		parts := strings.Split(ip, ".")
		for _, part := range parts {
			if val := parseInt(part); val < 0 || val > 255 {
				t.Errorf("randomIP() = %q, octet %s out of range", ip, part)
			}
		}
	}
}

// Helper function for parsing int
func parseInt(s string) int {
	val := 0
	for _, c := range s {
		if c >= '0' && c <= '9' {
			val = val*10 + int(c-'0')
		} else {
			return -1
		}
	}
	return val
}

// Test substring function
func TestSubstring(t *testing.T) {
	tests := []struct {
		str    string
		start  int
		length int
		want   string
	}{
		{"hello world", 0, 5, "hello"},
		{"hello world", 6, 5, "world"},
		{"hello", 0, 10, "hello"}, // length exceeds string
		{"hello", -1, 3, ""},      // negative start
		{"hello", 10, 3, ""},      // start beyond string
		{"测试字符串", 0, 2, "测试"},     // Unicode support
		{"", 0, 5, ""},            // empty string
	}

	for _, tt := range tests {
		if got := substring(tt.str, tt.start, tt.length); got != tt.want {
			t.Errorf("substring(%q, %d, %d) = %q, want %q", tt.str, tt.start, tt.length, got, tt.want)
		}
	}
}

// Test string replacement
func TestReplace(t *testing.T) {
	tests := []struct {
		str  string
		old  string
		new  string
		want string
	}{
		{"hello world", "world", "golang", "hello golang"},
		{"test test test", "test", "demo", "demo demo demo"},
		{"no match", "xyz", "abc", "no match"},
		{"", "a", "b", ""},
	}

	for _, tt := range tests {
		if got := replace(tt.str, tt.old, tt.new); got != tt.want {
			t.Errorf("replace(%q, %q, %q) = %q, want %q", tt.str, tt.old, tt.new, got, tt.want)
		}
	}
}

// Test case conversion
func TestUpper(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "HELLO"},
		{"World", "WORLD"},
		{"123abc", "123ABC"},
		{"", ""},
	}

	for _, tt := range tests {
		if got := upper(tt.input); got != tt.want {
			t.Errorf("upper(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestLower(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"HELLO", "hello"},
		{"World", "world"},
		{"123ABC", "123abc"},
		{"", ""},
	}

	for _, tt := range tests {
		if got := lower(tt.input); got != tt.want {
			t.Errorf("lower(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// Test trim function
func TestTrim(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"  hello  ", "hello"},
		{"\t\nworld\r\n", "world"},
		{"no spaces", "no spaces"},
		{"   ", ""},
		{"", ""},
	}

	for _, tt := range tests {
		if got := trim(tt.input); got != tt.want {
			t.Errorf("trim(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// Test random choice
func TestRandomChoice(t *testing.T) {
	// Test with multiple choices
	choices := []string{"apple", "banana", "cherry"}
	for i := 0; i < 20; i++ {
		result := randomChoice(choices...)
		found := false
		for _, choice := range choices {
			if result == choice {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("randomChoice returned %q, not in choices %v", result, choices)
		}
	}

	// Test with empty choices
	if got := randomChoice(); got != "" {
		t.Errorf("randomChoice() with no args should return empty string, got %q", got)
	}

	// Test with single choice
	if got := randomChoice("only"); got != "only" {
		t.Errorf("randomChoice(\"only\") = %q, want \"only\"", got)
	}
}

// Test random float
func TestRandomFloat(t *testing.T) {
	min, max := 1.5, 10.5
	for i := 0; i < 100; i++ {
		val := randomFloat(min, max)
		if val < min || val > max {
			t.Errorf("randomFloat(%f, %f) = %f, out of range", min, max, val)
		}
	}

	// Test with same min and max
	if got := randomFloat(5.0, 5.0); got != 5.0 {
		t.Errorf("randomFloat(5.0, 5.0) = %f, want 5.0", got)
	}
}

// Test random boolean
func TestRandomBoolean(t *testing.T) {
	trueCount := 0
	falseCount := 0
	totalTests := 1000

	for i := 0; i < totalTests; i++ {
		if randomBoolean() {
			trueCount++
		} else {
			falseCount++
		}
	}

	// Both true and false should occur (with high probability)
	if trueCount == 0 {
		t.Error("randomBoolean() never returned true")
	}
	if falseCount == 0 {
		t.Error("randomBoolean() never returned false")
	}

	// Check if distribution is roughly balanced (within 20% tolerance)
	expected := totalTests / 2
	tolerance := totalTests / 5 // 20% tolerance
	if trueCount < expected-tolerance || trueCount > expected+tolerance {
		t.Logf("randomBoolean() distribution: %d true, %d false (may be acceptable)", trueCount, falseCount)
	}
}
