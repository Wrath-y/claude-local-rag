"""Task 6.5 — rag_reindex_progress_ratio visible during rebuild."""

from __future__ import annotations

from fastapi.testclient import TestClient


def test_rebuild_metric_final_value_zero(isolated_store, seed_consistent_state):
    server = isolated_store["server"]
    seed_consistent_state(server, [{"text": f"chunk {i} 内容", "source": "s"} for i in range(3)])

    with TestClient(server.app) as c:
        # Baseline: gauge is 0 before rebuild
        body0 = c.get("/metrics").text
        # prometheus gauges serialize as floats; an untouched gauge is 0.0
        assert "rag_reindex_progress_ratio 0.0" in body0 or "rag_reindex_progress_ratio\n" in body0

        r = c.post("/index/rebuild")
        assert r.status_code == 200

        # Wait for completion, then gauge should be back to 0
        import time
        for _ in range(30):
            if not server._index_rebuilding:
                break
            time.sleep(0.1)

        body1 = c.get("/metrics").text
    # After completion, gauge set back to 0
    assert "rag_reindex_progress_ratio 0.0" in body1
