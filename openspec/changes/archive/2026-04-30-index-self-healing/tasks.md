## 1. 状态 + 指标

- [x] 1.1 `server.py` 新增 `_index_rebuilding: bool = False`、`_index_rebuild_progress: float = 0.0`、`_index_rebuild_reason: Optional[str] = None`
- [x] 1.2 `metrics.py` 新增 `reindex_progress_ratio` gauge

## 2. 启动自愈

- [x] 2.1 `load_store` 开头加分支：`chunks.pkl` 存在 + `index.bin` 不存在 → 调 `_rebuild_index_sync()`（复用重建核心逻辑，同步执行），写 manifest 后继续正常加载
- [x] 2.2 `load_store` 成功读取 index 后，比较 `index.d != DIM`：设 `_wal_readonly_reason = "index dim mismatch: ..."`，不 raise

## 3. /index/rebuild 端点

- [x] 3.1 新增 `_rebuild_index_core(chunks) -> (new_index, last_encoded_count)`，按 batch（64）encode 并 `new_index.add`，每批更新 `_index_rebuild_progress` + metric
- [x] 3.2 `POST /index/rebuild`：启动 `threading.Thread` 运行 `_rebuild_index_async()`；设置 `_wal_readonly_reason = "index rebuild in progress"`，返回 `{status: "started"}`
- [x] 3.3 后台线程持有 write_lock 执行 rebuild；成功后原子替换 `index`、`save_store`、清除 readonly reason 与 rebuilding flag；失败则保留 reason 指向错误
- [x] 3.4 `_rebuild_index_sync()`：启动自愈专用，同步调用 core + atomic_write_faiss + write manifest，不启新线程

## 4. /index/status 端点

- [x] 4.1 新增 `GET /index/status`：返回 `{state, progress_ratio?, reason?}`
- [x] 4.2 state 判定：`_index_rebuilding` → "rebuilding"；`_wal_readonly_reason` 非空 → "read-only"；否则 "normal"

## 5. /health 扩展

- [x] 5.1 增加 `_index_rebuilding` → degraded 分支；body 加 `index_rebuilding`、`index_state`

## 6. 测试

- [x] 6.1 `tests/index-self-healing/test_startup_index_missing.py`：删除 index.bin 重启 → 自动 rebuild 成功
- [x] 6.2 `test_rebuild_endpoint.py`：触发 rebuild → status=started；结束后 `_wal_readonly_reason` 清空；期间写端点返回 503
- [x] 6.3 `test_dim_mismatch_degradation.py`：写一个维度错误的 FAISS 索引文件 + 匹配 chunks + 手工 manifest，启动服务不 raise，进入 degraded
- [x] 6.4 `test_index_status_endpoint.py`：normal / rebuilding / read-only 三态
- [x] 6.5 `test_rebuild_metric.py`：rebuild 期间 `rag_reindex_progress_ratio` > 0；结束后 = 0

## 7. 文档

- [x] 7.1 `CLAUDE.md` 新增「索引自愈」小节
- [x] 7.2 `.claude/commands/rag-status.md` 提到 `/index/status` 查看 rebuild 进度
- [x] 7.3 `tests/README.md` 目录结构补 `index-self-healing/`
