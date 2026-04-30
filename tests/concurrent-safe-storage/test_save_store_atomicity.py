"""Task 6.4 — save_store atomic behavior: on mid-write failure, disk state unchanged."""

from __future__ import annotations

import os
import pickle
from unittest.mock import patch

import pytest


def _write_baseline(server, chunks, seed_fn):
    """Persist a known-good state via save_store and return the bytes on disk."""
    seed_fn(server, chunks)
    return {
        "index": open(server.INDEX_PATH, "rb").read(),
        "chunks": open(server.TEXTS_PATH, "rb").read(),
        "manifest": open(server.MANIFEST_PATH, "rb").read(),
    }


def test_save_store_failure_during_faiss_write_preserves_originals(isolated_store, seed_consistent_state):
    server = isolated_store["server"]

    baseline = _write_baseline(server, [{"text": "a", "source": "s1"}], seed_consistent_state)

    import faiss
    new_index = faiss.IndexFlatIP(server.DIM)
    new_chunks = [{"text": "a", "source": "s1"}, {"text": "b", "source": "s1"}]

    # Force FAISS write to fail mid-flight
    with patch("storage.faiss.write_index", side_effect=OSError("simulated faiss fail")):
        with pytest.raises(OSError):
            server.save_store(new_index=new_index, new_chunks=new_chunks)

    # Originals untouched
    assert open(server.INDEX_PATH, "rb").read() == baseline["index"]
    assert open(server.TEXTS_PATH, "rb").read() == baseline["chunks"]
    assert open(server.MANIFEST_PATH, "rb").read() == baseline["manifest"]

    # No orphan tempfile leaks in data dir or storage dir
    for fname in os.listdir(isolated_store["tmp_path"]):
        assert not fname.endswith((".tmp", ".new"))


def test_save_store_failure_during_chunks_write_preserves_originals(isolated_store, seed_consistent_state):
    server = isolated_store["server"]
    baseline = _write_baseline(server, [{"text": "a", "source": "s1"}], seed_consistent_state)

    import faiss
    new_index = faiss.IndexFlatIP(server.DIM)
    new_chunks = [{"text": "b", "source": "s2"}]

    call_count = {"n": 0}
    real_atomic_write = __import__("storage").atomic_write_bytes

    def fail_on_second(path, data):
        call_count["n"] += 1
        # atomic_write_bytes is called once for chunks, once for manifest; fail
        # on the chunks write (path endswith chunks.pkl)
        if path.endswith("chunks.pkl"):
            raise OSError("simulated chunks write fail")
        return real_atomic_write(path, data)

    with patch("storage.atomic_write_bytes", side_effect=fail_on_second):
        with pytest.raises(OSError):
            server.save_store(new_index=new_index, new_chunks=new_chunks)

    # Chunks and manifest unchanged. Index may have been written (we don't guarantee
    # atomicity across both files without WAL); that's covered by startup manifest
    # mismatch detection, tested in test_startup_mismatch.
    assert open(server.TEXTS_PATH, "rb").read() == baseline["chunks"]
    assert open(server.MANIFEST_PATH, "rb").read() == baseline["manifest"]


def test_save_store_roundtrip_updates_manifest(isolated_store):
    server = isolated_store["server"]
    import faiss
    new_index = faiss.IndexFlatIP(server.DIM)
    chunks = [{"text": "x", "source": "s"}]
    server.save_store(new_index=new_index, new_chunks=chunks)

    import storage as storage_mod
    manifest = storage_mod.read_manifest(server.MANIFEST_PATH)
    assert manifest is not None
    assert manifest.chunks.count == 1
    assert manifest.index.ntotal == 0  # empty index
    assert manifest.index.dim == server.DIM
