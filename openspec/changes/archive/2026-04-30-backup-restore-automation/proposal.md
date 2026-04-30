## Why

目前只有手动 `/export` 和 `/import` 端点：无定时备份、无保留策略、无恢复前自动快照。生产场景下：磁盘损坏、误执行 `/reset`、WAL 坏行导致的人工截断操作都会丢数据。运维需要「备份是自动的、恢复是可回滚的」。P0 的最后一块：把 export/import 升级为完整的备份-恢复生命周期。

## What Changes

- 新增 `POST /backup/run`：立即打包 `chunks.pkl` + `index.bin` + `storage/manifest.json` + `storage/wal.jsonl` 为 zip，落盘 `backups/YYYY-MM-DD/rag-<HHMMSS>.zip`。同步返回 `{path, size_bytes}`。
- 新增 `GET /backup/list`：列出 `backups/` 下所有 zip 文件，按 mtime 倒序返回 `[{path, size_bytes, modified_at}]`。
- 新增 `POST /backup/restore`（body: `{file: "backups/.../xxx.zip", confirm: true}`）：
  1. 在持写锁 + 只读降级状态下执行
  2. 先将当前状态打包到 `backups/pre-restore-<ts>.zip`
  3. 解压目标包到临时目录，原子替换 `chunks.pkl` / `index.bin` / `manifest.json` / `wal.jsonl`
  4. 重新 `load_store()`
  5. 失败时自动回滚到 `pre-restore-<ts>.zip`
- 定时备份：新增 `APScheduler`（或自研简单 threading.Timer 循环）在 `lifespan` 启动时注册；cron 由配置 `storage.backup.schedule` 指定（默认 `0 3 * * *` 每日 3 点）。
- 保留策略：按 `storage.backup.retention` 配置（默认 `days: 7, weeks: 4`）自动清理旧备份；备份成功后或每天 0 点触发一次扫描。
- `/rag-status` 输出最近备份时间与数量；新增指标 `rag_last_backup_timestamp_seconds`、`rag_backup_total`。
- 新增 slash commands：`/rag-backup`（触发 POST /backup/run）、`/rag-restore`（交互式列出最近备份 + 确认 + 调 restore）。
- **MODIFIED**：`service-health` 的 `/metrics` 补充 `rag_last_backup_timestamp_seconds`、`rag_backup_total`；MODIFIED 描述点明这两个指标。

## Capabilities

### New Capabilities

- `backup-lifecycle`: 定时备份、保留策略、pre-restore 快照 + 原子恢复 + 失败回滚。

### Modified Capabilities

- `service-health`: `/metrics` 导出备份相关指标；非破坏性追加。

## Impact

- **代码**：`server.py` 新增端点 + scheduler 启动/停止；新增 `backup.py` 模块（打包 / 解包 / 保留策略）；`metrics.py` 新增两个指标；`requirements.txt` 可选加 `apscheduler`（默认用纯标准库 `threading.Timer` 实现轻量定时，避免新依赖）。
- **磁盘**：`backups/` 目录，按日期分层；`.gitignore` 加 `backups/`。
- **配置**：`config.yaml` 新增 `storage.backup.{enabled, schedule, retention: {days, weeks}}`。
- **存量**：首次启动创建空 `backups/`；现有 `/export`、`/import` 端点保留兼容（作为本 change 的薄包装）。
- **不覆盖**：备份加密、远端对象存储（S3 / OSS）、跨租户备份 —— 留给 P2 企业能力。
