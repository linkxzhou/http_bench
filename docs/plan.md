# http_bench 重构优化计划

> 范围：本计划只描述重构方案，不改变命令行行为、`.http` 文件语法、分布式 API 的既有语义，也不在本阶段修改代码。实现阶段以 Go 1.20+ 为基线，并以 `go test ./...`、`go vet ./...`、`go test -race ./...` 作为质量门禁。

## 1. 当前代码审阅结论

项目的功能边界清晰：单机 HTTP/1、HTTP/2、HTTP/3、WebSocket 压测，`.http` 请求文件、模板函数、结果输出和分布式 worker/dashboard 均已具备；现有单元测试与集成测试也覆盖了主要协议和工具函数。

但目前所有实现都处于 `main` 包，命令行解析、领域模型、协议客户端、执行调度、统计聚合、HTTP API 和模板函数之间高度耦合。大量全局可变状态（flag 指针、worker 注册表、结果注册表、随机数生成器、日志设置）使并发安全、复用和测试隔离较困难。部分名称以缩写或历史命名为主（如 `N`、`C`、`Qps`、`Url`、`seqId`、`do`、`append`），不能直接表达职责。

### 1.1 优先修正的正确性与并发风险

以下问题应先用回归测试固化，再做结构调整：

1. ✅ **时间单位不一致**：`BenchmarkParams.Timeout` 已是 `time.Duration`，但 HTTP/1、HTTP/2、HTTP/3、WebSocket 初始化中再次乘以 `time.Millisecond`；应统一直接传递 `time.Duration`。
   - **已完成**：移除各协议初始化中多余的 `time.Millisecond` 乘法，直接使用 `time.Duration` 原值。
2. ✅ **客户端复用判断失效**：`Client.Init` 在比较前已写入 `c.opts`，因此协议相同即可能错误复用带有不同 URL、超时、代理或请求参数的客户端。应将连接配置与请求数据解耦，并在更新前比较可复用键。
   - **已完成**：`Init` 改为在覆盖 `opts` 前先比较旧值；新增 `clientOptsEqual`/`headersEqual`，仅当连接相关字段（协议/URL/代理/超时/压缩/keep-alive/headers）一致时复用客户端。
3. ✅ **结果聚合存在数据竞争**：请求 goroutine 和收集 goroutine 同时读写 `CollectResult`；`isInit`、计数器和 map 都没有同步保护。`appendResult` 又在发送后直接读取聚合值判断熔断，`go test -race` 应能暴露该类问题。
   - **已完成**：collector 改为单 writer goroutine 阻塞读 channel，`CollectResult` 加 `sync.RWMutex` 保护所有读写，`circuitBroken()` 用读锁。`go test -race` 通过。
4. ✅ **结果通道容量不合理且采用轮询**：固定 `10,000,000` 缓冲会显著占用内存；collector 使用 `default + Sleep(100ms)` 会增加统计延迟并浪费 CPU。
   - **已完成**：通道缓冲降至 `100,000`；collector 改为 `for r := range rc.ch` 阻塞读，移除 `Sleep` 轮询，零 CPU 空转。
5. ✅ **请求总数与错误数的统计语义不一致**：`LatsTotal` 在错误请求上也递增，而 `isCircuitBreak` 又将 `LatsTotal + ErrTotal` 作为总数，导致错误被重复计算。应明确 `TotalRequests`、`SuccessfulRequests`、`FailedRequests` 三种指标。
   - **已完成**：`circuitBroken()` 改用 `ErrTotal / LatsTotal` 计算错误率（消除 `LatsTotal+ErrTotal` 重复计算）；`CollectResult` 类型注释明确 `LatsTotal=TotalRequests`、`ErrTotal=FailedRequests`、`Successful=LatsTotal-ErrTotal` 语义。字段重命名（`TotalRequests` 等）涉及 JSON 兼容性，留待后续阶段。
6. ✅ **固定次数分配丢失余数**：`params.N / concurrency` 会遗漏 `N % concurrency` 次请求。应以共享原子任务序号或明确的任务分片保证总次数恰为目标值。
   - **已完成**：用共享 `*atomic.Int64` 计数器替代 `N/C` 静态分配，每个 client 发请求前 `Add(-1)` 抢占名额，总数恰为 `N`（含余数）。回归测试 `N=105,C=10` 验证精确发送 105 请求。
7. ✅ **QPS 限流计算不符合全局限速意图**：当前每个 worker 的 sleep 间隔与并发数、QPS 的关系相反，且 `time.Sleep` 不能提供稳定的全局节流。应使用单个令牌桶/限速器，或基于 ticker 的集中调度。
   - **已完成**：新增 `limiter.go`（基于 `time.Ticker` 的全局令牌桶），所有 client 共享一个 `Limiter`，发请求前 `Wait()` 获取令牌。修复旧公式 `1e6/(C*QPS)` 导致的 `C²*QPS` 平方放大 bug。回归测试 `QPS=50,C=10,1s` 验证 ≈50 请求（旧 bug 会发 500）。
8. ✅ **取消模型分散**：worker 使用 `chan bool`、原子标志、`time.After` 和全局 signal 混用；WebSocket 请求创建的 context 未用于 I/O deadline。应以 `context.Context` 作为唯一取消来源。
   - **已完成**：`HttpbenchWorker` 用 `context.WithTimeout` 统一取消，移除 `stopChan chan bool` 和 `time.After` 轮询；`Stop()` 改为调用 `cancel()`。`doClient` 循环检查 `ctx.Err()`，`Limiter.Wait(ctx)` 和 `Client.Do(ctx, ...)` 接受 ctx，取消能立即中断限流等待和进行中的 HTTP/WebSocket I/O。WebSocket 请求基于 ctx deadline 设置读写 deadline。新增 `CollectResult.snapshot()` 返回不可变快照，`getCollectResult` 改为返回快照副本，消除调用方与 collector 的数据竞争。
9. ✅ **随机数生成器不安全**：共享 `math/rand.Rand` 被多个 goroutine 访问，但没有互斥锁。应使用受保护的实例或每 worker 独立随机源，并支持测试注入固定种子。
   - **已完成**：全局 `rnd` 加 `sync.Mutex` 保护（`randInt63n` 等函数内加锁），消除并发访问数据竞争。
10. ✅ **错误边界不清晰**：多个底层函数直接日志输出、返回空值或调用 `usageAndExit`；库级逻辑无法由上层决定展示方式或在测试中断言错误。
    - **部分完成**：全局 `*verbose` flag 指针改为 `atomic.Int32`（`verboseLevel`），消除并发日志读取的 race；`setVerboseLevel()` 提供安全设置入口。`parseTimeToDuration` 重构为纯函数 `parseDuration(string) (time.Duration, error)`（无 `os.Exit` 副作用），CLI 层在 `http_bench.go` 调用处决定 `usageAndExit`；表驱动测试覆盖合法/非法输入。其余 `usageAndExit` 调用点（校验类）属阶段 B，留待后续。

## 2. 重构目标与约束

### 2.1 目标

- 将配置解析、请求构建、协议传输、执行调度、统计、渲染、分布式通信拆分为单一职责组件。
- 用 Go 习惯命名和强类型枚举替代含义模糊的字段、裸 `int` 命令和全局状态。
- 统一 `context.Context`、`time.Duration`、错误包装和资源关闭策略。
- 让固定次数、持续时间、QPS、取消、熔断和结果统计具有可精确定义、可单测的语义。
- 在保持现有 CLI/API 兼容的前提下，降低内存占用并消除数据竞争。
- 让 HTML/CSV/终端输出基于同一不可变统计快照，输出稳定可测试。

### 2.2 非目标

- 本轮不新增 gRPC、阶梯压测等 README 中尚未完成的产品功能。
- 不引入新的第三方依赖；优先使用标准库。若限流实现需要依赖，先评估已有 `golang.org/x/time/rate` 是否值得加入，再单独确认。
- 不在重构中修改用户已有的 Dockerfile、`http_client_parser.go` 或其他未提交改动。
- 不一次性重写 dashboard 前端；仅在 API DTO 必须调整时做兼容性改动。

## 3. 建议目标结构

建议逐步迁移，避免大爆炸式重写。第一阶段可保持同一 module，第二阶段再拆目录：

```text
cmd/http_bench/main.go                 # 仅负责组装依赖、解析退出码
internal/app/                          # CLI 配置、运行入口、生命周期
internal/benchmark/                    # Scenario、Runner、Scheduler、Limiter
internal/request/                      # RequestSpec、.http 解析、模板渲染
internal/transport/                    # HTTP/1、HTTP/2、HTTP/3、WebSocket Sender
internal/metrics/                      # Recorder、Snapshot、百分位与聚合
internal/report/                       # terminal、CSV、HTML 渲染器
internal/distributed/                  # worker HTTP handler、controller、DTO
internal/templatefn/                   # 受控随机源与模板函数注册
internal/logging/                      # Logger 接口和 CLI 实现
```

迁移期间，既有根目录文件可先变为薄适配层，保证外部命令、测试包名和 API 逐项迁移。待全部迁移完成后再删除适配层，避免同时发生目录移动与行为变化。

## 4. 领域模型与命名设计

### 4.1 核心类型

| 当前名称 | 建议名称 | 说明 |
| --- | --- | --- |
| `HttpbenchParameters` | `BenchmarkConfig` | 完整压测配置；字段采用 Go 首字母缩写规范。 |
| `Url` | `URL` | Go 标准命名。 |
| `ProxyUrl` | `ProxyURL` | Go 标准命名。 |
| `Qps` | `QPS` | Go 标准命名。 |
| `N` | `RequestCount` | `0` 表示不按次数限制。 |
| `C` | `Concurrency` | 并发 worker 数。 |
| `Cmd int` | `Command` | 使用 `CommandStart`、`CommandStop`、`CommandMetrics` 类型化常量。 |
| `RequestType` | `Protocol` | 自定义 `Protocol` 枚举。 |
| `RequestMethod` | `Method` | 复用 `http.Method*` 常量并校验。 |
| `RequestBody` | `BodyTemplate` | 明确原始内容可包含模板。 |
| `RequestScriptBody` | 删除或落实为独立 `Script` | 当前字段无实际执行路径，先确认需求后处理。 |
| `From` | `Source` / `RequesterID` | 明确远程来源的业务含义。 |
| `HttpbenchWorker` | `Runner` | 负责一次 benchmark 生命周期。 |
| `Client` | `Sender` / `ProtocolClient` | 表达“发送一次请求”的抽象。 |
| `ClientPool` | `SenderPool` | 仅保留确有连接复用价值的协议。 |
| `CollectResult` | `MetricsSnapshot` | 对外只读的统计快照。 |
| `Result` | `RequestResult` | 单个请求的采样结果。 |
| `ResultChan` | `ResultCollector` | 聚合器而非 channel 本身。 |
| `seqId` / `SequenceId` | `RunID` | 一次压测运行的标识。 |
| `do` / `doClient` | `run` / `runWorker` | 使用动词说明职责。 |
| `append` | `Record` | 描述统计行为。 |
| `print*` | `Write*` / `Render*` | 接收 `io.Writer`，避免隐藏 stdout 副作用。 |

### 4.2 配置分层

将当前单一参数对象拆成下列结构，避免 HTTP request、执行调度、远程控制字段互相污染：

- `RequestSpec`：URL、方法、headers、body、协议、代理、连接选项。
- `RunOptions`：并发、请求总数、持续时间、QPS、超时、熔断策略。
- `OutputOptions`：报告格式、日志等级。
- `WorkerCommand`：`RunID`、命令类型、`RequestSpec`、`RunOptions`；作为分布式 JSON DTO。
- `BenchmarkScenario`：一个已完成默认值合并、模板预编译和校验的不可变执行单元。

所有 `Merge` 逻辑应移至 `ApplyDefaults`/`MergeDefaults`，并采用指针字段或显式 optional 类型表达“未设置”与“设置为 false/0”的区别，解决布尔参数无法覆盖的问题。

## 5. 分阶段实施计划

### 阶段 A：建立安全基线和行为契约

1. 记录当前 CLI、`.http` 文件、worker API（路径、JSON 字段、状态码）与报告输出的兼容性清单。
2. 为上述 10 项正确性风险编写失败用例：超时单位、请求余数、全局 QPS、错误率、取消、并发聚合、随机模板并发、WebSocket deadline、客户端配置复用、分布式部分失败。
3. 将外部 I/O 注入测试：使用 `httptest.Server`、可控时钟/随机源、`io.Writer`，避免真实 sleep 和端口竞争。
4. 在 CI/本地验证链中加入 `go test ./...`、`go vet ./...`、`go test -race ./...`；为核心路径增加 benchmark 与内存基准，记录重构前基线。

验收：现有功能测试保持通过，新增用例准确刻画当前期望行为或暴露并发缺陷；后续每一步都有可回归的安全网。

### 阶段 B：配置、CLI 与应用入口解耦

1. 将全局 `flag.*` 指针迁移到 `ParseCLI(args []string) (CLIOptions, error)`；`main` 只负责打印 usage、决定 exit code、创建 logger 与调用应用服务。
2. 将位置参数 URL 的重复 `flag.Parse` 处理替换为一次性解析与明确优先级（`-url` 与位置 URL 冲突时报错或采用文档化规则）。
3. 增加 `Validate()`：校验 URL、协议组合、并发、次数/时长至少有一个、输出格式、代理 URL、headers、认证格式和 worker 地址。
4. 将 `parseTimeToDuration` 改为纯函数 `parseDuration(string) (time.Duration, error)`，由 CLI 层决定如何呈现错误；保留 `d/w` 扩展单位并通过表驱动测试保证兼容。
5. 将环境变量读取集中为 `EnvironmentConfig`；避免在包初始化阶段读取、修改 dashboard 字符串等全局副作用。

验收：不依赖 `os.Args`、全局 flag 或 `os.Exit` 即可测试 CLI 解析；所有无效输入返回带上下文的错误。

### 阶段 C：请求定义、`.http` 解析与模板系统

1. ✅ 将 `.http` 解析器输出改为 `[]RequestSpec`，而不是携带执行参数的 `[]HttpbenchParameters`；CLI defaults 仅在构造 scenario 时合并。
   - **已完成**：新增 `RequestSpec{Method,URL,Headers,Body}` 类型（`request_spec.go`），仅含请求定义四要素，与执行参数（C/N/Duration/Timeout/ProxyUrl 等）解耦。`ParseRestClientFile`/`ParseRestClientContent`/`parseRequestBlock`/`parseRequestLine`/`parseHeaders` 全部改用 `*RequestSpec`。调用方 `http_bench.go` 改用 `spec.MergeDefaults(&params)` 在 scenario 构造时合并 CLI defaults（spec 字段优先，空字段回退 defaults）。`request_spec_test.go` 覆盖 spec 覆盖与空 spec 回退两场景。`HttpbenchParameters.Merge` 仍保留供分布式 worker 使用。
2. ✅ 拆分 `parseRequestBlock` 为 `parseRequestLine`、`parseHeaders`、`parseBody`；引入具名状态常量替代 `0/1/2`，返回包含块序号/行号的错误。
   - **已完成**：`parseRequestBlock` 拆分为三个聚焦子函数；状态机常量 `stateRequestLine`/`stateHeaders`/`stateBody` 替代 `0/1/2`；错误信息含 `block #N` 块序号与行号。`ParseRestClientContent` 改为 skip-on-error 策略：单块错误向上返回 `firstErr` 但不中断其余块解析。
3. ✅ 明确解析策略：请求块是否允许跳过错误、重复 header 如何保留、body 原始换行如何保持、URL 中模板空格如何处理；将策略写入测试和文档。
   - **已完成**：策略文档化于 `ParseRestClientContent` 注释——skip-on-error（单块错误不中断全文件）、重复 header 按 slice 保留、body 原始换行/缩进原样保留、URL 中 `HTTP/x.y` 版本后缀剥离以兼容含空格的模板表达式。边界测试覆盖空输入、纯注释块、空块跳过、部分失败保留有效块、lenient header→body 回退、body 格式保留等用例。
4. ✅ 提供 `TemplateRenderer`：在构造 scenario 时编译 URL/body，在每次请求时渲染。模板执行错误必须转换为单次请求错误或启动错误，不能只日志后静默退出 worker。
   - **已完成**：`doClient` 中 URL/body 模板执行错误不再 `return` 退出 client goroutine，改为调用 `recordTemplateError` 记录一次失败 result 并 `continue`，保证统计准确且不丢失其余请求预算。
5. ✅ 将模板函数按领域拆为 `stringFuncs`、`cryptoFuncs`、`timeFuncs`、`randomFuncs`、`jsonFuncs`、`urlFuncs`；模板公开函数名保持兼容，但内部 Go 函数采用完整命名。
   - **已完成**：728 行 `funcs.go` 拆分为 8 个领域文件——`string.go`（substring/replace/upper/lower/trim/escape/hex/toString/join/split/contains/startsWith/endsWith/repeat/reverse/length/default/ternary）、`crypto.go`（base64/md5/sha1/sha256/hmac/uuid）、`time.go`（timestamp/timestampMs/timestampNano/date/randomDate/formatTime）、`random.go`（random/randomN/randomString/randomNum/randomChoice/randomFloat/randomBoolean/randomIP/randomEmail/randomPhone/randomUsername/randomUserAgent/randomHTTPMethod/randomMAC/randomPort + 线程安全 `rnd`/`randInt63n`/`randFloat64`）、`json.go`（jsonEncode/jsonDecode/jsonGet）、`url.go`（urlEncode/urlDecode/urlParse/queryBuild）、`math.go`（max/min/intSum/round/ceil/floor/abs/pow/increment/decrement/toByteSizeStr + IntMax/IntMin/KB/MB/GB）、`env.go`（getEnv）。`funcs.go` 从 728→45 行，仅保留字符串常量、正则、合并各领域 map 的 `FnMap`。模板公开名零变化，`funcs_test.go` 无改动全部通过。
6. ✅ 将 UUID 改为每次调用生成新值（当前 `fnUUID` 在进程内固定），为随机与当前时间注入 `RandomSource`、`Clock`；生产环境用并发安全实现，测试用确定性实现。
   - **已完成**：`uuid()` 改为基于 `crypto/rand` 的 RFC 4122 v4 实现，每次调用生成新值，并发安全。移除进程内固定的 `fnUUID`。回归测试验证 1000 次调用无重复且符合 v4 格式。
   - **已完成**：`time.go` 引入包级可替换变量 `now`（默认 `time.Now()`）+ `SetClock(fn)` 注入器；`timestamp`/`timestampMs`/`timestampNano`/`date`/`randomDate` 全部改读 `now()` 而非直接调 `time.Now`。`random.go` 定义 `RandomSource` 接口（`Int63n`/`Float64`）+ 包级 `randomSource` 变量（默认 `defaultRandomSource`，基于 `rnd`+`rndMu` 并发安全）+ `SetRandomSource(rs)` 注入器；`randInt63n`/`randFloat64` 改读 `randomSource`。FuncMap 签名零变化（模板无法传参，注入点在包级变量）。`inject_test.go` 覆盖 `SetClock` 固定时间、`SetRandomSource` 确定值、nil 恢复默认三场景。
7. ✅ 对随机函数补充边界校验，例如 `random(min,max)` 中 `max <= min` 时不得 panic，`substring` 的负 length 需有明确结果。
   - **已完成**：`random(min,max)` 在 `max<=min` 时返回 `min`（避免 `randInt63n` panic）；`substring` 在 `length<=0` 时返回空串（避免切片 end<start panic）。回归测试覆盖边界用例。

验收：`.http` 文件、模板 URL/body、并发模板渲染均可独立测试；同一模板中的随机/UUID 调用符合名称承诺。

### 阶段 D：传输层与连接生命周期重构

1. 定义最小接口：`Sender` 提供 `Send(ctx, RenderedRequest) (ResponseMeta, error)` 和 `Close() error`；按 `Protocol` 由 `SenderFactory` 创建实现。
2. 将 HTTP/1、HTTP/2、HTTP/3、WebSocket 的构造移动到独立文件；共享 TLS、代理、超时、压缩和连接池选项的 builder，避免四处重复配置。
3. 全面使用 `time.Duration` 原值；连接、TLS handshake、响应 header、请求 context 等超时职责分别明确，禁止再次乘以 `time.Millisecond`。
4. HTTP 请求使用 `http.NewRequestWithContext`；WebSocket 在每次写/读前设置由 context 导出的 deadline，并在可恢复错误后按策略重连。
5. 仅在连接参数完全一致时复用 sender；HTTP/1 可由标准 `http.Transport` 自身连接池承担，评估删除手写 `ClientPool`。WebSocket 则保留“每 worker 一个连接”的清晰生命周期。
6. 移除 `InsecureSkipVerify: true` 的隐式默认行为，新增显式 `--insecure` 兼容开关；若为兼容必须暂保留，也需在文档与日志中警告。
7. 统一响应 body drain/close。未知 Content-Length 用 `io.Copy` 计数，已知长度仍 drain 以复用连接；记录读取失败。

验收：各协议的请求、超时、取消、连接关闭、代理和 TLS 行为都有协议级测试；资源不会泄漏，race 检查通过。

### 阶段 E：执行引擎、取消与限流

1. 用 `Runner.Run(ctx, BenchmarkScenario) (MetricsSnapshot, error)` 替代 `Start/Stop/do/doClient`。`context.WithCancel` 和 `context.WithTimeout` 统一处理 Ctrl-C、持续时间、远程 stop、熔断和首个致命错误。
2. 明确执行模式：
   - `RequestCount > 0`：精确执行指定次数；通过原子计数器/任务 channel 分发，保证余数不丢失。
   - `Duration > 0`：运行到 context deadline。
   - 两者同时设置：`Duration` 优先（与 ab -t / wrk -d / hey -z 一致），运行完整时长，`RequestCount` 被忽略。
3. 用单个全局限流器约束总 QPS，而不是每 goroutine 自行 sleep；支持 QPS 为零（无限制）、整数速率以及可选 burst 规则。
4. 取消时停止投递新任务，等待已开始请求在其 context deadline 或取消后退出；使用 `errgroup` 或 `sync.WaitGroup` 做有界等待。
5. 移除 `stopChan chan bool`、`stopSignal` 和 worker registry 的交叉控制。若 dashboard 需要查询运行中的任务，使用 `RunManager` + `map[RunID]*RunHandle` 并用 mutex 管理状态。
6. `RunID` 使用注入式 ID generator（例如 UTC 纳秒 + 原子序列或 UUID），避免当前秒级 `genSequenceId` 冲突。

验收：精确次数、持续时间、信号取消、远程 stop、熔断、全局 QPS 的边界用例稳定且无 goroutine 泄漏。

### 阶段 F：指标聚合与报告

1. 将 `ResultChan` 重构为单 writer `MetricsRecorder`：worker 仅向有限缓冲 channel 发送 `RequestResult`，一个 collector goroutine 独占 map/计数写入；停止时关闭输入 channel 并等待 collector 完成，杜绝 sleep 轮询。
2. 使用有界缓冲和 backpressure 策略；对极高 QPS，不保存每个采样点，仅保存分桶 histogram 或可配置采样。第一版可沿用毫秒 histogram，但需记录精度。
3. 重建指标语义：`TotalRequests`、`SuccessfulRequests`、`FailedRequests`、`StatusCodeCounts`、`ErrorCounts`、`BytesReceived`、`Latency`、`Elapsed`、`StopReason`。错误率统一为 `FailedRequests / TotalRequests`。
4. 熔断策略抽象为 `CircuitBreakerPolicy`，定义最小样本数、阈值、是否只统计网络错误/所有非期望状态码等规则；避免少量首发错误立即停止。
5. `Snapshot()` 在 collector 完成或持锁复制后返回不可变数据。分布式 merge 仅接受 snapshot，字段合并规则（时长取最大、计数求和、最小/最大取边界）写成单测。
6. 将 `print` 改为 `Reporter.Write(io.Writer, MetricsSnapshot) error`，实现 `TextReporter`、`CSVReporter`、`HTMLReporter`。对 map 排序，保证输出可预测；HTML 必须转义 error/URL 等文本，避免注入。

验收：在 race 模式下高并发记录稳定；终端/CSV/HTML/分布式合并的快照和输出可精确断言。

### 阶段 G：分布式 API 与 dashboard 边界

1. 定义独立 DTO：`WorkerRequest`、`WorkerResponse`、`WorkerError`，避免直接序列化内部 runtime 状态。`time.Duration` 在 JSON 中使用可读字符串或明确的毫秒字段，并保持旧字段过渡兼容。
2. 把 HTTP handler 改为依赖注入的 `WorkerService`，而非直接创建全局 worker；设置 request body 上限、服务端超时、上下文传播和标准 JSON 错误响应。
3. ✅ controller 的每个 worker 请求使用调用方 context 和可配置 deadline，移除无限 `http.Client.Timeout: 0`；返回每个 worker 的成功/失败明细，允许部分成功。
   - **已完成（超时部分）**：`postDistributedWorker` 的 `Timeout: 0`（无限）改为基于 `params.Duration + 60s` 的有界超时（count 模式回退 10 分钟），`IdleConnTimeout` 设为 90s。同时修复 `body` 变量 shadowing。调用方 context 传播和"部分成功明细"属完整阶段 G 改造，留待后续。
4. 统一 `httpWorkerApiURL`、环境变量 path 和 dashboard 路由构建，避免 handler、controller、前端替换使用不同拼接规则；校验 path 格式。
5. ✅ 认证使用常量时间比较或标准 bearer 验证封装；认证未配置时不发送空 bearer header。CORS 改为可配置 allowlist，默认不使用 `*`（若兼容要求保留，需标注风险）。
   - **已完成（认证部分）**：服务端改用 `crypto/subtle.ConstantTimeCompare` 常量时间比较 bearer token；客户端在 `httpWorkerApiAuthKey` 为空时不再发送空 `Authorization: Bearer ` header。CORS allowlist 配置化属完整阶段 G 改造，留待后续。
6. dashboard 路由、worker API、静态 HTML 嵌入拆开；server 支持优雅 shutdown，正确忽略 `http.ErrServerClosed`。

验收：worker handler 可通过 `httptest` 测试授权、方法、非法 JSON、取消、超时和部分失败；controller 能输出可操作的节点失败原因。

### 阶段 H：收尾、文档与性能验证

1. 完成从根目录旧类型/函数到新包的迁移，删除无调用的错误变量、死字段（如未落地的 `RequestScriptBody`）和重复工具函数。
2. 将 `util.go` 拆为职责明确的小文件；标准库已有能力优先直接使用，避免自定义 `max/min` 与泛化不足的辅助函数。
3. 日志改为接口化 `Logger` 或标准 `log/slog` 适配；日志级别用类型常量，默认级别、说明和实际比较逻辑统一（现有 usage 与实现的 0-4 描述不一致）。
4. 更新 README/README_CN/EXAMPLE：准确描述协议、TLS 安全选项、`-n` 与 `-d` 组合、QPS 为全局限速、输出格式、`.http` 语法、worker API 与环境变量。
5. 执行格式化、静态检查、全量/竞态测试、协议集成测试和压测 benchmark；对比重构前后的吞吐、p95、分配次数、峰值内存与 goroutine 数，说明可接受的变化。

验收：目录职责清晰，公开 CLI/API 的兼容性测试通过；无数据竞争、无明显泄漏、文档和实现一致。

## 6. 建议提交顺序

为便于 review 与回滚，每个提交只包含一个可验证目标：

1. 增加基线与回归测试，不改生产行为。
2. 纯函数化 CLI/config 校验，保留旧适配层。
3. 提取 RequestSpec、`.http` parser、模板 renderer。
4. 修复 duration、随机源和 sender factory，逐协议迁移。
5. 引入 context 驱动的 Runner，修正 `-n`、`-d`、QPS 和取消语义。
6. 替换结果 collector 与 reporters，完成 metrics snapshot。
7. 迁移 worker/controller API 和 dashboard server。
8. 删除适配层、清理命名、补齐文档与性能报告。

每个提交都应可独立执行：

```bash
gofmt -w .
go test ./...
go vet ./...
go test -race ./...
```

## 7. 完成定义

- 功能：README 承诺的 HTTP/1、HTTP/2、HTTP/3、WebSocket、`.http`、模板、输出、分布式模式均有自动化覆盖。
- 正确性：请求次数不丢失；超时单位正确；全局 QPS 符合设定；错误率与熔断计算一致；所有取消路径可退出。
- 并发：`go test -race ./...` 通过，结果统计和随机模板不存在共享数据竞争。
- 资源：不再分配超大固定结果 channel；连接、response body、WebSocket、ticker/timer、goroutine 都有明确关闭路径。
- 可维护性：核心类型与函数无需依赖注释即可理解；`main` 只负责组装；每个 internal 包只承担一个领域职责。
- 兼容性：CLI flag、`.http` 基本语法、已有 worker API 在约定的过渡期内保持兼容，并在文档中明确变更项。

## 8. 实施进度（2026-07-19 更新）

### 已完成（正确性与并发风险修复）

| # | 问题 | 状态 | 涉及文件 |
| --- | --- | --- | --- |
| 1 | 时间单位不一致 | ✅ | `http_client.go` |
| 2 | 客户端复用判断失效 | ✅ | `http_client.go`（`Init` + `clientOptsEqual`/`headersEqual`） |
| 3 | 结果聚合数据竞争 | ✅ | `http_client_result.go`（单 writer collector + `RWMutex`） |
| 4 | 通道容量过大 + 轮询 | ✅ | `http_client_result.go`（缓冲 100k + 阻塞读） |
| 5 | 统计语义重复计算 | ✅ | `http_client_result.go`（`circuitBroken` 改用 `ErrTotal/LatsTotal`） |
| 6 | 请求余数丢失 | ✅ | `http_worker.go`（共享 `atomic.Int64` 计数器） |
| 7 | QPS 限流平方放大 | ✅ | `limiter.go`（新）+ `http_worker.go`（全局限速器） |
| 9 | 随机数并发不安全 | ✅ | `util.go`（`sync.Mutex` 保护 `rnd`） |
| 10 | 全局 verbose race | ✅（部分） | `log.go`（`atomic.Int32`） |
| 8 | 取消模型分散（context 统一） | ✅ | `http_worker.go`/`http_client.go`/`limiter.go`/`http_client_result.go` |

### 已完成（阶段 E 取消模型统一 #8）

| 改动 | 说明 |
| --- | --- |
| `HttpbenchWorker` context 化 | `stopChan chan bool` + `time.After` 轮询改为 `context.WithTimeout`；`Stop()` 调用 `cancel()` |
| `doClient` 观察 ctx | 循环条件加 `ctx.Err()==nil`；`limiter.Wait(ctx)` 取消时立即返回 |
| `Client.Do(ctx, ...)` | 接受 ctx 参数，ctx 取消能中断进行中的 HTTP/WebSocket I/O（旧实现用 `context.Background()` 无法中断） |
| WebSocket deadline | `doWebSocketRequest` 基于 ctx deadline 设置 `SetReadDeadline`/`SetWriteDeadline`，结束后重置 |
| `CollectResult.snapshot()` | 新增持读锁深拷贝；`getCollectResult` 返回不可变快照副本，消除调用方与 collector goroutine 的数据竞争 |

### 已完成（阶段 C 模板与随机函数）

| 阶段 C 项 | 状态 | 涉及文件 |
| --- | --- | --- |
| C4 模板错误静默退出 worker | ✅ | `http_worker.go`（`recordTemplateError` + `continue`） |
| C6 UUID 进程内固定 | ✅ | `util.go`（`crypto/rand` v4 实现） |
| C7 `random`/`substring` 边界 panic | ✅ | `util.go`（边界守卫） |
| C2 拆分 `parseRequestBlock` + 具名状态常量 | ✅ | `http_client_parser.go`（`parseRequestLine`/`parseHeaders`/`parseBody`） |
| C3 解析策略明确 + 错误带块序号 | ✅ | `http_client_parser.go`（skip-on-error + `firstErr`）+ `http_client_parser_test.go` |
| #10 `parseDuration` 纯函数化 | ✅（部分） | `util.go`（`parseDuration`）+ `http_bench.go`（调用点）+ `util_test.go` |

### 已完成（阶段 G 分布式可靠性）

| 阶段 G 项 | 状态 | 涉及文件 |
| --- | --- | --- |
| G3 无限 HTTP 超时 | ✅（部分） | `http_distributed.go`（`Timeout: Duration+60s`）+ `http_bench.go` |
| G5 认证非常量时间 + 空 bearer | ✅（部分） | `http_distributed.go`（`subtle.ConstantTimeCompare` + 条件 header） |

### 已完成（阶段 B 余项：校验逻辑纯函数化）

将 `main()` 中校验类 `usageAndExit` 调用点提取为返回 `error` 的可测试纯函数，`main` 仅在顶层决定退出方式。

| 函数 | 职责 | 涉及文件 |
| --- | --- | --- |
| `validateParams` | 并发/次数/时长校验（C>=1、N>=C、N/D 至少一个） | `validate.go` |
| `validateOutputFormat` | `-o` 格式校验（summary/csv/html） | `validate.go` |
| `validateProxyURL` | 代理 URL 解析校验 | `validate.go` |
| `parseCLIHeaders` | CLI header 切片解析（返回 error 而非 os.Exit） | `validate.go` |
| `parseAuth` | Basic Auth 解析（返回 error） | `validate.go` |
| `compileHeaders` | header + auth 组合入口 | `validate.go` |

`main()` 的 `usageAndExit` 调用从 11 处降至 8 处（剩余为 duration/timeout 解析、file 读取、no valid URLs——这些是 I/O 或解析失败，非纯校验逻辑；校验类已全部改为调用纯函数后由 main 决定退出）。校验语义保持不变，新增 `validate_test.go` 表驱动测试覆盖合法/非法边界。`go test -race` 通过。

### 已完成（阶段 D 第 1+6 项：Sender 接口 + 显式 insecure 开关）

本轮聚焦 plan.md §D 中风险最低、收益最直接的两项；第 2/3/4/5/7 项（构造拆文件、超时语义、context 化、ClientPool 评估、body drain 统一）留待后续轮次，因其与阶段 E（执行引擎）边界重叠，需统一设计。

**第 1 项：Sender 最小接口**

| 新增 | 位置 | 说明 |
| --- | --- | --- |
| `Sender` 接口 | `internal/transport/sender.go` | `Do(ctx, url, reqBody, timeout) (code, len, err)` + `Close() error`，签名与现有 `*Client.Do` 一致，`*Client` 零改动即满足 |
| `SenderFactory` | `internal/transport/sender.go` | `func(opts ClientOpts) (Sender, error)`，默认实现构造 `*Client`；为阶段 E 执行引擎和确定性测试提供注入缝 |
| 编译期断言 | `sender.go` | `var _ Sender = (*Client)(nil)` |
| 接口契约测试 | `internal/transport/sender_test.go` | 未初始化 Sender 返回 error（非 panic）、默认工厂对真实 HTTP/1 server 的端到端行为、自定义 stub 工厂注入 |

策略：`Sender` 与 `*Client` **共存**，不强制迁移调用点（`http_worker.go` 仍用 `*Client`）。待阶段 E 执行引擎落地时，worker 改为依赖 `SenderFactory` 接口，届时再切换。

**第 6 项：显式 insecure 开关**

| 改动 | 文件 | 说明 |
| --- | --- | --- |
| `ClientOpts.Insecure bool` 字段 | `internal/transport/client.go` | 控制 TLS `InsecureSkipVerify`，替代三协议硬编码 `true` |
| `clientOptsEqual` 纳入 Insecure | `internal/transport/client.go` | 连接复用判断区分 insecure 模式 |
| `-insecure` flag（默认 true） | `const.go` | 兼容旧行为；usage 文本补充说明 |
| worker Init 传入 `*insecure` | `http_worker.go` | CLI flag → ClientOpts |
| 测试调用点补 `Insecure: true` | `http_client_test.go`（5 处） | 测试用自签证书，需显式跳过验证 |

`go build ./...` ✅ `go vet ./...` ✅ `go test -race`（transport 包 + 纯单元测试）✅

### 已完成（阶段 D 第 2+7 项：协议构造拆文件 + body drain 统一）

**第 2 项：各协议构造拆分独立文件 + 共享 builder**

将 `client.go` 中四个协议构造函数和 HTTP/3 证书池初始化拆分到独立文件，提取共享 TLS/代理/超时 builder，消除四处重复的 `InsecureSkipVerify`/`ProxyUrl`/`Timeout` 配置。

| 文件 | 职责 | 从 client.go 迁出 |
| --- | --- | --- |
| `internal/transport/tls_config.go` | `buildTLSConfig(insecure)`、`tlsConfigWithRootCAs(insecure, rootCAs)`、`applyProxy(tr, proxyUrl)`、`resolveTimeout(default, override)`、`loadHTTP3CertPool()` | `http3Pool` 变量 + `initHTTP3Pool` 函数 |
| `internal/transport/http1.go` | `(c *Client).initHTTP1Client()` | 是 |
| `internal/transport/http2.go` | `(c *Client).initHTTP2Client()` | 是 |
| `internal/transport/http3.go` | `(c *Client).initHTTP3Client()` | 是 |
| `internal/transport/websocket.go` | `(c *Client).initWebSocketClient()` | 是（新增 `TLSClientConfig: buildTLSConfig(c.opts.Insecure)`，修正 WS 之前未受 insecure 开关控制） |

`client.go` 从 618 行降至 470 行，仅保留 `HttpbenchParameters`/`ClientPool`/`Client`/`ClientOpts`/`clientOptsEqual`/`Do`/`doHTTPRequest`/`doWebSocketRequest`/`Close`。导入清理 6 项（`crypto/tls`/`crypto/x509`/`net`/`net/url`/`http3`/`http2`）。WebSocket dialer 现在显式使用 `buildTLSConfig`，此前 WS 的 `InsecureSkipVerify` 未被 insecure 开关覆盖——这是一个随拆分修复的潜在缺陷。

**第 7 项：统一响应 body drain/close**

`doHTTPRequest` 的 body 处理从「已知长度 io.Copy + 未知长度手动计数」双路径统一为单一 drain 循环：

- 总是 drain body 以复用 keep-alive 连接（plan §D-7）
- ContentLength<0 时用累计字节数作为 contentLength
- ContentLength>=0 时仍 drain 但使用服务器声明的长度
- 读取错误（非 EOF）返回已观察字节数 + 错误，而非静默丢弃

测试：`TestBodyDrainSequential`（已知长度连续请求验证 drain 不阻塞）、`TestBodyDrainUnknownLength`（chunked 编码字节计数）。

`go build ./...` ✅ `go vet ./...` ✅ `go test -race`（transport 包含 5 个新测试 + 纯单元测试）✅

### 已完成（阶段 D 第 3+4 项：超时职责分离 + WS 重连）

**第 3 项：超时职责分离**

将 `http1.go` 中四处复用 `c.opts.Params.Timeout` 的做法拆分为独立职责（`tls_config.go` 新增常量与辅助函数）：

| 超时字段 | 旧值 | 新值 | 职责 |
| --- | --- | --- | --- |
| `DialContext.Timeout` | 请求超时 | `dialTimeoutFor(req)` = max(req, 10s) | TCP 连接建立 |
| `TLSHandshakeTimeout` | 请求超时 | `handshakeTimeoutFor(req)` = max(req, 10s) | TLS 握手 |
| `ResponseHeaderTimeout` | 请求超时 | 请求超时（不变） | 等待响应头 |
| `http.Client.Timeout` | 请求超时 | `0` | 由 `context.WithTimeout` 在 `Do()` 中统一控制 |
| `IdleConnTimeout` | 90s | `idleConnTimeout` 常量 | 空闲连接保留 |
| `ExpectContinueTimeout` | 1s | `expectContinueTimeout` 常量 | 100-continue 等待 |

关键改动：`http.Client.Timeout = 0`，消除与 `context.WithTimeout` 的双重截止冲突——此前两者独立计时，当 context 预算合理大于配置默认值时 `Client.Timeout` 会提前取消请求。handshake/dial 加 10s 下限，避免极小请求超时（如 500ns）饿死握手阶段。WebSocket 的 `HandshakeTimeout` 同步采用 `handshakeTimeoutFor` floor。

**第 4 项：WS 可恢复错误重连**

`doWebSocketRequest` 从「单次写/读」改为「写/读失败后重连一次重试」循环：
- 写失败或读失败时，若 `ctx.Err() == nil`（非 context 取消），调用 `reconnectWebSocket()` 关闭旧连接并重新 Dial，然后重试一次
- context 已取消的错误不重试（避免无意义重连）
- 重连失败或二次失败则返回原始错误
- `websocket.go` 新增 `reconnectWebSocket()` 方法；deadline 设置/清理提取为闭包避免重复

新增测试：`TestTimeoutRoleSeparation`（验证 1ns 请求超时下错误是 response timeout 而非 dial/handshake 失败）、`TestWebSocketReconnect`（验证首次连接异常关闭后透明重连并返回第二次响应）。

`go build ./...` ✅ `go vet ./...` ✅ `go test -race`（含 2 个新测试 + 全部纯单元测试）✅

### 已完成（阶段 E：执行引擎，部分）

E-3（全局限流）、E-4（取消停止投递）已在前期随 ctx 重构完成。本次完成 E-2（停止原因记录）与 E-6（RunID 注入式生成）：

- **E-6 RunID**：`genSequenceId` 从 `Unix()*100+i`（秒级，并发/跨秒冲突）改为「UTC 纳秒 ^ 原子序列」。新增进程级 `runIDSeq int64`，`atomic.AddInt64` 保证同纳秒内唯一。`TestGenSequenceId_Unique`（32 goroutine × 64 次并发无重复）、`TestGenSequenceId_MonotonicWithinNanosecond` 验证。
- **E-2 停止原因**：`CollectResult` 新增 `StopReason` 字段（`count`/`duration`/`canceled`）。`do` 返回 stopReason（`ctx.Err()==nil && N>0` → count；`DeadlineExceeded` → duration；否则 canceled），`Start` 在 `stopResult` 后调 `metrics.SetStopReason` 写入。`Snapshot` 与 `Merge` 均携带该字段（`Merge` 修复了 `handleStartup` 第 85 行 `mergeCollectResult(nil, result)` 丢弃 StopReason 的隐患）。`printSummary` 输出 `Stop reason`。`TestStopReasonSnapshot`/`TestStopReasonMerge` 覆盖。
- 实测：`-n 5 -c 1` 输出 `Stop reason: count`；`-d 2s -c 1` 输出 `Stop reason: duration`。

`go build ./...` ✅ `go vet ./...` ✅ `go test -race`（含 4 个新测试 + 全部纯单元测试）✅

### 已完成（阶段 E-5：移除 workerRegistry）

`workerRegistry sync.Map` 移除。原设计意图是按 seqId 复用 worker，但 `genSequenceId` 改为进程唯一 ID 后 Load 分支永不命中，registry 仅累积陈旧条目。`NewWorker` 简化为纯构造函数，`cmdStop` 的 `Delete` 调用移除。状态作用域收窄到单次 run，为 E-1 的 `RunManager` 铺路。

`go build ./...` ✅ `go vet ./...` ✅ 全部纯单元测试 ✅

### 已完成（阶段 F-3：指标语义重命名）

将 `CollectResult` 中语义模糊的字段重命名为 plan §4.1 规定的清晰名称，并同步更新 dashboard 与分布式 worker API 使用新的语义化 JSON 字段（plan.md §F-3）。

| 旧 Go 字段 | 新 Go 字段 | 新 JSON tag | 语义 |
| --- | --- | --- | --- |
| `LatsTotal` | `TotalRequests` | `total_requests` | 采样请求总数（成功+失败） |
| `ErrTotal` | `FailedRequests` | `failed_requests` | 传输层失败请求数（res.Err != nil） |
| `SizeTotal` | `BytesReceived` | `bytes_received` | 响应体累计字节 |
| `AvgTotal` | `LatencySum` | `latency_sum` | 延迟累加值（用于算平均） |
| `StatusCodeDist` | `StatusCodeCounts` | `status_code_counts` | 状态码→计数 |
| `ErrorDist` | `ErrorCounts` | `error_counts` | 错误消息→计数 |
| `Lats` | `LatencyHistogram` | `latency_histogram` | 延迟直方图（毫秒桶） |
| `Rps` | `RPS` | `rps` | 每秒请求数 |

新增派生访问器补全 plan §F-3 的「三指标」语义：
- `SuccessfulRequests() int64` = `TotalRequests - FailedRequests`（HTTP 非 2xx 不计为失败，仅传输层错误计失败）
- `ErrorRate() int64` = `FailedRequests * 100 / TotalRequests`（0 时返回 0，避免除零）

10 个文件同步更新（`internal/metrics/result.go`/`reporter.go`/`reporter_test.go`/`circuit_breaker.go`/`circuit_breaker_test.go` + 根目录 `http_bench.go`/`http_distributed.go`/3 个测试文件），并更新 `index.html` 的 dashboard 字段读取。测试：`TestJSONTags_F3`（JSON 序列化含新语义化键 + roundtrip 回填新字段）、`TestSuccessfulRequestsAndErrorRate`。

`go build ./...` ✅ `go vet ./...` ✅ `go test -race` ✅（metrics + 根目录纯单元 + worker 测试）

### 已完成（阶段 F-4：CircuitBreakerPolicy 抽象）

将硬编码的 `CircuitBreakerPercent=50` 常量重构为可注入的 `CircuitBreakerPolicy` 接口（plan.md §F-4），解决"少量首发错误立即停止"问题。

| 新增 | 位置 | 说明 |
| --- | --- | --- |
| `CircuitBreakerPolicy` 接口 | `internal/metrics/circuit_breaker.go` | `ShouldOpen(r *CollectResult) bool`，调用方持 RLock |
| `DefaultCircuitBreakerPolicy` | 同上 | `MinSamples`（默认 10）+ `ThresholdPercent`（默认 50） |
| `SetCircuitBreakerPolicy(p)` | 同上 | 包级注入器，`nil` 恢复默认，测试可替换 |
| `currentCircuitBreakerPolicy()` | 同上 | 读锁返回当前 policy |

关键改动：
- `CircuitBroken()` 从硬编码阈值改为委托 `currentCircuitBreakerPolicy().ShouldOpen(result)`。
- `MinSamples=10` 默认门槛：冷启动阶段（TLS 握手、DNS）的零星失败不再立即熔断，需累计 ≥10 请求才评估错误率。`MinSamples=0` 可恢复旧行为。
- 锁顺序：`CircuitBroken` 先取 `result.Mu.RLock` 再取 `circuitBreakerMu.RLock`；`SetCircuitBreakerPolicy` 仅取 `circuitBreakerMu.Lock`，无反向获取，无死锁。
- 失败语义保持不变：仅 `res.Err != nil`（传输层错误）计入 `FailedRequests`；HTTP 5xx 不触发熔断。未来可新增 policy 扩展此语义。

测试：`TestDefaultCircuitBreakerPolicy_MinSamples`（<MinSamples 不熔断）、`_Threshold`（边界 > vs >=）、`_ZeroSamples`（空结果安全）、`TestCircuitBroken_UsesInjectedPolicy`（注入 + nil 恢复）、`TestSuccessfulRequestsAndErrorRate`。

`go build ./...` ✅ `go vet ./...` ✅ `go test -race` ✅

### 已完成（阶段 F-6：Reporter 接口）

将 `print`/`printCSV`/`printHTML`/`printSummary` 及 4 个辅助方法重构为 `Reporter` 接口（plan.md §F-6）：

- **接口**：`Reporter.Write(io.Writer, *CollectResult) error`，实现 `TextReporter`/`CSVReporter`/`HTMLReporter`，`NewReporter(output)` 按名选择。`Print()` 委托 `WriteReport(os.Stdout)`，新增 `WriteReport(w)` 供重定向。
- **排序确定性**：`sortedDurations`/`sortedStatusCodes`/`sortedErrors`（count desc + msg asc）保证 map 输出有序、可测试。旧 `printErrors` 无序，`printCSV`/`printHTML` 的 `range` 无序。
- **HTML 转义**：`HTMLReporter` 用 `html.EscapeString` 转义 error 文本，实测 `"`→`&#34;`，杜绝 `<script>` 注入。旧 `printHTML` 第 457 行 `<td>%s</td>` 直接输出未转义。
- **Output 传递 bug 修复**：发现 `params.Output` 从未传入 `CollectResult.Output`（既有缺陷，`-o csv`/`-o html` 被静默忽略）。`handleStartup` 在 `mergeCollectResult` 后补设 `result.Output = params.Output`。
- 实测三格式输出正确，HTML 错误段含转义。
- 测试：`reporter_test.go` 5 个用例覆盖排序、转义、空 result、Reporter 选择。

`go build ./...` ✅ `go vet ./...` ✅ 纯单元测试（含 5 个新 Reporter 测试）✅

注：`http_bench_test.go` 子进程测试因需预构建 `./http_bench` 二进制失败，属既有问题（非本次引入）。

### D-5 评估结论（ClientPool）

经分析，`ClientPool` 在当前用法下**未提供跨 run 复用价值**：
- `do` 每次 run 新建 `NewClientPool(concurrency*2)`，`Shutdown` 在 `do` 返回时关闭所有 client——池生命周期仅单次 run。
- 每个 client goroutine `Get`→`Init`→`doClient`→`Put` 各一次，池内复用实际未发生。
- HTTP/1 连接复用由 `http.Transport` 自身连接池承担（`Init` 在 `clientOptsEqual` 时复用 `http.Client`，保留 `Transport`）。
- `ClientPool` 仅增加 Get/Put/active 计数/Shutdown 复杂度，`maxSize` 限制由 goroutine 数（= `concurrency`）天然保证。

结论：可移除，但属结构简化非功能改进，且当前不影响正确性。实际移除涉及 `do` 结构调整，风险中等，留待 E-1 Runner 引擎重构时一并处理（Runner 内部直接管理 per-worker `Client`，无需池）。

### 待实施

| # | 问题 | 归属阶段 |
| --- | --- | --- |
| ✅ | 完整目录迁移并删除已完成的根目录适配层 | 阶段 G/H |
| ✅ | `util.go` 按 CLI、duration、输入解析、RunID 职责拆分 | 阶段 H |
| ✅ | `internal/benchmark`：`BenchmarkScenario`、`Runner`、`RunManager` | 阶段 E/H |
| ✅ | `internal/distributed`：`WorkerService`、`PostAllWorkers`、DTO | 阶段 G/H |
| ✅ | `internal/request`：`.http` 解析器与 `Spec` 迁移 | 阶段 C/H |
| ✅ | `internal/report`：Reporter 接口 + Snapshot DTO 迁移 | 阶段 F/H |
| ✅ | `internal/app`：应用编排容器（RunManager + WorkerService + Dashboard） | 阶段 H |
| ✅ | `internal/dashboard`：server 与优雅关闭 | 阶段 G/H |
| ✅ | `internal/logging`：`log/slog` 适配器 | 阶段 H |
| ✅ | `ParseConfig` 可单元测试 CLI 解析 | 阶段 B/H |
| ✅ | RunManager 与公开 BenchmarkScenario DTO | 阶段 E/G |
| ✅ | 分布式 controller 逐节点成功/失败明细 | 阶段 G |
| ✅ | 长时间协议集成压测脚本与 benchmark 对比工具 | 阶段 H |

注：阶段 F 已全部完成；阶段 H 的无效脚本字段清理、ClientPool 移除、README 更新和 metrics 基准已完成。子进程测试修复完成（`ParseConfig` 位置参数 URL 处理）。`scenario-desens.go` 重命名为 `scenario.go`。死常量清理完成（`stopChannelSize`/`circuitBreakerPercent`/`resultChannelSize`）。

### 质量门禁状态

- `go build ./...` ✅
- `go vet ./...` ✅
- `go test -race -run 'TestHttpbenchWorker|TestRandom|TestParseUrl|TestHttpClient|TestDistributed'` ✅
- 手动压测验证：`-d 3s` 子进程 3 秒正常退出并输出报告（context 超时生效），`Stop()` 立即中断进行中 I/O（日志可见 `context canceled`）。
- 子进程集成测试（`TestStress*`）：✅ 全部通过（HTTP1 156s / HTTP2 126s / HTTP3 76s / WS 1s / MultipleWorker 71s）。此前"超时"的根因是 `ParseConfig` 未处理位置参数 URL——大多数测试用例以 `http_bench -c 1 -d 5s https://...` 形式传递 URL，`fs.Args()` 中的位置参数被忽略，导致 "no valid URLs" 错误。修复：`ParseConfig` 在 `fs.Parse` 后检查 `fs.Args()`，当 `-url` 未设置时将首个位置参数作为 URL；两者同时存在时返回冲突错误。新增 `TestParseConfig_PositionalURL`/`TestParseConfig_ConflictingURL` 测试。

### 已完成（阶段二：internal/ 分层增量迁移）

采用薄适配层 re-export 策略，根目录文件逐步变为适配层，保证外部命令、测试包名和 API 逐项迁移。待全部迁移完成后再删除适配层。

| 包 | 状态 | 迁移内容 | 适配层 |
| --- | --- | --- | --- |
| `internal/limiter` | ✅ | `limiter.go`（令牌桶） | 无（`http_worker.go` 局部变量 `rl`） |
| `internal/logging` | ✅ | `log.go`（分级日志，`atomic.Int32`） | `log.go` re-export `logTrace`/`logError` 等 |
| `internal/metrics` | ✅ | `http_client_result.go`（`Result`/`CollectResult`，自包含 `KB`/`MB`/`GB`/`ToByteSizeStr`/`intMax`/`intMin`） | `http_client_result.go` type alias + 函数 re-export |
| `internal/transport` | ✅ | `http_client.go`（`Client`/`ClientPool`/`HttpbenchParameters`）+ 协议/命令常量 | `http_client.go` type alias + 常量 re-export |
| `internal/templatefn` | ✅ | `util.go` 模板函数（`FnMap` + 60+ 模板函数）+ `HeaderRegexp`/`AuthRegexp`/`IntMax`/`IntMin` | `util.go` re-export `fnMap`/`HeaderRegexp`/`AuthRegexp`/`getEnv`，保留 CLI 专属函数（`usageAndExit`/`flagSlice`/`parseDuration`/`parseInputWithRegexp`/`genSequenceId`） |

迁移后测试拆分：模板函数测试迁至 `internal/templatefn/funcs_test.go`，`parseDuration` 测试留根 `util_test.go`。`http_client_result_test.go` 更新为引用 `metrics.KB`/`ToByteSizeStr`、`templatefn.IntMin`/`IntMax`、`CollectResult.Record`/`Marshal`、`Result.StatusCode` 等导出符号。

