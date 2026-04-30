## ADDED Requirements

### Requirement: POST /backup/run

`POST /backup/run` SHALL 在持写锁的前提下把 `chunks.pkl`、`index.bin`、`storage/manifest.json` 以及（若存在）`storage/wal.jsonl` 打包为 zip，落盘到 `backups/YYYY-MM-DD/rag-HHMMSS.zip`（本地时间）。端点 MUST 同步返回 `{status: "ok", path: str, size_bytes: int}`。失败 MUST 返回 5xx 且不留下半写文件。

#### Scenario: 正常备份成功

- **WHEN** 调用 `POST /backup/run`
- **THEN** 响应 HTTP 200
- **AND** body `status == "ok"`
- **AND** body `path` 指向一个实际存在的 zip 文件
- **AND** zip 内部包含 `chunks.pkl` 与 `index.bin`

### Requirement: GET /backup/list

`GET /backup/list` SHALL 扫描 `backups/` 目录下所有 `.zip` 文件，返回 `[{path, size_bytes, modified_at}]` 列表，按 `modified_at` 倒序排列。列表 MUST NOT 包含 `backups/pre-restore-*.zip` 之外的元数据文件。

#### Scenario: 列出最近备份

- **WHEN** 已有两次 `POST /backup/run` 成功
- **AND** 调用 `GET /backup/list`
- **THEN** 响应 body 长度 >= 2
- **AND** 首条 `modified_at` >= 次条

### Requirement: POST /backup/restore

`POST /backup/restore` body `{file: str, confirm: bool}` SHALL 按如下事务执行：
1. 持写锁、设 `_wal_readonly_reason = "restore in progress"`；
2. 先调用备份核心逻辑生成 `backups/pre-restore-<ts>.zip`；
3. 解压目标 zip 到临时目录；
4. 原子替换 `chunks.pkl` / `index.bin` / `manifest.json` / `wal.jsonl`；
5. 调 `load_store()` 重新加载；
6. 加载成功 → 清空 readonly reason；加载失败 → 从 pre-restore zip 自动回滚，readonly reason 记录失败原因。

`confirm` 为 `false` MUST 直接返回 400。目标 zip 不存在 MUST 返回 404。

#### Scenario: 恢复成功

- **WHEN** 已有备份 `/path/to/backup.zip`
- **AND** 调用 `POST /backup/restore` body `{file: "...", confirm: true}`
- **THEN** 响应 HTTP 200
- **AND** body `status == "ok"`
- **AND** `backups/pre-restore-<ts>.zip` 存在
- **AND** `_wal_readonly_reason` 已被清空

#### Scenario: confirm 为 false 直接拒绝

- **WHEN** 调用 `POST /backup/restore` body `{file: ..., confirm: false}`
- **THEN** 响应 HTTP 400

### Requirement: 保留策略自动清理

备份成功后 SHALL 按 `storage.backup.retention.days`（默认 7）与 `storage.backup.retention.weeks`（默认 4）保留最新：过去 N 天每天 1 份、过去 M 周每周 1 份，其他 MUST 删除。`backups/pre-restore-*.zip` MUST NOT 被该策略自动清理。

#### Scenario: 过期备份被清理

- **WHEN** `backups/` 下存在 15 份历史备份
- **AND** `retention.days = 7, retention.weeks = 4`
- **AND** 触发保留策略扫描
- **THEN** 最终保留不超过 `7 + 4 = 11` 份（去重后更少）
- **AND** `pre-restore-*.zip` 全部保留

### Requirement: 定时备份

服务 SHALL 在 lifespan 启动阶段按 `storage.backup.schedule` 注册定时任务，到期自动调用备份核心逻辑。关闭阶段 MUST 取消未触发的定时器。`storage.backup.enabled = false` 时 MUST 不注册任何定时任务。

#### Scenario: 默认配置注册定时任务

- **WHEN** 启动时 `storage.backup.enabled = true`
- **AND** 从 cron `0 3 * * *` 计算下一次触发点
- **THEN** `threading.Timer` 在该时间点前已启动
- **AND** 关闭服务时定时器被取消
