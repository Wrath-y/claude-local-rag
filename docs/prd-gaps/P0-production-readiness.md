# RAG 生产化 P0 — 稳定性与可靠性基线

> 优先级：P0（阻断上线）
> 期望交付：2 个迭代周期内完成
> 编写日期：2026-04-30

## 背景

当前实现面向单用户本地使用：FAISS 索引 + pickle 文档一次性加载进程内存，所有写操作无锁保护，崩溃即丢数据。日志仅打印到 `/tmp/claude-local-rag.log`，无指标，故障无告警。任何团队或多实例部署场景下，当前形态都不可用。

## 目标

- 任意写并发不产生数据损坏或索引不一致
- 服务崩溃/机器重启后数据完整，可在 5 分钟内恢复
- 关键故障（依赖掉线、索引损坏、磁盘满）有明确信号而非静默失败
- 可以安全地在多进程/容器环境运行

---

## 需求列表

### 需求 1：并发安全的存储层

**模块**：存储/索引

**背景**：`server.py` 中 `save_store()` 直接 pickle 覆盖写，`ingest` 和 `retrieve` 共享同一 FAISS index 对象无锁。并发 ingest 会丢失写入或损坏 pickle。

**核心功能**：

- 为 ingest / delete_source / reset 等写操作加进程内写锁（或 SQLite+WAL 作为元数据层）
- pickle 写入改为「写临时文件 + 原子 rename」，避免半写损坏
- FAISS 索引写入使用双 buffer：新索引构建完成后原子切换引用
- 提供 `/storage/integrity-check` 接口，启动时自校验 `chunks.pkl` 与 `index.bin` chunk 数量一致

**验收**：

- 并发 50 路 ingest 压测不丢数据、不产生 pickle 解析错误
- 在任意 ingest 过程中 `kill -9` 服务进程，重启后索引与 chunks 一致或回滚到上一致状态
- integrity-check 在索引/chunks 数量不匹配时以非 0 退出码报错

**涉及文件**：`server.py`（save_store/load_store/ingest/delete_source/reset）

---

### 需求 2：WAL 与崩溃恢复

**模块**：存储/索引

**背景**：当前 ingest 成功后才落盘，若服务在中途崩溃，已生成的 chunks/embeddings 丢失，调用方无法分辨。

**核心功能**：

- 引入 append-only write-ahead log（JSONL，按操作类型记录 ingest/delete/reset）
- 每次写操作先落 WAL 再更新内存索引与 pickle
- 服务启动时：校验 pickle 对应的 WAL 末尾 offset，若落后则回放后续 WAL
- 定期 checkpoint：WAL 超过阈值（默认 10MB）触发全量快照并截断 WAL

**验收**：

- 任意时刻强杀进程，重启后数据与最后一次成功返回的 ingest 响应一致
- WAL 文件永不无界增长（checkpoint 后 <100KB）

**涉及文件**：`server.py`、新增 `storage/wal.py`

---

### 需求 3：备份与恢复自动化

**模块**：运维

**背景**：仅提供手动 `/export` 和 `/import`，无定时备份、无版本保留策略、无恢复演练。

**核心功能**：

- 新增 `/backup/schedule` 配置项：定时（cron 表达式）触发 `/export` 至 `backups/YYYY-MM-DD/` 目录
- 保留策略：最近 N 天每天 1 份 + 最近 M 周每周 1 份，超出自动清理
- 新增 `/backup/restore --file <path>` 命令：带确认交互，替换当前数据库前先备份当前状态到 `backups/pre-restore-<ts>`
- `/rag-status` 输出最近一次备份时间与大小

**验收**：

- 按配置触发备份、按策略清理旧备份
- 恢复流程在本地/远端备份包之间可互换使用
- 误操作恢复后可回滚到「恢复前自动备份」

**涉及文件**：`server.py`、`config.yaml`、新增 `.claude/commands/rag-backup.md`、`rag-restore.md`

---

### 需求 4：健康监控与指标

**模块**：可观测性

**背景**：仅 `/health` 返回 ok，无请求耗时、QPS、embedding 模型加载状态、索引规模等指标。故障靠人工看日志发现。

**核心功能**：

- `/metrics` 端点输出 Prometheus 格式指标：
  - `rag_ingest_total{source_type}` / `rag_retrieve_total`
  - `rag_retrieve_latency_seconds`（histogram，拆 embedding/search/rerank 三阶段）
  - `rag_chunk_total`、`rag_index_bytes`
  - `rag_model_load_seconds`、`rag_last_backup_timestamp`
- `/health` 扩展为三态：ok / degraded（模型未加载完成等）/ error（索引损坏、磁盘可用 <1GB）
- 结构化日志（JSON）输出至 stdout，字段包含 request_id、latency_ms、retrieval_hit_count

**验收**：

- `curl /metrics` 返回可被 Prometheus scrape 的文本
- 模拟磁盘满时 `/health` 返回 error 且 HTTP 503
- 日志可被 `jq` 解析

**涉及文件**：`server.py`、`requirements.txt`

---

### 需求 5：索引损坏自愈

**模块**：存储/索引

**背景**：FAISS 索引文件损坏或维度不匹配时，当前服务启动会 crash loop，无自动处理。

**核心功能**：

- 启动时校验 index 维度与当前 embedding 模型维度一致，否则进入「只读降级」模式（可检索旧数据但拒写）
- 提供 `/index/rebuild` 端点：从 chunks.pkl 全量重建 FAISS 索引
- 触发条件：启动自检失败、手动调用、或 chunk/index 数量不一致超阈值时自动触发

**验收**：

- 删除 index.bin 后重启服务自动从 chunks 重建并恢复可用
- 更换 embedding 模型后，服务提示需 rebuild 且不误用旧向量

**涉及文件**：`server.py`

---


## 不在本期范围

- 多用户鉴权（见 P2）
- 检索效果评测（见 P1）
- Web 管理界面（见 P2）
