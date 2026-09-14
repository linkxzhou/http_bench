# http_bench 3.0 重构计划

> 状态：**已执行完成**（2026-09-14，见文末"执行记录"）
> 前置：plan.md（1.0）与 plan2.0.md 所有阶段已全部完成并通过验收。
> 目标：消除 `internal/` 的二级包层级，将所有内部代码平铺为 **单层 `internal/` 包**，
> 同时将根目录的辅助文件（`util.go`/`validate.go`/`cli_options.go`/`const.go`/`http_worker.go`）
> 下沉到 `internal/`，使根目录最终只保留两个文件：`http_bench.go` 与
> `http_bench_test.go`。
> 验收标准：`gofmt -w .`、`go build ./...`、`go vet ./...`、`go test -race ./...` 全通过；
> CLI 行为、`.http` 语法、worker API 的 JSON tag 与改动前完全一致。

---

## 0. 现状快照

### 0.1 根目录 Go 源文件

```
http_bench.go              307 行   主入口 + dashboard/分布式调度
http_worker.go             334 行   HttpbenchWorker 并发执行引擎
cli_options.go             191 行   CLI flag 解析（ParseConfig）
const.go                   147 行   常量、embed index.html、usage 字符串
util.go                    133 行   genSequenceId / parseDuration / normalizeWorkerAddrs
validate.go                118 行   validateParams / compileHeaders / validateProxyURL
```

### 0.2 `internal/` 包清单（执行前）

```
internal/
├── dashboard/      server.go (110)  + server_test.go
├── distributed/    controller.go (134) + handler.go (173) + distributed_test.go
├── limiter/        limiter.go (95) + limiter_test.go
├── logging/        log.go (159) + logging_test.go
├── metrics/        result.go (566) + circuit_breaker.go (99) + result_test.go + circuit_breaker_test.go
├── report/         reporter.go (215) + reporter_test.go
├── request/        parser.go (210) + parser_test.go
├── templatefn/     funcs.go + crypto.go + data.go + math.go + random.go + string.go + time.go
│                   + funcs_test.go + inject_test.go
└── transport/      client.go (445) + protocols.go (156) + sender.go (61) + tls_config.go (128)
                    + sender_test.go
```

### 0.3 已知符号冲突（扁平化前须解决）

| 标识符 | 冲突来源 | 解决方案 |
|---|---|---|
| `ToByteSizeStr` | `internal/metrics/result.go` vs `internal/report/reporter.go` | 保留 `report` 版本（更通用），`metrics` 版本改为包私有 `toByteSizeStr` 并内联使用 |
| `KB / MB / GB` | `internal/metrics/result.go` vs `internal/templatefn/math.go` | 统一声明在新合并文件 `internal/util.go` 中，两处原声明删除 |
| `HeaderRegexp / AuthRegexp` | `internal/templatefn/funcs.go` vs 根目录 `util.go` | 根目录 `util.go` 的两行引用改为从 `internal` 包读取（下沉后自然消除） |
| `IntMax / IntMin` | `internal/templatefn/math.go` 中定义，`metrics_integration_test.go` 跨包引用 | 测试引用路径随包迁移同步更新 |

---

## 1. 目标文件布局

```
/*
 * http_bench 3.0 项目布局
 *
 * ┌─────────────────────────────────────────────────────────────┐
 * │  根目录（package main）                                       │
 * │  http_bench.go        — main() + runBenchmark + dashboard   │
 * │  http_bench_test.go   — 所有根包测试（集成 + 端到端）          │
 * └───────────────┬─────────────────────────────────────────────┘
 *                 │ import
 * ┌───────────────▼─────────────────────────────────────────────┐
 * │  internal/  （单层，package bench）                           │
 * │                                                             │
 * │  worker.go         HttpbenchWorker，Run/Stop/doClient        │
 * │  cli.go            ParseConfig, Options, reorderArgs        │
 * │  config.go         常量、usage/examples 字符串、embed HTML   │
 * │  validate.go       validateParams / compileHeaders          │
 * │  util.go           genSequenceId / parseDuration /          │
 * │                    normalizeWorkerAddrs / KB/MB/GB           │
 * │                                                             │
 * │  transport.go      Client, HttpbenchParameters, 协议常量     │
 * │  protocols.go      initHTTP1/2/3/WS client 构造器           │
 * │  tls.go            TLS 配置、代理、超时辅助函数               │
 * │  sender.go         Sender/SenderFactory 接口（保留）         │
 * │                                                             │
 * │  metrics.go        CollectResult, Result, ResultChan        │
 * │  circuit.go        CircuitBreakerPolicy                     │
 * │  report.go         Reporter/Snapshot/TextReporter/CSV/HTML  │
 * │                                                             │
 * │  distributed.go    WorkerService, WorkerRunner, DTOs        │
 * │  controller.go     PostWorker / PostAllWorkers              │
 * │  dashboard.go      dashboard.Run (HTTP server)              │
 * │                                                             │
 * │  limiter.go        token-bucket rate limiter                │
 * │  logging.go        leveled logger + slog bridge             │
 * │                                                             │
 * │  request.go        Spec, ParseFile, ParseContent            │
 * │                                                             │
 * │  templatefn.go     FnMap 注册表 + envFuncs                   │
 * │  tmpl_string.go    stringFuncs                              │
 * │  tmpl_crypto.go    cryptoFuncs                              │
 * │  tmpl_time.go      timeFuncs                                │
 * │  tmpl_random.go    randomFuncs                              │
 * │  tmpl_data.go      jsonFuncs + urlFuncs                     │
 * │  tmpl_math.go      mathFuncs + IntMax/IntMin + KB/MB/GB     │
 * └─────────────────────────────────────────────────────────────┘
 */
```

### 1.1 包名决策

`internal/` 下统一使用 `package bench`。

理由：
- 当前各子包名（`transport`/`metrics`/`logging`/...）在同一包内会冲突。
- `bench` 语义清晰，不与标准库冲突，也不与测试框架的 `testing.B` 命名混淆。
- 根目录 `package main` 通过 `import "github.com/linkxzhou/http_bench/internal"` 即可访问全部符号，无需区分子包前缀（当前 `transport.Client` → `bench.Client`，`metrics.CollectResult` → `bench.CollectResult`，以此类推）。

---

## 2. 详细文件映射

### 2.1 `internal/worker.go`（来源：根目录 `http_worker.go`）

```
/*
 * worker.go — 并发压测引擎
 *
 *   HttpbenchWorker
 *     ├── Run(ctx, params) *CollectResult   — 对外主入口
 *     ├── Stop()                            — 线程安全停止
 *     └── do / doClient                    — 并发 goroutine 执行
 *                │
 *     ┌──────────▼──────────┐
 *     │  Limiter.Wait(ctx)  │  全局 token-bucket QPS 限流
 *     └──────────┬──────────┘
 *                │
 *     ┌──────────▼──────────┐
 *     │  Client.Do(ctx,...) │  HTTP/WS 请求执行
 *     └──────────┬──────────┘
 *                │
 *     ┌──────────▼──────────┐
 *     │  AppendResult(...)  │  结果写入 channel
 *     └─────────────────────┘
 */
```

迁移内容：`HttpbenchWorker`、`NewWorker`、`Run`、`Stop`、`GetResult`、`do`、`doClient`、`recordTemplateError`。

依赖变化：`limiter.NewLimiter` → `NewLimiter`；`transport.Client` → `Client`；`metrics.AppendResult` → `AppendResult`（同包直接调用）。

### 2.2 `internal/cli.go`（来源：根目录 `cli_options.go`）

```
/*
 * cli.go — CLI flag 解析
 *
 *   ParseConfig(args, getenv, stderr) (Options, error)
 *     ├── flag.FlagSet 解析
 *     ├── reorderArgs  — 将位置参数后移，允许 flag 与 URL 自由混排
 *     └── parseDuration — 调用 util.go 中的同名函数
 *
 *   Options struct — 所有 CLI 选项的类型安全载体
 */
```

### 2.3 `internal/config.go`（来源：根目录 `const.go`）

```
/*
 * config.go — 全局常量与嵌入资源
 *
 *   //go:embed index.html
 *   var DashboardHTML string
 *
 *   const defaultWorkerTimeout / defaultTimeout / defaultDuration / defaultVerboseLevel
 *   const usage / examples  — CLI 帮助文本
 */
```

注意：`index.html` 的 `//go:embed` 指令必须和被嵌入文件在同一目录——当前 `index.html` 在根目录，`config.go` 下沉到 `internal/` 后有两个选项：
- **方案 A（推荐）**：将 `index.html` 也移动到 `internal/`。`Dockerfile` 与 `go:embed` 路径同步更新。
- **方案 B**：在根目录保留一个薄的 `embed.go` 仅做嵌入，通过 `internal.SetDashboardHTML(s)` 注入。

计划采用方案 A，简单直接。

### 2.4 `internal/validate.go`（来源：根目录 `validate.go`）

迁移内容不变：`validateParams`、`validateOutputFormat`、`validateProxyURL`、`compileHeaders`、`parseCLIHeaders`、`parseAuth`、`parseInputWithRegexp`、`outputFormats`。

### 2.5 `internal/util.go`（来源：根目录 `util.go` + 合并 `KB/MB/GB`）

```
/*
 * util.go — 通用工具函数
 *
 *   genSequenceId() int64           — 进程唯一 RunID（时间戳 + 单调序号）
 *   parseDuration(s string)         — 支持 D/W 后缀的时长解析
 *   normalizeWorkerAddrs(...)       — 裸 host:port → 完整 API URL
 *   normalizeCaseInsensitive(s)     — 时长字符串大小写归一
 *
 *   const KB / MB / GB              — 字节单位（从 metrics/templatefn 合并至此）
 *   var runIDSeq int64
 */
```

### 2.6 `internal/transport.go`（来源：`internal/transport/client.go`）

```
/*
 * transport.go — HTTP/WS 客户端与参数 DTO
 *
 *   HttpbenchParameters  — 压测参数（JSON wire format，tag 不可变）
 *     ├── SequenceId, Cmd, URL, Method, Body, ...
 *     └── String() / GetRequestBody() / Merge()
 *
 *   Client               — 连接池 + 协议分发
 *     ├── Init(ClientOpts)
 *     ├── Do(ctx, url, body, timeout) (statusCode, size, err)
 *     └── Close()
 *
 *   const CmdStart / CmdStop / CmdMetrics
 *   const ProtocolHTTP1 / HTTP2 / HTTP3 / WS / WSS
 *   const BodyString / BodyHex
 *   const DefaultConcurrency
 */
```

### 2.7 `internal/protocols.go`（来源：`internal/transport/protocols.go`）

无结构变化，仅包名 `transport` → `bench`，方法接收者 `*Client` 不变。

### 2.8 `internal/tls.go`（来源：`internal/transport/tls_config.go`）

无结构变化。

### 2.9 `internal/sender.go`（来源：`internal/transport/sender.go`）

保留，包名更新，STATUS 注释保留。

### 2.10 `internal/metrics.go`（来源：`internal/metrics/result.go`）

```
/*
 * metrics.go — 结果采集与聚合
 *
 *   Result        — 单次请求样本
 *   ResultChan    — 每个 Run 的 channel + CollectResult 容器
 *   CollectResult — 聚合统计（TotalRequests / Fastest / RPS / ...）
 *     ├── Record(*Result)
 *     ├── Snapshot() *CollectResult
 *     ├── Print() / WriteReport(w)
 *     └── Merge(base, ...list) *CollectResult
 *
 *   NewResult(seqId)         — 注册 ResultChan
 *   AppendResult(seqId, r)   — 写入样本
 *   StopResult(seqId)        — 关闭 channel，触发 collect()
 *   GetCollectResult(seqId)  — 读取聚合结果
 *   SetStopReason(seqId, s)
 *
 *   注：ToByteSizeStr 迁移自 report.go，在此包内作为 package-level 工具；
 *       metrics.go 内原有的同名私有函数改名 toByteSizeStr（小写）
 */
```

### 2.11 `internal/circuit.go`（来源：`internal/metrics/circuit_breaker.go`）

无结构变化，包名更新。

### 2.12 `internal/report.go`（来源：`internal/report/reporter.go`）

```
/*
 * report.go — 报告输出
 *
 *   Snapshot     — 不可变的 CollectResult 快照（传给 Reporter）
 *   Reporter     — interface { Write(io.Writer, Snapshot) error }
 *   TextReporter / CSVReporter / HTMLReporter
 *   NewReporter(output string) Reporter
 *   ToByteSizeStr(b float64) string  — 统一字节格式化（从 report 包提升）
 */
```

`ToByteSizeStr` 冲突解决：`report.go` 版本作为包级导出函数保留；`metrics.go` 原有的 `ToByteSizeStr` 改为私有 `toByteSizeStr` 并只在本文件内使用。

### 2.13 `internal/distributed.go`（来源：`internal/distributed/handler.go`）

```
/*
 * distributed.go — 分布式 worker 服务与 HTTP 入口
 *
 *   WorkerRequest  = HttpbenchParameters  (type alias)
 *   WorkerResponse = CollectResult        (type alias)
 *   WorkerError / WorkerResultDetail / DistributedResult  — DTOs
 *
 *   WorkerService interface { Execute(ctx, req) (*WorkerResponse, error) }
 *   WorkerRunner  interface { RunWorker(ctx, params) (*WorkerResponse, error) }
 *   NewDefaultService(runner) WorkerService
 *   RequestValidator func type
 *   DefaultValidator(p HttpbenchParameters) error
 *   ServeRequest(service, validator, w, r)
 *
 *   var APIKey string
 *   var AllowOrigins map[string]struct{}
 */
```

### 2.14 `internal/controller.go`（来源：`internal/distributed/controller.go`）

无结构变化，包名更新，`PostWorker` / `PostAllWorkers` 函数签名不变。

### 2.15 `internal/dashboard.go`（来源：`internal/dashboard/server.go`）

```
/*
 * dashboard.go — Web Dashboard HTTP 服务
 *
 *   Config struct { Addr, HTML, WorkerAPIPath, WorkerService, ... }
 *   Run(ctx, Config) error
 *     ├── /           → 返回 HTML
 *     └── /api{path}  → ServeRequest(...)
 */
```

### 2.16 `internal/limiter.go`（来源：`internal/limiter/limiter.go`）

无结构变化，包名更新。

### 2.17 `internal/logging.go`（来源：`internal/logging/log.go`）

无结构变化，包名更新。

### 2.18 `internal/request.go`（来源：`internal/request/parser.go`）

无结构变化，包名更新。`Spec`、`ParseFile`、`ParseContent` 签名不变。

### 2.19 `internal/templatefn.go` + `tmpl_*.go`（来源：`internal/templatefn/`）

`funcs.go` → `templatefn.go`（主注册表 + `FnMap`）
其余六个领域文件按下表重命名，前缀 `tmpl_` 标明归属：

| 原文件 | 新文件 |
|---|---|
| `string.go` | `tmpl_string.go` |
| `crypto.go` | `tmpl_crypto.go` |
| `time.go` | `tmpl_time.go` |
| `random.go` | `tmpl_random.go` |
| `data.go` | `tmpl_data.go` |
| `math.go` | `tmpl_math.go`（含 `IntMax/IntMin`，KB/MB/GB 移至 `util.go`）|

---

## 3. 根目录最终结构

### 3.1 `http_bench.go`（精简后）

```
/*
 * http_bench.go — 程序入口
 *
 *   main()
 *     ├── bench.ParseConfig(os.Args[1:], ...)
 *     ├── bench.Run() / bench.RunBenchmark()
 *     └── bench.RunDashboardServer()
 *
 *   handleStartup / handleDistributedWorkers
 *   runBenchmark / runDashboardServer
 *   defaultRunner (实现 bench.WorkerRunner)
 *
 *   package main，仅含 main() 及最薄的 CLI → internal 胶水层
 */
```

### 3.2 `http_bench_test.go`（合并后）

将以下根目录测试文件全部合并为一个文件：

| 原文件 | 并入 `http_bench_test.go` 的测试组 |
|---|---|
| `cli_test.go` | `TestParseConfig_*` |
| `util_test.go` | `TestParseDuration*`、`TestGenSequenceId*`、`TestNormalizeWorkerAddrs` |
| `validate_test.go` | `TestValidate*`、`TestCompileHeaders` |
| `http_worker_test.go` | `TestHttpbenchWorker*` |
| `http_bench_distributed_test.go` | `TestStressMultipleWorkerHTTP1` |
| `metrics_integration_test.go` | `TestToByteSizeStr`、`TestGetCollectResult*`、`TestAppendAndMarshal`、`TestStopReason*` |
| `transport_integration_test.go` | `TestGetRequestBody`、`TestClientLifecycle`、`TestClientDo*`、`BenchmarkClient_Do` |
| `request_spec_test.go` | `TestRequestSpec_MergeDefaults` |
| `http_bench_test.go`（已有）| `TestStressHTTP*`、`TestStressWS` |

合并后 `http_bench_test.go` 按如下 ASCII 结构分区，各区之间以 `// ── <区名> ──` 分隔：

```
// ── CLI 解析 ──
// ── 工具函数 ──
// ── 参数校验 ──
// ── Worker 生命周期 ──
// ── Transport 集成 ──
// ── Metrics 集成 ──
// ── Request 解析 ──
// ── 端到端压测 (HTTP1/2/3/WS) ──
// ── 分布式压测 ──
```

---

## 4. `internal/` 测试文件映射

`internal/` 下现有的所有 `*_test.go` 全部随对应源文件迁移，**包名统一改为 `package bench`**（目前各自是 `package transport` / `package metrics` 等，合并后无需外部测试包隔离）：

| 原路径 | 新路径 |
|---|---|
| `internal/transport/sender_test.go` | `internal/sender_test.go` |
| `internal/metrics/result_test.go` | `internal/metrics_test.go` |
| `internal/metrics/circuit_breaker_test.go` | `internal/circuit_test.go` |
| `internal/report/reporter_test.go` | `internal/report_test.go` |
| `internal/request/parser_test.go` | `internal/request_test.go` |
| `internal/limiter/limiter_test.go` | `internal/limiter_test.go` |
| `internal/logging/logging_test.go` | `internal/logging_test.go` |
| `internal/dashboard/server_test.go` | `internal/dashboard_test.go` |
| `internal/distributed/distributed_test.go` | `internal/distributed_test.go` |
| `internal/templatefn/funcs_test.go` | `internal/templatefn_test.go` |
| `internal/templatefn/inject_test.go` | `internal/templatefn_inject_test.go` |

---

## 5. 每个新文件的 ASCII 架构图规范

每个 `internal/*.go` 文件开头（package 声明之后、import 之前）放置一段 `/* ... */` 注释，格式如下：

```go
/*
 * <filename> — <一句话职责>
 *
 * <ASCII 框图，展示本文件的核心类型/函数及其调用关系>
 *
 * 依赖：<列出本文件直接调用的同包其他文件中的关键符号>
 * 被依赖：<列出哪些文件/层调用本文件的导出符号>
 */
```

示例（`worker.go`）：

```go
/*
 * worker.go — 并发压测引擎
 *
 *   HttpbenchWorker.Run(ctx, params)
 *     │
 *     ├─► do(ctx, params)
 *     │     └─► doClient(ctx, client, remaining, rl)   [×C goroutines]
 *     │           ├─► Limiter.Wait(ctx)
 *     │           ├─► Client.Do(ctx, url, body, 0)
 *     │           └─► AppendResult(seqId, &Result{...})
 *     │
 *     └─► Stop() / StopResult() / GetCollectResult()
 *
 * 依赖：Client, Limiter, AppendResult, StopResult, GetCollectResult (同包)
 * 被依赖：http_bench.go (main package) → defaultRunner.RunWorker
 */
```

---

## 6. 符号冲突解决方案（完整清单）

| 冲突符号 | 当前位置 | 解决动作 |
|---|---|---|
| `ToByteSizeStr` | `metrics/result.go` + `report/reporter.go` | `metrics` 侧改为私有 `toByteSizeStr`；`report` 侧提升为 `bench.ToByteSizeStr` 保持导出 |
| `KB / MB / GB` | `metrics/result.go` + `templatefn/math.go` | 统一迁移至 `internal/util.go`，两处原声明删除，引用自动变同包 |
| `HeaderRegexp / AuthRegexp` | `templatefn/funcs.go` + 根 `util.go` | 根 `util.go` 下沉后两者合并为同包，直接引用，无冲突 |
| `IntMax / IntMin` | `templatefn/math.go` 导出，`metrics_integration_test.go` 引用 | 测试合并入根 `http_bench_test.go` 后 `import "internal"` 即可，路径更新 |
| `maxDuration` | `metrics/result.go`（私有） + `transport/tls_config.go`（私有） | 两者均为私有，下沉同包后需改名：`metrics` 侧 → `maxDurationMs`（操作 ms 语义更准确），`tls` 侧保留 `maxDuration` |

---

## 7. 执行顺序

每一步执行完后运行 `go build ./... && go vet ./... && go test -race -short ./...` 验证。

1. **§ 创建 `internal/` 目标文件框架**（空文件 + package 声明 + ASCII 图注释）。
2. **§ 迁移无外部依赖的叶子包**，顺序：`logging` → `limiter` → `templatefn/*`。
3. **§ 迁移 `transport`**（依赖 `logging`）：`tls.go`、`protocols.go`、`sender.go`、`transport.go`。
4. **§ 迁移 `metrics` + `report`**（依赖 `logging`、`transport`），解决 `ToByteSizeStr` / `KB/MB/GB` 冲突。
5. **§ 迁移 `request`**（依赖 `transport`）。
6. **§ 迁移 `distributed` + `dashboard`**（依赖 `transport`、`metrics`、`logging`）。
7. **§ 下沉根目录辅助文件**（`util.go`、`validate.go`、`cli_options.go`、`const.go`、`http_worker.go`）到 `internal/`，更新 `//go:embed` 路径，将 `index.html` 移入 `internal/`。
8. **§ 精简 `http_bench.go`**：移除下沉到 `internal/` 的代码，更新全部 `import` 路径为 `"github.com/linkxzhou/http_bench/internal"`。
9. **§ 合并根目录测试文件**：将 8 个散落的测试文件并入 `http_bench_test.go`，按分区注释组织，更新 `import`。
10. **§ 迁移 `internal/` 测试文件**：重命名 + 更新 `package` 声明。
11. **§ 删除旧包目录**：`rm -rf internal/transport internal/metrics internal/report internal/request internal/distributed internal/dashboard internal/limiter internal/logging internal/templatefn`。
12. **§ 全量验收**：`gofmt -w .`、`go build ./...`、`go vet ./...`、`go test -race ./...`、`docker build .`。

---

## 8. 验收标准

- `go build ./...` 零错误，`go vet ./...` 零警告。
- `go test -race ./...` 全通过（含端到端压测 `TestStressHTTP*`）。
- `internal/` 下**只有一层文件**，无子目录（`test/` 目录保持不动，不属于 `internal/`）。
- 根目录 Go 源文件**仅剩两个**：`http_bench.go` 与 `http_bench_test.go`。
- CLI 输出格式、`.http` 解析行为、worker API JSON tag 与改动前完全一致。
- 每个 `internal/*.go` 文件开头有 ASCII 架构图注释。
- `gofmt -l .` 输出为空（无未格式化文件）。

---

## 9. 风险与注意事项

### 9.1 `//go:embed` 路径约束

Go 工具链要求 `//go:embed` 指令所在文件与被嵌入文件**在同一目录或其子目录**中，且路径相对于源文件所在目录。将 `const.go` 下沉到 `internal/` 后，必须同步将 `index.html` 移至 `internal/index.html`，并将指令更新为 `//go:embed index.html`。

### 9.2 包名 `bench` 与 `testing.B` 无关

`package bench` 仅为包路径中的标识符，不影响 `testing.B`（属于标准库 `testing` 包）。

### 9.3 分布式 JSON tag 不可改变

`HttpbenchParameters` 的所有 JSON tag（`sequence_id`、`url`、`request_method` 等）是跨节点协议的一部分，**不得修改**。Go 标识符（字段名）可以改，tag 不可以。本次重构不涉及字段重命名，风险为零。

### 9.4 `test/` 目录不迁移

`test/` 目录（`servers_test.go`、`.http` 文件、证书/密钥、Shell 脚本）不属于 `internal/`，保持现状，仅在引用路径变化时更新 import。

### 9.5 `go.mod` 模块路径不变

模块路径 `github.com/linkxzhou/http_bench` 不变；内部 import 路径从 `github.com/linkxzhou/http_bench/internal/transport`（等）统一变为 `github.com/linkxzhou/http_bench/internal`。

---

## 10. 执行记录

| 日期 | 内容 |
|---|---|
| 2026-09-14 | 3.0 重构全部完成。`internal/` 扁平化为单层 `package bench`（9 个子包 → 28 个平铺文件）；根目录收敛为 `http_bench.go` + `http_bench_test.go` 两个文件；`index.html` 移入 `internal/`；新增 `internal/api.go` 作为 main 的导出包装层。符号冲突按 §6 解决：`ToByteSizeStr`（report 导出 + metrics 私有 `toByteSizeStr`）、`KB/MB/GB` 归并 `util.go`、`toByteSizeStr`（tmpl_math 侧改名 `tmplToByteSizeStr`）、`maxDuration`（metrics 侧改名 `maxDurationMs`）、`seqId`（导出 `NewWorkerForTest` 构造）。验收：`gofmt -l .` 空、`go build ./...` 通过、`go vet ./...` 零警告、`go test -race ./internal/` 通过（5.1s）、`go test -race -short .` 通过（212.9s，含 TestStressHTTP1/2/3/WS 与分布式子进程测试）、CLI 冒烟（`-n 10 -c 2`）输出正常。 |
