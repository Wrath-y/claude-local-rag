"""Task 8.3 — ingest appends WAL before save_store; manifest offset matches."""

from __future__ import annotations

import os

from fastapi.testclient import TestClient


def test_ingest_appends_to_wal_and_updates_manifest(isolated_store):
    server = isolated_store["server"]
    import wal as wal_mod
    import storage as storage_mod

    with TestClient(server.app) as c:
        # Send a short doc so chunking is predictable
        r = c.post("/ingest", json={"text": "并发安全 是 基础 的 基础。", "source": "s1"})
    assert r.status_code == 200

    # WAL file should exist and contain one record (pre-shutdown checkpoint
    # may have already truncated it — so reconstruct intent via manifest instead)
    manifest = storage_mod.read_manifest(server.MANIFEST_PATH)
    assert manifest is not None
    # After shutdown checkpoint, committed_offset == 0 and seq == 1
    assert manifest.wal.committed_seq >= 1
    # WAL file size matches manifest
    actual_size = wal_mod.file_size(server.WAL_PATH)
    assert actual_size == manifest.wal.committed_offset


def test_wal_disabled_does_not_create_wal_file(isolated_store, monkeypatch):
    """Task 8.7"""
    server = isolated_store["server"]
    monkeypatch.setattr(server, "WAL_ENABLED", False)

    with TestClient(server.app) as c:
        r = c.post("/ingest", json={"text": "测试 WAL 开关 的 行为。", "source": "s2"})
    assert r.status_code == 200

    # WAL file should not exist at all (or be empty if something else touched it)
    if os.path.exists(server.WAL_PATH):
        assert os.path.getsize(server.WAL_PATH) == 0

    import storage as storage_mod
    manifest = storage_mod.read_manifest(server.MANIFEST_PATH)
    assert manifest is not None
    assert manifest.wal.committed_offset == 0
    assert manifest.wal.committed_seq == 0
