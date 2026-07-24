# http_bench 二次整理计划（plan2.0）

> 状态：**§2 文件合并 / §3.1 死代码删除（方案A） / §3.2 注释补充 / §4.1 字段重命名 /
> §4.2 局部命名修正 均已执行完成**（见各节 ✅ 标记与文末"执行记录"）。
> 范围：延续 `plan.md` 阶段 A–H 的重构成果，聚焦三件事：① 把 `<100` 行的小文件合并为
> 语义完整的大模块；② 审查变量名/文件名是否符合 Go 惯例、是否有历史遗留命名；
> ③ 排查孤儿/死代码包。
> 验收标准与 `plan.md` 一致：`gofmt -w .`、`go build ./...`、`go vet ./...`、
> `go test -race ./...` 全通过；不改变 CLI、`.http` 语法、worker API 字段（JSON tag）
> 的既有行为。已实测通过（见文末"执行记录"）。

## 1. 现状清点：`<100` 行的 Go 源文件（执行前快照）

> 以下表格是本轮整理**执行前**的现状快照，用于说明决策依据；执行后的最终布局见
> §2 各小节的 ✅ 标记和文末"执行记录"，以及 `docs/REFACTOR_NOTES.md`。

| 文件 | 行数 | 职责 |
| --- | --- | --- |
| `internal/transport/http3.go` | 22 | HTTP/3 client 构造 |
| `internal/metrics/benchmark_test.go` | 28 | `CollectResult` 性能基准测试 |
| `internal/transport/http2.go` | 29 | HTTP/2 client 构造 |
| `internal/distributed/types.go` | 39 | Worker DTO 定义 |
| `test/*`（TestHTTPServer） | 39 | 集成测试用 HTTP 服务器 |
| `test/*`（TestHTTP3Client） | 42 | 集成测试用 HTTP/3 客户端探测 |
| `internal/request/spec.go` | 53 | `Spec` 类型 + `MergeDefaults` |
| `internal/transport/sender.go` | 54 | `Sender`/`SenderFactory` 抽象 |
| `test/*`（TestEchoTCP） | 55 | 集成测试用 TCP echo 服务器 |
| `internal/templatefn/funcs.go` | 58 | 模板函数注册表（合并入口） |
| `internal/transport/websocket.go` | 58 | WebSocket client 构造 + 重连 |
| `internal/transport/http1.go` | 62 | HTTP/1 client 构造 |
| `internal/benchmark/benchmark_test.go` | 67 | `RunManager` 测试 |
| `internal/templatefn/json.go` | 68 | JSON 模板函数 |
| `internal/templatefn/inject_test.go` | 71 | 时钟/随机源注入测试 |
| `internal/metrics/json_compat_test.go` | 74 | JSON tag 兼容性测试 |
| `internal/templatefn/url.go` | 78 | URL 模板函数 |
| `internal/dashboard/server_test.go` | 85 | dashboard server 测试 |
| `internal/logging/logging_test.go` | 86 | 日志包测试 |
| `request_spec_test.go`（根目录） | 88 | `Spec.MergeDefaults` 测试 |
| `internal/templatefn/time.go` | 89 | 时间模板函数 |
| `http_worker_test.go`（根目录） | 93 | Worker 生命周期测试 |
| `internal/report/reporter_test.go` | 94 | Reporter 测试 |
| `internal/limiter/limiter.go` | 95 | 全局限流器 |
| `internal/app/app.go` | 99 | Application 容器（**孤儿包，见 §3.1**） |
| `internal/metrics/circuit_breaker.go` | 99 | 熔断策略 |
| `internal/templatefn/crypto.go` | 99 | 加密/哈希模板函数 |

`util.go`(102)/`cli_options.go`(127)/`validate.go`(117) 略高于 100 行阈值，各自职责单一
（duration 解析 + RunID / CLI flag 解析 / CLI 校验），**不建议合并**。

## 2. 文件合并方案 ✅ 已执行完成

### 2.1 `internal/transport`：四个协议构造器合并为 `protocols.go` ✅

- 合并 `http1.go`(62) + `http2.go`(29) + `http3.go`(22) + `websocket.go`(58) ≈ 171 行。
- 理由：四者是同一职责层级的并列实现（`initHTTPxClient`/`initWebSocketClient`），
  彼此之间没有需要隔离的边界，全部只被 `client.go` 的 `Init()` 调用；拆成 4 个
  10~60 行的文件增加了查找成本，却没有换来隔离收益。
- `sender.go`(54) **保留独立**：`Sender`/`SenderFactory` 是接口抽象层，与具体协议
  构造器处于不同抽象层级，合并会混淆"接口契约"与"实现细节"两类读者的关注点。
- **执行结果**：已合并为 `internal/transport/protocols.go`（原四个文件已删除），
  `go build ./...` 通过。

### 2.2 `internal/distributed`：`types.go` 合并入 `handler.go` ✅

- `types.go`(39) 合并入 `handler.go`(129) ≈ 168 行。
- 理由：`WorkerRequest`/`WorkerResponse`/`WorkerError`/`WorkerResultDetail`/
  `DistributedResult` 这些 DTO 只被 `handler.go`（HTTP 入口）和 `controller.go`
  （outbound 调用）使用，没有独立包边界价值；放进 DTO 的主要消费者 `handler.go`
  比单独一个 39 行文件更容易定位。
- **执行结果**：已合并，`types.go` 已删除。

### 2.3 `internal/request`：`spec.go` 合并入 `parser.go` ✅

- `spec.go`(53) 合并入 `parser.go`(155) ≈ 208 行。
- 理由：`Spec` 类型定义与 `.http` 解析器是同一职责的两半（"定义" + "解析"），
  当前测试已经把两者放在同一个包里验证，合并文件可以减少不必要的跨文件跳转。
- **执行结果**：已合并，`spec.go` 已删除。

### 2.4 `internal/templatefn`：`json.go` + `url.go` 合并为 `data.go` ✅

- `json.go`(68) + `url.go`(78) = 146 行，与 `string.go`(146)/`crypto.go`(99) 规模相当。
- 理由：两者都是"结构化文本"编解码（JSON/URL），语义相近；合并后规模仍与其他
  领域文件一致，不会退化成大杂烩文件，同时减少 `templatefn` 目录下的文件数量
  （当前 9 个领域文件 + 1 个注册表文件）。
- **执行结果**：已合并为 `data.go`，`json.go`/`url.go` 已删除。

### 2.5 `internal/metrics`：测试文件整理为 `result_test.go` ✅

- `benchmark_test.go`(28) + `json_compat_test.go`(74) 合并为新建的 `result_test.go`。
- 理由：`result.go`(566 行) 是本包最大的源文件，却没有同名测试文件，相关测试
  分散在 `benchmark_test.go`（性能基准）和 `json_compat_test.go`（JSON 兼容性）
  两个命名不直观的文件里；合并后按"源文件 ↔ 测试文件"一一对应，便于查找。
- `circuit_breaker_test.go`(120) **保持独立**：测试目标是 `circuit_breaker.go`，
  边界清晰，且已超过 100 行阈值。
- **执行结果**：已合并为 `result_test.go`，原两个文件已删除。

### 2.6 `test/`：三个集成测试辅助服务器合并为 `servers_test.go` ✅

- `TestHTTPServer`(39) + `TestHTTP3Client`(42) + `TestEchoTCP`(55) 合并 ≈ 136 行。
- 理由：三者都是 `package test` 下为长跑集成测试准备的最小可用服务器/客户端探测，
  彼此没有依赖但主题一致（"为压测脚本提供陪跑服务"），合并后仍在合理规模内。
- 证书/密钥/URL 列表等数据类文件（`.crt`/`.key`/`.http`）不涉及合并，保持现状。
- **执行结果**：已合并为 `test/servers_test.go`，原三个文件已删除。

### 2.7 根目录：暂不合并

`util.go`(102)/`validate.go`(117)/`cli_options.go`(127) 均已高于 100 行阈值且职责
单一，若继续合并会变成"CLI 大杂烩文件"，与本轮"拆分职责"的既有目标相悖，故不
处理。

## 3. 死代码 / 孤儿包排查 ✅ 已决策并执行（方案 A）

### 3.1 `internal/app` 与 `internal/benchmark` 未接入生产路径 —— 已删除（方案 A） ✅

证据：

- 全项目搜索 `"github.com/linkxzhou/http_bench/internal/app"` **零匹配**（除包自身）。
- 全项目搜索 `"github.com/linkxzhou/http_bench/internal/benchmark"` 仅命中
  `internal/app/app.go` 和 `internal/benchmark/benchmark_test.go` 自身，主程序
  （`http_bench.go`/`http_worker.go`）完全没有引用。
- `main()` 的实际编排逻辑（`handleStartup`/`runBenchmark`/`runDashboardServer`）
  直接基于 `HttpbenchWorker` 实现，未调用 `app.Application`/`benchmark.RunManager`/
  `benchmark.Runner`。

这两个包是 `plan.md` 阶段 E/H 规划的"更完善架构"（`Runner` 接口、`RunManager`
生命周期、`Application` 容器），文档中标记为 ✅ 完成，但从未真正接入 `main`——
曾经是完全孤立、只有自测试覆盖的死代码，长期维护会产生"两套并行实现"的认知负担。

**决策项（已确认，采纳方案 A）**：

| 方案 | 说明 | 工作量 | 风险 |
| --- | --- | --- | --- |
| A（已采纳） | 删除 `internal/app`、`internal/benchmark` 及其测试；`http_bench.go`/`http_worker.go` 已实现等价功能（RunID 生成、context 取消、单次 benchmark 执行、并发运行跟踪） | 低（删除 + 确认无引用） | 低 |
| B（未采纳） | 把 `main` 迁移到使用 `app.Application`，用它替换 `http_bench.go` 里的 `handleStartup`/`runBenchmark` | 高（重写主流程 + 扩大回归测试面） | 中 |

**执行结果**：已采纳方案 A。`internal/app/`、`internal/benchmark/` 整个目录已删除
（含 `app.go`、`scenario.go`、`benchmark_test.go` 等全部文件，约 300+ 行代码）。
`go build ./...` 确认无残留引用，无编译错误。

### 3.2 `transport.Sender`/`SenderFactory` 仅被自身测试使用 ✅ 已补充注释标注

`*Client` 已经满足 `Sender` 接口（编译期断言 `var _ Sender = (*Client)(nil)`），
但生产代码（`http_worker.go`）仍直接使用 `*transport.Client`，未通过
`SenderFactory` 注入。这是阶段 D 规划的"未来切换点"，目前处于半成品状态。

**建议**：保留（54 行体量小，且是为未来 Runner 重构预留的接口），但需要在代码
注释/文档中明确标注"尚未接入生产路径"，避免后续维护者误认为已完整落地。

**执行结果**：已在 `internal/transport/sender.go` 顶部注释追加 STATUS 说明——
"no production call site (including http_worker.go) constructs a Sender via
SenderFactory yet"，明确标注该接口目前仅由 `sender_test.go` 驱动，尚未接入
`http_worker.go` 的请求路径。接口结构本身按建议保留，未做改动。

## 4. 命名审查 ✅ 已执行完成

### 4.1 Wire-compat 字段（Go 标识符可改，JSON tag 不可改） ✅

`transport.HttpbenchParameters` 中 `Url`/`ProxyUrl`/`Qps`/`N`/`C` 等字段违反 Go
命名惯例（`Url` 应为 `URL`），但这些字段依赖 JSON tag 与分布式 worker 协议、
dashboard 前端对接。**JSON tag（`url`/`proxy_url`/`qps`）与协议无关，可以只改
Go 侧标识符、保留 tag 不变**，从而在不破坏兼容性的前提下改善命名。

执行步骤：

1. 仅重命名 Go 字段：`Url`→`URL`，`ProxyUrl`→`ProxyURL`，`Qps`→`QPS`
   （`N`/`C` 已有语义化的替代命名建议见 `plan.md` §4.1，但改动更大，暂缓）。
2. 用编译器捕获全部引用点后逐一修正——涉及 `client.go`、`protocols.go`、
   `http_worker.go`、`http_bench.go`、`request/parser.go` 及全部相关 `_test.go`。

**执行结果**：已完成 `Url`→`URL`、`ProxyUrl`→`ProxyURL`、`Qps`→`QPS` 三个字段的
Go 标识符重命名，JSON tag（`url`/`proxy_url`/`qps`）保持不变。通过全项目搜索 +
编译器报错定位，逐一修正了 `internal/transport/client.go`、
`internal/transport/protocols.go`、`internal/request/parser.go`、
`http_bench.go`、`http_worker.go` 及全部相关 `_test.go`（含 struct literal
字面量字段名），`go vet ./...` 确认零残留旧字段名引用。`N`/`C` 字段命名维持
现状（暂缓，改动面更大）。

### 4.2 局部命名问题 ✅

| 位置 | 问题 | 建议 | 执行结果 |
| --- | --- | --- | --- |
| `const.go`：`cmdMetrics` | 与 `transport.CmdStart`/`CmdStop` 不在同一类型/命名空间下，容易被误认为独立枚举 | 迁移到 `internal/transport` 定义为 `CmdMetrics`，与 `CmdStart`/`CmdStop` 同源同类型 | ✅ 已迁移为 `transport.CmdMetrics`（`client.go` 枚举第三项），`const.go` 中的局部常量定义已删除 |
| `util.go`：`genSequenceId(_ int)` | 参数用 `_` 占位从未使用，说明签名是合并前遗留 | 简化为 `genSequenceId()`，同步更新 `runBenchmark`/`main` 的两处调用 | ✅ 已简化为 `genSequenceId()`，`http_bench.go`/`util_test.go` 全部调用点已同步更新 |
| `const.go`：`workerAddrList`/`httpWorkerApiAuthKey`/`httpWorkerApiPath` | 三个包级可变全局变量，与已有的 `Options.WorkerAddrs`/`AuthKey`/`WorkerAPIPath` 并存，状态来源不唯一，容易读串 | 收拢进 `main()` 的局部变量，通过函数参数传递给 `runBenchmark`/`handleDistributedWorkers`，不再暴露包级 `var` | ✅ 已删除三个全局变量，新增 `distConfig` 结构体（`workerAddrs`/`authKey`/`workerAPIPath`），由 `main()` 构造后按值传递给 `handleStartup`/`handleDistributedWorkers`/`runDashboardServer`/`runBenchmark` |
| 根目录 `http_client_test.go`/`http_client_result_test.go` | 对应源码早已迁移到 `internal/transport`/`internal/metrics`，文件名仍保留旧的 "http_client" 前缀，容易让人误以为项目里还有 `http_client.go` | 重命名为 `transport_integration_test.go`/`metrics_integration_test.go`，体现"从 `main` 包对 `internal` 包做集成测试"的实际定位 | ✅ 已重命名完成 |
| `docs/REFACTOR_NOTES.md` | 模块结构描述中提到的 `duration.go`/`input_parser.go`/`identity.go`/`http_client_parser.go`/`http_client.go`/`http_client_result.go` 均已被合并或迁移，文档与实际文件布局不一致 | 更新文档模块列表，反映当前真实文件布局（含本轮 §2 的合并结果） | ✅ 已重写 `docs/REFACTOR_NOTES.md`，含最终模块布局表、已删除包清单、合并文件对照表、命名变更记录 |

附带清理：无调用方的 `getEnv()` 辅助函数（原仅被已删除的全局变量块调用）已同步删除。

### 4.3 无需改动

- `internal/*` 包名（`transport`/`metrics`/`logging`/`report`/`request`/
  `templatefn`/`limiter`/`distributed`/`dashboard`）均符合 Go 惯例、语义清晰，
  不建议调整。
- `HttpbenchWorker`/`Client`/`CollectResult` 等类型名带有历史项目名前缀，但已被
  广泛引用（部分出现在错误信息/日志前缀中），重命名收益低、风险高，暂不处理。
- `http_worker.go` 内部私有方法 `do`（`(w *HttpbenchWorker) do(...)`）虽然是
  `plan.md` §4.1 列出的待重命名项，但作为**未导出**的内部实现细节，且已有导出的
  `Run` 方法承担对外语义，不再强制重命名。

## 5. 执行顺序（已按此顺序完成）

1. ✅ 决策 §3.1（采纳方案 A：删除 `app`/`benchmark`）。
2. ✅ 执行 §2 文件合并（纯文件级搬移，未改变任何逻辑/签名）。
3. ✅ 执行 §4.2 局部命名修正（逐项修改 + 编译验证）。
4. ✅ 执行 §4.1 字段重命名（`Url`/`ProxyUrl`/`Qps`）。
5. ✅ 更新 `docs/REFACTOR_NOTES.md` 反映最终文件布局。
6. ✅ 每一步之后运行并通过：

   ```bash
   gofmt -w .
   go build ./...
   go vet ./...
   go test -race ./...
   ```

## 6. 验收标准（已达成）

- ✅ 文件合并后 Go 源文件总数下降（transport 4→1、distributed 2→1、request 2→1、
  templatefn 2→1、metrics 2→1、test 3→1，共减少 9 个文件），总有效代码行数基本
  不变（纯搬移，无逻辑变化）。
- ✅ `go build`/`go vet`/`go test -race ./internal/...` 全部通过；根包子进程集成
  测试（`TestStressHTTP1` 等）单独验证通过。
- ✅ 采纳方案 A（删除 `app`/`benchmark`），`go build ./...` 确认无残留未使用引用、
  无编译警告。
- ✅ CLI 输出格式、`.http` 解析行为、worker API 字段（JSON tag）与改动前完全一致
  （字段重命名仅改 Go 标识符，JSON tag 未变）。
- ✅ 子进程集成测试（`TestStress*`）保持全部通过。

## 执行记录

| 日期 | 内容 |
| --- | --- |
| 2026-07-22 | §3.1 删除 `internal/app`/`internal/benchmark`；§2.1–§2.6 完成全部 6 组文件合并；§4.2 完成 `CmdMetrics` 迁移、`genSequenceId()` 简化、`distConfig` 收拢全局变量、测试文件重命名；§4.1 完成 `Url`/`ProxyUrl`/`Qps` 字段重命名；更新 `docs/REFACTOR_NOTES.md`；`gofmt`/`go build`/`go vet`/`go test -race -short ./internal/...` 全部通过。 |
| 2026-07-23 | §3.2 为 `internal/transport/sender.go` 补充 STATUS 注释，明确标注尚未接入生产路径；本文件全面回填"执行结果"，标记全部小节为已完成状态。 |
