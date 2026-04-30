"""Task 6.6 — /storage/integrity-check response includes disk_free_bytes."""

from __future__ import annotations

from fastapi.testclient import TestClient


def test_integrity_check_includes_disk_free(isolated_store, seed_consistent_state):
    server = isolated_store["server"]
    seed_consistent_state(server, [{"text": "a", "source": "s"}])

    with TestClient(server.app) as c:
        r = c.get("/storage/integrity-check")
    assert r.status_code == 200
    body = r.json()
    assert "disk_free_bytes" in body
    # Either a positive integer (real disk) or -1 (disk_usage raised OSError)
    assert isinstance(body["disk_free_bytes"], int)
    assert body["disk_free_bytes"] >= -1
