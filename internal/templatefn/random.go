package templatefn

// Random-data template functions (plan.md §C-5: "randomFuncs"): numeric,
// string, boolean, and domain-specific random generators (email, phone, IP,
// MAC, port, user agent, etc.). All draw from the package-level thread-safe
// source defined in funcs.go (rnd / randInt63n / randFloat64).

import (
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// randomFuncs returns the random-data subset of the template FuncMap.
func randomFuncs() map[string]interface{} {
	return map[string]interface{}{
		"random":           random,
		"randomString":     randomString,
		"randomNum":        randomNum,
		"randomChoice":     randomChoice,
		"randomFloat":      randomFloat,
		"randomBoolean":    randomBoolean,
		"randomIP":         randomIP,
		"randomEmail":      randomEmail,
		"randomPhone":      randomPhone,
		"randomUsername":   randomUsername,
		"randomUserAgent":  randomUserAgent,
		"randomHTTPMethod": randomHTTPMethod,
		"randomMAC":        randomMAC,
		"randomPort":       randomPort,
	}
}

// RandomSource abstracts the pseudo-random number generator so tests can
// inject a deterministic source (plan.md §C-6). The default implementation
// is a thread-safe *math/rand.Rand seeded from the wall clock; tests may call
// SetRandomSource to install a fake. Like the clock, this indirection keeps
// the FuncMap signatures stable — templates cannot pass an RNG argument.
//
// The default source is safe for concurrent use (guarded by rndMu).
// SetRandomSource is intended for test setup (serial) only.
type RandomSource interface {
	Int63n(n int64) int64
	Float64() float64
}

// rnd is the default thread-safe random generator.
var (
	rnd   = rand.New(rand.NewSource(time.Now().UnixNano()))
	rndMu sync.Mutex
)

// defaultRandomSource adapts the package-level rnd (with its mutex) to the
// RandomSource interface. It is the production source.
type defaultRandomSource struct{}

func (defaultRandomSource) Int63n(n int64) int64 {
	rndMu.Lock()
	defer rndMu.Unlock()
	return rnd.Int63n(n)
}

func (defaultRandomSource) Float64() float64 {
	rndMu.Lock()
	defer rndMu.Unlock()
	return rnd.Float64()
}

// randomSource is the indirection point. Template functions read it instead
// of touching rnd directly. Safe for concurrent reads; replace via
// SetRandomSource (test setup, serial).
var randomSource RandomSource = defaultRandomSource{}

// SetRandomSource replaces the package random source. Intended for testing
// only; calling it concurrently with template execution is not safe. Pass
// nil to restore the default (concurrent-safe) source.
func SetRandomSource(rs RandomSource) {
	if rs == nil {
		randomSource = defaultRandomSource{}
		return
	}
	randomSource = rs
}

// randInt63n returns a non-negative pseudo-random 63-bit integer in [0, n).
// Thread-safe under the default source.
func randInt63n(n int64) int64 {
	return randomSource.Int63n(n)
}

// randFloat64 returns a pseudo-random float64 in [0.0,1.0).
// Thread-safe under the default source.
func randFloat64() float64 {
	return randomSource.Float64()
}

func random(min, max int64) int64 {
	// Guard against max <= min to avoid panic in randInt63n (which requires
	// n > 0). When the range is empty or inverted, return min as a safe
	// deterministic fallback so templates never crash the worker.
	if max <= min {
		return min
	}
	return randInt63n(max-min) + min
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

// Random choice from array
func randomChoice(choices ...string) string {
	if len(choices) == 0 {
		return ""
	}
	return choices[randInt63n(int64(len(choices)))]
}

// Random float number
func randomFloat(min, max float64) float64 {
	return min + randFloat64()*(max-min)
}

// Random boolean value
func randomBoolean() bool {
	return randInt63n(2) == 1
}

// Random IP address
func randomIP() string {
	return fmt.Sprintf("%d.%d.%d.%d",
		randInt63n(256),
		randInt63n(256),
		randInt63n(256),
		randInt63n(256))
}

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
