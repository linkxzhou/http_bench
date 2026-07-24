# Refactor Notes (2026-07)

This file tracks the major refactor outcomes implemented across phases A–H of
[plan.md](../plan.md) and the secondary cleanup of [plan2.0.md](../plan2.0.md).

## Module Layout (post-plan2.0)

```
http_bench/
├── main package
│   ├── http_bench.go      — main(), runBenchmark, runDashboardServer, handleStartup
│   ├── cli_options.go     — ParseConfig (testable CLI parser)
│   ├── const.go           — usage/examples strings, defaults
│   ├── util.go            — genSequenceId, template re-exports, usageAndExit
│   ├── validate.go        — validateParams, validateOutputFormat (+ merged input_parser)
│   ├── http_worker.go     — HttpbenchWorker (Runner impl), Stop, Run
│   ├── transport_integration_test.go  — HTTP/1-3 + WS subprocess stress tests
│   ├── metrics_integration_test.go    — CollectResult snapshot/merge integration tests
│   └── util_test.go / validate_test.go / request_spec_test.go / cli_test.go
└── internal/
    ├── dashboard/    — http.Server with signal-aware graceful Shutdown
    ├── distributed/  — handler.go (DTOs + WorkerService + ServeRequest + controller.go)
    ├── limiter/      — global QPS limiter (token bucket)
    ├── logging/      — leveled log + slog.Handler adapter, race-free
    ├── metrics/      — result.go (CollectResult, Result, Record, Snapshot, Merge),
    │                  report_snapshot.go, result_test.go (bench + JSON tag compat)
    ├── report/       — Reporter interface (Text/CSV/HTML) over Snapshot DTO
    ├── request/      — parser.go (.http file parser + Spec.MergeDefaults)
    ├── templatefn/   — funcs.go (registry), data.go (JSON+URL fns), random.go, string.go, etc.
    ├── transport/    — client.go (Client, ClientOpts, HttpbenchParameters, Sender),
    │                  protocols.go (HTTP/1+2+3+WebSocket constructors), tls_config.go,
    │                  util.go, client_test.go, sender_test.go
    └── (removed) app/ and benchmark/ — dead-code packages deleted per plan2.0.md §3.1
```

### Removed packages

| Package | Reason |
|---------|--------|
| `internal/app` | Never imported by `main`; only referenced by `internal/benchmark`. Dead code. |
| `internal/benchmark` | Never imported by `main`; only referenced by `internal/app`. Dead code. |

### Merged files (plan2.0.md §2)

| Before | After | Rationale |
|--------|-------|-----------|
| `transport/http1.go` `http2.go` `http3.go` `websocket.go` | `transport/protocols.go` | All <65 lines; co-located as protocol constructors. |
| `distributed/types.go` | `distributed/handler.go` | Types + handler in one file; <135 lines total. |
| `request/spec.go` | `request/parser.go` | Spec type + parser in one file; <160 lines. |
| `templatefn/json.go` `url.go` | `templatefn/data.go` | Structured-text fns co-located; <130 lines. |
| `metrics/benchmark_test.go` `json_compat_test.go` | `metrics/result_test.go` | Bench + JSON tag tests merged. |
| `test/echotcp_test.go` `http3client_test.go` `httpsvr_test.go` | `test/servers_test.go` | Subprocess server helpers merged. |

## Key Contracts

- **`HttpbenchWorker`** (in `http_worker.go`) is the runtime lifecycle
  implementation. `Run(ctx, params) (*CollectResult, error)` is the only
  public surface. The `WorkerRunner` interface in `internal/distributed`
  decouples the HTTP handler from this concrete type; `defaultRunner` in
  `http_bench.go` bridges the two.

- **`CircuitBreakerPolicy`** lives in `internal/metrics` and is hot-swappable
  via `SetCircuitBreakerPolicy` (default: `MinSamples=10`,
  `ThresholdPercent=50`). The default avoids aborting a run on the first
  cold-start failure.

- **`Reporter`** (in `internal/report`) writes a `Snapshot` DTO. The DTO
  decouples the package boundary so `internal/metrics → internal/report`
  never forms a cycle; `CollectResult.ToReportSnapshot()` performs the
  deep copy under the result's RLock.

- **`logging.Slog()`** returns a `*slog.Logger` that respects the same
  threshold as the legacy leveled functions, so third-party libraries that
  already accept an `*slog.Logger` can plug in without changing the
  verbosity semantics.

## Naming Conventions (plan2.0.md §4)

### Field renames (Go identifiers only; JSON tags preserved)

| Old | New | JSON tag |
|-----|-----|----------|
| `HttpbenchParameters.Url` | `HttpbenchParameters.URL` | `json:"url"` |
| `HttpbenchParameters.ProxyUrl` | `HttpbenchParameters.ProxyURL` | `json:"proxy_url"` |
| `HttpbenchParameters.Qps` | `HttpbenchParameters.QPS` | `json:"qps"` |

### Other naming fixes

- `cmdMetrics` (local const in `const.go`) → `transport.CmdMetrics` (enum in
  `transport/client.go` for cohesion with `CmdStart`/`CmdStop`).
- `genSequenceId(_ int)` → `genSequenceId()` — unused parameter removed.
- `workerAddrList`, `httpWorkerApiAuthKey`, `httpWorkerApiPath` (package-level
  mutable globals) → `distConfig` struct passed by value from `main()` to
  `handleStartup` / `handleDistributedWorkers` / `runDashboardServer`.
- `getEnv()` helper removed (sole caller was the deleted global var block).
- Test files renamed to reflect their actual scope:
  `http_client_test.go` → `transport_integration_test.go`,
  `http_client_result_test.go` → `metrics_integration_test.go`.

## Performance Snapshot

```
BenchmarkCollectResultRecord   ~14 ns/op   0 B/op   0 allocs/op
BenchmarkCollectResultSnapshot ~260 ns/op  848 B/op 7 allocs/op
```

The snapshot allocates once per `Snapshot()` call (eight map fields plus a
~850-byte result struct) and is a single read-lock acquisition; callers
holding the snapshot can iterate without further synchronisation.

## CLI

`ParseConfig(args, getenv, stderr) (Options, error)` is the entry point for
tests that need to exercise the CLI surface. It does not touch `os.Args`,
`os.Exit`, or global state, satisfying plan §B verification ("the CLI parser
is testable without the process-global environment").

## Long-Run Scripts

- `long_benchmark.sh` — drives HTTP/1, HTTP/2, HTTP/3, and WebSocket against
  a configurable target for a configurable duration; gracefully continues
  when a protocol is unavailable.
