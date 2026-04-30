"""Task 6.1 — chunks.pkl present but index.bin missing → auto-rebuild on startup."""

from __future__ import annotations

import os


def test_startup_auto_rebuilds_missing_index(isolated_store, seed_consistent_state):
    server = isolated_store["server"]
    import wal as wal_mod

    seed_consistent_state(server, [
        {"text": "一 段 用于 测试 启动 自愈 的 文字。", "source": "s1"},
        {"text": "另 一 段 不同 文字 提高 召回。", "source": "s1"},
    ])

    # Delete index.bin to simulate loss
    os.remove(server.INDEX_PATH)
    assert os.path.exists(server.TEXTS_PATH)

    # Also clean the WAL since our seed bypassed WAL path; we just want startup to
    # self-heal the index.
    if os.path.exists(server.WAL_PATH):
        wal_mod.truncate_atomic(server.WAL_PATH)

    # Must not raise; must regenerate index.bin
    server.load_store()

    assert os.path.exists(server.INDEX_PATH)
    assert server.index.ntotal == len(server.stored_chunks)
    assert server._wal_readonly_reason is None
