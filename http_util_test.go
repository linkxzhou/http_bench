package main

import (
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
