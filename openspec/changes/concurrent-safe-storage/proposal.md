## Why

当前 `server.py` 所有写路径无锁保护：`save_store()` 直接 pickle 覆盖写，`ingest` / `delete_source` / `reset` 与 `retrieve` 共享同一 FAISS index 对象，并发 ingest 会互相覆盖、损坏 pickle，进程崩溃可能留下半写文件。这是生产上线的阻断级问题，必须在引入 WAL、备份、自愈等后续能力之前先打好串行化与原子化的底座。

## What Changes

- 在服务进程内引入写锁，所有涉及 chunks 列表 / FAISS index 的写路径（`ingest`、`delete_source`、`reset`、`save_store`、`load_store` 及后续 rebuild/restore）串行执行；读路径（`/retrieve`）持有索引引用不持写锁。
- `save_store()` 改为原子写：`chunks.pkl.tmp`、`index.bin.new` 先写入并 `fsync`，随后 `os.replace` 原子替换；任一步失败回滚，绝不破坏现有文件。
- FAISS 索引采用双 buffer：新索引构建完成后原子切换全局引用，旧索引在无读者时 GC。
- 新增 `storage/manifest.json` 记录最后一次成功 commit 的摘要（chunk 数、index 维度、`index.ntotal`、文件 SHA256、时间戳）。`load_store()` 启动时比对 manifest 与实际文件,异常拒绝启动或进入只读降级。
- 新增 `GET /storage/integrity-check` 端点：一致返回 200 + 摘要，不一致返回 409 + 差异说明。
- 存量用户无感升级：无 manifest 时首次启动自动生成，不改变现有 `chunks.pkl` / `index.bin` 格式。

## Capabilities

### New Capabilities

- `storage-integrity`: 存储层的写并发保护、原子落盘、manifest 一致性校验与自检端点。涵盖写锁、原子写、双 buffer 索引切换、manifest 生成与校验、`GET /storage/integrity-check`。

### Modified Capabilities

（本项目尚无既有 spec，不涉及修改）

## Impact

- **代码**：`server.py` 的 `save_store` / `load_store` / `ingest` / `delete_source` / `reset` 路径改写；新增 `/storage/integrity-check` 端点。
- **磁盘**：新增 `storage/manifest.json`（运行时生成，纳入 `.gitignore`）；写入期间短暂存在 `chunks.pkl.tmp`、`index.bin.new` 临时文件。
- **API**：新增 `GET /storage/integrity-check`；现有接口行为语义保持兼容。
- **依赖**：仅使用 Python 标准库（`threading`、`hashlib`、`os.replace`），不引入新第三方依赖。
- **向后兼容**：存量 `chunks.pkl` / `index.bin` 无需迁移，首次启动自动补齐 manifest。
- **为后续 change 打底**：WAL（`wal-crash-recovery`）、自愈（`index-self-healing`）、备份（`backup-restore-automation`）均复用本 change 提供的写锁与原子写机制。
