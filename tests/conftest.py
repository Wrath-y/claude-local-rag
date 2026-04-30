"""Shared pytest fixtures for storage / server tests."""

from __future__ import annotations

import os
import sys

# Ensure project root is importable so `import storage` / `import server` works
# regardless of where pytest is invoked from.
_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
if _ROOT not in sys.path:
    sys.path.insert(0, _ROOT)

# Force offline mode for HuggingFace so tests don't hit the network when the model
# cache is already populated. This must be set BEFORE any sentence_transformers import.
os.environ.setdefault("HF_HUB_OFFLINE", "1")
os.environ.setdefault("TRANSFORMERS_OFFLINE", "1")

import pytest


@pytest.fixture
def isolated_store(tmp_path, monkeypatch):
    """Swap server.py's storage paths to an isolated tmp directory.

    Server module is imported lazily so tests not needing it pay no model-load cost.
    After patching, `server.load_store()` can be called to initialize empty state.
    """
    import server  # noqa: E402 — heavy import, done lazily per test

    storage_dir = tmp_path / "storage"
    index_path = tmp_path / "index.bin"
    texts_path = tmp_path / "chunks.pkl"
    manifest_path = storage_dir / "manifest.json"
    wal_path = storage_dir / "wal.jsonl"

    monkeypatch.setattr(server, "DATA_DIR", str(tmp_path))
    monkeypatch.setattr(server, "STORAGE_DIR", str(storage_dir))
    monkeypatch.setattr(server, "INDEX_PATH", str(index_path))
    monkeypatch.setattr(server, "TEXTS_PATH", str(texts_path))
    monkeypatch.setattr(server, "MANIFEST_PATH", str(manifest_path))
    monkeypatch.setattr(server, "WAL_PATH", str(wal_path))

    # Reset in-memory globals to clean slate
    import faiss
    monkeypatch.setattr(server, "index", faiss.IndexFlatIP(server.DIM))
    monkeypatch.setattr(server, "stored_chunks", [])
    monkeypatch.setattr(server, "chunk_set", set())
    monkeypatch.setattr(server, "_wal_replaying", False)
    monkeypatch.setattr(server, "_wal_readonly_reason", None)
    monkeypatch.setattr(server, "_wal_next_seq", 0)
    server._emb_cache.clear()
    server._source_hashes.clear()
    server.rebuild_bm25()

    return {
        "server": server,
        "tmp_path": tmp_path,
        "storage_dir": storage_dir,
        "index_path": index_path,
        "texts_path": texts_path,
        "manifest_path": manifest_path,
        "wal_path": wal_path,
    }


@pytest.fixture
def seed_consistent_state():
    """Factory fixture: seed_consistent_state(server, chunks) -> index.

    Persists a consistent store (len(chunks) == index.ntotal) and syncs module
    globals so subsequent load_store() / endpoint calls operate on matching state.
    """
    def _seed(server, chunks):
        import faiss
        import numpy as np

        idx = faiss.IndexFlatIP(server.DIM)
        if chunks:
            rng = np.random.default_rng(42)
            vecs = rng.random((len(chunks), server.DIM), dtype=np.float32)
            norms = np.linalg.norm(vecs, axis=1, keepdims=True) + 1e-9
            vecs = (vecs / norms).astype(np.float32)
            idx.add(vecs)
        server.save_store(new_index=idx, new_chunks=chunks)
        server.index = idx
        server.stored_chunks = list(chunks)
        server.chunk_set = set(c["text"] for c in chunks)
        return idx

    return _seed
