## Context

P0 前 4 个 change 已提供：原子写、WAL、监控、自愈。最后一块是备份。本地运行的 RAG 数据全在一台机器，磁盘丢 → 全丢；`/export`、`/import` 目前只能手动调用，没有策略。目标：默认配置下把备份自动化，保留策略可控，恢复失败可回退。

## Goals / Non-Goals

**Goals:**
- 默认配置每天自动备份，无人值守。
- 备份包含恢复所需的所有文件：chunks / index / manifest / wal，未来新增文件能被声明式加入。
- 恢复是事务性的：失败或中途崩溃不会让用户卡在「半新半旧」状态，可一键回退到恢复前快照。
- 无新第三方依赖（APScheduler 纯 Python 没带 C 扩展，但仍然可选）。

**Non-Goals:**
- 备份加密 / 远端存储 / 多租户（见 P2）。
- 增量备份（每份都是全量，压缩由 zip 提供）。
- 备份压缩等级调优。

## Decisions

### 决策 1：调度实现

**选择**：纯标准库 `threading.Timer` 链式自调度：启动时计算下一次触发点，单次 timer 到期后再计算下一次，避免常驻 `while True` 线程。cron 解析用简化版（`croniter` 是额外依赖；手写一个只支持 `M H * * *` 和 `*/n` 的最小实现足够当前需求）。

**替代方案**：`APScheduler` — 功能全但引入依赖；本场景用不到持久化 job store。

### 决策 2：备份文件内容与命名

**选择**：zip 内置文件固定为 `chunks.pkl`、`index.bin`、`storage/manifest.json`、`storage/wal.jsonl`（如存在）；manifest 与 wal 放在 `storage/` 前缀与运行时目录对齐。命名 `backups/YYYY-MM-DD/rag-HHMMSS.zip`。

**替代方案**：按容器名用户自定义前缀 — 过度工程化。

### 决策 3：恢复事务性

**选择**：
1. 持写锁 + 设 `_wal_readonly_reason = "restore in progress"`。
2. 先触发 `_backup_run_core()` 生成 pre-restore 包到 `backups/pre-restore-<ts>.zip`。
3. 解压目标包到 `_restore_staging/`（临时目录）。
4. 用 `os.replace` 逐个替换 target 文件（chunks.pkl、index.bin、manifest.json、wal.jsonl）。
5. 调 `load_store()` 重新加载；加载成功 → 清理 `_restore_staging/`，设 readonly_reason = None；加载失败 → 用 pre-restore zip 回滚，记录错误。

**替代方案**：
- 不做 pre-restore 快照 — 失败无路可退；
- 把 restore 做成异步 + 状态机 — 复杂度不匹配当前规模。

### 决策 4：保留策略算法

**选择**：按「天/周」两个窗口：
- 过去 N 天（默认 7）每天保留最新 1 份
- 过去 M 周（默认 4）每周保留最新 1 份（若当天已有天窗口的保留则复用）
- 其余删除

运行时机：每次备份成功后 + `lifespan` 启动时各一次。算法扫描 `backups/**/*.zip` 按 mtime 分桶，保留桶内 latest。

**替代方案**：容量上限 — 难以预估；按时间更可预测。

### 决策 5：/rag-backup 与 /rag-restore 命令

**选择**：
- `/rag-backup` — 单次调用，直接 POST `/backup/run`，展示结果
- `/rag-restore` — 先 GET `/backup/list` 展示最近 10 个，用户输入编号或完整 path，确认后 POST `/backup/restore`。Claude Code 侧通过对话完成交互

## Risks / Trade-offs

- **[风险] 备份期间服务仍在写** → 备份流程先持写锁 snapshot 文件列表，`zipfile.write` 操作的是当时的文件内容；写操作会等。备份结束释放锁。
- **[权衡] 定时备份在正常负载上增加 I/O** → 默认 3 点时间偏移避开常见高峰；用户可改 cron。
- **[风险] 恢复中的包如果比当前 embedding 模型旧，可能触发 `index-self-healing` 的 dim mismatch** → 设计上这是对的行为，用户看到 degraded + /index/rebuild 提示即可。

## Migration Plan

1. 部署后自动创建 `backups/` 目录；首次定时触发点在上线后的下一个 cron match。
2. 回滚：config 里设 `storage.backup.enabled: false`；已有备份不动。
3. 存量：`/export`、`/import` 端点保留（现在内部走 `_backup_run_core` / `_restore_core`）。

## Open Questions

1. 备份失败是否应该入 `/health`？
   - 当前决策：不入 `error`，但 `/metrics` 暴露最后成功时间，运维可自行配告警（连续 24 小时无备份即告警）。
2. 是否允许同时多个 backup/restore 并发？
   - 当前决策：不允许，全部入写锁串行。
