package test

import (
	"fmt"
	"math/rand"
	"os"
	"testing"
	"text/template"
	"time"
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

const (
	letterIdxBits  = 6                    // 6 bits to represent a letter index
	letterIdxMask  = 1<<letterIdxBits - 1 // All 1-bits, as many as letterIdxBits
	letterIdxMax   = 63 / letterIdxBits   // # of letter indices fitting in 63 bits
	letterBytes    = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	letterNumBytes = "0123456789"
)

var (
	// for functions
	fnSrc = rand.NewSource(time.Now().UnixNano())
)

func randomString(n int) string {
	b := make([]byte, n)
	for i, cache, remain := n-1, fnSrc.Int63(), letterIdxMax; i >= 0; {
		if remain == 0 {
			cache, remain = fnSrc.Int63(), letterIdxMax
		}
		if idx := int(cache & letterIdxMask); idx < len(letterBytes) {
			b[i] = letterBytes[idx]
			i--
		}
		cache >>= letterIdxBits
		remain--
	}
	return string(b)
}

func randomNum(n int) string {
	b := make([]byte, n)
	for i, cache, remain := n-1, fnSrc.Int63(), letterIdxMax; i >= 0; {
		if remain == 0 {
			cache, remain = fnSrc.Int63(), letterIdxMax
		}
		if idx := int(cache & letterIdxMask); idx < len(letterNumBytes) {
			b[i] = letterNumBytes[idx]
			i--
		}
		cache >>= letterIdxBits
		remain--
	}
	return string(b)
}

func MyTest1() string {
	return "This is test1"
}

var (
	fnMap = template.FuncMap{
		"intSum":       intSum,
		"random":       random,
		"randomDate":   randomDate,
		"randomString": randomString,
		"randomNum":    randomNum,
		"date":         date,
		"test1":        MyTest1,
	}
)

func TestTemplateFunc(t *testing.T) {
	temp := `
	"intSum":       {{ intSum 1 2 3 4 5 }},
	"random":       {{ random 1 1000 }},
	"randomDate":   {{ randomDate "YMD" }},
	"randomString": {{ randomString 20 }},
	"randomNum":    {{ randomNum 10 }},
	"date":         {{ date "HMS" }},
	"test1":        {{ test1 }},
	`
	e, err := template.New("TestTemplateFunc").Funcs(fnMap).Parse(temp)
	if err != nil {
		fmt.Println("err: ", err)
	} else {
		e.Execute(os.Stdout, nil)
	}
}
