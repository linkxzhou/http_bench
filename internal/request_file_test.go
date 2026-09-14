package bench

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseFile_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.http")
	content := []byte(`GET http://example.com/a

###

POST http://example.com/b
Content-Type: application/json

{"ok":true}
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	specs, err := ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 2 {
		t.Fatalf("specs=%d", len(specs))
	}
	if specs[0].Method != "GET" || specs[1].Method != "POST" {
		t.Fatalf("%+v", specs)
	}
}

func TestParseFile_Missing(t *testing.T) {
	_, err := ParseFile(filepath.Join(t.TempDir(), "nope.http"))
	if err == nil {
		t.Fatal("want error")
	}
}
