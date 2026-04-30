## Why

当前 `/health` 仅返回静态 `status: ok`，无请求耗时、QPS、索引规模、模型加载等运行时指标；日志是自由文本难以被下游工具消费；故障靠人工看日志。生产/团队部署场景下无法接 Prometheus + Alertmanager，也无法快速判断降级原因（已经区分 `wal_replaying` / `wal_readonly_reason` 但粒度仍粗）。本 change 把 `/health` 扩展为三态健康探针，新增 `/metrics` Prometheus 端点，并为关键写读路径埋入结构化事件日志。

## What Changes

- `/health` 响应扩展为 `status: "ok" | "degraded" | "error"` 三态。判定顺序：磁盘可用 <1GB 或存储一致性异常 → `error`（HTTP 503）；WAL 只读降级或 embedding 模型加载中 → `degraded`（HTTP 200）；否则 → `ok`（HTTP 200）。保留已有字段（`total_chunks`、`rerank_enabled`、`verbose_enabled`、`wal_replaying`、`wal_readonly_reason`）。
- 新增 `GET /metrics` 端点返回 Prometheus 文本格式：`rag_ingest_total`、`rag_retrieve_total`、`rag_retrieve_latency_seconds`（histogram）、`rag_chunk_total`（gauge）、`rag_index_bytes`（gauge）、`rag_model_load_seconds`（gauge）、`rag_wal_replaying`（gauge 0/1）、`rag_last_commit_timestamp_seconds`（gauge，取自 manifest.committed_at）。
- 引入 `prometheus-client` 依赖，在 `requirements.txt` 显式声明。
- 提供 `structured_log(event: str, **kv)` 辅助：输出单行 JSON 到 stdout，字段含 `ts`、`event`、任意 kv；只在新引入的关键事件使用（ingest / retrieve / replay / checkpoint），不重写现有 `print` 输出，避免扰动已有日志约定。
- `/rag-status` 斜杠命令文档更新，描述三态判定与 metrics 端点。
- **MODIFIED**：`storage-integrity` 的 `/storage/integrity-check` 响应维度加一个 `disk_free_bytes` 字段用于辅助监控（非破坏性）；`/health` 新三态契约形成新的 capability `service-health`。

## Capabilities

### New Capabilities

- `service-health`: `/health` 三态契约、`/metrics` Prometheus 端点、关键事件结构化日志。

### Modified Capabilities

- `storage-integrity`: `/storage/integrity-check` 响应新增 `disk_free_bytes` 字段（纯加法）。

## Impact

- **代码**：`server.py` 的 `/health` 改造、新增 `/metrics`、在 ingest/retrieve 埋 metric 与事件日志；新增 `metrics.py` 模块集中管理 prometheus 定义；`requirements.txt` + `requirements-dev.txt` 加 `prometheus-client`。
- **磁盘检测**：使用 `shutil.disk_usage(DATA_DIR)` 判断低于 1GB；无新文件。
- **兼容性**：已有 `/health` 调用者看到的字段全部保留，`status` 仍可能为 `ok`；新状态 `degraded` / `error` 只在发生对应条件时出现。
- **依赖**：`prometheus-client >= 0.20` 纯 Python 包，不带 C 扩展。
- **不涉及**：告警规则 / Grafana 配置 / OpenTelemetry trace（留给 P2 或专门的运维 change）。
- **为后续 change 打底**：`index-self-healing` 的 `/health` → `degraded`/`error` 语义由本 change 定义；`backup-restore-automation` 的 `rag_last_backup_timestamp_seconds` metric 可在本 change 的基础上直接添加。
