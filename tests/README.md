# Tests

> 中文版见文末 · See Chinese version at the bottom

## Directory Layout

```text
tests/
├── conftest.py                       # Shared fixtures across changes
│                                     # (isolated_store, seed_consistent_state)
├── concurrent-safe-storage/          # Named after the openspec change (1:1 mapping)
│   ├── test_storage_unit.py
│   ├── test_save_store_atomicity.py
│   ├── test_startup_mismatch.py
│   ├── test_integrity_endpoint.py
│   ├── test_concurrent_writes.py
│   └── test_crash_smoke.py
├── wal-crash-recovery/
│   ├── test_wal_unit.py
│   ├── test_wal_append_integration.py
│   ├── test_wal_replay.py
│   ├── test_wal_readonly.py
│   └── test_wal_checkpoint.py
└── <next-change>/                    # Future changes follow the same pattern
    └── test_*.py
```

**Naming convention:**

- Each openspec change gets a sibling subdirectory with the exact same kebab-case name.
- After a change is archived (moved to `openspec/changes/archive/YYYY-MM-DD-<name>/`), its test subdirectory keeps the original name and serves as the regression suite.

## Running

```bash
pip install -r requirements-dev.txt
HF_HUB_OFFLINE=1 TRANSFORMERS_OFFLINE=1 pytest                               # full suite
HF_HUB_OFFLINE=1 TRANSFORMERS_OFFLINE=1 pytest tests/concurrent-safe-storage # single change
```

> `HF_HUB_OFFLINE=1` stops SentenceTransformer from reaching HuggingFace Hub when the model is already cached. `conftest.py` sets this by default; the explicit form above is for ad-hoc debugging.

## Conventions

- Unit tests: `test_<module>.py`, exercise modules like `storage.py` directly.
- Integration tests: cover `server.py` lifespan, FastAPI TestClient, concurrency.
- Every test uses the `tmp_path` fixture for isolation — never touches the project-root `chunks.pkl` / `index.bin`.
- Shared fixtures live in the top-level `conftest.py`; pytest automatically propagates them into subdirectories.

---

## 中文版

### 目录结构

```text
tests/
├── conftest.py                       # 跨 change 共享 fixtures
│                                     #（isolated_store、seed_consistent_state）
├── concurrent-safe-storage/          # 与 openspec change 同名（一一对应）
│   ├── test_storage_unit.py
│   ├── test_save_store_atomicity.py
│   ├── test_startup_mismatch.py
│   ├── test_integrity_endpoint.py
│   ├── test_concurrent_writes.py
│   └── test_crash_smoke.py
├── wal-crash-recovery/
│   ├── test_wal_unit.py
│   ├── test_wal_append_integration.py
│   ├── test_wal_replay.py
│   ├── test_wal_readonly.py
│   └── test_wal_checkpoint.py
└── <next-change>/                    # 后续 change 按此模式新建子目录
    └── test_*.py
```

**命名约定：**

- 每个 openspec change 对应一个同名子目录（kebab-case）。
- change 归档后（move 到 `openspec/changes/archive/YYYY-MM-DD-<name>/`），对应的 tests 子目录保留原名，作为回归测试集。

### 运行

```bash
pip install -r requirements-dev.txt
HF_HUB_OFFLINE=1 TRANSFORMERS_OFFLINE=1 pytest                               # 全量
HF_HUB_OFFLINE=1 TRANSFORMERS_OFFLINE=1 pytest tests/concurrent-safe-storage # 只跑某 change
```

> `HF_HUB_OFFLINE=1` 让 SentenceTransformer 在已缓存模型的机器上不访问 HuggingFace Hub。`conftest.py` 已默认设置，显式传入用于手动调试场景。

### 约定

- 单元测试：`test_<module>.py`，直接操作 `storage.py` 等模块。
- 集成测试：涉及 `server.py` 生命周期、FastAPI TestClient、并发。
- 每个测试用 `tmp_path` fixture 隔离，不污染项目根目录下的 `chunks.pkl` / `index.bin`。
- 共享 fixtures 放在顶层 `conftest.py`，由 pytest 自动下沉到各子目录。
