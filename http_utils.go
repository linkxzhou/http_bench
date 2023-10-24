package main

import (
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"math/rand"
	gourl "net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"
)

const (
	letterIdxBits  = 6                    // 6 bits to represent a letter index
	letterIdxMask  = 1<<letterIdxBits - 1 // All 1-bits, as many as letterIdxBits
	letterIdxMax   = 63 / letterIdxBits   // # of letter indices fitting in 63 bits
	letterBytes    = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	letterNumBytes = "0123456789"

	IntMax          = int(^uint(0) >> 1)
	IntMin          = ^IntMax
	ContentTypeJSON = "application/json"
)

var (
	ErrInitWsClient   = errors.New("init ws client error")
	ErrInitHttpClient = errors.New("init http client error")
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

func fastRead(r io.Reader) (int64, error) {
	n := int64(0)
	b := make([]byte, 0, 512)
	bSize := cap(b)
	for {
		if bsize, err := r.Read(b[0:bSize]); err != nil {
			if err == io.EOF {
				err = nil
			}
			return n, err
		} else {
			n += int64(bsize)
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

func checkURL(url string) bool {
	if _, err := gourl.ParseRequestURI(url); err != nil {
		fmt.Fprintln(os.Stderr, "parse URL err: ", err.Error())
		return false
	}
	return true
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
