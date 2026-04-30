## Why

经过 `concurrent-safe-storage` 后，写路径已做到「save_store 单次调用要么成功、要么不破坏旧状态」，但整条请求链路仍有一段窗口是不可恢复的：服务端已完成 chunking / embedding、正准备调用 `save_store` 时，如果进程崩溃，embedding 算力白白浪费，客户端超时重试虽然有 `source_hash` 幂等兜底，但大文档场景会再跑一次昂贵的 embedding。更一般地，当前「要么全成功要么丢」的所有 - or - nothing 模型缺乏一条可由服务端自行重放的恢复路径，也无法在后续支持批量提交、异步落盘等演进。

Write-Ahead Log 在写路径最前端记录不可丢的「意图」，崩溃后启动阶段自动 replay 至最后一次成功落盘之后的那些操作，实现「任意时刻强杀进程，重启后数据与最后一次成功响应一致」。

## What Changes

- 新增 `storage/wal.jsonl` 作为 append-only 写前日志，每条记录包含 `op_type` / `payload` / `timestamp` / `crc32`；支持的操作类型：`ingest` / `delete_source` / `reset`。
- 写路径改造：每个写操作在持写锁后，先 `wal.append(op)` 并 fsync，再执行原有的 embedding / `save_store`。成功落盘后更新 manifest 新增字段 `wal.committed_offset`。
- 启动阶段在 `cleanup_orphan_tempfiles` 之后、manifest 校验之前，读取 WAL：如果文件末尾 offset > `manifest.wal.committed_offset`，按顺序 replay 待提交的操作，replay 完成后写一次 checkpoint（`save_store` + manifest 更新 + WAL 截断）。
- Checkpoint 策略：WAL 体积超过阈值（默认 `storage.wal.max_size_mb = 10`）时，落盘后同步 checkpoint 并截断 WAL；正常关闭时也触发一次。
- 坏行容忍：单行 CRC 校验失败 → 放弃该行及其之后所有行，记录错误并进入只读降级（避免错误操作被误 replay）。
- **MODIFIED**：`storage-integrity` 的 manifest 结构与启动校验需要感知 `wal.committed_offset` 字段。
- 可配置项 `storage.wal.enabled`（默认 `true`）、`storage.wal.max_size_mb`。

## Capabilities

### New Capabilities

- `write-ahead-log`: 写前日志 append、启动 replay、checkpoint/truncation、CRC 坏行容忍。

### Modified Capabilities

- `storage-integrity`: manifest 新增 `wal.committed_offset` 字段；启动顺序在 tempfile 清理之后、chunks/index 一致性校验之前插入 WAL replay 阶段。

## Impact

- **代码**：`server.py` 的 `ingest` / `delete_source` / `reset` 在持锁后先 `wal.append`；`load_store` 增加 replay 环节；新增 `storage/wal.py` 模块（append / read / truncate / crc32 行编码）；`storage.py` 中 `ManifestV1` + `verify_manifest` 加上 `wal.committed_offset` 字段与 I/O。
- **磁盘**：新增 `storage/wal.jsonl`（运行时生成，`.gitignore` 已包含 `storage/`）；临时文件 `wal.jsonl.tmp`（checkpoint 时使用）。
- **配置**：`config.yaml` 新增 `storage.wal.max_size_mb`（默认 10）与 `storage.wal.enabled`（默认 true）。
- **依赖**：仅使用 Python 标准库（`zlib.crc32`、`os.fsync`、`json`），不引入新第三方依赖。
- **向后兼容**：无 WAL 文件的存量部署首次启动自动生成空 WAL；manifest 缺失 `wal.committed_offset` 时按 0 处理；WAL 可通过配置关闭回退到旧行为。
- **性能影响**：每次写操作额外一次小 JSONL append + fsync，预计 <5ms；embedding / save_store 主导耗时不变。
- **为后续 change 打底**：`index-self-healing` 的「只读降级」可复用本 change 引入的降级状态机；`backup-restore-automation` 的备份包将同时包含 chunks + index + manifest + WAL。
