package bench

import (
	"strings"
	"testing"
	"time"
)

func TestValidateParams_Table(t *testing.T) {
	cases := []struct {
		name    string
		p       HttpbenchParameters
		wantErr string
	}{
		{"c zero", HttpbenchParameters{C: 0, N: 10}, "concurrency"},
		{"n < c", HttpbenchParameters{C: 5, N: 2}, "cannot be less"},
		{"neither n nor d", HttpbenchParameters{C: 1}, "either"},
		{"duration only", HttpbenchParameters{C: 1, Duration: time.Second}, ""},
		{"n only", HttpbenchParameters{C: 2, N: 2}, ""},
		{"both", HttpbenchParameters{C: 2, N: 10, Duration: time.Second}, ""},
	}
	for _, tc := range cases {
		err := validateParams(&tc.p)
		if tc.wantErr == "" {
			if err != nil {
				t.Errorf("%s: unexpected %v", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: err=%v, want contains %q", tc.name, err, tc.wantErr)
		}
	}
}

func TestValidateOutputFormat_All(t *testing.T) {
	for _, ok := range []string{"", "summary", "csv", "html"} {
		if err := validateOutputFormat(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	if err := validateOutputFormat("xml"); err == nil {
		t.Fatal("xml should fail")
	}
}

func TestCompileHeaders_ErrorsAndAuthOnly(t *testing.T) {
	_, err := compileHeaders([]string{"bad-header"}, "")
	if err == nil {
		t.Fatal("bad header should fail")
	}
	_, err = compileHeaders(nil, "not-auth")
	if err == nil {
		t.Fatal("bad auth should fail")
	}
	h, err := compileHeaders(nil, "alice:secret")
	if err != nil {
		t.Fatal(err)
	}
	if len(h["Authorization"]) != 1 {
		t.Fatalf("auth missing: %#v", h)
	}
	h2, err := compileHeaders([]string{"Foo: bar", "Baz: qux"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if h2["Foo"][0] != "bar" || h2["Baz"][0] != "qux" {
		t.Fatalf("%#v", h2)
	}
}

func TestParseInputWithRegexp(t *testing.T) {
	_, err := parseInputWithRegexp("nope", HeaderRegexp)
	if err == nil {
		t.Fatal("want error")
	}
	m, err := parseInputWithRegexp("X-Test: value", HeaderRegexp)
	if err != nil || m[1] != "X-Test" || m[2] != "value" {
		t.Fatalf("got %#v err=%v", m, err)
	}
}
