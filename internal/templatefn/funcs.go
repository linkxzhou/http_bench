package templatefn

// Package templatefn provides the template function registry used by the
// request URL/body renderer. The functions themselves are split by domain
// (plan.md §C-5):
//   - string.go    stringFuncs
//   - crypto.go    cryptoFuncs
//   - time.go      timeFuncs
//   - random.go    randomFuncs
//   - json.go      jsonFuncs
//   - url.go       urlFuncs
//   - math.go      mathFuncs
//   - env.go       envFuncs
//
// This file retains only the shared constants/regexps and the merged FnMap.

import (
	"os"
	"regexp"
	"text/template"
)

const (
	// String generation constants used by random.go.
	letterBytes    = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	letterNumBytes = "0123456789"
)

func envFuncs() map[string]interface{} {
	return map[string]interface{}{"getEnv": os.Getenv}
}

var (
	HeaderRegexp = regexp.MustCompile(`^([\w-]+):\s*(.+)`)
	AuthRegexp   = regexp.MustCompile(`^(.+):([^\s].+)`)
)

// FnMap is the complete template function map, merged from the per-domain
// maps defined in the sibling files. Template names are stable across the
// refactor; only the internal Go organization changed.
var FnMap = func() template.FuncMap {
	m := make(template.FuncMap, 64)
	for _, src := range []map[string]interface{}{
		stringFuncs(),
		cryptoFuncs(),
		timeFuncs(),
		randomFuncs(),
		jsonFuncs(),
		urlFuncs(),
		mathFuncs(),
		envFuncs(),
	} {
		for name, fn := range src {
			m[name] = fn
		}
	}
	return m
}()
