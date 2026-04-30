"""Task 6.4 — /health three-state behavior."""

from __future__ import annotations

from collections import namedtuple
from unittest.mock import patch

from fastapi.testclient import TestClient


def test_health_ok_when_normal(isolated_store):
    server = isolated_store["server"]
    with TestClient(server.app) as c:
        r = c.get("/health")
    assert r.status_code == 200
    assert r.json()["status"] == "ok"


def test_health_degraded_during_wal_replay(isolated_store, monkeypatch):
    server = isolated_store["server"]
    with TestClient(server.app) as c:
        monkeypatch.setattr(server, "_wal_replaying", True)
        r = c.get("/health")
    assert r.status_code == 200
    body = r.json()
    assert body["status"] == "degraded"
    assert "replay" in body["reason"]


def test_health_degraded_when_wal_readonly(isolated_store, monkeypatch):
    server = isolated_store["server"]
    with TestClient(server.app) as c:
        monkeypatch.setattr(server, "_wal_readonly_reason", "wal corrupt at offset 42")
        r = c.get("/health")
    assert r.status_code == 200
    body = r.json()
    assert body["status"] == "degraded"
    assert "corrupt" in body["reason"]


def test_health_error_when_disk_low(isolated_store, monkeypatch):
    server = isolated_store["server"]
    FakeUsage = namedtuple("FakeUsage", ["total", "used", "free"])
    with TestClient(server.app) as c:
        with patch("server.shutil.disk_usage", return_value=FakeUsage(total=100, used=99, free=500_000_000)):
            r = c.get("/health")
    assert r.status_code == 503
    body = r.json()
    assert body["status"] == "error"
    assert "disk" in body["reason"]
