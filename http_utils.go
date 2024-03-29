package main

import (
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
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

func verbosePrint(level int, vfmt string, args ...interface{}) {
	if *verbose > level {
		return
	}

	switch level {
	case vTRACE:
		println("[TRACE] "+vfmt, args...)
	case vDEBUG:
		println("[DEBUG] "+vfmt, args...)
	case vINFO:
		println("[INFO] "+vfmt, args...)
	default:
		println("[ERROR] "+vfmt, args...)
	}
}

const (
	IntMax = int(^uint(0) >> 1)
	IntMin = ^IntMax
)

const (
	letterIdxBits  = 6                    // 6 bits to represent a letter index
	letterIdxMask  = 1<<letterIdxBits - 1 // All 1-bits, as many as letterIdxBits
	letterIdxMax   = 63 / letterIdxBits   // # of letter indices fitting in 63 bits
	letterBytes    = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	letterNumBytes = "0123456789"

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

func random(min, max int64) int64 {
	rand.Seed(time.Now().UnixNano())
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

func randomDate(fmt string) string {
	return formatTime(time.Unix(rand.Int63n(time.Now().Unix()-94608000)+94608000, 0), fmt)
}

func escape(u string) string {
	return gourl.QueryEscape(u)
}

func randomN(n int, letter string) string {
	b := make([]byte, n)
	for i, cache, remain := n-1, fnSrc.Int63(), letterIdxMax; i >= 0; {
		if remain == 0 {
			cache, remain = fnSrc.Int63(), letterIdxMax
		}
		if idx := int(cache & letterIdxMask); idx < len(letter) {
			b[i] = letter[idx]
			i--
		}
		cache >>= letterIdxBits
		remain--
	}
	return string(b)
}

func randomString(n int) string {
	return randomN(n, letterBytes)
}

func randomNum(n int) string {
	return randomN(n, letterNumBytes)
}

func uuid() string {
	return fnUUID
}

func getEnv(key string) string {
	return os.Getenv(key)
}

func hexToString(hexStr string) string {
	data, _ := hex.DecodeString(hexStr)
	return string(data)
}

func stringToHex(s string) string {
	data := []byte(s)
	return hex.EncodeToString(data)
}

func toString(args ...interface{}) string {
	return fmt.Sprintf(`"%v"`, args...)
}

func parseTime(timeStr string) int64 {
	var multi int64 = 1
	if timeStrLen := len(timeStr) - 1; timeStrLen > 0 {
		switch timeStr[timeStrLen] {
		case 's':
			timeStr = timeStr[:timeStrLen]
		case 'm':
			timeStr = timeStr[:timeStrLen]
			multi = 60
		case 'h':
			timeStr = timeStr[:timeStrLen]
			multi = 3600
		}
	}

	t, err := strconv.ParseInt(timeStr, 10, 64)
	if err != nil || t <= 0 {
		usageAndExit("Duration parse err: " + err.Error())
	}

	return multi * t
}

type byteBlock struct {
	block []byte
	cap   int
}

var bytePool = sync.Pool{
	New: func() interface{} {
		return &byteBlock{
			block: make([]byte, 0, 10240),
			cap:   10240,
		}
	},
}

// the purpose of this function is to reduce processing
func fastRead(r io.Reader, cycleRead bool) (int64, error) {
	var (
		n     = int64(0)
		b     = bytePool.Get().(*byteBlock)
		bsize int
		err   error
	)

	defer bytePool.Put(b)

	for {
		bsize, err = r.Read(b.block[0:b.cap])
		verbosePrint(vDEBUG, "fastRead: %v, bsize: %v", b, bsize)
		if err != nil {
			if err == io.EOF {
				err = nil
			} else {
				return n, err
			}
		}
		n += int64(bsize)

		// TODO: cycleRead isn't support
		if !cycleRead || bsize == 0 {
			return n, err
		}
	}
}

func parseInputWithRegexp(input, regx string) ([]string, error) {
	re := regexp.MustCompile(regx)
	matches := re.FindStringSubmatch(input)
	if len(matches) < 1 {
		return nil, fmt.Errorf("could not parse the provided input; input = %v", input)
	}
	return matches, nil
}

func parseFile(fileName string, delimiter []rune) ([]string, error) {
	var contentList []string
	file, err := os.Open(fileName)
	if err != nil {
		return contentList, err
	}

	defer file.Close()

	content, err := ioutil.ReadAll(file)
	if err != nil {
		return contentList, err
	}

	if delimiter == nil {
		return []string{string(content)}, nil
	}

	lines := strings.FieldsFunc(string(content), func(r rune) bool {
		for _, v := range delimiter {
			if r == v {
				return true
			}
		}
		return false
	})

	for _, line := range lines {
		if len(line) > 0 {
			contentList = append(contentList, line)
		}
	}

	return contentList, nil
}

type ConnOption struct {
	timeout           time.Duration
	disableKeepAlives bool
}

type tcpConn struct {
	tcpClient net.Conn
	uri       string
	option    ConnOption
}

func DialTCP(uri string, option ConnOption) (*tcpConn, error) {
	conn, err := net.Dial("tcp", uri)
	if err != nil {
		verbosePrint(vERROR, "DialTCP Dial err: %v", err)
		return nil, err
	}

	err = conn.SetDeadline(time.Now().Add(time.Millisecond * time.Duration(option.timeout)))
	if err != nil {
		verbosePrint(vERROR, "DialTCP SetDeadline err: %v", err)
		return nil, err
	}

	tcp := &tcpConn{
		tcpClient: conn,
		uri:       uri,
		option:    option,
	}
	return tcp, nil
}

func (tcp *tcpConn) Do(body []byte) (int64, error) {
	if tcp.tcpClient == nil {
		return 0, ErrInitTcpClient
	}

	_, err := tcp.tcpClient.Write(body)
	if err != nil {
		return 0, err
	}

	return fastRead(tcp.tcpClient, false) // !tcp.option.disableKeepAlives
}

func (tcp *tcpConn) Close() error {
	if tcp.tcpClient == nil {
		return ErrInitTcpClient
	}

	err := tcp.tcpClient.Close()
	tcp.tcpClient = nil
	return err
}
