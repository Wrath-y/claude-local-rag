## Context

`concurrent-safe-storage` 已实现「save_store 调用内原子化、写锁串行化、manifest 校验」，保证磁盘态永远是「最后一次成功提交」。但客户端视角的一致性还差一步：

- 写路径中「收到请求 → 完成 embedding → 调 save_store」这段时间很长（大文档场景可能几十秒），期间崩溃会丢失所有算力。
- 现有 `source_hash` 幂等机制让客户端重试成本可控，但对批量/后台入库场景不友好。
- 后续 `index-self-healing`、`backup-restore-automation` 都假设服务端有自恢复能力，不能纯依赖客户端重试。

WAL 的作用是把「操作意图」固化在廉价的 append-only 文件里，让服务端具备独立于客户端的恢复能力。

## Goals / Non-Goals

**Goals:**
- 服务崩溃后，下一次启动能自动将「已收到但未完成 save_store」的操作重放到一致状态。
- WAL 写入开销 <5ms（一次小 append + fsync），不显著增加写延迟。
- WAL 损坏（断电截断、磁盘故障）时不让错误操作被 replay；进入只读降级由人工介入。
- WAL 不无界增长：超阈值自动 checkpoint + 截断。

**Non-Goals:**
- 异步返回（client 收到 200 ≠ 数据已落盘）。本 change 仍然保持「save_store 完成才返回 200」，WAL 是「更早持久化意图」的增强，不是「更早响应」。
- 跨进程/跨机器一致性，本 change 仍是单进程。
- 细粒度增量更新，每次操作仍然整盘切换 FAISS 索引（受限于现有 `concurrent-safe-storage` 的双 buffer）。
- 服务降级的完整状态机（归属 `index-self-healing`）。本 change 在 WAL 校验失败时仅拒绝写入、允许检索，作为最小骨架。

## Decisions

### 决策 1：记录「操作意图」而非「物化结果」

**选择**：WAL 记录请求的原始入参（text、source、op_type），replay 时重跑 chunking + embedding + save_store。

**替代方案**：
- 记录已切好的 chunks + embeddings — 可加速 replay，但 WAL 体积膨胀百倍，且跨 embedding 模型升级无法复用。
- 两阶段 WAL：意图 + 结果，意图落盘先响应、结果落盘后 checkpoint — 复杂度高，放到后续「异步返回」change。

**理由**：replay 场景罕见（崩溃恢复），重跑开销能接受；模型升级时 WAL 可直接重放，不需要额外迁移逻辑。

### 决策 2：WAL 文件格式 —— JSONL + 行级 CRC32

**选择**：每行格式 `{"seq": int, "ts": iso8601, "op": str, "payload": {...}, "crc32": hex8}`，`crc32` 覆盖除自身以外的字段 JSON 串（不含换行）。

**替代方案**：
- 定长二进制 + 块级校验 — 更快但不可读，调试痛苦。
- 文件级 SHA256 — 只能全部接受或全部拒绝，损坏一行就全丢。

**理由**：JSONL 人类可读便于诊断；行级 CRC32 在行末检测到损坏就截断，前面的行仍可 replay；CRC32 轻量，append 成本可忽略。

### 决策 3：提交边界 —— manifest `wal.committed_offset`

**选择**：`ManifestV1.wal` 新增 `{ path, committed_offset, committed_seq }`。save_store 成功写完 chunks + index 后，manifest 原子写入包含最新 `committed_offset`（= WAL 当前写入位置）。启动时若 `wal.size > committed_offset` 则 replay，replay 成功后再写一次 manifest。

**替代方案**：
- WAL 里加 `committed: true` 标记 — 违背 append-only 语义。
- 双文件（pending / committed） — 一致性窗口增加。

**理由**：单文件 + 外部 offset 指针最简单；manifest 本身已经走原子写，天然承载提交点。

### 决策 4：Checkpoint 与截断

**选择**：满足任一条件触发 checkpoint：
1. WAL 文件字节数 > `storage.wal.max_size_mb * 1024 * 1024`（默认 10MB）
2. 启动完成 replay 后
3. 正常关闭（lifespan 退出）

Checkpoint 流程：在写锁内执行一次 no-op save_store（写当前 chunks + index + manifest.committed_offset = 0），然后原子替换 `wal.jsonl` 为空文件。失败则保留 WAL，下次再试。

**替代方案**：
- 直接 truncate `wal.jsonl` — 非原子，可能丢尾部未提交内容。
- 新建 `wal.jsonl.new` + 原子替换 — 在写锁内替换即可。

**理由**：WAL 是日志，直接整盘替换（写空文件→rename）是最安全的截断。

### 决策 5：CRC 坏行策略

**选择**：read 时逐行解析，遇到 CRC 不匹配 → 停止 replay，返回已读成功的前缀；记录 `storage.wal.truncation_at = <offset>`。启动阶段如果发现 truncation_at 与 committed_offset 之间有差距，**拒绝写入（只读降级）**，要求人工：

1. 备份 WAL 后手动截断至 truncation_at
2. 重启服务

**替代方案**：
- 跳过坏行继续 replay — 危险，可能错乱顺序。
- 自动截断并继续 — 隐式丢数据，违背「让人知道出了事」。

**理由**：WAL 损坏是罕见运维事件，优先「不静默丢数据」。只读降级保留检索能力让用户不至于完全停摆。

### 决策 6：replay 阶段锁与顺序

**选择**：
1. 启动先 `cleanup_orphan_tempfiles`。
2. 读 manifest（缺失则先生成——与 concurrent-safe-storage 保持一致）。
3. 读 WAL：从 `committed_offset` 开始顺序解析到 EOF 或坏行。
4. 对每条待 replay 操作，在写锁内调用对应的内部函数（不走 HTTP 路由），就像应用一次真实写请求。
5. 全部 replay 完成 → 一次 save_store + manifest 写（更新 `committed_offset`）→ 截断 WAL。
6. 失败（embedding 报错、save_store 报错）→ 进入只读降级，保留 WAL 等人工介入。

**理由**：替代方案都要引入新的内部抽象，代价大；复用写锁 + 已有的内部写函数能保证 replay 的语义和正常路径完全一致。

## Risks / Trade-offs

- **[风险] replay 依赖 embedding 模型可用** → 启动时若模型尚未加载完成就遇到 WAL，需等待模型初始化后再 replay。缓解：在 `lifespan` 里 model init 完成后再 `load_store`，本项目现已是这个顺序。
- **[风险] replay 期间 embedding 版本与当时不同导致向量不同** → 只要 embedding dim 不变不影响 FAISS 正确性；dim 变化属于 `index-self-healing` 范畴，本 change 不处理。
- **[风险] 单条 WAL entry 特别大（几十 MB 原始文本）拖慢 append + fsync** → 对 `ingest.text` 加上合理 size 限制（例如 10MB），超过直接拒绝；限制值通过 config 暴露，后续可调。
- **[权衡] replay 对终端用户是黑盒** → 启动日志输出 `[wal] replaying N ops from offset X`，同时 `/health` 在 replay 期间返回 degraded，avoiding silent long startups。
- **[权衡] WAL 关闭（`storage.wal.enabled=false`）回退到旧行为，为灰度上线兜底** → 但所有 change 的验收都基于 WAL 开启态；关闭时 manifest 的 `wal.committed_offset=0` 始终等于 0。

## Migration Plan

1. **代码上线**：部署后首次启动若无 `storage/wal.jsonl`，自动创建空文件 + 写一次 manifest（`wal.committed_offset=0`）。
2. **存量 manifest 兼容**：`read_manifest` 对缺失的 `wal` 字段填充默认值；`verify_manifest` 对 `wal.committed_offset` 校验「文件 size >= committed_offset」。
3. **回滚**：把 `storage.wal.enabled=false` 写进 config 并重启，服务回到 concurrent-safe-storage 的行为；WAL 文件保留，下次启用会自动 replay 或 checkpoint。
4. **灰度验证**：本地冒烟——ingest 中途 kill -9，重启后检查 `chunks + ntotal` 等于 ingest 返回成功的请求数。

## Open Questions

1. replay 期间是否让 `/health` 强制返回 degraded 直至完成？
   - 预案：是，`health-metrics-observability` change 会正式定义；本 change 先在 `/health` 里粗粒度加一个 `"wal_replaying": bool` 字段。
2. WAL 是否需要压缩归档（checkpoint 后保留若干分片便于事后审计）？
   - 本 change 默认直接截断；审计需求由 `backup-restore-automation` 提供（备份包含 WAL 快照）。
3. `storage.wal.enabled=false` 下 manifest 结构是否仍写 `wal` 字段？
   - 决策：仍写，`committed_offset` 固定为 0，便于开关切换时无缝。
