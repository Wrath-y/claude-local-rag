# storage-integrity

> 存储层一致性契约：写并发保护、原子落盘、manifest 校验、自检端点。
> 首次引入: 2026-04-30 (change: concurrent-safe-storage)


### Requirement: 写路径串行化

所有修改 chunks 列表或 FAISS 索引的操作（ingest、delete_source、reset、save_store、load_store 重建阶段，以及后续需求中的 rebuild / restore）SHALL 持有同一把进程级可重入写锁串行执行。只读的检索路径（`/retrieve`、`/sources`、`/stats`、`/storage/integrity-check`）MUST NOT 持有该写锁。

#### Scenario: 并发 ingest 不丢数据

- **WHEN** 50 个并发客户端同时调用 `/ingest`，每个请求写入 10 条不同内容的 chunk
- **THEN** 所有请求均返回成功
- **AND** 最终 `chunks.pkl` 中恰好包含 500 条 chunk
- **AND** FAISS 索引的 `ntotal` 等于 500

#### Scenario: 写操作期间检索不被阻塞

- **WHEN** 一个长耗时 ingest 正在执行（模拟大文件分块写入）
- **AND** 同时有 `/retrieve` 请求到达
- **THEN** `/retrieve` 在 ingest 完成前即可返回结果，使用写操作开始前的索引快照

### Requirement: 持久化原子写

`save_store` SHALL 通过「写临时文件 + fsync + 原子 rename」的顺序落盘 `chunks.pkl` 与 `index.bin`：先写 `chunks.pkl.tmp` 与 `index.bin.new` 并对文件描述符执行 fsync，再使用 `os.replace` 将其移动到目标路径，最后对目标所在目录执行 fsync。任一中间步骤失败 SHALL 回滚并抛错，MUST NOT 覆盖或破坏原有 `chunks.pkl` / `index.bin`。

#### Scenario: 写入中途进程被强杀

- **WHEN** 服务正在执行 `save_store`，写入临时文件阶段被 `kill -9` 终止
- **THEN** 重启后 `chunks.pkl` 与 `index.bin` 仍为上一次成功 commit 时的内容
- **AND** 重启流程自动清理遗留的 `*.tmp` / `*.new` 孤儿文件

#### Scenario: 临时文件写失败不破坏已有数据

- **WHEN** 调用 `save_store` 时磁盘写入失败（例如空间不足、权限变更）
- **THEN** 调用方收到错误响应
- **AND** 原有 `chunks.pkl` 与 `index.bin` 内容保持不变
- **AND** 下一次成功的写操作仍可基于原有数据继续

### Requirement: FAISS 索引双 buffer 切换

写操作对 FAISS 索引的修改 SHALL 在一个新构建的索引对象上完成，构建完成后 MUST 通过单次引用赋值原子替换模块级全局索引引用。检索路径 SHALL 在函数入口读取一次全局引用后基于该引用完成本次检索，MUST NOT 在检索过程中重新读取全局引用。

#### Scenario: 写操作期间在途检索使用旧索引

- **WHEN** 一个 `/retrieve` 请求已经拿到当前索引引用
- **AND** 期间另一个写操作完成并替换了全局索引引用
- **THEN** 该 `/retrieve` 仍基于旧引用返回正常结果，不出现 segfault 或索引崩溃

### Requirement: 存储 manifest 记录一致性摘要

服务 SHALL 维护 `storage/manifest.json` 文件，记录最后一次成功 commit 的一致性摘要，字段包括 `version`、`committed_at`、`chunks.path`、`chunks.count`、`chunks.sha256`、`index.path`、`index.dim`、`index.ntotal`、`index.sha256`、`wal.path`、`wal.committed_offset`、`wal.committed_seq`。每次写操作在 chunks 与 index 都成功原子替换后 SHALL 原子地更新 manifest；chunks 或 index 任一替换失败 MUST NOT 更新 manifest。启用 WAL 时 `wal.committed_offset` 与 `wal.committed_seq` 反映最新提交点；未启用时两字段均为 `0`。存量 manifest 缺失 `wal` 字段时 SHALL 在首次加载后补齐为默认值。

> 首次引入: concurrent-safe-storage (2026-04-30)；wal 字段由 wal-crash-recovery (2026-04-30) 追加。

#### Scenario: 首次启动时自动生成 manifest

- **WHEN** 服务启动时 `storage/manifest.json` 不存在
- **AND** `chunks.pkl` 与 `index.bin` 可正常加载且数量一致
- **THEN** 服务自动基于现有文件生成 manifest 并继续正常启动
- **AND** `wal.committed_offset` 等于当前 `wal.jsonl` 文件大小（若启用 WAL）或 `0`（若未启用）

#### Scenario: 写操作成功后 manifest 字段与文件一致

- **WHEN** 一次 ingest 成功完成
- **THEN** `storage/manifest.json` 中 `chunks.count` 等于 pickle 中的 chunk 数
- **AND** `index.ntotal` 等于 FAISS 索引的 `ntotal`
- **AND** `chunks.sha256` 与 `index.sha256` 等于对应文件的实际 SHA256
- **AND** 启用 WAL 时 `wal.committed_offset` 等于 `wal.jsonl` 当前文件大小

#### Scenario: 存量 manifest 向后兼容

- **WHEN** 升级服务后首次启动读取到旧版 manifest（缺失 `wal` 字段）
- **THEN** 服务自动填充默认值 `{"path": "wal.jsonl", "committed_offset": 0, "committed_seq": 0}`
- **AND** 正常启动，不报错

### Requirement: 启动一致性校验

服务启动时 SHALL 比对 `storage/manifest.json` 与磁盘上 `chunks.pkl` / `index.bin` 的实际内容（count、ntotal、sha256）。不一致时 SHALL 拒绝启动并在标准错误中输出差异字段，MUST NOT 自动静默修复。manifest 缺失且文件正常时可自动生成 manifest 后启动。

#### Scenario: manifest 与文件不一致时拒绝启动

- **WHEN** `chunks.pkl` 中有 100 条 chunk
- **AND** manifest 中 `chunks.count = 90`
- **THEN** 服务启动失败并在错误输出中标识 `chunks.count` 不匹配的期望值与实际值

### Requirement: `/storage/integrity-check` 自检端点

服务 SHALL 暴露 `GET /storage/integrity-check` 端点，返回当前存储一致性状态。一致时返回 HTTP 200 与 JSON 摘要（包含 count、ntotal、sha256、committed_at、wal.committed_offset、wal.committed_seq、`disk_free_bytes`）。manifest 与实际文件不一致时返回 HTTP 409 与差异字段描述。manifest 缺失且 chunks/index 无法读取时返回 HTTP 503。该端点 MUST 为只读操作，MUST NOT 修改任何文件内容（除非检测到 manifest 缺失需要自动补齐）。

> 首次引入: concurrent-safe-storage (2026-04-30)；wal + disk_free_bytes 字段由 wal-crash-recovery / health-metrics-observability (2026-04-30) 追加。

#### Scenario: 一致状态返回 200 + 磁盘信息

- **WHEN** 调用 `GET /storage/integrity-check`
- **AND** 存储层处于一致状态
- **THEN** 响应状态码为 200
- **AND** 响应体包含 `chunks.count`、`index.ntotal`、`committed_at`、`disk_free_bytes` 字段

#### Scenario: 检测出数量不一致

- **WHEN** `chunks.pkl` 有 100 条 chunk 而 `index.ntotal = 90`
- **AND** 调用 `GET /storage/integrity-check`
- **THEN** 响应状态码为 409
- **AND** 响应体标识 `chunks.count` 与 `index.ntotal` 不匹配，给出各自的实际值

#### Scenario: manifest 缺失但文件健康时自动补齐

- **WHEN** `storage/manifest.json` 被手动删除
- **AND** `chunks.pkl` 与 `index.bin` 仍保持一致
- **AND** 调用 `GET /storage/integrity-check`
- **THEN** 服务生成新的 manifest
- **AND** 响应状态码为 200

### Requirement: 孤儿临时文件清理

服务启动阶段 SHALL 扫描存储目录，删除 `chunks.pkl.tmp`、`index.bin.new` 等由上次崩溃残留的临时文件，MUST NOT 删除任何其他文件。

#### Scenario: 启动时清理上次崩溃的临时文件

- **WHEN** 数据目录存在 `chunks.pkl.tmp` 和 `index.bin.new` 但无正在运行的服务
- **AND** 启动服务
- **THEN** 这两个临时文件在服务可用前被删除
- **AND** `chunks.pkl`、`index.bin` 保持不变
