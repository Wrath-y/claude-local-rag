"""Task 6.3 — concurrent save_store through the write lock must produce a consistent
final state (chunks count == index.ntotal == expected).

Uses save_store directly with distinct chunks per thread; avoids loading the
embedding model for speed. This is a tight stress on the lock + atomic write paths.
"""

from __future__ import annotations

import threading

import numpy as np
import pytest


def _random_vec(dim, seed):
    rng = np.random.default_rng(seed)
    v = rng.random(dim, dtype=np.float32)
    v /= np.linalg.norm(v) + 1e-9
    return v.astype(np.float32)


def test_50_parallel_save_store_ends_consistent(isolated_store):
    server = isolated_store["server"]
    import faiss
    import storage as storage_mod

    dim = server.DIM
    n_threads = 50
    chunks_per_thread = 10

    errors = []

    def worker(tid):
        try:
            for j in range(chunks_per_thread):
                with storage_mod.write_lock():
                    text = f"t{tid}-c{j}"
                    # Clone current index, append one fake vector
                    new_index = faiss.clone_index(server.index)
                    vec = _random_vec(dim, tid * 1000 + j).reshape(1, -1)
                    new_index.add(vec)
                    new_chunks = server.stored_chunks + [
                        {"text": text, "source": f"s{tid}", "source_hash": ""}
                    ]
                    server.save_store(new_index=new_index, new_chunks=new_chunks)
                    server.index = new_index
                    server.stored_chunks = new_chunks
                    server.chunk_set.add(text)
        except Exception as e:  # capture and fail the main test
            errors.append((tid, e))

    threads = [threading.Thread(target=worker, args=(i,)) for i in range(n_threads)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()

    assert errors == [], f"worker failures: {errors}"

    expected = n_threads * chunks_per_thread
    assert len(server.stored_chunks) == expected
    assert server.index.ntotal == expected

    # Manifest agrees with live state
    manifest = storage_mod.read_manifest(server.MANIFEST_PATH)
    assert manifest is not None
    assert manifest.chunks.count == expected
    assert manifest.index.ntotal == expected

    # verify_manifest has zero mismatches
    mismatches = storage_mod.verify_manifest(
        manifest, server.TEXTS_PATH, expected, server.INDEX_PATH, server.index
    )
    assert mismatches == [], f"unexpected mismatches after concurrent writes: {mismatches}"
