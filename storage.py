"""Storage layer: write-path serialization, atomic persistence, manifest integrity.

Scope: skeleton for the `concurrent-safe-storage` change. Consumed by server.py's
`save_store` / `load_store` / `ingest` / `delete_source` / `reset` (to be wired in
batch 2), and by the `/storage/integrity-check` endpoint (batch 3).

Not in scope here: WAL (wal-crash-recovery), auto-rebuild (index-self-healing),
backup scheduling (backup-restore-automation).
"""

from __future__ import annotations

import hashlib
import json
import os
import threading
from contextlib import contextmanager
from dataclasses import asdict, dataclass
from datetime import datetime, timezone
from typing import Any, Iterator, List, Optional

import faiss


# ================= Lock =================

_STORE_WRITE_LOCK = threading.RLock()


@contextmanager
def write_lock() -> Iterator[None]:
    with _STORE_WRITE_LOCK:
        yield


# ================= Path constants =================

TMP_SUFFIX = ".tmp"
NEW_SUFFIX = ".new"
MANIFEST_FILENAME = "manifest.json"
ORPHAN_SUFFIXES = (TMP_SUFFIX, NEW_SUFFIX)


def manifest_path_for(data_dir: str) -> str:
    return os.path.join(data_dir, MANIFEST_FILENAME)


# ================= Atomic write helpers =================

def _fsync_dir(path: str) -> None:
    fd = os.open(path, os.O_RDONLY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def atomic_write_bytes(path: str, data: bytes) -> None:
    """Write bytes to `path` atomically: tmp file -> fsync -> os.replace -> dir fsync.

    On any failure, the temp file is removed and the original at `path` (if any)
    is left untouched.
    """
    directory = os.path.dirname(os.path.abspath(path)) or "."
    tmp_path = path + TMP_SUFFIX
    try:
        fd = os.open(tmp_path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o644)
        try:
            with os.fdopen(fd, "wb") as f:
                f.write(data)
                f.flush()
                os.fsync(f.fileno())
        except Exception:
            raise
        os.replace(tmp_path, path)
        _fsync_dir(directory)
    except Exception:
        if os.path.exists(tmp_path):
            try:
                os.remove(tmp_path)
            except OSError:
                pass
        raise


def atomic_write_faiss(path: str, index: Any) -> None:
    """Write a FAISS index to `path` atomically using faiss.write_index + os.replace."""
    directory = os.path.dirname(os.path.abspath(path)) or "."
    new_path = path + NEW_SUFFIX
    try:
        faiss.write_index(index, new_path)
        # Best-effort fsync of the written file; faiss writes through libc, so we
        # re-open to flush OS buffers before renaming.
        fd = os.open(new_path, os.O_RDONLY)
        try:
            os.fsync(fd)
        finally:
            os.close(fd)
        os.replace(new_path, path)
        _fsync_dir(directory)
    except Exception:
        if os.path.exists(new_path):
            try:
                os.remove(new_path)
            except OSError:
                pass
        raise


def sha256_of_file(path: str) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for block in iter(lambda: f.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def cleanup_orphan_tempfiles(data_dir: str) -> List[str]:
    """Remove leftover `*.tmp` / `*.new` files in data_dir. Returns removed paths."""
    if not os.path.isdir(data_dir):
        return []
    removed: List[str] = []
    for name in os.listdir(data_dir):
        if name.endswith(ORPHAN_SUFFIXES):
            full = os.path.join(data_dir, name)
            if os.path.isfile(full):
                try:
                    os.remove(full)
                    removed.append(full)
                except OSError:
                    pass
    return removed


# ================= Manifest =================

MANIFEST_VERSION = 1


@dataclass
class ChunksSummary:
    path: str
    count: int
    sha256: str


@dataclass
class IndexSummary:
    path: str
    dim: int
    ntotal: int
    sha256: str


@dataclass
class ManifestV1:
    version: int
    committed_at: str
    chunks: ChunksSummary
    index: IndexSummary

    def to_dict(self) -> dict:
        return {
            "version": self.version,
            "committed_at": self.committed_at,
            "chunks": asdict(self.chunks),
            "index": asdict(self.index),
        }


def _now_iso() -> str:
    return datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


def read_manifest(path: str) -> Optional[ManifestV1]:
    """Load manifest. Returns None if file missing or schema invalid."""
    if not os.path.exists(path):
        return None
    try:
        with open(path, "r", encoding="utf-8") as f:
            raw = json.load(f)
    except (OSError, json.JSONDecodeError) as e:
        print(f"[storage] manifest unreadable at {path}: {e}")
        return None
    if not isinstance(raw, dict) or raw.get("version") != MANIFEST_VERSION:
        print(f"[storage] manifest version mismatch at {path}: {raw.get('version')!r}")
        return None
    try:
        return ManifestV1(
            version=raw["version"],
            committed_at=raw["committed_at"],
            chunks=ChunksSummary(**raw["chunks"]),
            index=IndexSummary(**raw["index"]),
        )
    except (KeyError, TypeError) as e:
        print(f"[storage] manifest schema invalid at {path}: {e}")
        return None


def write_manifest(path: str, manifest: ManifestV1) -> None:
    data = json.dumps(manifest.to_dict(), ensure_ascii=False, indent=2).encode("utf-8")
    atomic_write_bytes(path, data)


def build_manifest_from_files(
    chunks_path: str,
    index_path: str,
    chunks_count: int,
    index_obj: Any,
) -> ManifestV1:
    """Construct a manifest matching the current on-disk files and in-memory index."""
    return ManifestV1(
        version=MANIFEST_VERSION,
        committed_at=_now_iso(),
        chunks=ChunksSummary(
            path=os.path.basename(chunks_path),
            count=chunks_count,
            sha256=sha256_of_file(chunks_path),
        ),
        index=IndexSummary(
            path=os.path.basename(index_path),
            dim=index_obj.d,
            ntotal=index_obj.ntotal,
            sha256=sha256_of_file(index_path),
        ),
    )


@dataclass
class Mismatch:
    field: str
    expected: Any
    actual: Any

    def to_dict(self) -> dict:
        return {"field": self.field, "expected": self.expected, "actual": self.actual}


def verify_manifest(
    manifest: ManifestV1,
    chunks_path: str,
    chunks_count: int,
    index_path: str,
    index_obj: Any,
) -> List[Mismatch]:
    """Compare manifest fields against the live chunks/index state.

    Returns a list of mismatches; empty list means fully consistent.
    """
    mismatches: List[Mismatch] = []

    if manifest.chunks.count != chunks_count:
        mismatches.append(Mismatch("chunks.count", manifest.chunks.count, chunks_count))
    actual_chunks_sha = sha256_of_file(chunks_path)
    if manifest.chunks.sha256 != actual_chunks_sha:
        mismatches.append(Mismatch("chunks.sha256", manifest.chunks.sha256, actual_chunks_sha))

    if manifest.index.dim != index_obj.d:
        mismatches.append(Mismatch("index.dim", manifest.index.dim, index_obj.d))
    if manifest.index.ntotal != index_obj.ntotal:
        mismatches.append(Mismatch("index.ntotal", manifest.index.ntotal, index_obj.ntotal))
    actual_index_sha = sha256_of_file(index_path)
    if manifest.index.sha256 != actual_index_sha:
        mismatches.append(Mismatch("index.sha256", manifest.index.sha256, actual_index_sha))

    return mismatches
