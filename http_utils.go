package main

import (
	"bytes"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net"
	gourl "net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
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
	vTRACE: "TRACE",
	vDEBUG: "DEBUG",
	vINFO:  "INFO",
}

// Optimize verbosePrint function to avoid unnecessary formatting
func verbosePrint(level int, vfmt string, args ...interface{}) {
	if *verbose > level {
		return
	}

	prefix := "[ERROR]"
	if l, ok := logLevels[level]; ok {
		prefix = "[" + l + "]"
	}

	// Avoid unnecessary Sprintf calls when there are no arguments
	if len(args) == 0 {
		fmt.Println(prefix + " " + vfmt)
	} else {
		fmt.Printf(prefix+" "+vfmt+"\n", args...)
	}
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
)

var (
	ErrInitWsClient   = errors.New("init ws client error")
	ErrInitHttpClient = errors.New("init http client error")
	ErrInitTcpClient  = errors.New("init tcp client error")
	ErrUrl            = errors.New("check url error")
)

var (
	fnSrc = rand.NewSource(time.Now().UnixNano()) // for functions
	fnMap = template.FuncMap{
		"intSum":       intSum,
		"random":       random,
		"randomDate":   randomDate,
		"randomString": randomString,
		"randomNum":    randomNum,
		"date":         date,
		"UUID":         uuid,
		"escape":       escape,
		"getEnv":       getEnv,
		"hexToString":  hexToString,
		"stringToHex":  stringToHex,
		"toString":     toString,
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

// Add a mutex to protect concurrent access to fnSrc
var fnSrcMutex sync.Mutex

// Optimize random function to ensure thread safety
func random(min, max int64) int64 {
	fnSrcMutex.Lock()
	defer fnSrcMutex.Unlock()
	return rand.Int63n(max-min) + min
}

func formatTime(now time.Time, fmt string) string {
	switch fmt {
	case "YMD":
		return now.Format("20060201")
	case "HMS":
		return now.Format("150405")
	default:
		return now.Format("20060201-150405")
	}
}

// YMD = yyyyMMdd, HMS = HHmmss, YMDHMS = yyyyMMdd-HHmmss
func date(fmt string) string {
	return formatTime(time.Now(), fmt)
}

// Optimize randomDate function to ensure thread safety
func randomDate(fmt string) string {
	fnSrcMutex.Lock()
	randomTime := time.Unix(rand.Int63n(time.Now().Unix()-94608000)+94608000, 0)
	fnSrcMutex.Unlock()
	return formatTime(randomTime, fmt)
}

func escape(u string) string {
	return gourl.QueryEscape(u)
}

// Optimize randomN function for more efficient random number generation
func randomN(n int, letter string) string {
    if n <= 0 {
        return ""
    }

    b := make([]byte, n)
    letterLen := int64(len(letter))

    fnSrcMutex.Lock()
    for i := 0; i < n; i++ {
        b[i] = letter[rand.Int63n(letterLen)%letterLen]
    }
    fnSrcMutex.Unlock()

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
		verbosePrint(vERROR, "hex decode error: %v", err)
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

// Optimize parseTime function to support more time units and better error handling
func parseTime(timeStr string) int64 {
	if len(timeStr) == 0 {
		usageAndExit("empty time string")
	}

	unit := timeStr[len(timeStr)-1]
	valueStr := timeStr
	multi := int64(1)

	switch unit {
	case 's':
		valueStr = timeStr[:len(timeStr)-1]
	case 'm':
		valueStr = timeStr[:len(timeStr)-1]
		multi = 60
	case 'h':
		valueStr = timeStr[:len(timeStr)-1]
		multi = 3600
	case 'd':
		valueStr = timeStr[:len(timeStr)-1]
		multi = 86400
	}

	// If the last character is not a valid time unit, assume seconds
	if unit >= '0' && unit <= '9' {
		valueStr = timeStr
	}

	t, err := strconv.ParseInt(valueStr, 10, 64)
	if err != nil || t <= 0 {
		usageAndExit(fmt.Sprintf("invalid duration: %s", timeStr))
	}

	return multi * t
}

var bytePool = sync.Pool{
	New: func() interface{} {
		return &bytes.Buffer{}
	},
}

// Optimize fastRead function with buffer size limit
func fastRead(r io.Reader, cycleRead bool) (int64, error) {
	buf := bytePool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bytePool.Put(buf)

	// Set maximum read size to prevent memory overflow
	const maxReadSize = 10 * 1024 * 1024 // 10MB
	
	var n int64
	var err error
	
	// Use LimitReader to restrict single read size
	limitedReader := io.LimitReader(r, maxReadSize)
	n, err = io.Copy(buf, limitedReader)
	
	if err != nil && err != io.EOF {
		return n, err
	}
	
	// If reading reaches the limit and needs to continue reading
	if n == maxReadSize && cycleRead {
		// Continue reading remaining data without saving, only calculate size
		for {
			nn, err := io.Copy(io.Discard, io.LimitReader(r, maxReadSize))
			n += nn
			if err != nil || nn < maxReadSize {
				break
			}
		}
	}

	return n, nil
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

type ConnOption struct {
	Timeout           time.Duration `json:"timeout"`
	DisableKeepAlives bool          `json:"disable_keep_alives"`
}

type tcpConn struct {
	tcpClient net.Conn
	uri       string
	option    ConnOption
	lastUsed  time.Time  // Add lastUsed field to track when the connection was last used
}

// Add a connection pool to reuse TCP connections
var tcpConnPool = sync.Pool{
	New: func() interface{} {
		return &tcpConn{}
	},
}

// DialTCP creates a new TCP connection with timeout control and connection pooling
func DialTCP(uri string, option ConnOption) (*tcpConn, error) {
	// Get TCP connection object from pool
	tcpConn := tcpConnPool.Get().(*tcpConn)
	
	// Add connection timeout control
	dialer := net.Dialer{
		Timeout: option.Timeout,
	}
	
	conn, err := dialer.Dial("tcp", uri)
	if err != nil {
		// Put the object back to the pool when connection fails
		tcpConnPool.Put(tcpConn)
		return nil, fmt.Errorf("failed to dial TCP: %w", err)
	}

	if err := conn.SetDeadline(time.Now().Add(option.Timeout)); err != nil {
		conn.Close()
		tcpConnPool.Put(tcpConn)
		return nil, fmt.Errorf("failed to set deadline: %w", err)
	}

	// Reuse connection object
	tcpConn.tcpClient = conn
	tcpConn.uri = uri
	tcpConn.option = option
	tcpConn.lastUsed = time.Now()  // Initialize lastUsed with current time
	
	return tcpConn, nil
}

// Optimize tcpConn.Do method with write timeout control
func (tcp *tcpConn) Do(body []byte) (int64, error) {
	if tcp.tcpClient == nil {
		return 0, ErrInitTcpClient
	}

	// Set write timeout
	if err := tcp.tcpClient.SetWriteDeadline(time.Now().Add(tcp.option.Timeout)); err != nil {
		return 0, fmt.Errorf("set write deadline failed: %w", err)
	}
	
	if _, err := tcp.tcpClient.Write(body); err != nil {
		return 0, fmt.Errorf("write failed: %w", err)
	}

	// Set read timeout
	if err := tcp.tcpClient.SetReadDeadline(time.Now().Add(tcp.option.Timeout)); err != nil {
		return 0, fmt.Errorf("set read deadline failed: %w", err)
	}
	
	// Update lastUsed time after successful operation
	tcp.lastUsed = time.Now()
	
	return fastRead(tcp.tcpClient, false)
}

// Optimize tcpConn.Close method with connection pool support
func (tcp *tcpConn) Close() error {
	if tcp.tcpClient == nil {
		return ErrInitTcpClient
	}

	err := tcp.tcpClient.Close()
	tcp.tcpClient = nil
	
	// Put the connection object back to the pool for reuse
	tcpConnPool.Put(tcp)
	
	if err != nil {
		return fmt.Errorf("close failed: %w", err)
	}
	
	return nil
}

// Add connection status check method
func (tcp *tcpConn) isExpired() bool {
    if tcp.tcpClient == nil {
        return true
    }
    
    if time.Since(tcp.lastUsed) > tcp.option.Timeout {
        tcp.Close()
        return true
    }
    
    return false
}

// Helper functions
func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}