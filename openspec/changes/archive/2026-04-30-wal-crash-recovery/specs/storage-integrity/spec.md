## MODIFIED Requirements

### Requirement: 存储 manifest 记录一致性摘要

服务 SHALL 维护 `storage/manifest.json` 文件，记录最后一次成功 commit 的一致性摘要，字段包括 `version`、`committed_at`、`chunks.path`、`chunks.count`、`chunks.sha256`、`index.path`、`index.dim`、`index.ntotal`、`index.sha256`、`wal.path`、`wal.committed_offset`、`wal.committed_seq`。每次写操作在 chunks 与 index 都成功原子替换后 SHALL 原子地更新 manifest；chunks 或 index 任一替换失败 MUST NOT 更新 manifest。启用 WAL 时 `wal.committed_offset` 与 `wal.committed_seq` 反映最新提交点；未启用时两字段均为 `0`。存量 manifest 缺失 `wal` 字段时 SHALL 在首次加载后补齐为默认值。

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
