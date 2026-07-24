package main

import (
	"context"
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

	"github.com/linkxzhou/http_bench/internal/dashboard"
	"github.com/linkxzhou/http_bench/internal/distributed"
	"github.com/linkxzhou/http_bench/internal/logging"
	"github.com/linkxzhou/http_bench/internal/metrics"
	"github.com/linkxzhou/http_bench/internal/request"
	"github.com/linkxzhou/http_bench/internal/transport"
)

// distConfig carries the distributed-mode settings that were formerly
// package-level mutable globals (plan2.0.md §4.2). main() builds it from
// parsed CLI options and passes it by value to the functions that need it.
type distConfig struct {
	workerAddrs   []string
	authKey       string
	workerAPIPath string
}

// handleStartup starts HTTP benchmark testing
func handleStartup(ctx context.Context, worker *HttpbenchWorker, params transport.HttpbenchParameters, dc distConfig) (result *metrics.CollectResult, err error) {
	if len(dc.workerAddrs) > 0 {
		fmt.Printf("[%v][%v] running distributed worker %v for %d secs @ %s\n",
			params.RequestType, params.RequestMethod, dc.workerAddrs,
			int(params.Duration.Seconds()), params.URL)
		logging.Info(0, "distributed mode: %v", dc.workerAddrs)
		return handleDistributedWorkers(params, dc)
	}
	seqId := params.SequenceId
	switch params.Cmd {
	case transport.CmdStart:
		logging.Debug(seqId, "starting benchmark worker...")
		result, err = worker.Run(ctx, params)
		if err != nil {
			return nil, err
		}
		logging.Debug(seqId, "benchmark completed - requests: %d, errors: %d, rps: %d",
			result.TotalRequests, result.FailedRequests, result.RPS)
	case transport.CmdStop:
		worker.Stop()
		result, err = metrics.GetCollectResult(seqId)
		if err != nil {
			return nil, err
		}
	case transport.CmdMetrics:
		result, err = metrics.GetCollectResult(seqId)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported command: %d", params.Cmd)
	}
	result = metrics.Merge(nil, result)
	if result.Output == "" && params.Output != "" {
		result.Output = params.Output
	}
	if params.Cmd == transport.CmdStart && params.From != "" {
		result.Print()
	}
	return result, nil
}

func handleDistributedWorkers(params transport.HttpbenchParameters, dc distConfig) (*metrics.CollectResult, error) {
	seqId := params.SequenceId
	jsonBody, err := json.Marshal(&params)
	if err != nil {
		result := metrics.NewCollectResult()
		result.ErrCode = -998
		result.ErrMsg = fmt.Sprintf("parameter marshaling failed: %v", err)
		return result, nil
	}
	distributedHTTPTimeout := params.Duration + 60*time.Second
	distributed.APIKey = dc.authKey
	distributedResult, err := distributed.PostAllWorkers(dc.workerAddrs, jsonBody, distributedHTTPTimeout)
	if err != nil {
		logging.Error(seqId, "distributed workers execution failed: %v", err)
		result := metrics.NewCollectResult()
		result.ErrCode = -999
		result.ErrMsg = fmt.Sprintf("distributed execution failed: %v", err)
		return result, nil
	}
	logging.Info(seqId, "distributed benchmark completed successfully")
	return distributedResult.Merged, nil
}

func main() {
	flag.Usage = func() { fmt.Print(usage) }
	opts, err := ParseConfig(os.Args[1:], os.Getenv, os.Stderr)
	if err != nil {
		if err == flag.ErrHelp {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, err)
		flag.Usage()
		os.Exit(2)
	}
	logging.SetLevel(opts.Verbose)
	if opts.PrintExample {
		fmt.Print(examples)
		return
	}

	runtime.GOMAXPROCS(opts.CPUs)
	logging.Debug(0, "using %d CPU cores", opts.CPUs)

	seqId := genSequenceId()
	params := transport.HttpbenchParameters{SequenceId: seqId}
	params.N = opts.Count
	params.C = opts.Concurrency
	params.QPS = opts.QPS
	params.Duration = opts.Duration
	if vErr := validateParams(&params); vErr != nil {
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
	headers, hErr := compileHeaders(opts.Headers, opts.Auth)
	if hErr != nil {
		usageAndExit(hErr.Error())
	}
	params.Headers = headers
	if oErr := validateOutputFormat(opts.Output); oErr != nil {
		usageAndExit(oErr.Error())
	}
	params.Output = opts.Output
	params.Timeout = opts.Timeout
	if opts.ProxyAddr != "" {
		if pErr := validateProxyURL(opts.ProxyAddr); pErr != nil {
			usageAndExit(pErr.Error())
		}
		params.ProxyURL = opts.ProxyAddr
	}
	if opts.GOGC != "" {
		if v, err := strconv.Atoi(opts.GOGC); err == nil {
			debug.SetGCPercent(v)
		}
	}
	if opts.WorkerAPIPath != "" {
		dashboardHtml = strings.ReplaceAll(dashboardHtml, "/cb9ab101f9f725cb7c3a355bd5631184", opts.WorkerAPIPath)
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

	var paramsList []transport.HttpbenchParameters
	if len(opts.URL) > 0 {
		params.URL = opts.URL
		paramsList = append(paramsList, params)
	} else if len(opts.File) > 0 {
		specs, parseErr := request.ParseFile(opts.File)
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
	logging.Info(seqId, "all benchmarks completed")
}

func runDashboardServer(listen string, dc distConfig) {
	if err := dashboard.Run(context.Background(), dashboard.Config{
		Addr:          listen,
		HTML:          dashboardHtml,
		WorkerAPIPath: dc.workerAPIPath,
		WorkerService: globalWorkerService,
	}); err != nil {
		logging.Error(0, "dashboard server stopped: %v", err)
	}
}

var globalWorkerService distributed.WorkerService

func init() {
	globalWorkerService = distributed.NewDefaultService(&defaultRunner{})
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
func (r *defaultRunner) RunWorker(ctx context.Context, params transport.HttpbenchParameters) (*metrics.CollectResult, error) {
	switch params.Cmd {
	case transport.CmdMetrics:
		return snapshotOrPending(params.SequenceId), nil
	case transport.CmdStop:
		if v, ok := r.dashboardWorkers.LoadAndDelete(params.SequenceId); ok {
			_ = v.(*HttpbenchWorker).Stop()
		}
		return snapshotOrPending(params.SequenceId), nil
	default: // transport.CmdStart
		if params.From == "" {
			worker := NewWorker(params.SequenceId)
			return worker.Run(ctx, params)
		}
		// 浏览器 dashboard：异步执行，立即返回。压测生命周期由
		// params.Duration 与 CmdStop 控制，不能使用 HTTP 请求的 ctx
		// （响应返回后即被取消）。
		metrics.NewResult(params.SequenceId)
		worker := NewWorker(params.SequenceId)
		r.dashboardWorkers.Store(params.SequenceId, worker)
		go func() {
			defer r.dashboardWorkers.Delete(params.SequenceId)
			if _, err := worker.Run(context.Background(), params); err != nil {
				logging.Error(params.SequenceId, "dashboard benchmark failed: %v", err)
			}
		}()
		return emptyPendingResult(), nil
	}
}

// snapshotOrPending 返回 seqId 当前的指标快照；压测尚未产生样本时返回空
// 结果（err_code=0），避免前端把启动初期的轮询误判为失败。
func snapshotOrPending(seqId int64) *metrics.CollectResult {
	if result, err := metrics.GetCollectResult(seqId); err == nil {
		return result
	}
	return emptyPendingResult()
}

// emptyPendingResult 构造启动初期的空指标结果（Fastest/Slowest 归零，
// 避免 NewCollectResult 的哨兵值直接展示到前端）。
func emptyPendingResult() *metrics.CollectResult {
	result := metrics.NewCollectResult()
	result.Fastest = 0
	result.Slowest = 0
	return result
}

func runBenchmark(paramsList []transport.HttpbenchParameters, dc distConfig) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	for _, params := range paramsList {
		seqId := genSequenceId()
		params.SequenceId = seqId
		params.Cmd = transport.CmdStart
		logging.Debug(seqId, "benchmark parameters: %s", params.String())
		worker := NewWorker(seqId)
		result, _ := handleStartup(ctx, worker, params, dc)
		if result != nil {
			result.Print()
		}
		if ctx.Err() != nil {
			break
		}
	}
}
