## 1. Backup 模块

- [x] 1.1 新增 `backup.py`：`make_backup(src_paths, dst_zip)` 打包四件套；`restore_from(zip_path, staging_dir)` 解压返回文件清单
- [x] 1.2 新增 `list_backups(backups_dir) -> List[dict]`：扫描 `.zip` 文件，返回 `[{path, size_bytes, modified_at}]`
- [x] 1.3 新增 `prune(backups_dir, days, weeks)`：按保留策略分桶保留；跳过 `pre-restore-*.zip`
- [x] 1.4 新增 `parse_cron(expr) -> NextFireFn`：支持 `M H * * *`、`*/N`、`*` 基础语法；返回 `next_fire(from_ts) -> timestamp`

## 2. 配置与指标

- [x] 2.1 `config.yaml` 新增 `storage.backup.{enabled, schedule, retention: {days, weeks}}`
- [x] 2.2 `server.py` 读取上述配置：`BACKUP_ENABLED`、`BACKUP_CRON`、`BACKUP_RETENTION_DAYS`、`BACKUP_RETENTION_WEEKS`、`BACKUPS_DIR = _dir/backups`
- [x] 2.3 `metrics.py` 新增 `backup_total: Counter`、`last_backup_timestamp_seconds: Gauge`

## 3. 核心流程

- [x] 3.1 `_backup_run_core(name_override=None)`：持写锁 → 构造目标 zip 路径（默认 `backups/YYYY-MM-DD/rag-HHMMSS.zip`）→ `backup.make_backup` → 更新指标 + `structured_log("backup_done", ...)`
- [x] 3.2 `_restore_core(zip_path)`：写锁 + 设 readonly reason → 生成 pre-restore zip → 解压 staging → 原子替换四件套 → `load_store()` → 失败回滚
- [x] 3.3 `_prune_backups()`：调用 `backup.prune`；在 backup 完成后调用一次

## 4. 端点

- [x] 4.1 `POST /backup/run` → `_backup_run_core()` → 返回 `{status, path, size_bytes}`
- [x] 4.2 `GET /backup/list` → `backup.list_backups(BACKUPS_DIR)`
- [x] 4.3 `POST /backup/restore` body `{file, confirm}` → 校验 confirm/file 存在 → `_restore_core(file)`

## 5. 定时任务

- [x] 5.1 `_schedule_next_backup()`：按 cron 算下次时间，注册 `threading.Timer` 回调；回调内再次调度（链式）
- [x] 5.2 `lifespan` 启动阶段：`storage.backup.enabled` → 调 `_schedule_next_backup()`；同时立即触发一次 `_prune_backups()`
- [x] 5.3 `lifespan` 关闭阶段：取消当前未触发的 timer

## 6. `/rag-status` 信息

- [x] 6.1 `/rag-status` 命令展示最近备份时间与数量（通过 `GET /backup/list` 取）

## 7. 斜杠命令

- [x] 7.1 新增 `.claude/commands/rag-backup.md`：调 `POST /backup/run`，展示结果
- [x] 7.2 新增 `.claude/commands/rag-restore.md`：先列出最近 10 个备份，让用户选择，带确认再调 `POST /backup/restore`

## 8. 测试

- [x] 8.1 `tests/backup-restore-automation/test_backup_run.py`：POST /backup/run → 返回 200，目标 zip 存在且包含 4 个成员
- [x] 8.2 `test_backup_list.py`：先 POST /backup/run 两次 → GET /backup/list 返回 2 项且按 modified_at 倒序
- [x] 8.3 `test_backup_restore.py`：备份 → 改动数据 → restore → 数据恢复到备份时的状态；pre-restore zip 存在
- [x] 8.4 `test_restore_failure_rollback.py`：构造非法 zip 让 load_store 失败，确认自动回滚到 pre-restore
- [x] 8.5 `test_retention_prune.py`：造 15 个假 zip（mtime 分布 30 天），调用 prune → 保留数 <=11 且 pre-restore 保留
- [x] 8.6 `test_cron_parser.py`：`parse_cron("0 3 * * *")` + `parse_cron("*/15 * * * *")` 给定时间点计算下次触发正确
- [x] 8.7 `test_backup_metrics.py`：POST /backup/run → `/metrics` 显示 `rag_backup_total >= 1` 且 `rag_last_backup_timestamp_seconds > 0`

## 9. 文档与清理

- [x] 9.1 `.gitignore` 加入 `backups/`
- [x] 9.2 `CLAUDE.md` 新增「备份与恢复」小节
- [x] 9.3 `tests/README.md` 补 `backup-restore-automation/`
