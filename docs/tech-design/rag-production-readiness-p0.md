# RAG 生产化 P0 — 技术方案

> PRD 文档：docs/prd-gaps/P0-production-readiness.md
> 编写日期：2026-04-30

---

## 概览

P0 聚焦将现有单进程、无锁、无持久化保障的本地 RAG 服务升级为「崩溃不丢数据、故障可感知、可备份可恢复」的生产基线。改造集中在 `server.py` 的存储与读写路径，并新增 `storage/` 子模块（WAL、备份）与 `/metrics`、`/health`、`/storage/integrity-check`、`/index/rebuild`、`/backup/*` 等运维端点。所有改造保持向后兼容，存量单用户数据可无感升级。

---

## 需求列表

| # | Change | 标题 | 模块 | 优先级 |
|---|--------|------|------|--------|
| 1 | `concurrent-safe-storage` | 并发安全的存储层 | 存储/索引 | HIGH |
| 2 | `wal-crash-recovery` | WAL 与崩溃恢复 | 存储/索引 | HIGH |
| 3 | `backup-restore-automation` | 备份与恢复自动化 | 运维 | HIGH |
| 4 | `health-metrics-observability` | 健康监控与指标 | 可观测性 | HIGH |
| 5 | `index-self-healing` | 索引损坏自愈 | 存储/索引 | HIGH |

---

## 数据库变更汇总

本期不涉及业务数据库（无关系型 DB）。涉及的本地存储文件：

### 新增文件

- `storage/wal.jsonl` — append-only 写前日志（来自 wal-crash-recovery）
- `storage/manifest.json` — 记录最后一次成功 checkpoint 的 offset 与索引摘要（来自 wal-crash-recovery / concurrent-safe-storage）
- `backups/YYYY-MM-DD/*.zip` — 定时备份文件（来自 backup-restore-automation）
- `backups/pre-restore-<ts>.zip` — 恢复前自动快照（来自 backup-restore-automation）

### 现有文件行为变更

| 文件 | 变更 | 来源需求 |
|------|------|----------|
| `chunks.pkl` | 写入改为「temp + rename」原子替换 | concurrent-safe-storage |
| `index.bin` | 写入采用双 buffer：`index.bin.new` → 原子 rename | concurrent-safe-storage |
| `config.yaml` | 新增 `backup.schedule`、`backup.retention`、`storage.wal.max_size_mb` 字段 | backup-restore-automation / wal-crash-recovery |

---

## 接口变更汇总

### RAG 服务（FastAPI）

| 接口 | 变更类型 | 来源需求 | 说明 |
|------|----------|----------|------|
| `GET /health` | 修改 | health-metrics-observability | 返回 ok/degraded/error 三态，error 同时返回 HTTP 503 |
| `GET /metrics` | 新增 | health-metrics-observability | Prometheus 文本格式指标 |
| `GET /storage/integrity-check` | 新增 | concurrent-safe-storage | 校验 chunks 与 index 一致性 |
| `POST /index/rebuild` | 新增 | index-self-healing | 从 chunks 重建索引，支持同步/异步 |
| `GET /index/status` | 新增 | index-self-healing | 返回 normal/read-only/rebuilding |
| `POST /backup/run` | 新增 | backup-restore-automation | 立即执行一次备份 |
| `POST /backup/restore` | 新增 | backup-restore-automation | 从备份文件恢复 |
| `GET /backup/list` | 新增 | backup-restore-automation | 列出现有备份 |
| `POST /ingest` | 修改 | concurrent-safe-storage / wal-crash-recovery | 加写锁 + 先落 WAL |
| `DELETE /source` | 修改 | concurrent-safe-storage / wal-crash-recovery | 同上 |
| `DELETE /reset` | 修改 | concurrent-safe-storage / wal-crash-recovery | 同上 |

### Claude Code Slash Commands

| 命令 | 变更类型 | 来源需求 | 说明 |
|------|----------|----------|------|
| `/rag-backup` | 新增 | backup-restore-automation | 触发 POST /backup/run |
| `/rag-restore` | 新增 | backup-restore-automation | 交互式 POST /backup/restore |
| `/rag-status` | 修改 | backup-restore-automation / health-metrics-observability | 额外显示最近备份时间、健康三态 |

---

## 各需求方案摘要

### #1 concurrent-safe-storage — 并发安全的存储层

- 在 `server.py` 进程级引入 `threading.RLock`（FastAPI 单进程 uvicorn 默认线程池）保护所有涉及 chunks / index 的写路径。读路径（/retrieve）使用 copy-on-write 的索引引用，不持锁。
- `save_store()` 改写：先写 `chunks.pkl.tmp`、`index.bin.new`，`fsync` 后 `os.replace` 原子替换。任一步失败则回滚并抛错，不破坏原文件。
- `load_store()` 启动校验维度与 chunks 长度一致，通过 `storage/manifest.json` 记录最后 commit 的摘要（chunk 数、index 维度、SHA256）。
- 新增 `GET /storage/integrity-check` 读取 manifest 对照实际文件，异常返回 409。

### #2 wal-crash-recovery — WAL 与崩溃恢复

- 新增 `storage/wal.py`：`append(op_type, payload)`、`replay(from_offset)`、`truncate()`。WAL 采用 JSONL + CRC32 行尾校验，防截断污染。
- ingest / delete / reset 流程：先 `wal.append` → 更新内存索引 → 原子落盘 pickle/index → 更新 manifest.offset。
- 启动流程：读 manifest.offset，若 `wal.size > offset`，按顺序回放剩余行重建内存状态后再持久化一次，最后 `wal.truncate()`。
- checkpoint：WAL 超 `storage.wal.max_size_mb`（默认 10）触发全量快照并截断，避免无界增长。
- 注意 WAL 写入本身也要 fsync，否则断电场景依然可能丢数据。

### #3 backup-restore-automation — 备份与恢复自动化

- 使用 `APScheduler`（轻量）实现 cron 调度，在服务进程内运行，不引入外部 cron 依赖。
- 备份 = 现有 `/export` 的打包逻辑，写入 `backups/YYYY-MM-DD/rag-<HHMMSS>.zip`。
- 保留策略：启动时和每次备份后执行清理：按「最近 N 天每天 1 份 + 最近 M 周每周 1 份」保留最新，多余删除。默认 N=7、M=4。
- 恢复：`/backup/restore` 先将当前 chunks/index/wal 打包为 `backups/pre-restore-<ts>.zip`，再执行替换；失败自动回滚。
- 新增 slash commands：`rag-backup`、`rag-restore`，封装对应 HTTP 调用。

### #4 health-metrics-observability — 健康监控与指标

- 引入 `prometheus-client`，注册 Counter/Histogram/Gauge：`rag_ingest_total`、`rag_retrieve_total`、`rag_retrieve_latency_seconds{stage}`、`rag_chunk_total`、`rag_index_bytes`、`rag_model_load_seconds`、`rag_last_backup_timestamp_seconds`。
- `/health` 判定顺序：模型未加载 → degraded；索引 read-only / 磁盘 <1GB / 校验失败 → error（HTTP 503）；其他 → ok。
- 日志切换到 `logging` + `JsonFormatter`（自定义或 `python-json-logger`），每条含 `request_id`、`path`、`latency_ms`、`hit_count`。为 FastAPI 注册中间件生成 request_id。
- `/metrics` 暴露为无鉴权端点（仅 localhost 监听下低风险，生产接 Prometheus 可通过反代控制）。

### #5 index-self-healing — 索引损坏自愈

- 启动阶段检查：FAISS index 维度 vs 当前 embedding model 维度；chunks 数 vs index.ntotal 偏差阈值（默认 ±0）。
- 异常处理：
  - 维度不一致 → 进入 read-only 模式（ingest 返回 409），提示调用 `/index/rebuild`。
  - 索引文件缺失或损坏 → 自动触发 rebuild；成功则切回 normal。
- `/index/rebuild`：异步任务（后台线程），期间 `/index/status` 返回 `rebuilding`，写接口返回 503。重建完成原子切换并写 manifest。
- 重建期间 `/metrics` 暴露 `rag_reindex_progress_ratio` 供监控。

---

## 依赖关系与实施顺序

1. **第一批**（基础设施，互相独立）：`concurrent-safe-storage`、`health-metrics-observability`
2. **第二批**（依赖第一批的写路径改造）：`wal-crash-recovery`、`index-self-healing`
3. **第三批**（依赖以上全部落地，确保备份内容完整一致）：`backup-restore-automation`

理由：
- `wal-crash-recovery` 依赖 `concurrent-safe-storage` 提供的写锁与原子写能力，否则 WAL 与 pickle 仍可能不一致。
- `index-self-healing` 的 rebuild 流程复用 `concurrent-safe-storage` 的双 buffer 切换机制。
- `backup-restore-automation` 备份的内容需要涵盖 chunks、index、WAL、manifest 四者，放最后实施能避免备份格式反复迭代。
- `health-metrics-observability` 与其他需求弱耦合，可并行启动，且其指标会被其他需求复用（如 `rag_last_backup_timestamp_seconds`）。

---

## 风险与待澄清项

| # | 来源需求 | 问题描述 |
|---|----------|----------|
| 1 | wal-crash-recovery | WAL 中是否存储完整 embedding 向量？存储会增大 WAL 体积，不存储则回放时需重算 embedding（慢但省空间）。建议默认不存储向量、回放重算，通过配置开关开启「快速回放」 |
| 2 | backup-restore-automation | 备份文件是否加密？当前明文 zip 在单用户本地低风险，但团队共享存储场景可能需要可选加密 |
| 3 | health-metrics-observability | `/metrics` 是否需要鉴权？本期默认不加（localhost），P2 多租户阶段再补 |
| 4 | index-self-healing | 自动 rebuild 触发时机如果选择过激（如数量偏差 > 1 即重建），可能在正常 ingest 过程中误触发。建议只在启动自检 + 手动触发两种路径 |
| 5 | 全部 | 存量用户的 `chunks.pkl` 不含 manifest，首次启动需有一键迁移：读取现有文件生成初始 manifest + 空 WAL |

---
