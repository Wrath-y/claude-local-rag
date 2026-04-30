"""Task 6.4 — /index/status reports normal / rebuilding / read-only."""

from __future__ import annotations

from fastapi.testclient import TestClient


def test_index_status_normal(isolated_store):
    server = isolated_store["server"]
    with TestClient(server.app) as c:
        r = c.get("/index/status")
    assert r.status_code == 200
    assert r.json()["state"] == "normal"


def test_index_status_readonly(isolated_store, monkeypatch):
    server = isolated_store["server"]
    monkeypatch.setattr(server, "_wal_readonly_reason", "test degraded reason")
    with TestClient(server.app) as c:
        r = c.get("/index/status")
    body = r.json()
    assert body["state"] == "read-only"
    assert "degraded" in body["reason"]


def test_index_status_rebuilding(isolated_store, monkeypatch):
    server = isolated_store["server"]
    monkeypatch.setattr(server, "_index_rebuilding", True)
    monkeypatch.setattr(server, "_index_rebuild_progress", 0.42)
    with TestClient(server.app) as c:
        r = c.get("/index/status")
    body = r.json()
    assert body["state"] == "rebuilding"
    assert abs(body["progress_ratio"] - 0.42) < 1e-6
