## MODIFIED Requirements

### Requirement: /health 三态响应

`GET /health` SHALL 返回 `status` 字段取值之一：`"ok"` / `"degraded"` / `"error"`。判定顺序：`DATA_DIR` 磁盘可用字节 < 1GB → `error` 且 HTTP 503；索引正在 rebuild（`_index_rebuilding == True`）→ `degraded` + 原因 `"index rebuild in progress"`；`_wal_readonly_reason` 非 null（涵盖 WAL 坏行、索引维度不匹配、rebuild 触发等）→ `degraded` + 对应 reason；`_wal_replaying == True` → `degraded` + `"wal replay in progress"`；否则 `ok`。响应 MUST 保留字段 `total_chunks`、`rerank_enabled`、`verbose_enabled`、`wal_replaying`、`wal_readonly_reason`、`disk_free_bytes`，并新增 `index_rebuilding`（bool）与 `index_state`（同 `/index/status` 的 state 字段）。

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

- **WHEN** `_wal_readonly_reason` 为 `"wal corrupt at offset X"` 且无 rebuild 在进行
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
