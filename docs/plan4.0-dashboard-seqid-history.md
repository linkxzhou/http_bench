# http_bench 4.0 计划：Dashboard SeqID 与本地压测历史

> 状态：**已实现（待手工验收）**（2026-09-21）
> 前置：plan3.0 重构已完成（单层 `internal/` + 根目录 `index.html` embed）
> 范围：Dashboard UI（`index.html`）为主；必要时小幅扩展 worker API 响应字段
> 非目标：本计划不改 CLI 行为、不改分布式协议 JSON tag（除明确新增的可选字段）、不引入服务端持久化存储

---

## 0. 问题与目标

### 0.1 现状问题

1. **SeqID 由前端伪造**：`startTest()` 使用 `Date.now() * 1000 + Math.random()` 生成 `sequence_id`，与后端 `GenSequenceId()` 语义不一致，UI 底部也未展示权威 SeqID。
2. **无历史回溯**：刷新页面后曲线、参数、时间全部丢失；`localStorage` 目前仅保存语言偏好。
3. **轮询结果与运行未绑定到可切换的会话**：无法在「最近几次压测」之间切换查看参数与曲线。
4. **后端启动响应不回传 SeqID**：`emptyPendingResult()` / `CollectResult` JSON 无 `sequence_id` 字段，前端即使留空也无法从响应填入。

### 0.2 目标（验收标准）

| # | 验收项 |
|---|---|
| A | 新发起压测时请求可携带 `sequence_id: 0` / 省略；后端生成并在**启动响应**中返回；UI 底部展示该 SeqID |
| B | 底部 SeqID 为 `<select>`（或等价选择器），可选最近最多 **5** 条历史 |
| C | 历史项在 `localStorage` 中保存：请求参数快照、发起时间、压测曲线时间序列、最终/阶段性指标 |
| D | 切换选中的 SeqID 时，自动恢复该次的参数展示、发起时间、以及对应曲线（不必重新请求后端，除非该次仍在跑） |
| E | 曲线数据以 SeqID 为 key 落本地；新跑覆盖/追加策略明确，总条数 ≤ 5（FIFO 淘汰最旧） |
| F | 不破坏现有 CmdStart / CmdMetrics / CmdStop 主流程；CLI / 分布式路径行为不变 |

---

## 1. 现状快照（实现依据）

### 1.1 前端（`index.html`）

- 启动：`startTest()` → 本地生成 `sequenceId` → `buildRequestData(START)` → `POST` worker API → `setupMetricsMonitoring` 定时 `CmdMetrics`
- 曲线：内存中 `monitor.timeLabels` / `qpsData` / `statusCodes`，由 `processMetricsData` 追加
- 停止：`stopTest()` → `CmdStop`，或 `is_last` 时自动停
- `localStorage`：仅 `preferred-language`

### 1.2 后端（dashboard 路径）

- `From != ""`（浏览器）：`defaultRunner` 使用请求里的 `params.SequenceId`，异步 `worker.Run`，立即返回 `emptyPendingResult()`（**不含 sequence_id**）
- CLI 路径：`main` 自行 `GenSequenceId()` 写入 params
- `CollectResult` JSON：**无** `sequence_id` 字段

---

## 2. 设计概要

```
┌──────────────── Dashboard (index.html) ─────────────────┐
│  表单参数 …                                             │
│  [开始] [停止]                                          │
│                                                         │
│  图表区（QPS / Latency / Error）                         │
│                                                         │
│  底部栏：SeqID [ select ▼ ]  发起时间: …                │
│           ↑                                             │
│           └─ localStorage: http_bench.runs.v1           │
│                (最多 5 条，key=seqId)                    │
└──────────────────────────┬──────────────────────────────┘
                           │ POST /api
                           ▼
┌──────────────── Worker API ─────────────────────────────┐
│  CmdStart: sequence_id==0 → GenSequenceId()             │
│            响应 CollectResult 增加 sequence_id          │
│  CmdMetrics / CmdStop: 仍按请求中的 sequence_id 查找    │
└─────────────────────────────────────────────────────────┘
```

### 2.1 SeqID 生成职责（单一权威源）

| 场景 | 请求 `sequence_id` | 行为 |
|------|-------------------|------|
| Dashboard 新跑 | `0` 或不传（Go 零值） | 后端 `GenSequenceId()`，写入 worker，并在启动响应中回传 |
| Dashboard 轮询/停止 | 已填入的权威 ID | 后端按 ID 取快照 / 停止 |
| CLI | （内部生成，不经此 UI） | 保持现状 |
| 分布式控制器 | 控制器已有 ID | 保持现状；**不**因 `0` 误生成（见 §4 守卫） |

**前端规则**：点击「开始」时先清空当前 `sequenceId`（或置 0），发送 START；拿到响应中的 `sequence_id` 后再写入 UI select，并用该 ID 启动 metrics 轮询。

> 注意：今日实现是「先本地造 ID 再轮询」。新流程必须改为 **等 START 响应返回 SeqID 后再 `setupMetricsMonitoring`**，否则会用 0 去轮询。

### 2.2 后端响应扩展（小改动）

在 `CollectResult`（或启动专用响应）中新增：

```go
SequenceId int64 `json:"sequence_id,omitempty"`
```

- `emptyPendingResult()` / `snapshotOrPending()` / 异步 Start 返回前：写入本次 `params.SequenceId`
- JSON tag 为**新增可选字段**，旧客户端忽略即可；不修改既有 tag
- Dashboard Start：若 `params.SequenceId == 0`，先 `params.SequenceId = GenSequenceId()`

**守卫**：仅当 `params.From == "browser"`（或明确 Dashboard 路径）且 `SequenceId == 0` 时自动生成，避免误伤其它调用方。

### 2.3 本地存储模型

**Storage key**：`http_bench.runs.v1`（版本化，便于日后迁移）

**结构**（示意）：

```json
{
  "version": 1,
  "activeSeqId": "1789…",
  "order": ["1789…", "1788…"],
  "runs": {
    "1789…": {
      "sequenceId": "1789…",
      "startedAt": "2026-09-21T01:23:45.678Z",
      "finishedAt": null,
      "status": "running",
      "request": {
        "url": "https://…",
        "request_method": "GET",
        "request_type": "http1",
        "n": 0,
        "c": 10,
        "duration_ms": 10000,
        "timeout_ms": 3000,
        "qps": 0,
        "headers": {},
        "proxy_url": "",
        "request_body": "",
        "request_bodytype": "string",
        "disable_compression": false,
        "disable_keepalives": false,
        "insecure": true,
        "worker_api_endpoint": "/api"
      },
      "series": {
        "timeLabels": ["01:23:46", "01:23:47"],
        "qpsData": [12.3, 15.1],
        "statusCodes": { "200": [12.3, 15.1] },
        "latencyHistogram": {},
        "errorCounts": {}
      },
      "stats": {
        "average": "1ms",
        "rps": 15,
        "fastest": "0ms",
        "slowest": "5ms",
        "errTotal": 0,
        "totalRequests": 30,
        "stopReason": ""
      }
    }
  }
}
```

**约束**：

- `order.length ≤ 5`；超出时删除 `order` 头部最旧项及对应 `runs[id]`
- SeqID 在 JSON/localStorage 中建议用**字符串**存（避免 JS 大整数精度问题；后端 int64 可能超过 `Number.MAX_SAFE_INTEGER`）
- 单条 `series` 各序列长度上限 **600**（FIFO 丢最旧点）；仍需防止 localStorage 配额打满（写入失败 toast）
- 写盘策略：metrics 每次成功处理后 **debounce 300–500ms** 写一次；`is_last` / `stop` 时立即 flush

### 2.4 UI 布局（底部栏）

在控制面板底部或页面 footer 增加：

```
┌──────────────────────────────────────────────────────────┐
│ SeqID  [ 1789… ▼ ]     发起: 2026-09-21 09:23:45         │
│ 状态: running / finished / failed                        │
└──────────────────────────────────────────────────────────┘
```

- `<select>` options：展示 `seqId` 短码 + 时间（如 `…8912 · 09:23:45`）
- 新跑开始且尚未拿到后端 ID：select 显示「生成中…」或临时占位，禁用切换（可选）
- 选中历史项：
  1. **自动回填** `request` 到表单控件
  2. 用 `series` 重绘三张图
  3. 更新 stats / 发起时间
- **正在跑的那次**被选中时：继续实时追加曲线；切到历史项时暂停对「当前跑」的 UI 绑定（轮询可继续写 localStorage，但不刷新图表，直到切回）
- 底部提供 **清除历史** 按钮：确认后清空 localStorage 历史并重置 UI
- 每条 run 的 `series` 各数组长度上限 **600**

---

## 3. 详细任务拆分

### 3.1 后端（小）

1. `CollectResult` 增加 `SequenceId int64 \`json:"sequence_id,omitempty"\``
2. `defaultRunner.RunWorker` CmdStart（browser）：`SequenceId==0` 时 `GenSequenceId()`
3. `emptyPendingResult` 改为接受 seqId，或 Start 返回前赋值 `result.SequenceId`
4. `snapshotOrPending` / Stop 返回快照时同样带上 `sequence_id`（便于前端校验）
5. 单测：browser Start 且 seq=0 → 响应含非 0 sequence_id；seq≠0 → 保持原值；CLI/From 空路径不因 0 误伤（按守卫实现）

### 3.2 前端 — SeqID 展示与启动时序

1. `data` 明确增加：`sequenceId`（string）、`runHistory`、`startedAt` 等
2. 底部 select + 时间文案 + i18n（中/英）
3. `startTest` 时序改造：
   - 校验通过 → `isRunning=true` → `sequenceId=''` → POST START（`sequence_id: 0`）
   - 响应解析 `data.sequence_id` → 写入 `sequenceId` → `upsertRun` → **再** `setupMetricsMonitoring`
   - 失败则回滚 `isRunning`
4. metrics / stop 请求一律使用当前权威 `sequenceId`

### 3.3 前端 — localStorage 历史

1. 封装 `RunStore`：`load` / `save` / `upsert` / `touchOrder` / `pruneTo5` / `get(seqId)`
2. 启动成功：写入 `request` 快照 + `startedAt` + `status=running`
3. `processMetricsData`：更新 `series` + `stats`；debounce 持久化
4. 结束（`is_last` / stop）：`status=finished`，写 `finishedAt` / `stopReason`
5. 页面 `created`：加载历史；若无 active 则选最近一条仅展示（不自动开跑）

### 3.4 前端 — 切换 SeqID / 清除历史

1. `@change` → `selectRun(seqId)`：**自动回填**表单参数
2. `renderPerfChart` / `renderLatChart` / `renderErrChart` 用历史 series 全量 `setOption`
3. 若选中的是非 running 项，不启动轮询；若选中仍在 running 的当前项，恢复实时模式
4. 「清除历史」：确认 → 删除 `http_bench.runs.v1` → 清空 select / monitor / stats（不影响当前是否仍在跑的后端任务；若正在跑可保留内存中的当前 run 或一并停止——实现时选择：清除仅本地历史，不自动 CmdStop）
5. `trimSeries(series, 600)`：追加点后裁剪

### 3.5 兼容与迁移

- 无旧 runs key 时可忽略
- 若未来改 schema：bump `http_bench.runs.v2`，读取时迁移或丢弃 v1

---

## 4. 风险与决策点

| 风险 | 处理 |
|------|------|
| int64 SeqID 超出 JS 安全整数 | localStorage 与 Vue 状态一律 **string**；JSON 解析用字符串或 `json.Decoder` 大数策略；展示用完整十进制字符串 |
| START 与首屏 metrics 竞态 | 必须等 START 返回 SeqID 后再轮询 |
| localStorage 配额 | 限制 5 条 + 每条点数上限；写入失败时 toast 提示并降级为仅内存 |
| 切换历史时误改表单影响新跑 | 已定案为自动回填；若当前有 running，切换前先提示或仅允许切历史只读展示当前跑仍在后台写盘 |
| 后端自动生成误伤分布式 | 仅 `From=="browser" && SequenceId==0` 时生成 |
| 多 tab 同时写 localStorage | `storage` 事件同步 order；或 last-write-wins + 启动时重新 load |

**已定案（2026-09-21）**：

1. 切换历史时表单 **自动回填**（载入该次 `request` 到表单控件）。
2. 单条曲线点数上限 **600**（超出丢弃最旧点，保持时间轴对齐）。
3. 增加 **清除历史** 按钮（清空 `http_bench.runs.v1`，重置 select / 图表状态）。

---

## 5. 建议实现顺序

1. 后端：SeqID 自动生成 + 响应字段 + 测试  
2. 前端：启动时序改造 + 底部 SeqID 展示（先不做 select 历史）  
3. 前端：`RunStore` + 写曲线到 localStorage  
4. 前端：select 切换恢复参数/时间/曲线  
5. i18n、边界（配额、多 tab）、手工验收清单  

---

## 6. 验收清单（实现阶段使用）

- [x] 新跑请求 body 中 `sequence_id` 为 0；响应带回非 0；底部显示一致  
- [x] 刷新页面后 select 仍能看到最近 ≤5 次  
- [x] 选中历史项可见：参数、发起时间、QPS/延迟/错误曲线  
- [x] 第 6 次新跑后最旧一条被淘汰  
- [x] 运行中切到历史再切回，实时曲线可继续（或行为符合 §2.4 文档）  
- [x] CLI `go test` / 现有 dashboard 启停冒烟通过  
- [x] 不传 `From=browser` 的 Start 不因 seq=0 改变既有语义（按守卫）  

---

## 7. 文档与交付物

| 交付物 | 说明 |
|--------|------|
| 本文 `docs/plan4.0-dashboard-seqid-history.md` | 计划正文 |
| 实现 PR（后续） | 分后端 / 前端 commit；暂不在本计划阶段提交代码 |

---

## 8. 执行记录

| 日期 | 内容 |
|------|------|
| 2026-09-21 | 撰写计划；明确「只出 plan、不写代码」 |
| 2026-09-21 | 定案：自动回填、曲线上限 600、清除历史按钮；开始实现 |
| 2026-09-21 | 后端 CollectResult.sequence_id + browser SeqID=0 生成；前端启动时序/RunStore/底部 Select/清除历史 |
