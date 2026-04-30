# RAG 生产化 P0 — 需求拆分

> 来源文档：docs/prd-gaps/P0-production-readiness.md
> 期望交付：2 个迭代周期内完成
> 拆分日期：2026-04-30

---

## 需求清单

| # | 优先级 | Change 名称 | 标题 | 模块 | openspec 状态 |
|---|--------|-------------|------|------|---------------|
| 1 | HIGH | `concurrent-safe-storage` | 并发安全的存储层 | 存储/索引 | ✅ 已归档 |
| 2 | HIGH | `wal-crash-recovery` | WAL 与崩溃恢复 | 存储/索引 | ⏳ 待生成 |
| 3 | HIGH | `backup-restore-automation` | 备份与恢复自动化 | 运维 | ⏳ 待生成 |
| 4 | HIGH | `health-metrics-observability` | 健康监控与指标 | 可观测性 | ⏳ 待生成 |
| 5 | HIGH | `index-self-healing` | 索引损坏自愈 | 存储/索引 | ⏳ 待生成 |

---

## 需求详情

### #1 [HIGH] concurrent-safe-storage — 并发安全的存储层

**模块**：存储/索引

**摘要**：`server.py` 当前 `save_store()` 直接 pickle 覆盖写，`ingest` 与 `retrieve` 共享同一 FAISS index 对象无锁，并发 ingest 会丢数据或损坏 pickle。为所有写操作加进程内写锁；pickle 写入改为「写临时文件 + 原子 rename」；FAISS 索引采用双 buffer，新索引构建完成后原子切换引用；启动及运行时提供 `/storage/integrity-check` 校验 chunks 与 index 数量一致。

**接口**（均使用 POST）：

- `GET /storage/integrity-check` — 返回 chunks.pkl 与 index.bin 的一致性报告，数量不匹配返回非 200

**涉及文件**：`server.py`（`save_store` / `load_store` / `ingest` / `delete_source` / `reset`）

**验收**：

- 并发 50 路 ingest 压测无丢数据、无 pickle 解析错误
- ingest 中途 `kill -9` 重启后索引与 chunks 一致或回滚到上一致状态
- integrity-check 发现不一致时以非 0 退出码 / 非 200 响应报错

---

### #2 [HIGH] wal-crash-recovery — WAL 与崩溃恢复

**模块**：存储/索引

**摘要**：当前 ingest 成功后才统一落盘，中途崩溃则已生成的 chunks/embeddings 丢失且调用方无法分辨。引入 append-only JSONL WAL，按操作类型记录 ingest/delete/reset；每次写操作先落 WAL 再更新内存索引与 pickle；服务启动时比对 pickle 的 WAL offset 并回放落后部分；WAL 超过阈值（默认 10MB）触发全量快照并截断。

**接口**：

- 无新增对外接口（内部 storage 层 API）

**涉及文件**：`server.py`（ingest / delete_source / reset / 启动流程）、新增 `storage/wal.py`

**验收**：

- 任意时刻强杀进程，重启后数据与最后一次成功返回的 ingest 响应一致
- WAL 文件 checkpoint 后 <100KB，不会无界增长

---

### #3 [HIGH] backup-restore-automation — 备份与恢复自动化

**模块**：运维

**摘要**：目前仅提供手动 `/export`、`/import`，无定时备份、无保留策略、无恢复演练。新增定时（cron 表达式）触发 `/export` 至 `backups/YYYY-MM-DD/`；保留策略：最近 N 天每天 1 份 + 最近 M 周每周 1 份，超出自动清理；新增 `/backup/restore` 替换前自动备份当前状态到 `backups/pre-restore-<ts>`；`/rag-status` 显示最近一次备份时间与大小。

**接口**（均使用 POST）：

- `POST /backup/run` — 立即触发一次备份
- `POST /backup/restore` — 从指定备份文件恢复，参数 `file`，带确认标志
- `GET /backup/list` — 列出现有备份与大小

**涉及文件**：`server.py`、`config.yaml`（新增 `backup.schedule`、`backup.retention`）、新增 `.claude/commands/rag-backup.md`、`.claude/commands/rag-restore.md`

**验收**：

- 按配置触发备份并按策略清理旧备份
- 恢复流程在本地/远端备份包之间可互换使用
- 误操作恢复后可回滚到「恢复前自动备份」

---

### #4 [HIGH] health-metrics-observability — 健康监控与指标

**模块**：可观测性

**摘要**：当前仅 `/health` 返回 ok，无请求耗时、QPS、embedding 模型加载状态、索引规模等指标，故障靠人工看日志发现。新增 `/metrics` Prometheus 端点，`/health` 扩展为 ok/degraded/error 三态，结构化 JSON 日志输出至 stdout（字段含 request_id、latency_ms、retrieval_hit_count）。指标覆盖 ingest/retrieve 次数、延迟直方图（拆 embedding/search/rerank 三阶段）、chunk 总量、索引字节、模型加载耗时、最近备份时间戳。

**接口**：

- `GET /metrics` — Prometheus 文本格式指标
- `GET /health` — 返回 `{status: ok|degraded|error, details: {...}}`

**涉及文件**：`server.py`、`requirements.txt`（新增 `prometheus-client`）

**验收**：

- `curl /metrics` 返回可被 Prometheus scrape 的文本
- 模拟磁盘可用 <1GB 时 `/health` 返回 error 且 HTTP 503
- 日志可被 `jq` 直接解析

---

### #5 [HIGH] index-self-healing — 索引损坏自愈

**模块**：存储/索引

**摘要**：FAISS 索引损坏或维度与当前 embedding 模型不匹配时，当前服务启动会 crash loop 无自愈。启动时校验索引维度与模型维度一致，否则进入「只读降级」模式（可检索旧数据但拒写）；提供 `/index/rebuild` 从 `chunks.pkl` 全量重建；触发条件覆盖启动自检失败、手动调用、chunk/index 数量不一致超阈值。

**接口**（均使用 POST）：

- `POST /index/rebuild` — 从 chunks.pkl 重建 FAISS 索引，支持 `async=true` 后台执行
- `GET /index/status` — 返回当前索引状态（normal / read-only / rebuilding）

**涉及文件**：`server.py`（启动流程、读写路径上加「只读降级」判断）

**验收**：

- 删除 `index.bin` 后重启服务可自动从 chunks 重建并恢复可用
- 更换 embedding 模型后服务明确提示需 rebuild 且不误用旧向量

---
