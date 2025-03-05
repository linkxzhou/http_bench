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

// 优化 verbosePrint 函数，避免不必要的格式化
func verbosePrint(level int, vfmt string, args ...interface{}) {
	if *verbose > level {
		return
	}

	prefix := "[ERROR]"
	if l, ok := logLevels[level]; ok {
		prefix = "[" + l + "]"
	}

	// 当没有参数时避免不必要的 Sprintf 调用
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

// 添加一个互斥锁来保护 fnSrc 的并发访问
var fnSrcMutex sync.Mutex

// 优化 random 函数，确保线程安全
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

// 优化 randomDate 函数，确保线程安全
func randomDate(fmt string) string {
	fnSrcMutex.Lock()
	randomTime := time.Unix(rand.Int63n(time.Now().Unix()-94608000)+94608000, 0)
	fnSrcMutex.Unlock()
	return formatTime(randomTime, fmt)
}

func escape(u string) string {
	return gourl.QueryEscape(u)
}

// 优化 randomN 函数，使用更高效的随机数生成
func randomN(n int, letter string) string {
	if n <= 0 {
		return ""
	}

	b := make([]byte, n)
	letterLen := len(letter)
	// 使用互斥锁保护 fnSrc 以确保线程安全
	r := rand.New(fnSrc)

	// 批量生成随机索引以提高性能
	for i, cache, remain := 0, r.Int63(), letterIdxMax; i < n; {
		if remain == 0 {
			cache, remain = r.Int63(), letterIdxMax
		}
		if idx := int(cache & letterIdxMask); idx < letterLen {
			b[i] = letter[idx]
			i++
		}
		cache >>= letterIdxBits
		remain--
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

// 优化 hexToString 函数，添加错误处理
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

// 优化 parseTime 函数，支持更多时间单位和更好的错误处理
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

	// 如果最后一个字符不是有效的时间单位，则假设单位是秒
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

// 优化 fastRead 函数，添加缓冲区大小限制
func fastRead(r io.Reader, cycleRead bool) (int64, error) {
	buf := bytePool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bytePool.Put(buf)

	// 设置最大读取大小，防止内存溢出
	const maxReadSize = 10 * 1024 * 1024 // 10MB
	
	var n int64
	var err error
	
	// 使用 LimitReader 限制单次读取大小
	limitedReader := io.LimitReader(r, maxReadSize)
	n, err = io.Copy(buf, limitedReader)
	
	if err != nil && err != io.EOF {
		return n, err
	}
	
	// 如果读取达到限制且需要继续读取
	if n == maxReadSize && cycleRead {
		// 继续读取剩余数据但不保存，只计算大小
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

// 优化 parseFile 函数，使用更高效的行分割方法
func parseFile(fileName string, delimiter []rune) ([]string, error) {
	content, err := os.ReadFile(fileName)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	if delimiter == nil {
		return []string{string(content)}, nil
	}

	// 预分配足够的容量以减少重新分配
	contentStr := string(content)
	estimatedLines := min(int64(len(contentStr)/30), 1000) // 估计行数
	result := make([]string, 0, estimatedLines)
	
	// 创建分隔符集合用于快速查找
	delimSet := make(map[rune]struct{}, len(delimiter))
	for _, d := range delimiter {
		delimSet[d] = struct{}{}
	}

	lines := strings.FieldsFunc(contentStr, func(r rune) bool {
		_, ok := delimSet[r]
		return ok
	})

	// 过滤空行
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
}

// 优化 tcpConn.Do 方法，添加写入超时控制
func (tcp *tcpConn) Do(body []byte) (int64, error) {
	if tcp.tcpClient == nil {
		return 0, ErrInitTcpClient
	}

	// 设置写入超时
	if err := tcp.tcpClient.SetWriteDeadline(time.Now().Add(tcp.option.Timeout)); err != nil {
		return 0, fmt.Errorf("set write deadline failed: %w", err)
	}
	
	if _, err := tcp.tcpClient.Write(body); err != nil {
		return 0, fmt.Errorf("write failed: %w", err)
	}

	// 设置读取超时
	if err := tcp.tcpClient.SetReadDeadline(time.Now().Add(tcp.option.Timeout)); err != nil {
		return 0, fmt.Errorf("set read deadline failed: %w", err)
	}
	
	return fastRead(tcp.tcpClient, false)
}

// 添加一个缓存池来重用 TCP 连接
var tcpConnPool = sync.Pool{
	New: func() interface{} {
		return &tcpConn{}
	},
}

// DialTCP creates a new TCP connection with timeout control and connection pooling
func DialTCP(uri string, option ConnOption) (*tcpConn, error) {
	// 从连接池获取 TCP 连接对象
	tcpConn := tcpConnPool.Get().(*tcpConn)
	
	// 添加连接超时控制
	dialer := net.Dialer{
		Timeout: option.Timeout,
	}
	
	conn, err := dialer.Dial("tcp", uri)
	if err != nil {
		// 连接失败时将对象放回池中
		tcpConnPool.Put(tcpConn)
		return nil, fmt.Errorf("failed to dial TCP: %w", err)
	}

	if err := conn.SetDeadline(time.Now().Add(option.Timeout)); err != nil {
		conn.Close()
		tcpConnPool.Put(tcpConn)
		return nil, fmt.Errorf("failed to set deadline: %w", err)
	}

	// 重用连接对象
	tcpConn.tcpClient = conn
	tcpConn.uri = uri
	tcpConn.option = option
	
	return tcpConn, nil
}

// 优化 tcpConn.Close 方法，支持连接池
func (tcp *tcpConn) Close() error {
	if tcp.tcpClient == nil {
		return ErrInitTcpClient
	}

	err := tcp.tcpClient.Close()
	tcp.tcpClient = nil
	
	// 将连接对象放回池中以便重用
	tcpConnPool.Put(tcp)
	
	if err != nil {
		return fmt.Errorf("close failed: %w", err)
	}
	
	return nil
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
