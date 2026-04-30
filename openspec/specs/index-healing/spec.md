# index-healing

> 索引自愈：启动时缺失自动重建、维度不匹配只读降级、运行时 /index/rebuild 异步重建。
> 首次引入: 2026-04-30 (change: index-self-healing)


### Requirement: /index/rebuild 端点

`POST /index/rebuild` SHALL 启动一个后台线程重建 FAISS 索引：对 `stored_chunks` 的全部 `text` 重新计算 embedding 并构造新索引，完成后在写锁内原子替换全局索引引用并执行 `save_store`。端点 MUST 立即返回 HTTP 200 + `{status: "started"}`，而非等待重建完成。重建期间写路径 MUST 返回 503。

#### Scenario: 触发后立即返回 started

- **WHEN** 调用 `POST /index/rebuild`
- **THEN** 响应 HTTP 200
- **AND** body 包含 `status == "started"`

#### Scenario: 重建期间写路径被阻断

- **WHEN** rebuild 后台线程已开始但未完成
- **AND** 调用 `POST /ingest`
- **THEN** 响应 HTTP 503
- **AND** `_wal_readonly_reason` 包含 "index rebuild"

### Requirement: /index/status 端点

`GET /index/status` SHALL 返回当前索引状态：`state` 取值 `"normal"` / `"read-only"` / `"rebuilding"`；`state == "rebuilding"` 时 MUST 包含 `progress_ratio`（0-1 之间的 float）；非 normal 时 MUST 包含 `reason` 字符串。

#### Scenario: 正常状态

- **WHEN** 服务处于健康状态且未在重建
- **AND** 调用 `GET /index/status`
- **THEN** 响应 200 + `{state: "normal"}`

#### Scenario: 重建进行中展示进度

- **WHEN** rebuild 进行到已处理 60% chunks
- **AND** 调用 `GET /index/status`
- **THEN** 响应 `state == "rebuilding"`
- **AND** `progress_ratio` ≈ 0.6

### Requirement: 启动时索引缺失自动重建

服务启动时若 `chunks.pkl` 存在但 `index.bin` 不存在，SHALL 自动（同步）从 chunks 重建索引并写 manifest 后继续启动，MUST NOT raise。重建成功后 startup 日志打印 `[index] rebuilt from N chunks`。

#### Scenario: 仅索引缺失时自动恢复

- **WHEN** 启动前 `index.bin` 被手动删除但 `chunks.pkl` 完好
- **AND** 启动服务
- **THEN** 启动过程输出 `[index] rebuilt from N chunks`
- **AND** `index.ntotal == len(stored_chunks)`
- **AND** 服务进入 `ok` 状态而非拒绝启动

### Requirement: 启动时索引维度不匹配进入只读降级

服务启动时若 `index.bin` 可读但 `index.d != DIM`（当前 embedding 模型维度），SHALL 保留旧索引使检索仍可服务，并将 `_wal_readonly_reason` 设置为 `"index dim mismatch: expected=<DIM>, actual=<index.d> — run /index/rebuild"`，MUST NOT raise，MUST NOT 自动触发 rebuild。

#### Scenario: 维度不一致时不 raise 但进入降级

- **WHEN** 启动时索引维度 = 384 而模型维度 = 512
- **THEN** 服务正常启动完成
- **AND** `_wal_readonly_reason` 非空并指向 dim mismatch
- **AND** `GET /retrieve` 仍可调用（使用旧索引）
- **AND** `POST /ingest` 返回 503

### Requirement: rag_reindex_progress_ratio metric

服务 SHALL 在 `/metrics` 暴露 `rag_reindex_progress_ratio` gauge，取值范围 [0, 1]。非重建时该指标 MUST 为 0；重建进行中 MUST 每批 embedding 完成后更新。

#### Scenario: 重建过程指标变化

- **WHEN** rebuild 进行到 60%
- **AND** 抓取 `/metrics`
- **THEN** body 中 `rag_reindex_progress_ratio` 约等于 0.6
