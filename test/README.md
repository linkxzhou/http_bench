# test/ — 验证与辅助资产

统一用 `verify_*.sh` 命名；一键入口：

```bash
bash test/verify_all.sh              # 布局 / internal / 门禁 / CLI 冒烟 / flag 矩阵
bash test/verify_all.sh --full       # 额外跑根包 go test -race -short
bash test/verify_all.sh --with-long  # 额外长时多协议压测（DURATION 默认 30）
bash test/verify_all.sh --only flags
```

| 脚本 | 作用 |
|------|------|
| `common.sh` | 共享 PASS/FAIL、颜色、`resolve_bench` |
| `verify_plan3_checklist.sh` | 对照 `docs/plan3.0.md` 布局与符号 |
| `verify_internal_packages.sh` | 无旧子包 import、internal 可编 |
| `verify_build_gate.sh` | gofmt / build / vet / race |
| `verify_cli_smoke.sh` | 快速 CLI 冒烟 |
| `verify_flags.sh` | 全 flag 行为矩阵 |
| `verify_long_benchmark.sh` | http1/2/3/ws 长时冒烟 |
| `long_benchmark.sh` | 兼容旧路径，转调 `verify_long_benchmark.sh` |
| `verify_all.sh` | 统一入口 |

其它：`*.http`、证书、`servers_test.go`（给根包集成测试用的辅助进程）。
