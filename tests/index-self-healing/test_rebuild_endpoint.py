"""Task 6.2 — POST /index/rebuild starts async rebuild; write endpoints blocked during it."""

from __future__ import annotations

import time

from fastapi.testclient import TestClient


def test_rebuild_started_and_completes(isolated_store, seed_consistent_state):
    server = isolated_store["server"]
    seed_consistent_state(server, [
        {"text": f"基线 chunk 编号 {i} 的 文字 内容。", "source": "base"}
        for i in range(4)
    ])

    with TestClient(server.app) as c:
        r = c.post("/index/rebuild")
        assert r.status_code == 200
        assert r.json()["status"] == "started"

        # Wait briefly for the background thread to complete
        for _ in range(30):
            status = c.get("/index/status").json()
            if status["state"] == "normal":
                break
            time.sleep(0.1)
        else:
            raise AssertionError(f"rebuild didn't finish, state={status}")

    assert server._wal_readonly_reason is None
    assert server.index.ntotal == len(server.stored_chunks)


def test_rebuild_blocks_writes_while_running(isolated_store, seed_consistent_state, monkeypatch):
    server = isolated_store["server"]
    seed_consistent_state(server, [{"text": "some text", "source": "s"}])

    # Force the rebuilding flag on and make the write path attempt during "rebuild"
    monkeypatch.setattr(server, "_index_rebuilding", True)
    monkeypatch.setattr(server, "_wal_readonly_reason", "index rebuild in progress")

    with TestClient(server.app) as c:
        r = c.post("/ingest", json={"text": "新 数据 被 rebuild 阻断。", "source": "s"})
    assert r.status_code == 503
