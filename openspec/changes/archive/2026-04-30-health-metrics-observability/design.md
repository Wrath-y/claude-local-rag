## Context

`concurrent-safe-storage` + `wal-crash-recovery` 让服务端有了完整的数据一致性与崩溃恢复机制；下一个缺口是「运维侧怎么发现问题」。当前 `/health` 静态返回 ok，日志散落在 stdout 自由文本，没有 Prometheus 可抓的指标端点。生产部署需要三件事：可探活（三态 /health）、可抓指标（/metrics）、可接日志管道（结构化 stdout）。

## Goals / Non-Goals

**Goals:**
- `/health` 语义化：ok / degraded / error 三态映射到 200 / 200 / 503。
- `/metrics` 暴露写读次数、延迟直方图、存储规模、WAL 状态等。
- 关键路径（ingest、retrieve、WAL replay、checkpoint）输出结构化 JSON 行便于下游解析。
- 不引入 C 扩展依赖；部署方式不变。

**Non-Goals:**
- 全量结构化日志改造（保留现有 `print(_t(...))` 不动）。
- 接入 Grafana / Alertmanager 配置。
- OpenTelemetry trace / 分布式追踪。
- 监控鉴权（`/metrics` 默认无鉴权，P2 再补）。

## Decisions

### 决策 1：/health 判定顺序

**选择**：
1. 磁盘可用 <1GB → `error` + HTTP 503
2. `_wal_readonly_reason` 非 None → `degraded` + HTTP 200
3. `_wal_replaying` == True → `degraded` + HTTP 200
4. 否则 → `ok` + HTTP 200

error 条件只挑不可承接新写的情况；degraded 表示「能检索、但写路径受限或数据在恢复」。

**替代方案**：
- 统一 200 只用 body 区分 — 不符合常见负载均衡/探活工具的 HTTP 码语义。
- error 条件扩展到「索引损坏」—— 归 `index-self-healing`，本 change 不包含。

### 决策 2：Prometheus metrics 命名

**选择**：前缀 `rag_`，遵循官方命名规范（`_total` 结尾计数、`_seconds` 单位、gauge 无后缀）。核心指标：

| 名称 | 类型 | 标签 | 含义 |
|------|------|------|------|
| `rag_ingest_total` | Counter | `result: ok\|skip\|error` | ingest 次数 |
| `rag_retrieve_total` | Counter | `hit: true\|false` | retrieve 次数（hit=是否返回 >=1 chunk） |
| `rag_retrieve_latency_seconds` | Histogram | — | 整条 retrieve 耗时 |
| `rag_chunk_total` | Gauge | — | 当前 chunks 数量 |
| `rag_index_bytes` | Gauge | — | index.bin 文件大小 |
| `rag_model_load_seconds` | Gauge | — | 模型加载耗时（启动时一次性测） |
| `rag_wal_replaying` | Gauge | — | 0/1 |
| `rag_last_commit_timestamp_seconds` | Gauge | — | unix 秒，来自 manifest.committed_at |

**替代方案**：
- 把 embedding/search/rerank 分阶段拆进 histogram label — 增加卡片量且对小规模意义有限，放弃。

### 决策 3：结构化日志通道

**选择**：新增 `structured_log(event, **kv)` 辅助，统一走 `print(json.dumps(...))` 到 stdout。只覆盖 ingest 完成、retrieve 完成、wal replay 开始/结束、checkpoint 执行这几个「指标事件」；现有 `_t("store_loaded", ...)` 等日志保持不变。

**替代方案**：
- 替换整个 `print` 流 — 牵一发动全身，本 change 不做。
- 写文件日志 — 容器/systemd 都消费 stdout，不用另起文件。

### 决策 4：metric 的收集点

**选择**：
- `/ingest` 处理末尾（含 skip 分支）更新 `rag_ingest_total` 与 `rag_chunk_total` / `rag_index_bytes`。
- `/retrieve` 处理末尾更新 `rag_retrieve_total` 与 observe histogram；用 `time.perf_counter()` 在入口打点。
- `_maybe_checkpoint` 成功后更新 `rag_last_commit_timestamp_seconds`（从 manifest 取）。
- `_replay_wal_if_needed` 进入/退出修改 `rag_wal_replaying` gauge。
- 启动时 model load 耗时写入 `rag_model_load_seconds`。

**替代方案**：
- 用 FastAPI 中间件统一埋点 — 需要对每条路由写 per-route label，耦合大。直接在已知端点函数里打点更简单。

### 决策 5：磁盘低于 1GB 判定

**选择**：`shutil.disk_usage(DATA_DIR).free < 1024**3`。阈值写进 module 常量 `DISK_FREE_ERROR_BYTES = 1 * 1024**3`，便于后续 PR 调参。

**替代方案**：
- 按百分比（如剩余 <5%）—— 大容量盘上 5% 可能还有几百 GB，未必是 error。

## Risks / Trade-offs

- **[风险] prometheus-client 全局 registry 在 pytest 多次 import 时会冲突** → 缓解：metrics 模块把定义放在 `if not already_registered` 保护下；测试用独立 registry 或 `prometheus_client.CollectorRegistry` 手动隔离。
- **[权衡] `/health` 返回 error+503 会让负载均衡把实例摘下，但磁盘满场景本来就该摘** → 预期行为。
- **[权衡] structured_log 输出 JSON 行，与现有自由文本日志混在 stdout** → 下游可按行首 `{` 过滤；后续（P2）可迁移全部 logging。
- **[风险] `rag_model_load_seconds` 在 lifespan 之外无合适测点** → 在 `load_store` 之前计时（仅在实际启动时触发），单测通过 monkeypatch 覆盖。

## Migration Plan

1. 部署新版本后，调用方继续看到 `/health` 返回 200，新字段 `disk_free_bytes` 可选消费。
2. 磁盘满场景会开始返回 503，运维提前准备告警规则（非本 change 范围）。
3. 回滚策略：`/health` 对现有调用方向后兼容；`/metrics` 端点可被直接忽略；不需要回滚磁盘数据。

## Open Questions

1. `rag_retrieve_latency_seconds` 的 bucket 边界是否需要按 p50/p95/p99 调整？
   - 预案：使用 prometheus-client 默认 bucket，观察一周后按需调。
2. 是否需要把 WAL 坏行事件上报为独立 counter？
   - 本 change 用 `rag_wal_replaying` + `/health.wal_readonly_reason` 组合覆盖，单独 counter 留给后续。
