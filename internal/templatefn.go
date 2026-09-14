/*
 * templatefn.go — 模板函数注册表（合并入口）
 *
 *   FnMap = stringFuncs + cryptoFuncs + timeFuncs + randomFuncs
 *         + jsonFuncs  + urlFuncs   + mathFuncs + envFuncs
 *            │            │           │            │
 *   tmpl_string.go  tmpl_crypto.go  tmpl_time.go  tmpl_random.go
 *            │            │           │            │
 *   tmpl_data.go (json+url)      tmpl_math.go
 *
 *   var HeaderRegexp / AuthRegexp — CLI header 与 auth 解析正则
 *   const letterBytes / letterNumBytes — random 生成字符集
 *
 * 依赖：tmpl_*.go 中的各领域函数表
 * 被依赖：worker.go (URL/body 模板), validate.go (HeaderRegexp/AuthRegexp)
 */

package bench

import (
	"os"
	"regexp"
	"text/template"
)

const (
	// String generation constants used by tmpl_random.go.
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
// maps defined in the tmpl_*.go files. Template names are stable across
// refactors; only the internal Go organization changes.
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
