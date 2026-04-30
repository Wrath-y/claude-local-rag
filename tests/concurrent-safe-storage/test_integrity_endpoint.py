"""Task 6.6 — GET /storage/integrity-check endpoint behavior."""

from __future__ import annotations

import os
import pickle

from fastapi.testclient import TestClient


def _client(server):
    return TestClient(server.app)


def test_integrity_check_returns_200_on_consistent_state(isolated_store, seed_consistent_state):
    server = isolated_store["server"]
    seed_consistent_state(server, [{"text": "a", "source": "s"}])

    with _client(server) as c:
        r = c.get("/storage/integrity-check")

    assert r.status_code == 200
    body = r.json()
    assert body["status"] == "ok"
    assert body["regenerated"] is False
    assert body["chunks"]["count"] == 1


def test_integrity_check_returns_409_on_mismatch(isolated_store, seed_consistent_state):
    server = isolated_store["server"]
    seed_consistent_state(server, [{"text": "a", "source": "s"}])

    with _client(server) as c:
        # Tamper AFTER lifespan startup so load_store doesn't abort first
        with open(server.TEXTS_PATH, "wb") as f:
            pickle.dump([{"text": "DIFFERENT", "source": "s"}], f)
        r = c.get("/storage/integrity-check")

    assert r.status_code == 409
    body = r.json()
    assert body["status"] == "mismatch"
    fields = {m["field"] for m in body["mismatches"]}
    assert "chunks.sha256" in fields


def test_integrity_check_autogenerates_missing_manifest(isolated_store, seed_consistent_state):
    server = isolated_store["server"]
    seed_consistent_state(server, [{"text": "a", "source": "s"}])

    with _client(server) as c:
        os.remove(server.MANIFEST_PATH)
        assert not os.path.exists(server.MANIFEST_PATH)
        r = c.get("/storage/integrity-check")

    assert r.status_code == 200
    body = r.json()
    assert body["regenerated"] is True
    assert os.path.exists(server.MANIFEST_PATH)


def test_integrity_check_503_when_files_missing(isolated_store):
    server = isolated_store["server"]
    assert not os.path.exists(server.INDEX_PATH)

    with _client(server) as c:
        r = c.get("/storage/integrity-check")

    assert r.status_code == 503
