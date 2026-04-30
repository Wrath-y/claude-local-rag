## 1. 存储模块骨架

- [x] 1.1 在 `server.py` 顶部或新建 `storage.py` 中声明模块级 `_STORE_WRITE_LOCK = threading.RLock()`，并为写锁设计一个 `with_write_lock` 装饰器或 context manager
- [x] 1.2 定义数据目录常量（chunks 路径、index 路径、manifest 路径、临时文件后缀 `.tmp` / `.new`），避免散落字符串
- [x] 1.3 新增工具函数 `atomic_write_bytes(path, data)`、`atomic_write_faiss(path, index)`：写临时文件 → fsync → `os.replace` → 父目录 fsync；失败回滚并清理临时文件
- [x] 1.4 新增工具函数 `sha256_of_file(path)` 与 `cleanup_orphan_tempfiles(data_dir)`（仅删除 `chunks.pkl.tmp` 与 `index.bin.new`）

## 2. Manifest 读写

- [x] 2.1 定义 `ManifestV1` dataclass/TypedDict，字段与 design.md 决策 4 一致（version、committed_at、chunks.{path,count,sha256}、index.{path,dim,ntotal,sha256}）
- [x] 2.2 实现 `read_manifest(path) -> ManifestV1 | None`：文件缺失返回 None，字段缺失/version 不匹配返回 None 并打印告警
- [x] 2.3 实现 `write_manifest(path, manifest)`：使用 `atomic_write_bytes` 序列化 JSON 落盘
- [x] 2.4 实现 `build_manifest_from_files(chunks_path, index_path, index_obj)`：基于当前文件和内存索引对象构造一致的 manifest
- [x] 2.5 实现 `verify_manifest(manifest, chunks, index_obj) -> list[Mismatch]`：对比 count、ntotal、sha256，返回差异列表

## 3. save_store / load_store 改造

- [x] 3.1 将 `save_store()` 改为在写锁内执行：序列化 chunks → `atomic_write_bytes`；`faiss.write_index` 至 `index.bin.new` → fsync → `os.replace`；最后 `write_manifest`
- [x] 3.2 `save_store()` 中任一步失败：清理未提交的临时文件，抛出异常，MUST 不更新 manifest
- [x] 3.3 将 `load_store()` 改为：启动先 `cleanup_orphan_tempfiles`，再读取 manifest；manifest 缺失且文件可加载 → 构造并写入 manifest；不一致 → 打印差异并 raise 阻止启动
- [x] 3.4 `load_store()` 中确保 FAISS 索引通过 `faiss.read_index` 载入后以模块级全局引用暴露（如 `GLOBAL_INDEX`），替换直接 mutable 共享

## 4. 写路径串行化与双 buffer

- [x] 4.1 为 `ingest` 函数入口加写锁；内部对 chunk 列表与 FAISS 索引的修改改为「拷贝当前索引 → 在新对象上 add → 成功后替换全局引用」
- [x] 4.2 为 `delete_source` 函数入口加写锁；删除对应 chunk 后构建新索引对象并替换全局引用
- [x] 4.3 为 `reset` 函数入口加写锁；清空 chunks 列表、构造空索引、通过 `save_store` 原子落盘
- [x] 4.4 `/retrieve` 路径保持无锁，函数入口一次性读取模块级全局索引引用，后续全部基于该本地引用调用 FAISS

## 5. `/storage/integrity-check` 端点

- [x] 5.1 新增 `@app.get("/storage/integrity-check")`：持读锁（或无锁，仅调用只读工具函数），读取 manifest + 文件 + 全局索引，调用 `verify_manifest`
- [x] 5.2 一致 → 返回 200 + 摘要 JSON；不一致 → 返回 409 + 差异字段列表；manifest 缺失但文件正常 → 自动补齐 manifest（持写锁）后返回 200
- [x] 5.3 无法读取 pickle 或 FAISS 文件（损坏/缺失） → 返回 503 + 错误说明，不抛出未处理异常

## 6. 测试

- [x] 6.1 单元测试：`atomic_write_bytes` 在中途抛错时原文件保持不变
- [x] 6.2 单元测试：`write_manifest` → `read_manifest` 往返一致；version 不匹配时 `read_manifest` 返回 None
- [x] 6.3 并发测试：使用 `threading` 启 50 个线程并发调用 `ingest`，断言最终 `len(chunks) == index.ntotal == 期望值`
- [x] 6.4 崩溃测试（集成）：在 ingest 内部制造异常（monkeypatch `faiss.write_index` 抛错），断言 `chunks.pkl`、`index.bin`、`manifest.json` 均未被部分更新
- [x] 6.5 启动校验测试：手动构造 `chunks.pkl` 与 manifest `count` 不匹配的场景，断言 `load_store` 抛出启动异常并打印差异
- [x] 6.6 端点测试：一致状态下 `/storage/integrity-check` 返回 200；人为改坏索引后返回 409；删除 manifest 后调用自动补齐并返回 200

## 7. 文档与清理

- [x] 7.1 在 `.gitignore` 中加入 `storage/manifest.json`、`*.tmp`、`*.new`（若尚未覆盖）
- [x] 7.2 在 `CLAUDE.md` / `README` 中新增「存储一致性」段落：说明 manifest 用途与 `/storage/integrity-check` 行为
- [x] 7.3 在 `.claude/commands/rag-status.md` 中补充：调用 `/storage/integrity-check` 并显示最近一次 `committed_at` 与状态
- [x] 7.4 本地跑一次全流程冒烟：启动 → ingest → kill -9 → 重启 → `/storage/integrity-check` 返回 200
