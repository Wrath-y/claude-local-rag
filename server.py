import hashlib
import io
import os
import pickle
import re
import shutil
import time
import zipfile
from datetime import datetime
import numpy as np
import faiss
import yaml
from contextlib import asynccontextmanager
from fastapi import FastAPI, File, HTTPException, UploadFile
from fastapi.responses import JSONResponse, StreamingResponse
from pydantic import BaseModel
from sentence_transformers import SentenceTransformer, CrossEncoder
from rank_bm25 import BM25Okapi
from typing import List, Dict, Optional

import storage
import wal as wal_mod
import metrics
import backup
from obs import structured_log

# ================= CONFIG =================
_dir = os.path.dirname(os.path.abspath(__file__))

with open(os.path.join(_dir, "config.yaml"), "r") as f:
    config = yaml.safe_load(f)

MODEL_NAME = config["model"]["name"]
CHUNK_MIN = config["chunk"]["min_tokens"]
CHUNK_MAX = config["chunk"]["max_tokens"]
TOP_K = config["retrieve"]["top_k"]
CONTEXT_WINDOW = config["retrieve"].get("context_window", 180000)
RESPONSE_RESERVE = config["retrieve"].get("response_reserve", 8000)
AVG_CHUNK_TOKENS = (CHUNK_MIN + CHUNK_MAX) // 2  # 每个 chunk 的平均 token 数估算
INDEX_PATH = os.path.join(_dir, config["storage"]["index_path"])
TEXTS_PATH = os.path.join(_dir, config["storage"]["texts_path"])
DATA_DIR = _dir
STORAGE_DIR = os.path.join(_dir, "storage")
MANIFEST_PATH = storage.manifest_path_for(STORAGE_DIR)
WAL_PATH = wal_mod.wal_path_for(STORAGE_DIR)

_wal_cfg = config["storage"].get("wal", {})
WAL_ENABLED: bool = bool(_wal_cfg.get("enabled", True))
WAL_MAX_SIZE_BYTES: int = int(float(_wal_cfg.get("max_size_mb", 10)) * 1024 * 1024)

DISK_FREE_ERROR_BYTES: int = 1 * 1024 * 1024 * 1024  # /health → error if free < 1GB

_backup_cfg = config["storage"].get("backup", {})
BACKUP_ENABLED: bool = bool(_backup_cfg.get("enabled", True))
BACKUP_CRON: str = str(_backup_cfg.get("schedule", "0 3 * * *"))
_retention = _backup_cfg.get("retention", {})
BACKUP_RETENTION_DAYS: int = int(_retention.get("days", 7))
BACKUP_RETENTION_WEEKS: int = int(_retention.get("weeks", 4))
BACKUPS_DIR: str = os.path.join(_dir, "backups")
_backup_timer = None  # threading.Timer handle


def _safe_disk_free() -> int:
    try:
        return shutil.disk_usage(DATA_DIR).free
    except OSError:
        return -1
DOC_PREFIX = config["embedding"]["doc_prefix"]
QUERY_PREFIX = config["embedding"]["query_prefix"]

LOG_LANG = config.get("log", {}).get("lang", "zh")

# ================= I18N =================
_MSGS: dict = {
    "zh": {
        "model_loading":           "[1/3] 加载 embedding 模型：{name} ...",
        "model_loaded":            "[1/3] 模型加载完成，向量维度：{dim}",
        "store_loaded":            "[2/3] 向量库加载完成，已有 {n} 个 chunk",
        "store_empty":             "[2/3] 向量库初始化（空库）",
        "service_ready":           "[3/3] 服务就绪，监听 http://127.0.0.1:{port}",
        "retrieve_query":          "[retrieve] 查询: {q}",
        "retrieve_empty_store":    "[retrieve] 向量库为空，返回空结果",
        "retrieve_dynamic_top_k":  "[retrieve] dynamic_top_k={k}（已用≈{used} tokens，剩余≈{remaining} tokens）",
        "retrieve_faiss":          "[retrieve] FAISS 返回 {k} 个候选（库总量 {n}）",
        "retrieve_threshold":      "[retrieve] 阈值过滤（< {t}）丢弃 {d} 个，剩余 {r} 个",
        "retrieve_rerank_header":  "[retrieve] rerank 后顺序:",
        "retrieve_final":          "[retrieve] 最终返回 {n} 个 chunks",
        "rerank_loading":          "[rerank] 首次开启，加载模型：{model} ...",
        "rerank_loaded":           "[rerank] 模型加载完成",
        "ingest_skip":             "[ingest] 来源 '{source}' 内容未变更，跳过入库",
    },
    "en": {
        "model_loading":           "[1/3] Loading embedding model: {name} ...",
        "model_loaded":            "[1/3] Model loaded, embedding dim: {dim}",
        "store_loaded":            "[2/3] Vector store loaded, {n} chunks",
        "store_empty":             "[2/3] Vector store initialized (empty)",
        "service_ready":           "[3/3] Service ready at http://127.0.0.1:{port}",
        "retrieve_query":          "[retrieve] query: {q}",
        "retrieve_empty_store":    "[retrieve] store is empty, returning no results",
        "retrieve_dynamic_top_k":  "[retrieve] dynamic_top_k={k} (used≈{used} tokens, remaining≈{remaining} tokens)",
        "retrieve_faiss":          "[retrieve] FAISS returned {k} candidates (total: {n})",
        "retrieve_threshold":      "[retrieve] threshold filter (< {t}) discarded {d}, remaining {r}",
        "retrieve_rerank_header":  "[retrieve] after rerank:",
        "retrieve_final":          "[retrieve] returning {n} chunks",
        "rerank_loading":          "[rerank] First enable — loading model: {model} ...",
        "rerank_loaded":           "[rerank] Model loaded",
        "ingest_skip":             "[ingest] source '{source}' unchanged, skipping ingest",
    },
}


def _t(key: str, **kwargs) -> str:
    lang = LOG_LANG if LOG_LANG in _MSGS else "zh"
    template = _MSGS[lang].get(key, key)
    return template.format(**kwargs) if kwargs else template


SCORE_THRESHOLD = 0.45
# 固定 2 句，行为稳定可预期。
OVERLAP_SENTENCES = 2
RERANK_MODEL_NAME = config["rerank"]["model"]

# ================= MODEL =================
print(_t("model_loading", name=MODEL_NAME))
_model_t0 = time.perf_counter()
model = SentenceTransformer(MODEL_NAME)
DIM = model.get_embedding_dimension()
metrics.model_load_seconds.set(time.perf_counter() - _model_t0)
print(_t("model_loaded", dim=DIM))

# rerank 模型：lazy 加载，首次开启时初始化
rerank_enabled: bool = config["rerank"]["enabled"]
reranker: CrossEncoder = None

verbose_enabled: bool = config["retrieve"].get("verbose", True)
dynamic_top_k_enabled: bool = config["retrieve"].get("dynamic_top_k", False)

index: faiss.IndexFlatIP = None
stored_chunks: List[Dict] = []
chunk_set: set = set()
bm25: BM25Okapi = None
_emb_cache: Dict[str, np.ndarray] = {}  # text → normalized embedding
_source_hashes: Dict[str, str] = {}  # source → MD5 of full ingested text (Plan A skip)
_stats = {"total_queries": 0, "zero_hit_queries": 0, "total_chunks_returned": 0}

# WAL runtime state (module-scoped, guarded by storage write lock for writes)
_wal_replaying: bool = False
_wal_readonly_reason: "Optional[str]" = None  # type: ignore[name-defined]
_wal_next_seq: int = 0  # incremented on every append

# Index rebuild state
_index_rebuilding: bool = False
_index_rebuild_progress: float = 0.0

# ================= STORAGE =================
def encode_with_cache(texts: List[str]) -> np.ndarray:
    """Encode texts with DOC_PREFIX, returning cache hits instantly and batching misses."""
    result = np.zeros((len(texts), DIM), dtype=np.float32)
    miss_idx: List[int] = []
    miss_prefixed: List[str] = []

    for i, t in enumerate(texts):
        if t in _emb_cache:
            result[i] = _emb_cache[t]
        else:
            miss_idx.append(i)
            miss_prefixed.append(f"{DOC_PREFIX}{t}")

    if miss_prefixed:
        embs = model.encode(miss_prefixed, normalize_embeddings=True, show_progress_bar=False)
        for j, i in enumerate(miss_idx):
            _emb_cache[texts[i]] = embs[j]
            result[i] = embs[j]

    return np.array(result, dtype=np.float32)


def rebuild_bm25():
    global bm25
    if stored_chunks:
        corpus = [_bigrams(c["text"]) or [c["text"].lower()] for c in stored_chunks]
        bm25 = BM25Okapi(corpus)
    else:
        bm25 = None


def _backup_members():
    """Members to include in every backup / restore."""
    return [
        backup.MemberSpec(arcname="chunks.pkl", abs_path=TEXTS_PATH),
        backup.MemberSpec(arcname="index.bin", abs_path=INDEX_PATH),
        backup.MemberSpec(arcname="storage/manifest.json", abs_path=MANIFEST_PATH),
        backup.MemberSpec(arcname="storage/wal.jsonl", abs_path=WAL_PATH),
    ]


def _backup_run_core(name_override: "Optional[str]" = None) -> dict:
    """Pack the four files to a zip under BACKUPS_DIR. Caller MUST hold write_lock.

    Returns `{status, path, size_bytes}`.
    """
    if name_override:
        dst = os.path.join(BACKUPS_DIR, name_override)
    else:
        now = datetime.now()
        day_dir = os.path.join(BACKUPS_DIR, now.strftime("%Y-%m-%d"))
        dst = os.path.join(day_dir, f"rag-{now.strftime('%H%M%S-%f')}.zip")
    size = backup.make_backup(_backup_members(), dst)
    metrics.backup_total.inc()
    metrics.last_backup_timestamp_seconds.set(time.time())
    structured_log("backup_done", path=dst, size_bytes=size)
    return {"status": "ok", "path": dst, "size_bytes": size}


def _prune_backups():
    try:
        deleted = backup.prune(BACKUPS_DIR, BACKUP_RETENTION_DAYS, BACKUP_RETENTION_WEEKS)
        if deleted:
            structured_log("backup_pruned", count=len(deleted))
    except Exception as e:
        print(f"[backup] prune failed: {e}")


def _restore_core(zip_path: str) -> dict:
    """Restore from a zip with transactional pre-restore snapshot + rollback."""
    global _wal_readonly_reason
    import shutil as _shutil
    import tempfile

    if not os.path.isfile(zip_path):
        raise HTTPException(status_code=404, detail=f"backup not found: {zip_path}")

    _wal_readonly_reason = "restore in progress"
    try:
        with storage.write_lock():
            # 1. Snapshot current state first
            pre_restore_name = f"pre-restore-{int(time.time())}.zip"
            pre_restore_path = os.path.join(BACKUPS_DIR, pre_restore_name)
            backup.make_backup(_backup_members(), pre_restore_path)

            # 2. Extract target zip to staging
            staging = tempfile.mkdtemp(prefix="rag_restore_", dir=DATA_DIR)
            try:
                extracted = backup.restore_from(zip_path, staging)

                # 3. Atomic replace each target file
                mapping = {
                    "chunks.pkl": TEXTS_PATH,
                    "index.bin": INDEX_PATH,
                    "storage/manifest.json": MANIFEST_PATH,
                    "storage/wal.jsonl": WAL_PATH,
                }
                for arc, target in mapping.items():
                    src = os.path.join(staging, arc)
                    if os.path.exists(src):
                        os.makedirs(os.path.dirname(os.path.abspath(target)), exist_ok=True)
                        os.replace(src, target)
                    else:
                        # If the backup predates a file (e.g. WAL didn't exist yet),
                        # truncate/remove the current one so the post-restore state
                        # matches the backup's intent. Keeping stale content here
                        # would silently leak post-backup writes through WAL replay.
                        if arc == "storage/wal.jsonl" and os.path.exists(target):
                            wal_mod.truncate_atomic(target)
                        elif arc == "storage/manifest.json" and os.path.exists(target):
                            os.remove(target)

                # 4. Reload — failure triggers rollback
                load_store()
            except Exception:
                # Rollback from pre-restore snapshot
                print("[restore] reload failed, rolling back from pre-restore snapshot")
                try:
                    rollback_staging = tempfile.mkdtemp(prefix="rag_rollback_", dir=DATA_DIR)
                    backup.restore_from(pre_restore_path, rollback_staging)
                    for arc, target in mapping.items():
                        src = os.path.join(rollback_staging, arc)
                        if os.path.exists(src):
                            os.replace(src, target)
                    load_store()
                    _wal_readonly_reason = "restore failed, rolled back — see logs"
                finally:
                    try:
                        _shutil.rmtree(rollback_staging, ignore_errors=True)
                    except Exception:
                        pass
                raise
            finally:
                try:
                    _shutil.rmtree(staging, ignore_errors=True)
                except Exception:
                    pass

            _wal_readonly_reason = None
            return {"status": "ok", "pre_restore": pre_restore_path, "restored_entries": extracted}
    finally:
        # If something set _wal_readonly_reason to "restore in progress" and we didn't clear it,
        # leave it as-is so operator sees the failure.
        pass


def _schedule_next_backup():
    """Register a one-shot Timer for the next cron fire; on fire run backup and re-schedule."""
    global _backup_timer
    if not BACKUP_ENABLED:
        return
    try:
        next_fire_fn = backup.parse_cron(BACKUP_CRON)
        next_ts = next_fire_fn(time.time())
        delay = max(1.0, next_ts - time.time())
    except Exception as e:
        print(f"[backup] failed to parse cron {BACKUP_CRON!r}: {e}")
        return

    import threading as _threading

    def _fire():
        global _backup_timer
        try:
            with storage.write_lock():
                _backup_run_core()
                _prune_backups()
        except Exception as e:
            print(f"[backup] scheduled run failed: {e}")
        # Re-schedule next one regardless of success
        _schedule_next_backup()

    _backup_timer = _threading.Timer(delay, _fire)
    _backup_timer.daemon = True
    _backup_timer.start()


def _cancel_backup_timer():
    global _backup_timer
    if _backup_timer is not None:
        try:
            _backup_timer.cancel()
        except Exception:
            pass
        _backup_timer = None


def _rebuild_index_core(chunks_list, batch_size: int = 64):
    """Rebuild a FAISS index from chunks by re-encoding in batches.

    Updates _index_rebuild_progress and the reindex metric each batch.
    Returns the new IndexFlatIP (caller swaps the global ref under write lock).
    """
    global _index_rebuild_progress
    new_index = faiss.IndexFlatIP(DIM)
    total = len(chunks_list)
    if total == 0:
        _index_rebuild_progress = 1.0
        metrics.reindex_progress_ratio.set(1.0)
        return new_index

    texts = [c["text"] for c in chunks_list]
    encoded = 0
    for i in range(0, total, batch_size):
        batch = texts[i : i + batch_size]
        prefixed = [f"{DOC_PREFIX}{t}" for t in batch]
        vecs = model.encode(prefixed, normalize_embeddings=True, show_progress_bar=False)
        vecs = np.array(vecs, dtype=np.float32)
        new_index.add(vecs)
        # Refresh embedding cache with freshly computed vectors
        for j, t in enumerate(batch):
            _emb_cache[t] = vecs[j]
        encoded += len(batch)
        _index_rebuild_progress = encoded / total
        metrics.reindex_progress_ratio.set(_index_rebuild_progress)
    return new_index


def _rebuild_index_sync():
    """Startup self-heal path: rebuild from in-memory stored_chunks synchronously."""
    global index
    new_index = _rebuild_index_core(stored_chunks)
    storage.atomic_write_faiss(INDEX_PATH, new_index)
    index = new_index
    save_store(new_index=new_index, new_chunks=stored_chunks,
               wal_offset=wal_mod.file_size(WAL_PATH) if WAL_ENABLED else 0,
               wal_seq=_wal_next_seq)
    print(f"[index] rebuilt from {len(stored_chunks)} chunks")


def _rebuild_index_async():
    """Background rebuild triggered by POST /index/rebuild."""
    global index, _index_rebuilding, _wal_readonly_reason, _index_rebuild_progress
    try:
        with storage.write_lock():
            new_index = _rebuild_index_core(stored_chunks)
            save_store(new_index=new_index, new_chunks=stored_chunks,
                       wal_offset=wal_mod.file_size(WAL_PATH) if WAL_ENABLED else 0,
                       wal_seq=_wal_next_seq)
            index = new_index
        print(f"[index] rebuild completed, ntotal={new_index.ntotal}")
    except Exception as e:
        print(f"[index] rebuild failed: {e}")
        _wal_readonly_reason = f"index rebuild failed: {e}"
    finally:
        _index_rebuilding = False
        _index_rebuild_progress = 0.0
        metrics.reindex_progress_ratio.set(0.0)
        # If readonly reason was set by rebuild kickoff AND rebuild succeeded, clear it.
        if _wal_readonly_reason == "index rebuild in progress":
            _wal_readonly_reason = None


def _apply_wal_record(record) -> None:
    """Re-apply a single WAL record via the internal *_core helpers.

    Caller MUST hold storage.write_lock(). Raises on business failure.
    """
    if record.op == "ingest":
        _ingest_core(record.payload["text"], record.payload.get("source", "unknown"))
    elif record.op == "delete_source":
        try:
            _delete_source_core(record.payload["name"])
        except HTTPException as e:
            # Source may have been deleted by a later op or already removed; skip gracefully.
            if e.status_code == 404:
                print(f"[wal] replay: delete_source '{record.payload['name']}' already absent, skipping")
            else:
                raise
    elif record.op == "reset":
        _reset_core()
    else:
        raise RuntimeError(f"unknown WAL op: {record.op!r}")


def _replay_wal_if_needed(manifest) -> None:
    """Replay uncommitted WAL entries under the write lock.

    Sets _wal_replaying / _wal_readonly_reason as needed. Truncates WAL on success.
    """
    global _wal_replaying, _wal_readonly_reason, _wal_next_seq
    if not WAL_ENABLED:
        return
    if not os.path.exists(WAL_PATH):
        return

    committed_offset = manifest.wal.committed_offset if manifest is not None else 0
    wal_size = wal_mod.file_size(WAL_PATH)
    if wal_size <= committed_offset:
        # Set _wal_next_seq so future appends continue from the last committed seq
        if manifest is not None:
            _wal_next_seq = manifest.wal.committed_seq
        return

    print(f"[wal] replaying ops from offset {committed_offset} (size={wal_size})")
    _wal_replaying = True
    metrics.wal_replaying.set(1)
    structured_log("wal_replay_start", committed_offset=committed_offset, wal_size=wal_size)
    replayed = 0
    last_seq = manifest.wal.committed_seq if manifest is not None else 0

    try:
        with storage.write_lock():
            for start, end, record, err in wal_mod.iter_records(WAL_PATH, committed_offset):
                if err is not None:
                    _wal_readonly_reason = f"wal corrupt at offset {err.offset}: {err}"
                    print(f"[wal] {_wal_readonly_reason}")
                    return
                try:
                    _apply_wal_record(record)
                except Exception as e:
                    _wal_readonly_reason = f"replay failed at seq {record.seq}: {e}"
                    print(f"[wal] {_wal_readonly_reason}")
                    return
                last_seq = record.seq
                replayed += 1

            # All replayed successfully — checkpoint and truncate.
            _wal_next_seq = last_seq
            save_store(new_index=index, new_chunks=stored_chunks, wal_offset=0, wal_seq=last_seq)
            wal_mod.truncate_atomic(WAL_PATH)
            print(f"[wal] replayed {replayed} ops, WAL truncated")
            structured_log("wal_replay_done", replayed=replayed, last_seq=last_seq)
    finally:
        _wal_replaying = False
        metrics.wal_replaying.set(0)


def load_store():
    global index, stored_chunks, chunk_set, _wal_next_seq
    _emb_cache.clear()
    _source_hashes.clear()

    # 清理上次崩溃残留的临时文件，避免干扰判断
    removed = storage.cleanup_orphan_tempfiles(DATA_DIR)
    if removed:
        print(f"[storage] cleaned orphan tempfiles: {[os.path.basename(p) for p in removed]}")

    # Self-heal: chunks present but index missing → sync rebuild from texts.
    if os.path.exists(TEXTS_PATH) and not os.path.exists(INDEX_PATH):
        with open(TEXTS_PATH, "rb") as f:
            raw = pickle.load(f)
        if raw and isinstance(raw[0], str):
            stored_chunks = [{"text": t, "source": "unknown"} for t in raw]
        else:
            stored_chunks = raw
        chunk_set = set(c["text"] for c in stored_chunks)
        index = faiss.IndexFlatIP(DIM)
        print(f"[index] index.bin missing, rebuilding from {len(stored_chunks)} chunks")
        _rebuild_index_sync()
        rebuild_bm25()
        return

    if os.path.exists(INDEX_PATH) and os.path.exists(TEXTS_PATH):
        global _wal_readonly_reason
        index = faiss.read_index(INDEX_PATH)
        # Dim mismatch degrades to read-only (old index still serves retrieves).
        if index.d != DIM:
            _wal_readonly_reason = (
                f"index dim mismatch: expected={DIM}, actual={index.d} — run /index/rebuild"
            )
            print(f"[index] {_wal_readonly_reason}")
        with open(TEXTS_PATH, "rb") as f:
            raw = pickle.load(f)
        # 向后兼容：旧版存储为 List[str]，迁移为 List[Dict]
        if raw and isinstance(raw[0], str):
            stored_chunks = [{"text": t, "source": "unknown"} for t in raw]
        else:
            stored_chunks = raw
        chunk_set = set(c["text"] for c in stored_chunks)

        # 先做一致性校验：count vs ntotal 不匹配时立即报错，避免后续 reconstruct 崩溃
        if len(stored_chunks) != index.ntotal:
            raise RuntimeError(
                f"storage inconsistency: chunks.count={len(stored_chunks)} != "
                f"index.ntotal={index.ntotal}"
            )

        # Manifest 校验：缺失则自动补齐；存在但与实际不一致则拒绝启动
        manifest = storage.read_manifest(MANIFEST_PATH)
        if manifest is None:
            os.makedirs(STORAGE_DIR, exist_ok=True)
            new_manifest = storage.build_manifest_from_files(
                TEXTS_PATH, INDEX_PATH, len(stored_chunks), index,
                wal_path=os.path.basename(WAL_PATH),
                wal_committed_offset=wal_mod.file_size(WAL_PATH) if WAL_ENABLED else 0,
                wal_committed_seq=0,
            )
            storage.write_manifest(MANIFEST_PATH, new_manifest)
            print(f"[storage] manifest missing, generated at {MANIFEST_PATH}")
            manifest = new_manifest
        else:
            mismatches = storage.verify_manifest(
                manifest, TEXTS_PATH, len(stored_chunks), INDEX_PATH, index,
                wal_path=WAL_PATH if WAL_ENABLED else None,
            )
            if mismatches:
                details = "; ".join(
                    f"{m.field}: expected={m.expected} actual={m.actual}" for m in mismatches
                )
                raise RuntimeError(
                    f"storage manifest mismatch, refusing to start: {details}"
                )

        # WAL replay AFTER manifest check (uses manifest.wal.committed_offset as anchor).
        # Replay may mutate index/stored_chunks, so embedding cache rebuild runs afterward.
        _replay_wal_if_needed(manifest)

        # 从 FAISS 向量直接恢复 embedding 缓存，避免 delete_source 重建索引时重复 encode
        # Dim mismatch 时跳过：reconstruct 的缓冲区与 index.d 不匹配，会损坏内存。
        _emb_cache.clear()
        if index.d == DIM:
            for i, chunk in enumerate(stored_chunks):
                vec = np.zeros(DIM, dtype=np.float32)
                index.reconstruct(i, vec)
                _emb_cache[chunk["text"]] = vec
        # 重建 source → hash 映射（Plan A 跳过相同内容重复入库）
        for chunk in stored_chunks:
            h = chunk.get("source_hash", "")
            if h:
                _source_hashes[chunk.get("source", "unknown")] = h
        print(_t("store_loaded", n=len(stored_chunks)))
    else:
        index = faiss.IndexFlatIP(DIM)
        stored_chunks = []
        chunk_set = set()
        print(_t("store_empty"))
    rebuild_bm25()


def _assert_writable():
    """Raise 503 if service is in WAL-induced read-only mode."""
    if _wal_readonly_reason is not None:
        raise HTTPException(status_code=503, detail=f"storage read-only: {_wal_readonly_reason}")


def _wal_append_op(op: str, payload: Dict) -> tuple:
    """Append one WAL record and return (new_offset, seq). No-op when WAL disabled.

    Caller MUST hold storage.write_lock().
    """
    global _wal_next_seq
    if not WAL_ENABLED:
        return 0, 0
    os.makedirs(STORAGE_DIR, exist_ok=True)
    _wal_next_seq += 1
    record = wal_mod.make_record(seq=_wal_next_seq, op=op, payload=payload)
    new_offset = wal_mod.append(WAL_PATH, wal_mod.encode_record(record))
    return new_offset, _wal_next_seq


def _maybe_checkpoint():
    """Truncate WAL if it has grown past the configured threshold.

    Caller MUST hold storage.write_lock(). Failure logs and leaves WAL intact.
    """
    if not WAL_ENABLED:
        return
    size = wal_mod.file_size(WAL_PATH)
    if size <= WAL_MAX_SIZE_BYTES:
        return
    try:
        save_store(new_index=index, new_chunks=stored_chunks, wal_offset=0, wal_seq=_wal_next_seq)
        wal_mod.truncate_atomic(WAL_PATH)
        print(f"[wal] checkpoint: truncated WAL ({size} bytes) after seq={_wal_next_seq}")
        metrics.last_commit_timestamp_seconds.set(time.time())
        structured_log("checkpoint_done", wal_seq=_wal_next_seq, wal_size_before=size)
    except Exception as e:
        print(f"[wal] checkpoint failed, keeping WAL: {e}")


def save_store(new_index=None, new_chunks=None, *, wal_offset: int = 0, wal_seq: int = 0):
    """Atomically persist the store.

    If new_index / new_chunks are provided, persist those instead of the current
    globals. Caller is responsible for swapping the module globals on success.
    On any failure, existing files on disk are left untouched.

    wal_offset / wal_seq are written into the manifest as the new commit point;
    pass 0 / 0 when WAL is disabled or when checkpointing (post-truncation).
    """
    idx_obj = new_index if new_index is not None else index
    chunks_obj = new_chunks if new_chunks is not None else stored_chunks

    storage.atomic_write_faiss(INDEX_PATH, idx_obj)
    storage.atomic_write_bytes(TEXTS_PATH, pickle.dumps(chunks_obj))

    os.makedirs(STORAGE_DIR, exist_ok=True)
    manifest = storage.build_manifest_from_files(
        TEXTS_PATH, INDEX_PATH, len(chunks_obj), idx_obj,
        wal_path=os.path.basename(WAL_PATH),
        wal_committed_offset=wal_offset,
        wal_committed_seq=wal_seq,
    )
    storage.write_manifest(MANIFEST_PATH, manifest)


# ================= CHUNK =================
def chunk_text(text: str) -> List[str]:
    sentences = re.split(r'(?<=[。！？.!?\n])\s*', text)
    sentences = [s.strip() for s in sentences if s.strip()]

    chunks = []
    current: List[str] = []
    current_len = 0

    def flush() -> None:
        nonlocal current, current_len
        chunks.append("".join(current))
        overlap = current[-OVERLAP_SENTENCES:]
        overlap_len = sum(len(s) for s in overlap)
        # overlap 本身超过 CHUNK_MAX 时丢弃，避免下一句立即触发溢出导致重复输出
        if overlap_len >= CHUNK_MAX:
            current = []
            current_len = 0
        else:
            current = overlap
            current_len = overlap_len

    for sentence in sentences:
        # CJK 字符按 1 token/字，其余（英文、数字、空格等）按 4 字符/token 估算
        cjk = sum(1 for c in sentence if '\u4e00' <= c <= '\u9fff')
        est_tokens = cjk + max(1, (len(sentence) - cjk) // 4)

        if current_len + est_tokens > CHUNK_MAX and current:
            flush()

        current.append(sentence)
        current_len += est_tokens

        if current_len >= CHUNK_MIN:
            flush()

    if current:
        chunks.append("".join(current))

    return [c for c in chunks if c.strip()]


# ================= HYBRID SEARCH =================
# 字符级 bigram（连续2字符）：中文词自然包含 bigram，英文也兼容。
# 同时作为 BM25 的 tokenizer，BM25 负责 TF-IDF 加权，比原始覆盖率更准确。
def _bigrams(s: str) -> List[str]:
    s = s.lower()
    return [s[i:i+2] for i in range(len(s) - 1)]


# ================= STARTUP =================
# @app.on_event("startup") 在 FastAPI 0.93+ 已弃用，改用 lifespan 上下文管理器
@asynccontextmanager
async def lifespan(_app: FastAPI):
    load_store()
    if BACKUP_ENABLED:
        os.makedirs(BACKUPS_DIR, exist_ok=True)
        _prune_backups()
        _schedule_next_backup()
    print(_t("service_ready", port=config['server']['port']))
    yield
    # Clean-shutdown checkpoint so the next start has nothing to replay.
    _cancel_backup_timer()
    if WAL_ENABLED and _wal_readonly_reason is None:
        try:
            with storage.write_lock():
                _maybe_checkpoint()
        except Exception as e:
            print(f"[wal] shutdown checkpoint failed: {e}")
    global reranker
    if reranker is not None:
        del reranker
        reranker = None
    import gc
    gc.collect()


# ================= INIT =================
app = FastAPI(title="Local RAG Plugin", lifespan=lifespan)


# ================= API =================
class IngestRequest(BaseModel):
    text: str
    source: str = "unknown"


class RetrieveRequest(BaseModel):
    text: str
    context_tokens_used: int = 0  # 由 hook 传入，用于动态调整 top_k


class RetrieveResponse(BaseModel):
    chunks: List[str]


# ================= INGEST =================
def _ingest_core(text: str, source: str) -> Dict:
    """Core ingest logic callable from both HTTP handler and WAL replay.

    Caller MUST hold storage.write_lock(). Does NOT append WAL — that is the
    handler's job so that replay of a WAL entry doesn't re-append itself.
    """
    global index, stored_chunks, chunk_set

    content_hash = hashlib.md5(text.encode("utf-8")).hexdigest()
    if source in _source_hashes and _source_hashes[source] == content_hash:
        print(_t("ingest_skip", source=source))
        return {"status": "skip", "chunks_added": 0, "reason": "content unchanged"}

    chunks = chunk_text(text)
    new_chunks = []
    local_chunk_set = set(chunk_set)
    for c in chunks:
        if c not in local_chunk_set:
            local_chunk_set.add(c)
            new_chunks.append({"text": c, "source": source, "source_hash": content_hash})

    if not new_chunks:
        _source_hashes[source] = content_hash
        return {"status": "ok", "chunks_added": 0}

    embeddings = encode_with_cache([c["text"] for c in new_chunks])
    new_index = faiss.clone_index(index)
    new_index.add(embeddings)
    merged_chunks = stored_chunks + new_chunks

    # WAL offset + seq are bookkept by the handler before calling us; pass current
    # file size as the new committed offset and current _wal_next_seq.
    save_store(
        new_index=new_index,
        new_chunks=merged_chunks,
        wal_offset=wal_mod.file_size(WAL_PATH) if WAL_ENABLED else 0,
        wal_seq=_wal_next_seq,
    )

    index = new_index
    stored_chunks = merged_chunks
    chunk_set = local_chunk_set
    _source_hashes[source] = content_hash
    rebuild_bm25()
    return {"status": "ok", "chunks_added": len(new_chunks)}


@app.post("/ingest")
def ingest(req: IngestRequest):
    _assert_writable()
    if not req.text.strip():
        metrics.ingest_total.labels(result="error").inc()
        raise HTTPException(status_code=400, detail="text is empty")

    try:
        with storage.write_lock():
            _wal_append_op("ingest", {"text": req.text, "source": req.source})
            result = _ingest_core(req.text, req.source)
            _maybe_checkpoint()
    except HTTPException:
        metrics.ingest_total.labels(result="error").inc()
        raise
    except Exception:
        metrics.ingest_total.labels(result="error").inc()
        raise

    label = "skip" if result.get("status") == "skip" else "ok"
    metrics.ingest_total.labels(result=label).inc()
    metrics.chunk_total.set(len(stored_chunks))
    try:
        metrics.index_bytes.set(os.path.getsize(INDEX_PATH))
    except OSError:
        pass
    structured_log(
        "ingest_done",
        source=req.source,
        status=result.get("status"),
        chunks_added=result.get("chunks_added", 0),
    )
    return result


# ================= RETRIEVE =================
@app.post("/retrieve", response_model=RetrieveResponse)
def retrieve(req: RetrieveRequest):
    if not req.text.strip():
        raise HTTPException(status_code=400, detail="text is empty")

    t0 = time.perf_counter()

    # 入口一次性 snapshot 全局引用：写路径可能在检索中途原子替换 index/chunks，
    # 使用本地引用保证单次检索全程基于一致快照（GIL 保证单次赋值读原子）
    local_index = index
    local_chunks = stored_chunks
    local_bm25 = bm25

    def log(msg: str):
        if verbose_enabled:
            print(msg)

    q_short = req.text[:60] + ("..." if len(req.text) > 60 else "")
    log(_t("retrieve_query", q=q_short))

    if local_index.ntotal == 0:
        log(_t("retrieve_empty_store"))
        return RetrieveResponse(chunks=[])

    # 动态 top_k：根据已用 token 数计算剩余空间，避免 RAG 结果撑爆上下文窗口
    if dynamic_top_k_enabled and req.context_tokens_used > 0:
        remaining = CONTEXT_WINDOW - req.context_tokens_used - RESPONSE_RESERVE
        chunk_budget = remaining // AVG_CHUNK_TOKENS
        dynamic_top_k = max(1, min(TOP_K, chunk_budget))
        log(_t("retrieve_dynamic_top_k", k=dynamic_top_k, used=req.context_tokens_used, remaining=remaining))
    else:
        dynamic_top_k = TOP_K

    query = f"{QUERY_PREFIX}{req.text}"
    embedding = model.encode([query], normalize_embeddings=True, show_progress_bar=False)
    embedding = np.array(embedding, dtype=np.float32)

    k = min(dynamic_top_k * 3, local_index.ntotal)
    scores, indices = local_index.search(embedding, k)
    log(_t("retrieve_faiss", k=k, n=local_index.ntotal))

    # 先过滤无效候选，再批量计算 BM25（避免逐条调用，性能更好）
    valid_indices: List[int] = []
    valid_vec_scores: List[float] = []
    dropped_threshold = 0
    for score, i in zip(scores[0], indices[0]):
        if i >= len(local_chunks):
            continue
        if score < SCORE_THRESHOLD:
            dropped_threshold += 1
            continue
        valid_indices.append(int(i))
        valid_vec_scores.append(float(score))

    log(_t("retrieve_threshold", t=SCORE_THRESHOLD, d=dropped_threshold, r=len(valid_indices)))

    # BM25 批量评分，归一化到 [0, 1]
    if local_bm25 is not None and valid_indices:
        query_tokens = _bigrams(req.text)
        all_bm25 = local_bm25.get_scores(query_tokens)
        raw_kw = [float(all_bm25[i]) for i in valid_indices]
        max_kw = max(raw_kw) if max(raw_kw) > 0 else 1.0
        kw_scores = [s / max_kw for s in raw_kw]
    else:
        kw_scores = [0.0] * len(valid_indices)

    candidates = []
    for idx, vec_score, kw in zip(valid_indices, valid_vec_scores, kw_scores):
        chunk = local_chunks[idx]
        final_score = vec_score * 0.7 + kw * 0.3
        candidates.append((final_score, chunk, vec_score, kw))

    candidates.sort(key=lambda x: x[0], reverse=True)
    top_candidates = candidates[:dynamic_top_k]

    for final_score, chunk, vec_score, kw in top_candidates:
        src = chunk.get("source", "unknown")
        preview = chunk["text"][:40].replace("\n", " ")
        log(f"  vec={vec_score:.3f} bm25={kw:.3f} final={final_score:.3f} [{src}] {preview!r}")

    if rerank_enabled and reranker is not None and top_candidates:
        pairs = [(req.text, c["text"]) for _, c, _, _ in top_candidates]
        rerank_scores = reranker.predict(pairs, num_workers=0)
        reranked = sorted(zip(rerank_scores, top_candidates), key=lambda x: x[0], reverse=True)
        log(_t("retrieve_rerank_header"))
        for rs, (_, chunk, _, _) in reranked:
            preview = chunk["text"][:40].replace("\n", " ")
            log(f"  rerank={rs:.3f} {preview!r}")
        top_candidates = [t for _, t in reranked]

    log(_t("retrieve_final", n=len(top_candidates)))

    _stats["total_queries"] += 1
    if not top_candidates:
        _stats["zero_hit_queries"] += 1
    _stats["total_chunks_returned"] += len(top_candidates)

    results = [
        f"[来源: {c['source']}]\n{c['text']}"
        for _, c, _, _ in top_candidates
    ]

    latency_s = time.perf_counter() - t0
    metrics.retrieve_latency_seconds.observe(latency_s)
    metrics.retrieve_total.labels(hit="true" if top_candidates else "false").inc()
    structured_log(
        "retrieve_done",
        hit=bool(top_candidates),
        latency_ms=round(latency_s * 1000, 2),
        returned_chunks=len(top_candidates),
    )
    return RetrieveResponse(chunks=results)


# ================= RERANK TOGGLE =================
@app.post("/rerank/toggle")
def rerank_toggle(enabled: bool):
    global rerank_enabled, reranker
    rerank_enabled = enabled
    if enabled and reranker is None:
        print(_t("rerank_loading", model=RERANK_MODEL_NAME))
        reranker = CrossEncoder(RERANK_MODEL_NAME)
        print(_t("rerank_loaded"))
    return {"rerank_enabled": rerank_enabled}


# ================= VERBOSE TOGGLE =================
@app.post("/retrieve/verbose")
def retrieve_verbose(enabled: bool):
    global verbose_enabled
    verbose_enabled = enabled
    return {"verbose_enabled": verbose_enabled}


# ================= DYNAMIC TOP_K TOGGLE =================
@app.post("/retrieve/dynamic-top-k")
def toggle_dynamic_top_k(enabled: bool):
    global dynamic_top_k_enabled
    dynamic_top_k_enabled = enabled
    return {"dynamic_top_k_enabled": dynamic_top_k_enabled}


# ================= STORAGE INTEGRITY =================
@app.get("/storage/integrity-check")
def storage_integrity_check():
    """Check on-disk files vs manifest vs in-memory index consistency.

    - 200: all consistent (returns summary + committed_at)
    - 200 with regenerated=True: manifest was missing, auto-generated from live state
    - 409: manifest present but mismatches actual files/index
    - 503: pickle or FAISS file unreadable / missing
    """
    if not os.path.exists(INDEX_PATH) or not os.path.exists(TEXTS_PATH):
        raise HTTPException(
            status_code=503,
            detail="storage files missing: chunks.pkl or index.bin not present",
        )

    # Only light sanity checks on live state (no file re-read — globals are authoritative)
    try:
        actual_count = len(stored_chunks)
        actual_ntotal = index.ntotal
        actual_dim = index.d
    except Exception as e:
        raise HTTPException(status_code=503, detail=f"in-memory index unreadable: {e}")

    manifest = storage.read_manifest(MANIFEST_PATH)

    if manifest is None:
        with storage.write_lock():
            # Re-check under lock: another caller may have generated it
            manifest = storage.read_manifest(MANIFEST_PATH)
            if manifest is None:
                os.makedirs(STORAGE_DIR, exist_ok=True)
                manifest = storage.build_manifest_from_files(
                    TEXTS_PATH, INDEX_PATH, actual_count, index
                )
                storage.write_manifest(MANIFEST_PATH, manifest)
        return {
            "status": "ok",
            "regenerated": True,
            "committed_at": manifest.committed_at,
            "chunks": {"count": manifest.chunks.count, "sha256": manifest.chunks.sha256},
            "index": {
                "dim": manifest.index.dim,
                "ntotal": manifest.index.ntotal,
                "sha256": manifest.index.sha256,
            },
            "wal": {
                "committed_offset": manifest.wal.committed_offset,
                "committed_seq": manifest.wal.committed_seq,
            },
            "disk_free_bytes": _safe_disk_free(),
        }

    try:
        mismatches = storage.verify_manifest(
            manifest, TEXTS_PATH, actual_count, INDEX_PATH, index
        )
    except OSError as e:
        raise HTTPException(status_code=503, detail=f"storage file unreadable: {e}")

    if mismatches:
        return JSONResponse(
            status_code=409,
            content={
                "status": "mismatch",
                "committed_at": manifest.committed_at,
                "mismatches": [m.to_dict() for m in mismatches],
            },
        )

    return {
        "status": "ok",
        "regenerated": False,
        "committed_at": manifest.committed_at,
        "chunks": {"count": manifest.chunks.count, "sha256": manifest.chunks.sha256},
        "index": {
            "dim": manifest.index.dim,
            "ntotal": manifest.index.ntotal,
            "sha256": manifest.index.sha256,
        },
        "wal": {
            "committed_offset": manifest.wal.committed_offset,
            "committed_seq": manifest.wal.committed_seq,
        },
        "disk_free_bytes": _safe_disk_free(),
    }


# ================= HEALTH =================
@app.get("/health")
def health():
    try:
        free_bytes = shutil.disk_usage(DATA_DIR).free
    except OSError:
        free_bytes = -1  # treat unreadable disk as ok-unknown, not error

    if free_bytes >= 0 and free_bytes < DISK_FREE_ERROR_BYTES:
        status_value = "error"
        reason = f"disk free {free_bytes} bytes below threshold {DISK_FREE_ERROR_BYTES}"
        http_code = 503
    elif _index_rebuilding:
        status_value = "degraded"
        reason = "index rebuild in progress"
        http_code = 200
    elif _wal_readonly_reason is not None:
        status_value = "degraded"
        reason = _wal_readonly_reason
        http_code = 200
    elif _wal_replaying:
        status_value = "degraded"
        reason = "wal replay in progress"
        http_code = 200
    else:
        status_value = "ok"
        reason = None
        http_code = 200

    if _index_rebuilding:
        index_state = "rebuilding"
    elif _wal_readonly_reason is not None:
        index_state = "read-only"
    else:
        index_state = "normal"

    body = {
        "status": status_value,
        "reason": reason,
        "total_chunks": len(stored_chunks),
        "rerank_enabled": rerank_enabled,
        "verbose_enabled": verbose_enabled,
        "wal_replaying": _wal_replaying,
        "wal_readonly_reason": _wal_readonly_reason,
        "disk_free_bytes": free_bytes,
        "index_rebuilding": _index_rebuilding,
        "index_state": index_state,
    }
    if http_code == 200:
        return body
    return JSONResponse(status_code=http_code, content=body)


@app.get("/metrics")
def metrics_endpoint():
    from fastapi.responses import Response
    return Response(content=metrics.render(), media_type=metrics.content_type)


@app.post("/backup/run")
def backup_run_endpoint():
    _assert_writable()
    with storage.write_lock():
        result = _backup_run_core()
        _prune_backups()
    return result


@app.get("/backup/list")
def backup_list_endpoint():
    return backup.list_backups(BACKUPS_DIR)


class RestoreRequest(BaseModel):
    file: str
    confirm: bool = False


@app.post("/backup/restore")
def backup_restore_endpoint(req: RestoreRequest):
    if not req.confirm:
        raise HTTPException(status_code=400, detail="confirm=true required for destructive restore")
    return _restore_core(req.file)


@app.post("/index/rebuild")
def index_rebuild_endpoint():
    global _index_rebuilding, _wal_readonly_reason, _index_rebuild_progress
    import threading as _threading
    if _index_rebuilding:
        raise HTTPException(status_code=409, detail="rebuild already in progress")
    _index_rebuilding = True
    _index_rebuild_progress = 0.0
    _wal_readonly_reason = "index rebuild in progress"
    t = _threading.Thread(target=_rebuild_index_async, daemon=True)
    t.start()
    return {"status": "started", "total_chunks": len(stored_chunks)}


@app.get("/index/status")
def index_status_endpoint():
    if _index_rebuilding:
        return {
            "state": "rebuilding",
            "progress_ratio": _index_rebuild_progress,
            "reason": "index rebuild in progress",
        }
    if _wal_readonly_reason is not None:
        return {"state": "read-only", "reason": _wal_readonly_reason}
    return {"state": "normal"}


# ================= STATS =================
@app.get("/stats")
def stats():
    total = _stats["total_queries"]
    zero = _stats["zero_hit_queries"]
    returned = _stats["total_chunks_returned"]
    hit_rate = round((total - zero) / total * 100, 1) if total > 0 else None
    avg_chunks = round(returned / total, 2) if total > 0 else None
    return {
        "total_queries": total,
        "zero_hit_queries": zero,
        "hit_rate_pct": hit_rate,
        "avg_chunks_per_query": avg_chunks,
        "note": "重启服务后统计重置"
    }


# ================= SOURCES =================
# 列出所有已入库的来源及各来源 chunk 数，便于管理和溯源。
@app.get("/sources")
def sources():
    counter: Dict[str, int] = {}
    for c in stored_chunks:
        src = c.get("source", "unknown")
        counter[src] = counter.get(src, 0) + 1
    return {"sources": [{"name": k, "chunks": v} for k, v in sorted(counter.items())]}


# ================= DELETE BY SOURCE =================
# 按来源名称删除 chunks。
# FAISS IndexFlatIP 不支持按 id 删除，必须用剩余 chunks 重建整个索引。
def _delete_source_core(name: str) -> Dict:
    """Core delete-source logic callable from both HTTP handler and replay.
    Caller MUST hold storage.write_lock()."""
    global index, stored_chunks, chunk_set

    remaining = [c for c in stored_chunks if c.get("source") != name]
    removed = len(stored_chunks) - len(remaining)
    if removed == 0:
        raise HTTPException(status_code=404, detail=f"source '{name}' not found")

    new_index = faiss.IndexFlatIP(DIM)
    if remaining:
        embeddings = encode_with_cache([c["text"] for c in remaining])
        new_index.add(embeddings)

    save_store(
        new_index=new_index,
        new_chunks=remaining,
        wal_offset=wal_mod.file_size(WAL_PATH) if WAL_ENABLED else 0,
        wal_seq=_wal_next_seq,
    )

    index = new_index
    stored_chunks = remaining
    chunk_set = set(c["text"] for c in remaining)
    for k in [k for k in list(_emb_cache.keys()) if k not in chunk_set]:
        del _emb_cache[k]
    _source_hashes.pop(name, None)
    rebuild_bm25()
    return {"status": "ok", "removed_chunks": removed}


@app.delete("/source")
def delete_source(name: str):
    _assert_writable()
    with storage.write_lock():
        _wal_append_op("delete_source", {"name": name})
        result = _delete_source_core(name)
        _maybe_checkpoint()
        return result


# ================= RESET =================
def _reset_core() -> Dict:
    """Core reset logic callable from both HTTP handler and replay.
    Caller MUST hold storage.write_lock()."""
    global index, stored_chunks, chunk_set

    new_index = faiss.IndexFlatIP(DIM)
    save_store(
        new_index=new_index,
        new_chunks=[],
        wal_offset=wal_mod.file_size(WAL_PATH) if WAL_ENABLED else 0,
        wal_seq=_wal_next_seq,
    )

    index = new_index
    stored_chunks = []
    chunk_set = set()
    _emb_cache.clear()
    _source_hashes.clear()
    rebuild_bm25()
    return {"status": "reset"}


@app.delete("/reset")
def reset():
    _assert_writable()
    with storage.write_lock():
        _wal_append_op("reset", {})
        result = _reset_core()
        _maybe_checkpoint()
        return result


# ================= EXPORT =================
@app.get("/export")
def export():
    if not os.path.exists(INDEX_PATH) or not os.path.exists(TEXTS_PATH):
        raise HTTPException(status_code=404, detail="向量库为空，无数据可导出")
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as zf:
        zf.write(INDEX_PATH, "index.bin")
        zf.write(TEXTS_PATH, "chunks.pkl")
    buf.seek(0)
    return StreamingResponse(
        buf,
        media_type="application/zip",
        headers={"Content-Disposition": "attachment; filename=rag_backup.zip"},
    )


# ================= IMPORT =================
@app.post("/import")
async def import_kb(file: UploadFile = File(...)):
    content = await file.read()
    buf = io.BytesIO(content)
    try:
        with zipfile.ZipFile(buf, "r") as zf:
            if "index.bin" not in zf.namelist() or "chunks.pkl" not in zf.namelist():
                raise HTTPException(status_code=400, detail="无效备份：缺少 index.bin 或 chunks.pkl")
            tmp_index = INDEX_PATH + ".tmp"
            tmp_texts = TEXTS_PATH + ".tmp"
            with open(tmp_index, "wb") as f:
                f.write(zf.read("index.bin"))
            with open(tmp_texts, "wb") as f:
                f.write(zf.read("chunks.pkl"))
    except zipfile.BadZipFile:
        raise HTTPException(status_code=400, detail="不是有效的 zip 文件")
    shutil.move(tmp_index, INDEX_PATH)
    shutil.move(tmp_texts, TEXTS_PATH)
    load_store()
    return {"status": "ok", "chunks_imported": len(stored_chunks)}
