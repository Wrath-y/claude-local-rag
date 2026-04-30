## 1. 依赖与模块骨架

- [x] 1.1 `requirements.txt` / `requirements-dev.txt` 新增 `prometheus-client>=0.20`
- [x] 1.2 新增 `metrics.py`：定义所有 `rag_*` 指标（Counter/Histogram/Gauge），用独立 `CollectorRegistry` 保护，防止 pytest 多次 import 时重复注册
- [x] 1.3 `metrics.py` 暴露 `registry`、`render() -> bytes`（走 `prometheus_client.generate_latest(registry)`）
- [x] 1.4 新增 `obs.py`：定义 `structured_log(event: str, **kv)`，格式化为单行 JSON 写 stdout

## 2. /health 三态与 disk 检查

- [x] 2.1 `server.py` 引入 `DISK_FREE_ERROR_BYTES = 1 * 1024**3`
- [x] 2.2 改造 `/health`：按 error → degraded → ok 顺序判定；error 路径返回 503 via `JSONResponse`
- [x] 2.3 响应 body 添加 `disk_free_bytes`、`reason` 字段；保留已有字段

## 3. /metrics 端点

- [x] 3.1 新增 `@app.get("/metrics")`：调用 `metrics.render()`，返回 `Response(content=..., media_type="text/plain; version=0.0.4")`
- [x] 3.2 `ingest` 成功/skip/失败分支分别 `metrics.ingest_total.labels(result=...).inc()`；末尾更新 `rag_chunk_total`、`rag_index_bytes`
- [x] 3.3 `retrieve` 入口 `t0 = time.perf_counter()`；末尾 `rag_retrieve_latency_seconds.observe(...)` + counter（hit 根据结果是否为空）
- [x] 3.4 `_maybe_checkpoint` 成功分支更新 `rag_last_commit_timestamp_seconds`（manifest.committed_at 转 unix 秒）
- [x] 3.5 `_replay_wal_if_needed`: 进入置 `rag_wal_replaying.set(1)`，退出 `set(0)`
- [x] 3.6 启动阶段 `model = SentenceTransformer(...)` 前后打点，`rag_model_load_seconds.set(duration)`

## 4. 结构化日志事件

- [x] 4.1 `ingest` 成功末尾 `structured_log("ingest_done", source=..., chunks_added=..., status=...)`
- [x] 4.2 `retrieve` 末尾 `structured_log("retrieve_done", hit=..., latency_ms=..., returned_chunks=...)`
- [x] 4.3 `_replay_wal_if_needed` 开始/结束 `structured_log("wal_replay_start"/"wal_replay_done", replayed=..., offset=...)`
- [x] 4.4 `_maybe_checkpoint` 成功后 `structured_log("checkpoint_done", wal_seq=..., wal_size=...)`

## 5. /storage/integrity-check 扩展

- [x] 5.1 响应体加入 `disk_free_bytes`（从 `shutil.disk_usage(DATA_DIR).free` 取）

## 6. 测试

- [x] 6.1 `tests/health-metrics-observability/test_metrics_endpoint.py`：访问 `/metrics` 响应 200 + `text/plain`，包含关键指标名
- [x] 6.2 同文件：一次 `/ingest` 后 `rag_ingest_total{result="ok"}` 前进
- [x] 6.3 同文件：一次 `/retrieve` 后 counter/histogram 更新
- [x] 6.4 `test_health_states.py`：正常 → ok；mock `_wal_replaying=True` → degraded/200；mock `shutil.disk_usage` 返回 free=500MB → error/503
- [x] 6.5 `test_structured_log.py`：调用 ingest，捕获 stdout，断言 `ingest_done` JSON 行字段齐全并能 `json.loads` 解析
- [x] 6.6 `test_integrity_disk_free.py`：`/storage/integrity-check` 响应含 `disk_free_bytes` 字段

## 7. 文档

- [x] 7.1 `.claude/commands/rag-status.md` 更新：描述三态、`/metrics` 端点、结构化日志
- [x] 7.2 `CLAUDE.md` 新增「监控与可观测性」小节
- [x] 7.3 `tests/README.md` 目录结构补 `health-metrics-observability/`
