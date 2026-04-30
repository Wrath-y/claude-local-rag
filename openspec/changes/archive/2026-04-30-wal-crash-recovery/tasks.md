## 1. WAL 模块骨架

- [x] 1.1 新增 `storage/wal.py`：定义 `WAL_FILENAME = "wal.jsonl"` 常量、`WAL_TMP_SUFFIX = ".new"`，及 `wal_path_for(data_dir: str) -> str` 工具函数
- [x] 1.2 实现 `encode_record(seq: int, ts: str, op: str, payload: dict) -> bytes`：按「除 `crc32` 外字段的 JSON 串」计算 `zlib.crc32` 并附加在行末，返回包含换行的 UTF-8 字节序列
- [x] 1.3 实现 `decode_line(raw: bytes) -> Record`：解析并校验 `crc32`，不匹配抛 `WALCorruptError`；JSON 非法同样抛 `WALCorruptError`
- [x] 1.4 实现 `append(wal_path: str, record_bytes: bytes) -> int`：以 `O_WRONLY|O_APPEND` 打开文件、`write` + `fsync(fd)` + 父目录 fsync，返回 append 后的文件大小（新 offset）
- [x] 1.5 实现 `iter_records(wal_path: str, start_offset: int) -> Iterator[(start, end, Record)]`：按字节 offset 顺序读取，遇坏行停止并在返回里给出坏行 offset
- [x] 1.6 实现 `truncate_atomic(wal_path: str)`：写空 `wal.jsonl.new` → `os.replace` → 目录 fsync

## 2. Manifest 扩展

- [x] 2.1 在 `storage.py` 的 `ManifestV1` 中新增 `wal` 字段（dataclass：`WALSummary(path: str, committed_offset: int, committed_seq: int)`），`to_dict` 同步带上
- [x] 2.2 `read_manifest` 对缺失 `wal` 字段的旧版 manifest 填充默认值 `WALSummary("wal.jsonl", 0, 0)`，不记为错误
- [x] 2.3 `build_manifest_from_files` 接受可选 `wal_path` / `wal_committed_offset` / `wal_committed_seq` 参数（默认 0）
- [x] 2.4 `verify_manifest` 新增检查：`wal.committed_offset <= wal.jsonl.size`（真正文件大小），超出报 `Mismatch("wal.committed_offset", ...)`
- [x] 2.5 `storage.py` 单元测试更新：覆盖新字段读写与向后兼容的解析路径

## 3. 服务端配置与状态

- [x] 3.1 `config.yaml` 新增 `storage.wal.enabled`（默认 `true`）、`storage.wal.max_size_mb`（默认 `10`）
- [x] 3.2 `server.py` 读取上述配置；引入模块级 `_wal_replaying = False` 和 `_wal_readonly_reason: Optional[str] = None`（只读降级原因）
- [x] 3.3 `server.py` 新增常量 `WAL_PATH = wal.wal_path_for(DATA_DIR)`；初始化时确保 `STORAGE_DIR` 存在

## 4. 写路径接入 WAL

- [x] 4.1 在 `ingest`、`delete_source`、`reset` 入口持写锁后、做实际工作之前，构造 record 并调用 `wal.append`；记录 append 后的 `new_offset` / `seq` 供 save_store 使用
- [x] 4.2 修改 `save_store(new_index, new_chunks, *, wal_offset=None, wal_seq=None)`：在写 manifest 时把 `wal.committed_offset` 与 `wal.committed_seq` 写进去
- [x] 4.3 写路径在 WAL 开关关闭时跳过 append，manifest 的 `wal.committed_offset` 固定为 0
- [x] 4.4 `_wal_readonly_reason` 非 None 时，所有写端点返回 503 + 说明文字；只有 `/retrieve`、`/sources`、`/stats`、`/storage/integrity-check` 正常服务

## 5. 启动阶段 replay

- [x] 5.1 在 `load_store` 的「cleanup_orphan_tempfiles」与「chunks/index 一致性校验」之间插入 `_replay_wal_if_needed()`：读取 manifest 中 `wal.committed_offset`，通过 `wal.iter_records` 读取后续记录
- [x] 5.2 replay 期间置 `_wal_replaying = True`；逐条调用 `_apply_op_locally(record)`（内部函数，复用 `ingest` / `delete_source` / `reset` 核心逻辑但不走 HTTP 路由）
- [x] 5.3 replay 全部成功后：`save_store` 一次 + `wal.truncate_atomic`；manifest 的 `wal.committed_offset/seq` 归零
- [x] 5.4 replay 中途遇到 `WALCorruptError`：停止后续 replay，设置 `_wal_readonly_reason = "wal corrupted at offset X"` 并记录日志；已 replay 成功的前缀保留
- [x] 5.5 replay 中途真实业务错误（embedding 失败、IOError）：同样进入只读降级，`_wal_readonly_reason = "replay failed: ..."`
- [x] 5.6 replay 结束置 `_wal_replaying = False`

## 6. Checkpoint 与关闭钩子

- [x] 6.1 引入 `_checkpoint()` 函数：在写锁内执行一次 `save_store(GLOBAL_INDEX, stored_chunks, wal_offset=0, wal_seq=<last_committed_seq>)` + `wal.truncate_atomic`
- [x] 6.2 `ingest` / `delete_source` / `reset` 完成 save_store 成功后检查 WAL 大小，超 `max_size_mb` 触发 `_checkpoint`
- [x] 6.3 在 `lifespan` 关闭阶段调用 `_checkpoint`（try/except 失败不阻止关闭）

## 7. 端点与可观测性

- [x] 7.1 `GET /health` 响应体新增 `"wal_replaying": bool` 与 `"wal_readonly_reason": str | null` 字段
- [x] 7.2 `GET /storage/integrity-check` 响应摘要增加 `wal.committed_offset`、`wal.committed_seq` 字段
- [x] 7.3 `/rag-status` 斜杠命令文档更新：展示 WAL 大小与最近 seq（可选）

## 8. 测试

- [x] 8.1 `tests/wal-crash-recovery/test_wal_unit.py`：`encode_record` → `decode_line` 往返；CRC 篡改后 `decode_line` 抛异常
- [x] 8.2 同文件：`iter_records` 在坏行处停止，返回的 offset 等于坏行起始
- [x] 8.3 `test_wal_append_integration.py`：ingest 后 `wal.jsonl` 增长，行内容与请求一致，`manifest.wal.committed_offset` 等于当前文件大小
- [x] 8.4 `test_wal_replay.py`：用伪造 WAL 文件 + 偏旧的 manifest 触发 replay，断言 chunks/index 匹配 replay 后的预期
- [x] 8.5 `test_wal_readonly.py`：损坏 WAL 一行，启动后 `_wal_readonly_reason` 非 None；`/ingest` 返回 503；`/retrieve` 正常
- [x] 8.6 `test_wal_checkpoint.py`：设置 `max_size_mb = 0.001`（约 1KB），数次 ingest 后 checkpoint 触发，`wal.jsonl` 归零、manifest offset 归零
- [x] 8.7 `test_wal_disabled.py`：`storage.wal.enabled = false` 时无 WAL 文件生成，manifest 的 `wal.committed_offset` 保持 0
- [x] 8.8 `test_wal_health.py`：mock replay 延迟，startup 期间 `/health` 返回 `wal_replaying: true`

## 9. 文档与归档准备

- [x] 9.1 更新 `CLAUDE.md` 的「存储一致性」段落：新增 WAL 小节，说明崩溃恢复语义与坏行降级行为
- [x] 9.2 更新 `tests/README.md`：添加 `tests/wal-crash-recovery/` 子目录到结构示意
- [x] 9.3 本地冒烟：启动 → 发起一个长 ingest → 在 save_store 完成前 `kill -9` → 重启 → `/storage/integrity-check` 返回 200、`/sources` 体现出 ingest 的来源
