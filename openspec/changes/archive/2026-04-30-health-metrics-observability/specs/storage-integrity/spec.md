## MODIFIED Requirements

### Requirement: `/storage/integrity-check` 自检端点

服务 SHALL 暴露 `GET /storage/integrity-check` 端点，返回当前存储一致性状态。一致时返回 HTTP 200 与 JSON 摘要（包含 count、ntotal、sha256、committed_at、wal.committed_offset、wal.committed_seq、`disk_free_bytes`）。manifest 与实际文件不一致时返回 HTTP 409 与差异字段描述。manifest 缺失且 chunks/index 无法读取时返回 HTTP 503。该端点 MUST 为只读操作，MUST NOT 修改任何文件内容（除非检测到 manifest 缺失需要自动补齐）。

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
