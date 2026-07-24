package templatefn

// Cryptographic template functions (plan.md §C-5: "cryptoFuncs"): base64,
// hex, MD5/SHA1/SHA256/HMAC, and UUID generation.

import (
	"crypto/hmac"
	"crypto/md5"
	cryptorand "crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash"
	"strings"

	"github.com/linkxzhou/http_bench/internal/logging"
)

// cryptoFuncs returns the cryptographic subset of the template FuncMap.
func cryptoFuncs() map[string]interface{} {
	return map[string]interface{}{
		"base64Encode": base64Encode,
		"base64Decode": base64Decode,
		"md5":          md5Hash,
		"sha1":         sha1Hash,
		"sha256":       sha256Hash,
		"hmac":         hmacSign,
		"UUID":         uuid,
	}
}

func base64Encode(s string) string {
	return base64.StdEncoding.EncodeToString([]byte(s))
}

func base64Decode(s string) string {
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		logging.Error(0, "base64 decode error: %v", err)
		return ""
	}
	return string(data)
}

func md5Hash(s string) string {
	h := md5.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

func sha1Hash(s string) string {
	h := sha1.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

func sha256Hash(s string) string {
	h := sha256.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

func hmacSign(key, message, hashType string) string {
	var h func() hash.Hash
	switch strings.ToLower(hashType) {
	case "md5":
		h = md5.New
	case "sha1":
		h = sha1.New
	case "sha256":
		h = sha256.New
	default:
		h = sha256.New // Default to SHA256
	}

	mac := hmac.New(h, []byte(key))
	mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}

// uuid returns a freshly generated RFC 4122 v4 UUID string on every call.
// Previously this returned a package-level value computed once at init time,
// so every call yielded the same identifier — defeating the purpose of a UUID
// in request templates. The new implementation uses crypto-secure randomness
// and is safe for concurrent use.
func uuid() string {
	var b [16]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		for i := 0; i < 16; i++ {
			b[i] = byte(randInt63n(256))
		}
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
