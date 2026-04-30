"""Task 8.6 — exceeding max_size_mb triggers a checkpoint that truncates WAL."""

from __future__ import annotations

from fastapi.testclient import TestClient


def test_checkpoint_truncates_wal_when_threshold_exceeded(isolated_store, monkeypatch):
    server = isolated_store["server"]
    import wal as wal_mod

    # Shrink the threshold so a couple of ingests blow past it
    monkeypatch.setattr(server, "WAL_MAX_SIZE_BYTES", 200)  # 200 bytes — very small

    with TestClient(server.app) as c:
        for i in range(5):
            r = c.post(
                "/ingest",
                json={"text": f"测试 checkpoint 行为 的 第 {i} 批 数据 段落。" * 5,
                      "source": f"src-{i}"},
            )
            assert r.status_code == 200

    # Final WAL should be zero (last ingest's checkpoint truncated it, then the
    # shutdown checkpoint in lifespan re-confirmed emptiness).
    assert wal_mod.file_size(server.WAL_PATH) == 0

    # Service remains healthy and data is preserved
    import storage as storage_mod
    manifest = storage_mod.read_manifest(server.MANIFEST_PATH)
    assert manifest is not None
    assert manifest.chunks.count == len(server.stored_chunks)
