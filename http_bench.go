/*
 * http_bench.go — 程序入口（package main，最薄的 CLI → internal 胶水层）
 *
 *   main()
 *     ├─ bench.ParseConfig(args, os.Getenv, os.Stderr)
 *     ├─ bench.SetLevel(verbose) / GOMAXPROCS / debug.SetGCPercent
 *     ├─ 校验：bench.validateParams / validateOutputFormat / validateProxyURL
 *     ├─ bench.compileHeaders(-H × N, -a)
 *     ├─ -listen → runDashboardServer(listen, dc)
 *     └─ -url / -file → runBenchmark(paramsList, dc)
 *
 *   runBenchmark — 逐场景：NewWorker → handleStartup → result.Print
 *   handleStartup — 本地 worker.Run 或分布式 handleDistributedWorkers
 *   handleDistributedWorkers — json.Marshal → PostAllWorkers → Merged
 *   defaultRunner — 实现 bench.WorkerRunner（dashboard/worker 节点执行器）
 *     ├─ CmdMetrics / CmdStop → snapshotOrPending
 *     ├─ From==""（控制器）→ 同步执行 + Merge + Print
 *     └─ From!=""（浏览器）→ 异步执行，立即返回 pending
 *
 *   distConfig — 分布式配置（worker 地址 / 密钥 / API 路径），main 构造按值传递
 */

package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	bench "github.com/linkxzhou/http_bench/internal"
)

//go:embed index.html
var embeddedDashboardHTML string

func init() {
	bench.SetDashboardHTML(embeddedDashboardHTML)
}

// distConfig carries the distributed-mode settings that were formerly
// package-level mutable globals. main() builds it from parsed CLI options and
// passes it by value to the functions that need it.
type distConfig struct {
	workerAddrs   []string
	authKey       string
	workerAPIPath string
}

// handleStartup starts HTTP benchmark testing
func handleStartup(ctx context.Context, worker *bench.HttpbenchWorker, params bench.HttpbenchParameters, dc distConfig) (result *bench.CollectResult, err error) {
	if len(dc.workerAddrs) > 0 {
		fmt.Printf("[%v][%v] running distributed worker %v for %d secs @ %s\n",
			params.RequestType, params.RequestMethod, dc.workerAddrs,
			int(params.Duration.Seconds()), params.URL)
		bench.Info(0, "distributed mode: %v", dc.workerAddrs)
		return handleDistributedWorkers(params, dc)
	}
	seqId := params.SequenceId
	switch params.Cmd {
	case bench.CmdStart:
		bench.Debug(seqId, "starting benchmark worker...")
		result, err = worker.Run(ctx, params)
		if err != nil {
			return nil, err
		}
		bench.Debug(seqId, "benchmark completed - requests: %d, errors: %d, rps: %d",
			result.TotalRequests, result.FailedRequests, result.RPS)
	case bench.CmdStop:
		worker.Stop()
		result, err = bench.GetCollectResult(seqId)
		if err != nil {
			return nil, err
		}
	case bench.CmdMetrics:
		result, err = bench.GetCollectResult(seqId)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported command: %d", params.Cmd)
	}
	result = bench.Merge(nil, result)
	if result.Output == "" && params.Output != "" {
		result.Output = params.Output
	}
	if params.Cmd == bench.CmdStart && params.From != "" {
		result.Print()
	}
	return result, nil
}

func handleDistributedWorkers(params bench.HttpbenchParameters, dc distConfig) (*bench.CollectResult, error) {
	seqId := params.SequenceId
	jsonBody, err := json.Marshal(&params)
	if err != nil {
		result := bench.NewCollectResult()
		result.ErrCode = -998
		result.ErrMsg = fmt.Sprintf("parameter marshaling failed: %v", err)
		return result, nil
	}
	// With -n only (no -d) the run length is unpredictable, so leave the
	// timeout to PostAllWorkers' default instead of a too-tight 60s cap.
	var distributedHTTPTimeout time.Duration
	if params.Duration > 0 {
		distributedHTTPTimeout = params.Duration + 60*time.Second
	}
	bench.APIKey = dc.authKey
	workerURLs := bench.NormalizeWorkerAddrs(dc.workerAddrs, dc.workerAPIPath)
	bench.Info(seqId, "dispatching task to workers: %v", workerURLs)
	distributedResult, err := bench.PostAllWorkers(workerURLs, jsonBody, distributedHTTPTimeout)
	if err != nil {
		bench.Error(seqId, "distributed workers execution failed: %v", err)
		result := bench.NewCollectResult()
		result.ErrCode = -999
		result.ErrMsg = fmt.Sprintf("distributed execution failed: %v", err)
		return result, nil
	}
	bench.Info(seqId, "distributed benchmark completed successfully")
	return distributedResult.Merged, nil
}

func main() {
	flag.Usage = func() { fmt.Print(bench.Usage) }
	opts, err := bench.ParseConfig(os.Args[1:], os.Getenv, os.Stderr)
	if err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, err)
		flag.Usage()
		os.Exit(2)
	}
	bench.SetLevel(opts.Verbose)
	if opts.PrintExample {
		fmt.Print(bench.Examples)
		return
	}

	runtime.GOMAXPROCS(opts.CPUs)
	bench.Debug(0, "using %d CPU cores", opts.CPUs)

	seqId := bench.GenSequenceId()
	params := bench.HttpbenchParameters{SequenceId: seqId}
	params.N = opts.Count
	params.C = opts.Concurrency
	params.QPS = opts.QPS
	params.Duration = opts.Duration
	if vErr := bench.ValidateParams(&params); vErr != nil {
		usageAndExit(vErr.Error())
	}

	params.RequestMethod = strings.ToUpper(opts.Method)
	params.DisableCompression = opts.DisableCompression
	params.DisableKeepAlives = opts.DisableKeepAlives
	params.Insecure = opts.Insecure
	params.RequestBody = opts.Body
	params.RequestBodyType = opts.BodyType
	if strings.ToLower(opts.Protocol) != "" {
		params.RequestType = strings.ToLower(opts.Protocol)
	} else {
		params.RequestType = strings.ToLower(opts.HTTPType)
	}
	headers, hErr := bench.CompileHeaders(opts.Headers, opts.Auth)
	if hErr != nil {
		usageAndExit(hErr.Error())
	}
	params.Headers = headers
	if oErr := bench.ValidateOutputFormat(opts.Output); oErr != nil {
		usageAndExit(oErr.Error())
	}
	params.Output = opts.Output
	params.Timeout = opts.Timeout
	if opts.ProxyAddr != "" {
		if pErr := bench.ValidateProxyURL(opts.ProxyAddr); pErr != nil {
			usageAndExit(pErr.Error())
		}
		params.ProxyURL = opts.ProxyAddr
	}
	if opts.GOGC != "" {
		if v, err := strconv.Atoi(opts.GOGC); err == nil {
			debug.SetGCPercent(v)
		}
	}
	dc := distConfig{
		workerAddrs:   append([]string(nil), opts.WorkerAddrs...),
		authKey:       opts.AuthKey,
		workerAPIPath: opts.WorkerAPIPath,
	}

	if len(opts.Listen) > 0 {
		runDashboardServer(opts.Listen, dc)
		return
	}

	var paramsList []bench.HttpbenchParameters
	if len(opts.URL) > 0 {
		params.URL = opts.URL
		paramsList = append(paramsList, params)
	} else if len(opts.File) > 0 {
		specs, parseErr := bench.ParseFile(opts.File)
		if parseErr != nil {
			usageAndExit(fmt.Sprintf("failed to read URL file %s: %v", opts.File, parseErr))
		}
		for _, spec := range specs {
			paramsList = append(paramsList, spec.MergeDefaults(&params))
		}
	} else {
		usageAndExit("no valid URLs")
	}
	runBenchmark(paramsList, dc)
	bench.Info(seqId, "all benchmarks completed")
}

func usageAndExit(msg string) {
	if msg != "" {
		fmt.Fprintln(os.Stderr, msg)
	}
	flag.Usage()
	os.Exit(1)
}

func runDashboardServer(listen string, dc distConfig) {
	html := bench.DashboardHTML()
	if opts := dc.workerAPIPath; opts != "" {
		html = strings.ReplaceAll(html, "/cb9ab101f9f725cb7c3a355bd5631184", opts)
	}
	if err := bench.Run(context.Background(), bench.Config{
		Addr:          listen,
		HTML:          html,
		WorkerAPIPath: dc.workerAPIPath,
		WorkerService: globalWorkerService,
	}); err != nil {
		bench.Error(0, "dashboard server stopped: %v", err)
	}
}

var globalWorkerService bench.WorkerService

func init() {
	globalWorkerService = bench.NewDefaultService(&defaultRunner{})
}

// defaultRunner 是 dashboard/worker 节点上的默认任务执行器。
type defaultRunner struct {
	// dashboardWorkers 记录 dashboard 异步启动的压测任务（按 seqId 索引），
	// 供 CmdStop 查找并停止正在运行的 worker。
	dashboardWorkers sync.Map
}

// RunWorker 按 params.Cmd 分发 dashboard 请求：
//   - CmdStart: 浏览器发起的请求（From 非空）异步执行并立即返回，避免 HTTP
//     请求被整个压测时长阻塞；分布式控制器（From 为空）保持同步执行以返回
//     最终结果用于汇总；
//   - CmdMetrics: 返回当前指标快照，不启动新任务；
//   - CmdStop: 停止对应 seqId 的任务并返回当前指标。
func (r *defaultRunner) RunWorker(ctx context.Context, params bench.HttpbenchParameters) (*bench.CollectResult, error) {
	switch params.Cmd {
	case bench.CmdMetrics:
		return snapshotOrPending(params.SequenceId), nil
	case bench.CmdStop:
		if v, ok := r.dashboardWorkers.LoadAndDelete(params.SequenceId); ok {
			_ = v.(*bench.HttpbenchWorker).Stop()
		}
		return snapshotOrPending(params.SequenceId), nil
	default: // bench.CmdStart
		if params.From == "" {
			worker := bench.NewWorker(params.SequenceId)
			result, err := worker.Run(ctx, params)
			if err != nil {
				return nil, err
			}
			// RPS/Average 是 Merge 计算的派生指标；先 Merge 与
			// handleStartup 的本地 CLI 路径保持一致，再在 worker 节点
			// 本地打印压测 Summary，结果同时经 HTTP 响应返回控制器汇总。
			result = bench.Merge(nil, result)
			result.Print()
			return result, nil
		}
		// 浏览器 dashboard：异步执行，立即返回。压测生命周期由
		// params.Duration 与 CmdStop 控制，不能使用 HTTP 请求的 ctx
		// （响应返回后即被取消）。
		bench.NewResult(params.SequenceId)
		worker := bench.NewWorker(params.SequenceId)
		r.dashboardWorkers.Store(params.SequenceId, worker)
		go func() {
			defer r.dashboardWorkers.Delete(params.SequenceId)
			if _, err := worker.Run(context.Background(), params); err != nil {
				bench.Error(params.SequenceId, "dashboard benchmark failed: %v", err)
			}
		}()
		return emptyPendingResult(), nil
	}
}

// snapshotOrPending 返回 seqId 当前的指标快照；压测尚未产生样本时返回空
// 结果（err_code=0），避免前端把启动初期的轮询误判为失败。
func snapshotOrPending(seqId int64) *bench.CollectResult {
	if result, err := bench.GetCollectResult(seqId); err == nil {
		return result
	}
	return emptyPendingResult()
}

// emptyPendingResult 构造启动初期的空指标结果（Fastest/Slowest 归零，
// 避免 NewCollectResult 的哨兵值直接展示到前端）。
func emptyPendingResult() *bench.CollectResult {
	result := bench.NewCollectResult()
	result.Fastest = 0
	result.Slowest = 0
	return result
}

func runBenchmark(paramsList []bench.HttpbenchParameters, dc distConfig) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	for _, params := range paramsList {
		seqId := bench.GenSequenceId()
		params.SequenceId = seqId
		params.Cmd = bench.CmdStart
		bench.Debug(seqId, "benchmark parameters: %s", params.String())
		worker := bench.NewWorker(seqId)
		result, _ := handleStartup(ctx, worker, params, dc)
		if result != nil {
			result.Print()
		}
		if ctx.Err() != nil {
			break
		}
	}
}
