"""Integration tests for /backup/run, /backup/list, /backup/restore."""

from __future__ import annotations

import os
import pickle
import zipfile

from fastapi.testclient import TestClient


def _point_backups(server, monkeypatch, tmp_path):
    backups_dir = tmp_path / "backups"
    monkeypatch.setattr(server, "BACKUPS_DIR", str(backups_dir))
    # Prevent scheduler from actually starting a timer during the test
    monkeypatch.setattr(server, "BACKUP_ENABLED", False)
    return str(backups_dir)


def test_backup_run_creates_zip(isolated_store, seed_consistent_state, tmp_path, monkeypatch):
    server = isolated_store["server"]
    backups_dir = _point_backups(server, monkeypatch, tmp_path)
    seed_consistent_state(server, [{"text": "一 段 文字。", "source": "s"}])

    with TestClient(server.app) as c:
        r = c.post("/backup/run")
    assert r.status_code == 200
    body = r.json()
    assert body["status"] == "ok"
    assert os.path.exists(body["path"])

    with zipfile.ZipFile(body["path"]) as zf:
        names = zf.namelist()
    assert "chunks.pkl" in names
    assert "index.bin" in names
    assert "storage/manifest.json" in names


def test_backup_list_reports_backups(isolated_store, seed_consistent_state, tmp_path, monkeypatch):
    """Two backups separated by > 1 day so retention policy doesn't drop them."""
    server = isolated_store["server"]
    backups_dir = _point_backups(server, monkeypatch, tmp_path)
    seed_consistent_state(server, [{"text": "abc", "source": "s"}])

    with TestClient(server.app) as c:
        r1 = c.post("/backup/run")
        assert r1.status_code == 200
        # Backdate the first backup file to simulate a "yesterday" backup
        import time as _time
        yesterday_ts = _time.time() - 86400
        os.utime(r1.json()["path"], (yesterday_ts, yesterday_ts))

        c.post("/backup/run")
        r = c.get("/backup/list")

    body = r.json()
    assert len(body) >= 2
    assert body[0]["modified_at"] >= body[1]["modified_at"]


def test_restore_requires_confirm(isolated_store, tmp_path, monkeypatch):
    server = isolated_store["server"]
    _point_backups(server, monkeypatch, tmp_path)
    with TestClient(server.app) as c:
        r = c.post("/backup/restore", json={"file": "/tmp/no.zip", "confirm": False})
    assert r.status_code == 400


def test_restore_roundtrips_data(isolated_store, seed_consistent_state, tmp_path, monkeypatch):
    server = isolated_store["server"]
    _point_backups(server, monkeypatch, tmp_path)
    seed_consistent_state(server, [{"text": "原始 文档。", "source": "orig"}])

    with TestClient(server.app) as c:
        r1 = c.post("/backup/run")
        backup_path = r1.json()["path"]

        # Mutate: reset everything
        r2 = c.post("/ingest", json={"text": "新 数据 要 被 覆盖 掉。", "source": "mutated"})
        assert r2.status_code == 200

        # Restore
        r3 = c.post("/backup/restore", json={"file": backup_path, "confirm": True})
        assert r3.status_code == 200
        assert r3.json()["status"] == "ok"

    # After restore: only original source visible
    sources = {c["source"] for c in server.stored_chunks}
    assert "orig" in sources
    assert "mutated" not in sources
    # pre-restore snapshot was created
    pre_restore_files = [
        f for f in os.listdir(os.path.join(tmp_path, "backups"))
        if f.startswith("pre-restore-")
    ]
    assert pre_restore_files


def test_backup_metrics_update(isolated_store, seed_consistent_state, tmp_path, monkeypatch):
    server = isolated_store["server"]
    _point_backups(server, monkeypatch, tmp_path)
    seed_consistent_state(server, [{"text": "metric test", "source": "m"}])

    with TestClient(server.app) as c:
        c.post("/backup/run")
        body = c.get("/metrics").text
    assert "rag_backup_total" in body
    assert "rag_last_backup_timestamp_seconds" in body
