## MODIFIED Requirements

### Requirement: /metrics Prometheus 端点

`GET /metrics` SHALL 返回 Prometheus 文本格式（`Content-Type: text/plain; version=0.0.4`），包含以下指标：`rag_ingest_total{result}`、`rag_retrieve_total{hit}`、`rag_retrieve_latency_seconds`（histogram）、`rag_chunk_total`（gauge）、`rag_index_bytes`（gauge）、`rag_model_load_seconds`（gauge）、`rag_wal_replaying`（gauge 0/1）、`rag_last_commit_timestamp_seconds`（gauge）、`rag_reindex_progress_ratio`（gauge）、`rag_backup_total`（counter）、`rag_last_backup_timestamp_seconds`（gauge）。

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
