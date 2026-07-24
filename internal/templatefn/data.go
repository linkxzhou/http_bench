package templatefn

// Structured-text template functions (plan.md §C-5): JSON and URL encode /
// decode / parse. The two function families are co-located because they both
// operate on structured text representations and share the same abstraction
// level.

import (
	"encoding/json"
	"fmt"
	"strings"

	gourl "net/url"

	"github.com/linkxzhou/http_bench/internal/logging"
)

// ---------------------------------------------------------------- JSON ---

// jsonFuncs returns the JSON subset of the template FuncMap.
func jsonFuncs() map[string]interface{} {
	return map[string]interface{}{
		"jsonEncode": jsonEncode,
		"jsonDecode": jsonDecode,
		"jsonGet":    jsonGet,
	}
}

// jsonEncode converts a value to JSON string
func jsonEncode(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		logging.Error(0, "json encode error: %v", err)
		return ""
	}
	return string(data)
}

// jsonDecode parses JSON string to map
func jsonDecode(s string) map[string]interface{} {
	var result map[string]interface{}
	err := json.Unmarshal([]byte(s), &result)
	if err != nil {
		logging.Error(0, "json decode error: %v", err)
		return make(map[string]interface{})
	}
	return result
}

// jsonGet extracts value from JSON string by key path (e.g., "user.name")
func jsonGet(jsonStr, keyPath string) string {
	var data map[string]interface{}
	err := json.Unmarshal([]byte(jsonStr), &data)
	if err != nil {
		logging.Error(0, "json parse error: %v", err)
		return ""
	}

	keys := strings.Split(keyPath, ".")
	var current interface{} = data

	for _, key := range keys {
		if m, ok := current.(map[string]interface{}); ok {
			current = m[key]
			if current == nil {
				return ""
			}
		} else {
			return ""
		}
	}

	return fmt.Sprintf("%v", current)
}

// ---------------------------------------------------------------- URL ---

// urlFuncs returns the URL subset of the template FuncMap.
func urlFuncs() map[string]interface{} {
	return map[string]interface{}{
		"urlEncode":  urlEncode,
		"urlDecode":  urlDecode,
		"urlParse":   urlParse,
		"queryBuild": queryBuild,
	}
}

func urlEncode(s string) string {
	return gourl.QueryEscape(s)
}

func urlDecode(s string) string {
	decoded, err := gourl.QueryUnescape(s)
	if err != nil {
		logging.Error(0, "url decode error: %v", err)
		return s
	}
	return decoded
}

// urlParse extracts component from URL (scheme, host, path, query, fragment)
func urlParse(urlStr, component string) string {
	u, err := gourl.Parse(urlStr)
	if err != nil {
		logging.Error(0, "url parse error: %v", err)
		return ""
	}

	switch strings.ToLower(component) {
	case "scheme":
		return u.Scheme
	case "host":
		return u.Host
	case "hostname":
		return u.Hostname()
	case "port":
		return u.Port()
	case "path":
		return u.Path
	case "query":
		return u.RawQuery
	case "fragment":
		return u.Fragment
	default:
		return urlStr
	}
}

// queryBuild builds query string from key-value pairs
// Usage: queryBuild("key1", "value1", "key2", "value2")
func queryBuild(pairs ...string) string {
	if len(pairs)%2 != 0 {
		logging.Error(0, "queryBuild requires even number of arguments")
		return ""
	}

	values := gourl.Values{}
	for i := 0; i < len(pairs); i += 2 {
		values.Add(pairs[i], pairs[i+1])
	}
	return values.Encode()
}
