# write-ahead-log

> 写前日志（WAL）：append、启动 replay、checkpoint、坏行只读降级。
> 首次引入: 2026-04-30 (change: wal-crash-recovery)


### Requirement: 写前日志 append

所有修改 chunks 或 FAISS 索引的操作（`ingest`、`delete_source`、`reset`）SHALL 在持有写锁之后、调用 `save_store` 之前，向 `storage/wal.jsonl` append 一条记录并 fsync。记录 MUST 包含字段 `seq`（单调递增整数）、`ts`（ISO8601 UTC）、`op`（`ingest`/`delete_source`/`reset`）、`payload`（操作的原始入参，如 `ingest` 的 `text` 与 `source`）、`crc32`（除 `crc32` 本身外所有字段的 JSON 串的 CRC32 十六进制值）。

#### Scenario: ingest 的写前日志先于 save_store

- **WHEN** 客户端发起一次 `/ingest`
- **AND** 在 `save_store` 执行中途进程被强杀
- **THEN** `storage/wal.jsonl` 已包含该 ingest 的完整一行
- **AND** 行末 `crc32` 可被独立校验

#### Scenario: WAL append 失败拒绝继续

- **WHEN** `wal.jsonl` 所在磁盘空间耗尽导致 append 失败
- **THEN** 对应写请求返回 5xx
- **AND** chunks/index 磁盘状态不发生任何变化

### Requirement: 启动 WAL replay

服务启动阶段在清理孤儿临时文件之后、完成 chunks/index 一致性校验与 embedding cache 重建之前，SHALL 读取 `wal.jsonl` 从 `manifest.wal.committed_offset` 起的所有行。对每一行解析、校验 CRC32、通过写锁内调用对应的内部操作执行 replay。全部 replay 成功后 MUST 执行一次 `save_store` 并更新 manifest；随后对 `wal.jsonl` 原子截断为空。

#### Scenario: 单条未提交记录成功 replay

- **WHEN** 上次运行时 ingest 已 append WAL 但 `save_store` 途中崩溃
- **AND** 重启服务
- **THEN** 启动日志打印 `[wal] replaying 1 op from offset X`
- **AND** replay 完成后 chunks/index 数量等于该 ingest 应当添加的数量
- **AND** `wal.jsonl` 被截断为 0 字节
- **AND** `manifest.wal.committed_offset == 0`

#### Scenario: 无待 replay 时跳过

- **WHEN** 上次正常关闭，`wal.jsonl.size == manifest.wal.committed_offset`
- **AND** 启动服务
- **THEN** 不执行 replay，启动继续正常流程

### Requirement: Checkpoint 与截断

满足下列任一条件 SHALL 触发一次 checkpoint（执行一次 `save_store` + manifest 更新 + WAL 原子截断）：

1. WAL 文件字节数超过 `storage.wal.max_size_mb * 1024 * 1024`（默认 10MB）
2. 启动阶段的 replay 成功完成之后
3. 服务正常关闭（lifespan 退出钩子）

截断 MUST 通过「写空 `wal.jsonl.new` → `os.replace` → 目录 fsync」原子完成，MUST NOT 直接 truncate 原文件。

#### Scenario: 超过阈值触发 checkpoint

- **WHEN** `storage.wal.max_size_mb = 1`
- **AND** 累计 ingest 使 WAL 文件达到 1.1MB
- **THEN** 在当前 ingest 完成 save_store 后自动触发 checkpoint
- **AND** checkpoint 完成后 `wal.jsonl.size == 0`
- **AND** `manifest.wal.committed_offset == 0`
- **AND** 后续 ingest 正常继续 append

### Requirement: WAL 坏行容忍与只读降级

解析 WAL 时若某一行的 `crc32` 校验失败或 JSON 格式非法，SHALL 停止继续 replay，记录坏行起始 offset 为 `truncation_at`。若 `truncation_at < wal.jsonl.size`（即坏行之后还有未 replay 的数据），服务 SHALL 进入「只读降级」状态：检索路径（`/retrieve`、`/sources`、`/stats`、`/storage/integrity-check`）正常服务；写路径（`/ingest`、`/source` DELETE、`/reset`）MUST 返回 HTTP 503 + 说明要求人工介入。

#### Scenario: 断电导致末行半写

- **WHEN** `wal.jsonl` 末尾有一行 JSON 被截断（模拟断电写入一半）
- **AND** 启动服务
- **THEN** replay 处理到该行时停止，不继续读取该行之后的任何字节
- **AND** 已 replay 成功的前缀行全部生效
- **AND** 服务进入只读降级，`/ingest` 返回 503

#### Scenario: 只读降级下检索仍可用

- **WHEN** 服务处于 WAL 坏行导致的只读降级
- **AND** 调用 `/retrieve`
- **THEN** 基于 replay 成功前缀的 chunks/index 返回正常检索结果

### Requirement: 配置开关

`config.yaml` SHALL 支持 `storage.wal.enabled`（默认 `true`）与 `storage.wal.max_size_mb`（默认 `10`）两项配置。`storage.wal.enabled = false` 时，写路径不 append WAL，启动也不做 replay，manifest 的 `wal.committed_offset` 固定为 `0`，行为回退到 `concurrent-safe-storage` 级别。

#### Scenario: 关闭 WAL 时不落日志不 replay

- **WHEN** `storage.wal.enabled = false`
- **AND** 发起一次 ingest
- **THEN** `storage/wal.jsonl` 文件大小不变或不存在
- **AND** save_store 路径与 concurrent-safe-storage 一致

### Requirement: replay 期间健康信号

服务 replay WAL 期间 `/health` 响应 SHALL 包含 `wal_replaying: true`；replay 完成后或无 replay 任务时 SHALL 为 `false`。

#### Scenario: replay 进行中 /health 指示 replaying

- **WHEN** 启动过程中 replay 有 10 条待执行的 ingest
- **AND** replay 进行到第 5 条时发起 `GET /health`
- **THEN** 响应体包含 `"wal_replaying": true`

#### Scenario: 无 replay 时 /health 指示空闲

- **WHEN** 启动完成且 `wal.jsonl` 为空
- **AND** 发起 `GET /health`
- **THEN** 响应体包含 `"wal_replaying": false`
