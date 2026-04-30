## Context

本项目是一个本地 RAG 服务：`server.py` 基于 FastAPI + uvicorn 单进程运行，使用 FAISS 作为向量索引、`chunks.pkl` 作为 chunk 文本与元数据的持久化。现有实现假设单用户低并发，写路径（ingest / delete_source / reset）直接覆盖写 pickle 与索引文件，未做任何锁保护与原子写。该假设在生产化、自动化入库、多客户端接入场景下不成立：并发 ingest 会导致 chunk 列表互相覆盖、pickle 半写损坏，与之关联的 FAISS index 也会落后或错位。

本 change 是 P0 生产化计划中的第一块底座，后续的 WAL（`wal-crash-recovery`）、自愈（`index-self-healing`）、备份（`backup-restore-automation`）都将复用此处的写锁与原子写机制，因此设计需要考虑向后可扩展。

## Goals / Non-Goals

**Goals:**
- 所有对 chunks / index 的写路径进程内串行化，消除写-写、写-读数据竞争。
- 所有持久化文件落盘原子化，任何异常中断不会破坏上一个一致状态。
- 提供可机器校验的一致性自检：本地文件 ↔ manifest ↔ 内存状态。
- 存量用户零迁移成本：无 manifest 时自动补齐，无需任何用户操作。
- 不引入新的第三方依赖。

**Non-Goals:**
- 崩溃回放 / WAL（交给 `wal-crash-recovery`）。
- 索引自动重建 / 只读降级的完整策略（交给 `index-self-healing`，本 change 只预留「manifest 校验失败 → 拒绝启动」骨架）。
- 跨进程 / 跨机器锁；本 change 假定单进程，需要多进程/容器部署时由 WAL + 外部锁（后续 change）解决。
- 鉴权、限流、指标（分别由 P2、`health-metrics-observability` 覆盖）。

## Decisions

### 决策 1：进程内写锁使用 `threading.RLock`

**选择**：模块级 `threading.RLock`（可重入），所有写路径入口持锁，读路径不持锁。

**替代方案**：
- `asyncio.Lock` —— FastAPI 路由可能是同步或异步，uvicorn 默认把同步端点放 threadpool，`asyncio.Lock` 无法跨线程生效。
- 文件锁（`fcntl.flock`）—— 跨平台差（Windows 行为不同），当前单进程场景用不上。
- 细粒度分段锁 —— 收益低、复杂度高，当前规模下单把写锁足够。

**理由**：当前所有写路径都是同步函数且在 threadpool 中执行，`threading.RLock` 最小代价实现串行化；RLock 保留可重入性，让 `ingest` 调用 `save_store` 时不会自锁；未来 WAL append 可复用同一把锁。

### 决策 2：原子写策略 —— temp + fsync + `os.replace`

**选择**：
1. 写 `chunks.pkl.tmp`、`index.bin.new` → `fsync(fd)` → `os.replace(tmp, target)` → `fsync(dir)`。
2. FAISS 索引用 `faiss.write_index` 写临时文件；读回来时通过 `faiss.read_index` 载入新对象，替换全局引用。
3. 写 manifest 放在最后一步，只有 chunks 与 index 都成功切换后才更新 manifest。

**替代方案**：
- 直接覆盖写 —— 现状，已证明会损坏。
- 事务化 DB（SQLite）—— 体量小、改动大、跨需求影响广，留到后续架构演进再评估。

**理由**：`os.replace` 在 POSIX 和 Windows 上都保证原子替换，配合 fsync 能保证 crash-consistency。目录 fsync 确保目录条目落盘，避免在断电场景回到旧 inode。Windows 对 `os.replace` 支持也已稳定（Python 3.3+）。

### 决策 3：双 buffer 索引切换

**选择**：ingest/delete/reset 修改一个本地 FAISS 索引对象（拷贝自现有索引），完成后替换模块级全局引用（单条赋值天然原子）。读路径在函数入口取一次引用即可，不持锁。

**替代方案**：
- 所有读写共享同一索引对象 + 读写锁 —— FAISS 对象线程安全性依赖编译选项，风险较高。
- RCU 风格的引用计数 —— 复杂度高，Python 的 GIL 已提供单引用赋值原子性。

**理由**：Python 引用赋值在 GIL 下为原子操作；旧索引对象在最后一个读者释放后自然 GC；写路径复制开销在当前索引规模（MB 级）可接受，后续量级变大可重新评估增量更新方案。

### 决策 4：manifest 结构与位置

**选择**：`storage/manifest.json`，字段：

```json
{
  "version": 1,
  "committed_at": "2026-04-30T12:34:56Z",
  "chunks": {
    "path": "chunks.pkl",
    "count": 123,
    "sha256": "..."
  },
  "index": {
    "path": "index.bin",
    "dim": 512,
    "ntotal": 123,
    "sha256": "..."
  }
}
```

**替代方案**：
- 不引入 manifest —— 无法分辨 chunks/index 不一致的根因（到底是 chunks 多还是 index 多，上一个一致状态是什么）。
- 把 manifest 嵌入 pickle 头部 —— 增加 pickle 反序列化脆弱面。

**理由**：JSON 可被人类审计与外部工具（监控、备份、CI）解析；`version` 字段给后续字段演进留口子；SHA256 让备份/恢复场景（后续 change）能判断是否需要再索引。

### 决策 5：`/storage/integrity-check` 行为

**选择**：
- 200：manifest 存在、所有字段与文件实际情况一致（count、ntotal、sha256 全部匹配）。
- 409：manifest 与文件不一致，body 返回差异字段（哪一项不匹配、期望 vs 实际）。
- 503：manifest 缺失且无法自动生成（如 pickle 损坏导致 count 无法读出）。
- 只读操作，不修改任何文件；首次调用发现 manifest 缺失但文件正常则自动补齐 manifest（落锁后写入）。

**替代方案**：401/500 统一用 500 —— 抹平运维语义，监控难以写规则。

**理由**：三态区分让上层（`/health`、运维脚本、未来 web 面板）能根据 HTTP 状态码直接路由告警级别。

### 决策 6：启动时 manifest 校验策略（本 change 的最小骨架）

**选择**：
- manifest 缺失 + 文件正常 → 自动生成 manifest，正常启动。
- manifest 存在 + 与文件一致 → 正常启动。
- manifest 与文件不一致 → 当前 change 先**拒绝启动并打印差异**，为 `index-self-healing` 留出扩展点（后续 change 再加只读降级与自动 rebuild）。

**替代方案**：立刻实现只读降级 —— 与 `index-self-healing` 重叠，放大本 change 范围。

**理由**：本 change 专注底座，不越权实现自愈；拒绝启动是最保守且可观察的行为。

## Risks / Trade-offs

- **[风险] 写路径全局单锁导致吞吐上限低** → 缓解：当前使用场景是个人/小团队低 QPS 入库，读路径不受影响；规模上来后用分片/分来源锁优化。
- **[风险] 双 buffer 复制在索引很大时内存翻倍** → 缓解：当前规模下几十 MB 可接受；大规模场景通过后续 change 切换到 Milvus/Qdrant。
- **[风险] 目录 fsync 在部分网络文件系统（NFS）不可靠** → 缓解：文档声明本服务的持久化要求本地磁盘，NFS 不作为官方支持目标。
- **[风险] manifest 与文件实际内容之间仍有 race（比如手动把文件拷出去再拷回来）** → 缓解：这类外部篡改超出范围；`/storage/integrity-check` 会检测并报 409。
- **[权衡] 读路径不持锁依赖 GIL 保证引用赋值原子** → 若未来迁移到 no-GIL Python 或其他语言运行时，需要重新评估；当前 CPython 下成立且主流实现稳定。

## Migration Plan

1. **代码上线**：部署新版本 `server.py`，首次启动时若 `storage/manifest.json` 缺失则自动生成一次（相当于一次 commit）。
2. **回滚**：旧版代码不读 manifest，能直接忽略该文件；`chunks.pkl`、`index.bin` 格式未变，可直接回滚。
3. **临时文件清理**：启动时扫描数据目录，删除孤儿 `*.tmp`、`*.new`（上次崩溃残留）。
4. **文档**：更新 `CLAUDE.md` / `README`，说明新增的 `storage/manifest.json` 与 `/storage/integrity-check`。

## Open Questions

1. manifest SHA256 对大文件（未来 chunks.pkl > 1GB）启动时计算耗时是否可接受？
   - 预案：加一个「快速校验」模式只对比 size + mtime，深度校验按需触发。
2. 双 buffer 切换时，正在进行的读请求拿到旧索引对象是否允许？
   - 当前决策：允许短暂「读到旧版本」，因为检索不是事务边界；若业务需要强一致性，后续加读锁版本号。
