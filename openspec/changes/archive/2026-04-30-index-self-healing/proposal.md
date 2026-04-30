## Why

现在启动流程对「索引损坏 / 维度不匹配 / 文件缺失」的容错是「拒绝启动」：好处是不静默，坏处是恢复路径完全靠人工（删文件、重启、担心丢数据）。PRD 要求具备自愈能力：索引文件缺失 → 从 chunks 重建；维度不匹配 → 只读降级提示；运行时可手动触发 `/index/rebuild` 异步重建。本 change 把这一层加上，作为 P0 最后一个与数据面相关的 change。

## What Changes

- 新增 `POST /index/rebuild`：在写锁内后台任务执行，从当前 `stored_chunks` 的文本重新 embedding 并构造新 FAISS 索引，原子落盘并切换全局引用；重建期间服务进入 `_wal_readonly_reason = "index rebuild in progress"` 只读降级，避免竞态。
- 新增 `GET /index/status`：返回 `{state: "normal" | "read-only" | "rebuilding", progress_ratio?: float, reason?: str}`。
- `load_store` 行为扩展：
  - `index.bin` 缺失但 `chunks.pkl` 存在 → 自动 rebuild（启动阻塞），不再 raise；
  - 启动时索引维度 ≠ embedding 模型维度 → 进入只读降级（保留旧索引服务检索），不自动 rebuild；需人工调用 `/index/rebuild`；
  - chunks/index 数量偏差 > 允许阈值（默认 `> 0`，即任何不一致）→ 拒绝启动（已有行为，本 change 不放宽）。
- 新增指标 `rag_reindex_progress_ratio`（gauge，0-1，非重建时为 0）。
- **MODIFIED**：`service-health` 的 `/health` degraded 判定顺序扩展：在现有 WAL 相关条件之前插入「索引只读降级 / 正在 rebuild」。
- **MODIFIED**：`write-ahead-log` 的「只读降级」列举原因集合从「仅 WAL 坏行」扩展到「WAL 坏行 OR 索引维度不匹配 OR 索引 rebuild 进行中」。

## Capabilities

### New Capabilities

- `index-healing`: `/index/rebuild` + `/index/status` + 启动时索引缺失自动重建。

### Modified Capabilities

- `service-health`: `/health` degraded 条件扩展，新增 `index_rebuilding` / `index_dim_mismatch` 原因字符串。
- `write-ahead-log`: 只读降级不再仅由 WAL 触发，本 capability 的「只读降级」Requirement 描述改为面向所有 `_wal_readonly_reason` 的来源。

## Impact

- **代码**：`server.py` 新增端点 + `_rebuild_index()` 后台任务 + 启动自愈分支；`metrics.py` 新增 `reindex_progress_ratio` gauge；`storage.py` 辅助函数 `load_index_dim(path) -> int`（读 FAISS header 拿 d，不全量 load，可选优化，本 change 直接用 `faiss.read_index` 再读 `.d`）。
- **运行时风险**：rebuild 期间服务只读；长文档多时可能耗时分钟级 → `/index/status` 暴露进度（按已 encoded chunk 数 / 总数）。
- **配置**：无新增（阈值 / 维度来自现有 `DIM` 与 manifest）。
- **向后兼容**：未触发异常时行为与当前完全一致；新端点额外启用。
- **不覆盖**：索引后端替换（FAISS → Milvus/Qdrant，属 P3）；embedding 模型动态切换（本 change 不改模型配置加载方式）。
