/*
 * api.go — 对 package main 暴露的导出包装
 *
 *   internal 包内部实现大多为小写（Go 惯例），根目录 http_bench.go
 *   通过本文件的薄包装访问：
 *
 *     Usage / Examples / DashboardHTML        ← config.go 的 usage/examples/dashboardHTML
 *     GenSequenceId()                          ← util.go 的 genSequenceId
 *     NormalizeWorkerAddrs(addrs, path)        ← util.go 的 normalizeWorkerAddrs
 *     ValidateParams / ValidateOutputFormat / ValidateProxyURL   ← validate.go
 *     CompileHeaders(headers, auth)            ← validate.go 的 compileHeaders
 *
 * 依赖：config.go, util.go, validate.go
 * 被依赖：http_bench.go (package main)
 */

package bench

// Usage is the CLI help text printed by flag.Usage on error or -h.
const Usage = usage

// Examples is the detailed usage examples text printed by -example.
const Examples = examples

// DashboardHTML returns the embedded dashboard HTML page.
func DashboardHTML() string { return dashboardHTML }

// GenSequenceId returns a process-unique run ID (see util.go).
func GenSequenceId() int64 { return genSequenceId() }

// NormalizeWorkerAddrs rewrites bare worker addresses into full worker API
// URLs (see util.go).
func NormalizeWorkerAddrs(addrs []string, workerAPIPath string) []string {
	return normalizeWorkerAddrs(addrs, workerAPIPath)
}

// ValidateParams checks the core run options (see validate.go).
func ValidateParams(p *HttpbenchParameters) error { return validateParams(p) }

// ValidateOutputFormat checks the -o flag value (see validate.go).
func ValidateOutputFormat(output string) error { return validateOutputFormat(output) }

// ValidateProxyURL parses the proxy address (see validate.go).
func ValidateProxyURL(proxyAddr string) error { return validateProxyURL(proxyAddr) }

// CompileHeaders builds the headers map from CLI inputs (see validate.go).
func CompileHeaders(headerSlice []string, authHeader string) (map[string][]string, error) {
	return compileHeaders(headerSlice, authHeader)
}
