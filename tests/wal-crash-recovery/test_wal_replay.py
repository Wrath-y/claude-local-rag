"""Task 8.4 — simulate crash-between-wal-append-and-save_store, verify replay."""

from __future__ import annotations

from fastapi.testclient import TestClient


def test_replay_reconstructs_uncommitted_ingest(isolated_store, seed_consistent_state):
    server = isolated_store["server"]
    import wal as wal_mod
    import storage as storage_mod

    # Baseline: 1 committed chunk with WAL offset=0 (as if previous shutdown checkpointed)
    seed_consistent_state(server, [{"text": "baseline", "source": "base"}])

    # Manually forge a WAL entry that was appended but never checkpointed (simulates crash
    # immediately after wal.append but before save_store succeeded).
    payload = {"text": "新 增 的 文档 等待 replay 恢复 原样。", "source": "replay-src"}
    record = wal_mod.make_record(seq=42, op="ingest", payload=payload)
    wal_mod.append(server.WAL_PATH, wal_mod.encode_record(record))

    # Force manifest.wal.committed_offset to 0 so replay path triggers
    manifest = storage_mod.read_manifest(server.MANIFEST_PATH)
    assert manifest is not None
    manifest.wal.committed_offset = 0
    manifest.wal.committed_seq = 0
    storage_mod.write_manifest(server.MANIFEST_PATH, manifest)

    # Restart — load_store will see uncommitted WAL and replay
    server.load_store()

    # After replay: chunks contain baseline + replayed doc's chunks
    sources = {c["source"] for c in server.stored_chunks}
    assert "base" in sources
    assert "replay-src" in sources

    # WAL truncated post-replay, manifest seq advanced
    assert wal_mod.file_size(server.WAL_PATH) == 0
    manifest_after = storage_mod.read_manifest(server.MANIFEST_PATH)
    assert manifest_after.wal.committed_offset == 0
    assert manifest_after.wal.committed_seq == 42

    # /health exits replaying state
    with TestClient(server.app) as c:
        r = c.get("/health")
    body = r.json()
    assert body["wal_replaying"] is False
    assert body["wal_readonly_reason"] is None
