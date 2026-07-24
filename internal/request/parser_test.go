package request

import (
	"strings"
	"testing"
)

func TestParseContent_Basic(t *testing.T) {
	content := `GET http://example.com/test
Content-Type: application/json
X-Trace: 123

{"foo":"bar"}

###

POST http://example.com/post
Authorization: Bearer abc
`
	reqs, err := ParseContent([]byte(content))
	if err != nil {
		t.Fatalf("ParseContent failed: %v", err)
	}
	if len(reqs) != 2 {
		t.Fatalf("expected 2 specs, got %d", len(reqs))
	}
	if reqs[0].Method != "GET" || reqs[0].URL != "http://example.com/test" {
		t.Errorf("first spec mismatch: %+v", reqs[0])
	}
	if reqs[0].Headers["Content-Type"][0] != "application/json" {
		t.Errorf("first spec headers mismatch: %+v", reqs[0].Headers)
	}
	if !strings.Contains(reqs[0].Body, `{"foo":"bar"}`) {
		t.Errorf("first spec body mismatch: %q", reqs[0].Body)
	}
	if reqs[1].Method != "POST" || reqs[1].URL != "http://example.com/post" {
		t.Errorf("second spec mismatch: %+v", reqs[1])
	}
}

func TestParseContent_EmptyInput(t *testing.T) {
	reqs, err := ParseContent([]byte(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reqs) != 0 {
		t.Errorf("expected 0 specs, got %d", len(reqs))
	}
}

func TestParseContent_OnlyComments(t *testing.T) {
	content := "# this is a comment\n# another comment\n"
	_, err := ParseContent([]byte(content))
	if err == nil {
		t.Fatal("expected error: comment-only block has no request line")
	}
}

func TestParseContent_EmptyBlockSkipped(t *testing.T) {
	content := "###\n\n###\n\nGET http://example.com"
	reqs, err := ParseContent([]byte(content))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(reqs) != 1 {
		t.Fatalf("expected 1 spec, got %d", len(reqs))
	}
}

func TestParseContent_PartialFailureKeepsValidBlocks(t *testing.T) {
	content := `###

GET

###

GET http://example.com/ok
`
	reqs, err := ParseContent([]byte(content))
	if err == nil {
		t.Fatal("expected error for invalid block")
	}
	if len(reqs) != 1 {
		t.Fatalf("expected 1 valid spec despite error, got %d", len(reqs))
	}
	if reqs[0].URL != "http://example.com/ok" {
		t.Errorf("unexpected URL: %q", reqs[0].URL)
	}
}

func TestParseContent_HTTPVersionStripped(t *testing.T) {
	content := "GET / HTTP/1.1\nHost: example.com\n"
	reqs, err := ParseContent([]byte(content))
	if err != nil {
		t.Fatalf("ParseContent: %v", err)
	}
	if len(reqs) != 1 {
		t.Fatalf("expected 1 spec")
	}
	if reqs[0].URL != "/" {
		t.Errorf("URL not stripped: %q", reqs[0].URL)
	}
}

func TestParseContent_DefaultGET(t *testing.T) {
	content := "http://example.com\n"
	reqs, err := ParseContent([]byte(content))
	if err != nil {
		t.Fatalf("ParseContent: %v", err)
	}
	if reqs[0].Method != "GET" {
		t.Errorf("expected GET, got %q", reqs[0].Method)
	}
	if reqs[0].URL != "http://example.com" {
		t.Errorf("URL mismatch: %q", reqs[0].URL)
	}
}
