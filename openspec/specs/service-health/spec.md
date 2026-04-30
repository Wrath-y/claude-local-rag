# service-health

> `/health` 三态探活、`/metrics` Prometheus 端点、关键事件结构化日志。
> 首次引入: 2026-04-30 (change: health-metrics-observability)


### Requirement: /health 三态响应

`GET /health` SHALL 返回 `status` 字段取值之一：`"ok"` / `"degraded"` / `"error"`。判定顺序：`DATA_DIR` 磁盘可用字节 < 1GB → `error` 且 HTTP 503；索引正在 rebuild（`_index_rebuilding == True`）→ `degraded` + 原因 `"index rebuild in progress"`；`_wal_readonly_reason` 非 null（涵盖 WAL 坏行、索引维度不匹配、rebuild 触发等）→ `degraded` + 对应 reason；`_wal_replaying == True` → `degraded` + `"wal replay in progress"`；否则 `ok`。响应 MUST 保留字段 `total_chunks`、`rerank_enabled`、`verbose_enabled`、`wal_replaying`、`wal_readonly_reason`、`disk_free_bytes`，并新增 `index_rebuilding`（bool）与 `index_state`（同 `/index/status` 的 state 字段）。

> 首次引入: health-metrics-observability (2026-04-30)；index_rebuilding / index_state 字段由 index-self-healing (2026-04-30) 追加。

#### Scenario: 正常状态返回 ok

- **WHEN** 磁盘可用 > 1GB 且无 WAL 降级、无 rebuild、无 replay
- **AND** 调用 `GET /health`
- **THEN** 响应 HTTP 200
- **AND** body `status == "ok"`
- **AND** body `reason == null`
- **AND** body `index_rebuilding == false`

#### Scenario: rebuild 进行中返回 degraded

- **WHEN** 后台 rebuild 线程正在执行
- **AND** 调用 `GET /health`
- **THEN** 响应 HTTP 200
- **AND** body `status == "degraded"`
- **AND** body `index_rebuilding == true`

#### Scenario: WAL 坏行降级返回 degraded

- **WHEN** `_wal_readonly_reason` 为 `"wal corrupt at offset X"`
- **AND** 调用 `GET /health`
- **THEN** 响应 HTTP 200
- **AND** body `status == "degraded"`
- **AND** body `reason` 包含 `wal_readonly_reason` 的内容

#### Scenario: 磁盘不足返回 error + 503

- **WHEN** `shutil.disk_usage(DATA_DIR).free < 1GB`
- **AND** 调用 `GET /health`
- **THEN** 响应 HTTP 503
- **AND** body `status == "error"`
- **AND** body `reason` 提示磁盘不足

### Requirement: /metrics Prometheus 端点

`GET /metrics` SHALL 返回 Prometheus 文本格式（`Content-Type: text/plain; version=0.0.4`），包含以下指标：`rag_ingest_total{result}`、`rag_retrieve_total{hit}`、`rag_retrieve_latency_seconds`（histogram）、`rag_chunk_total`（gauge）、`rag_index_bytes`（gauge）、`rag_model_load_seconds`（gauge）、`rag_wal_replaying`（gauge 0/1）、`rag_last_commit_timestamp_seconds`（gauge）、`rag_reindex_progress_ratio`（gauge）、`rag_backup_total`（counter）、`rag_last_backup_timestamp_seconds`（gauge）。

> 首次引入: health-metrics-observability (2026-04-30)；`rag_reindex_progress_ratio` 由 index-self-healing 追加；`rag_backup_total` / `rag_last_backup_timestamp_seconds` 由 backup-restore-automation 追加。

#### Scenario: /metrics 返回可被 Prometheus 解析的文本

- **WHEN** 调用 `GET /metrics`
- **THEN** 响应状态码 200
- **AND** Content-Type 包含 `text/plain`
- **AND** body 包含 `rag_ingest_total`、`rag_retrieve_total`、`rag_chunk_total`、`rag_wal_replaying`、`rag_backup_total` 这几个指标名

#### Scenario: ingest 后 counter 递增

- **WHEN** 调用一次 `POST /ingest` 成功
- **AND** 随后调用 `GET /metrics`
- **THEN** `rag_ingest_total{result="ok"}` 至少为 1

#### Scenario: retrieve 后 counter 与 histogram 更新

- **WHEN** 调用一次 `POST /retrieve` 成功（已入库至少 1 条 chunk）
- **AND** 随后调用 `GET /metrics`
- **THEN** `rag_retrieve_total{hit="true"}` 至少为 1
- **AND** `rag_retrieve_latency_seconds_count` 至少为 1

#### Scenario: 备份成功后指标更新

- **WHEN** 调用一次 `POST /backup/run` 成功
- **AND** 随后调用 `GET /metrics`
- **THEN** `rag_backup_total` 至少为 1
- **AND** `rag_last_backup_timestamp_seconds` > 0

### Requirement: 结构化事件日志

服务 SHALL 提供 `structured_log(event: str, **kv)` 辅助函数，输出单行 JSON 至 stdout，包含字段 `ts`（ISO8601 UTC）、`event`、及调用方传入的任意 kv。以下关键事件 MUST 通过此函数输出：`ingest_done`、`retrieve_done`、`wal_replay_start`、`wal_replay_done`、`checkpoint_done`。

#### Scenario: ingest 成功发出结构化事件

- **WHEN** 调用 `POST /ingest` 返回成功
- **THEN** stdout 出现一行 JSON，`event == "ingest_done"`
- **AND** JSON 字段包含 `source`、`chunks_added`
- **AND** 该行可被 `json.loads` 直接解析

#### Scenario: retrieve 成功发出结构化事件

- **WHEN** 调用 `POST /retrieve` 返回成功
- **THEN** stdout 出现一行 JSON，`event == "retrieve_done"`
- **AND** JSON 字段包含 `hit`（bool）、`latency_ms`、`returned_chunks`
