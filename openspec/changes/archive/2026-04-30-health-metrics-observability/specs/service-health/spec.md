## ADDED Requirements

### Requirement: /health 三态响应

`GET /health` SHALL 返回 `status` 字段取值之一：`"ok"` / `"degraded"` / `"error"`。判定顺序：`DATA_DIR` 磁盘可用字节 < 1GB → `error` 且 HTTP 503；`_wal_readonly_reason` 非 null → `degraded` 且 HTTP 200；`_wal_replaying == True` → `degraded` 且 HTTP 200；否则 `ok` 且 HTTP 200。响应 MUST 保留字段 `total_chunks`、`rerank_enabled`、`verbose_enabled`、`wal_replaying`、`wal_readonly_reason`，并新增 `disk_free_bytes`、`reason`（当 status != ok 时说明原因）。

#### Scenario: 正常状态返回 ok

- **WHEN** 磁盘可用 > 1GB 且无 WAL 降级或 replay 中
- **AND** 调用 `GET /health`
- **THEN** 响应 HTTP 200
- **AND** body `status == "ok"`
- **AND** body `reason == null`

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

`GET /metrics` SHALL 返回 Prometheus 文本格式（`Content-Type: text/plain; version=0.0.4`），包含以下指标：`rag_ingest_total{result}`、`rag_retrieve_total{hit}`、`rag_retrieve_latency_seconds`（histogram）、`rag_chunk_total`（gauge）、`rag_index_bytes`（gauge）、`rag_model_load_seconds`（gauge）、`rag_wal_replaying`（gauge 0/1）、`rag_last_commit_timestamp_seconds`（gauge）。

#### Scenario: /metrics 返回可被 Prometheus 解析的文本

- **WHEN** 调用 `GET /metrics`
- **THEN** 响应状态码 200
- **AND** Content-Type 包含 `text/plain`
- **AND** body 包含 `rag_ingest_total`、`rag_retrieve_total`、`rag_chunk_total`、`rag_wal_replaying` 这几个指标名

#### Scenario: ingest 后 counter 递增

- **WHEN** 调用一次 `POST /ingest` 成功
- **AND** 随后调用 `GET /metrics`
- **THEN** `rag_ingest_total{result="ok"}` 至少为 1

#### Scenario: retrieve 后 counter 与 histogram 更新

- **WHEN** 调用一次 `POST /retrieve` 成功（已入库至少 1 条 chunk）
- **AND** 随后调用 `GET /metrics`
- **THEN** `rag_retrieve_total{hit="true"}` 至少为 1
- **AND** `rag_retrieve_latency_seconds_count` 至少为 1

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
