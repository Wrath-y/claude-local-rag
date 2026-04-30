## Context

`concurrent-safe-storage` + `wal-crash-recovery` 解决了「写路径崩溃不丢数据」，但对文件层面的损坏/缺失仍然全靠启动时 `raise`。运维角度：当前 RAG 是个人/小团队规模，磁盘偶发丢文件（误删、云盘同步冲突、热迁移）需要不中断服务自动恢复。

## Goals / Non-Goals

**Goals:**
- `index.bin` 缺失但 `chunks.pkl` 存在时自动重建，服务能正常启动。
- 索引维度与当前 embedding 模型不匹配时进入只读降级而非 crash；运维可随时发起 `/index/rebuild` 异步修复。
- 重建期间服务保持可检索（基于 old index），写路径被 `_wal_readonly_reason` 阻断；重建完成原子切换。

**Non-Goals:**
- 替换索引后端。
- 修改嵌入模型切换 API（仍通过改 config + 重启）。
- 自动检测低质量索引（如 recall 下降）并触发 rebuild。

## Decisions

### 决策 1：rebuild 调度

**选择**：`POST /index/rebuild` 立即返回 `{status: "started"}`；后台由 `threading.Thread(target=..., daemon=True)` 执行；线程内部持有 `storage.write_lock()`（阻塞新写）。进度通过 module 级变量 + `/index/status` 暴露。

**替代方案**：同步执行 rebuild、阻塞 HTTP 请求 — 大 chunks 场景可能超 30s 请求超时。

### 决策 2：rebuild 期间的写路径

**选择**：rebuild 开始设 `_wal_readonly_reason = "index rebuild in progress"`，完成后清空。写端点通过已有 `_assert_writable` 直接返回 503。读路径继续用 old index 服务（线程安全来自现有双 buffer）。

**替代方案**：rebuild 期间让写端点等待 — 可能等几分钟，差体验。拒绝 + 告知重试更好。

### 决策 3：进度度量

**选择**：`progress_ratio = encoded_so_far / total_chunks`，每 batch（默认 64 条）更新一次。`GET /index/status` 返回当前值；`rag_reindex_progress_ratio` gauge 同步更新。

**替代方案**：不暴露进度 — 运维不透明；做增量流式 — 对内部数据结构改造大。

### 决策 4：启动时维度不匹配处理

**选择**：读 `index.bin` → 取 `.d`；若 `!= DIM`：
1. 保留旧索引（启动到只读服务状态）
2. 设 `_wal_readonly_reason = "index dim mismatch: expected=DIM, actual=.d — run /index/rebuild"`
3. `/health` 返回 degraded，运维看到后手动调用 rebuild

**替代方案**：自动 rebuild — 风险是启动可能阻塞很久（大 corpus）且无人监视。人工触发更安全。

### 决策 5：索引缺失（chunks 仍在）处理

**选择**：启动时若 `chunks.pkl` 存在但 `index.bin` 不存在 → 同步 rebuild（阻塞启动）+ 写 manifest；启动继续。这是「安装迁移后首次启动」常见场景，自动修复价值大。

**替代方案**：同样要求人工触发 — 对典型故障无谓增加摩擦。

## Risks / Trade-offs

- **[风险] rebuild 过程中服务进程被 kill** → 重启后 index.bin 可能处于部分状态？`atomic_write_faiss` 保证不会。可能出现 index 缺失但 manifest 还指向旧 sha，启动校验会 fail — 人工介入即可。
- **[权衡] 单进程 FAISS 重建是 CPU 密集** → 可能占满核；不提供限流（本 change 规模下无意义）。
- **[权衡] rebuild 期间写被拒** → 业务影响由运维窗口控制，文档里注明。

## Migration Plan

1. 上线后无需动作。
2. 维度变更场景：先改 `config.yaml` 的模型 → 重启 → 看到 degraded 提示 → 调 `/index/rebuild` → 等进度到 1.0。
3. 索引文件丢失：直接重启，自动重建。

## Open Questions

1. rebuild 是否需要「干跑」验证（先生成到临时文件、和预期 ntotal 对比）？
   - 当前决策：不做，atomic_write_faiss 已保证原子性。
