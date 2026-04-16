import io
import os
import pickle
import re
import shutil
import zipfile
import numpy as np
import faiss
import yaml
from contextlib import asynccontextmanager
from fastapi import FastAPI, File, HTTPException, UploadFile
from fastapi.responses import StreamingResponse
from pydantic import BaseModel
from sentence_transformers import SentenceTransformer, CrossEncoder
from rank_bm25 import BM25Okapi
from typing import List, Dict

# ================= CONFIG =================
_dir = os.path.dirname(os.path.abspath(__file__))

with open(os.path.join(_dir, "config.yaml"), "r") as f:
    config = yaml.safe_load(f)

MODEL_NAME = config["model"]["name"]
CHUNK_MIN = config["chunk"]["min_tokens"]
CHUNK_MAX = config["chunk"]["max_tokens"]
TOP_K = config["retrieve"]["top_k"]
INDEX_PATH = os.path.join(_dir, config["storage"]["index_path"])
TEXTS_PATH = os.path.join(_dir, config["storage"]["texts_path"])
DOC_PREFIX = config["embedding"]["doc_prefix"]
QUERY_PREFIX = config["embedding"]["query_prefix"]

SCORE_THRESHOLD = 0.45
# 固定 2 句，行为稳定可预期。
OVERLAP_SENTENCES = 2
RERANK_MODEL_NAME = config["rerank"]["model"]

# ================= MODEL =================
print(f"[1/3] 加载 embedding 模型：{MODEL_NAME} ...")
model = SentenceTransformer(MODEL_NAME)
DIM = model.get_embedding_dimension()
print(f"[1/3] 模型加载完成，向量维度：{DIM}")

# rerank 模型：lazy 加载，首次开启时初始化
rerank_enabled: bool = config["rerank"]["enabled"]
reranker: CrossEncoder = None

verbose_enabled: bool = config["retrieve"].get("verbose", True)

index: faiss.IndexFlatIP = None
stored_chunks: List[Dict] = []
chunk_set: set = set()
bm25: BM25Okapi = None
_emb_cache: Dict[str, np.ndarray] = {}  # text → normalized embedding
_stats = {"total_queries": 0, "zero_hit_queries": 0, "total_chunks_returned": 0}

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


def load_store():
    global index, stored_chunks, chunk_set
    _emb_cache.clear()
    if os.path.exists(INDEX_PATH) and os.path.exists(TEXTS_PATH):
        index = faiss.read_index(INDEX_PATH)
        with open(TEXTS_PATH, "rb") as f:
            raw = pickle.load(f)
        # 向后兼容：旧版存储为 List[str]，迁移为 List[Dict]
        if raw and isinstance(raw[0], str):
            stored_chunks = [{"text": t, "source": "unknown"} for t in raw]
        else:
            stored_chunks = raw
        chunk_set = set(c["text"] for c in stored_chunks)
        # 从 FAISS 向量直接恢复 embedding 缓存，避免 delete_source 重建索引时重复 encode
        for i, chunk in enumerate(stored_chunks):
            vec = np.zeros(DIM, dtype=np.float32)
            index.reconstruct(i, vec)
            _emb_cache[chunk["text"]] = vec
        print(f"[2/3] 向量库加载完成，已有 {len(stored_chunks)} 个 chunk")
    else:
        index = faiss.IndexFlatIP(DIM)
        stored_chunks = []
        chunk_set = set()
        print("[2/3] 向量库初始化（空库）")
    rebuild_bm25()


def save_store():
    faiss.write_index(index, INDEX_PATH)
    with open(TEXTS_PATH, "wb") as f:
        pickle.dump(stored_chunks, f)


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
    print(f"[3/3] 服务就绪，监听 http://127.0.0.1:{config['server']['port']}")
    yield
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


class RetrieveResponse(BaseModel):
    chunks: List[str]


# ================= INGEST =================
@app.post("/ingest")
def ingest(req: IngestRequest):
    if not req.text.strip():
        raise HTTPException(status_code=400, detail="text is empty")

    chunks = chunk_text(req.text)
    new_chunks = []
    for c in chunks:
        if c not in chunk_set:
            chunk_set.add(c)
            new_chunks.append({"text": c, "source": req.source})

    if not new_chunks:
        return {"status": "ok", "chunks_added": 0}

    embeddings = encode_with_cache([c["text"] for c in new_chunks])
    index.add(embeddings)
    stored_chunks.extend(new_chunks)
    save_store()
    rebuild_bm25()

    return {"status": "ok", "chunks_added": len(new_chunks)}


# ================= RETRIEVE =================
@app.post("/retrieve", response_model=RetrieveResponse)
def retrieve(req: RetrieveRequest):
    if not req.text.strip():
        raise HTTPException(status_code=400, detail="text is empty")

    def log(msg: str):
        if verbose_enabled:
            print(msg)

    q_short = req.text[:60] + ("..." if len(req.text) > 60 else "")
    log(f"[retrieve] 查询: {q_short!r}")

    if index.ntotal == 0:
        log("[retrieve] 向量库为空，返回空结果")
        return RetrieveResponse(chunks=[])

    query = f"{QUERY_PREFIX}{req.text}"
    embedding = model.encode([query], normalize_embeddings=True, show_progress_bar=False)
    embedding = np.array(embedding, dtype=np.float32)

    k = min(TOP_K * 3, index.ntotal)
    scores, indices = index.search(embedding, k)
    log(f"[retrieve] FAISS 返回 {k} 个候选（库总量 {index.ntotal}）")

    # 先过滤无效候选，再批量计算 BM25（避免逐条调用，性能更好）
    valid_indices: List[int] = []
    valid_vec_scores: List[float] = []
    dropped_threshold = 0
    for score, i in zip(scores[0], indices[0]):
        if i >= len(stored_chunks):
            continue
        if score < SCORE_THRESHOLD:
            dropped_threshold += 1
            continue
        valid_indices.append(int(i))
        valid_vec_scores.append(float(score))

    log(f"[retrieve] 阈值过滤（< {SCORE_THRESHOLD}）丢弃 {dropped_threshold} 个，剩余 {len(valid_indices)} 个")

    # BM25 批量评分，归一化到 [0, 1]
    if bm25 is not None and valid_indices:
        query_tokens = _bigrams(req.text)
        all_bm25 = bm25.get_scores(query_tokens)
        raw_kw = [float(all_bm25[i]) for i in valid_indices]
        max_kw = max(raw_kw) if max(raw_kw) > 0 else 1.0
        kw_scores = [s / max_kw for s in raw_kw]
    else:
        kw_scores = [0.0] * len(valid_indices)

    candidates = []
    for idx, vec_score, kw in zip(valid_indices, valid_vec_scores, kw_scores):
        chunk = stored_chunks[idx]
        final_score = vec_score * 0.7 + kw * 0.3
        candidates.append((final_score, chunk, vec_score, kw))

    candidates.sort(key=lambda x: x[0], reverse=True)
    top_candidates = candidates[:TOP_K]

    for final_score, chunk, vec_score, kw in top_candidates:
        src = chunk.get("source", "unknown")
        preview = chunk["text"][:40].replace("\n", " ")
        log(f"  vec={vec_score:.3f} bm25={kw:.3f} final={final_score:.3f} [{src}] {preview!r}")

    if rerank_enabled and reranker is not None and top_candidates:
        pairs = [(req.text, c["text"]) for _, c, _, _ in top_candidates]
        rerank_scores = reranker.predict(pairs, num_workers=0)
        reranked = sorted(zip(rerank_scores, top_candidates), key=lambda x: x[0], reverse=True)
        log("[retrieve] rerank 后顺序:")
        for rs, (_, chunk, _, _) in reranked:
            preview = chunk["text"][:40].replace("\n", " ")
            log(f"  rerank={rs:.3f} {preview!r}")
        top_candidates = [t for _, t in reranked]

    log(f"[retrieve] 最终返回 {len(top_candidates)} 个 chunks")

    _stats["total_queries"] += 1
    if not top_candidates:
        _stats["zero_hit_queries"] += 1
    _stats["total_chunks_returned"] += len(top_candidates)

    results = [
        f"[来源: {c['source']}]\n{c['text']}"
        for _, c, _, _ in top_candidates
    ]
    return RetrieveResponse(chunks=results)


# ================= RERANK TOGGLE =================
@app.post("/rerank/toggle")
def rerank_toggle(enabled: bool):
    global rerank_enabled, reranker
    rerank_enabled = enabled
    if enabled and reranker is None:
        print(f"[rerank] 首次开启，加载模型：{RERANK_MODEL_NAME} ...")
        reranker = CrossEncoder(RERANK_MODEL_NAME)
        print("[rerank] 模型加载完成")
    return {"rerank_enabled": rerank_enabled}


# ================= VERBOSE TOGGLE =================
@app.post("/retrieve/verbose")
def retrieve_verbose(enabled: bool):
    global verbose_enabled
    verbose_enabled = enabled
    return {"verbose_enabled": verbose_enabled}


# ================= HEALTH =================
@app.get("/health")
def health():
    return {"status": "ok", "total_chunks": len(stored_chunks), "rerank_enabled": rerank_enabled, "verbose_enabled": verbose_enabled}


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
@app.delete("/source")
def delete_source(name: str):
    global index, stored_chunks, chunk_set
    remaining = [c for c in stored_chunks if c.get("source") != name]
    removed = len(stored_chunks) - len(remaining)
    if removed == 0:
        raise HTTPException(status_code=404, detail=f"source '{name}' not found")

    new_index = faiss.IndexFlatIP(DIM)
    if remaining:
        embeddings = encode_with_cache([c["text"] for c in remaining])
        new_index.add(embeddings)

    index = new_index
    stored_chunks = remaining
    chunk_set = set(c["text"] for c in remaining)
    # 清理不再存在于任何来源的缓存条目，避免 rag-update 后缓存持续膨胀
    for k in [k for k in list(_emb_cache.keys()) if k not in chunk_set]:
        del _emb_cache[k]
    save_store()
    rebuild_bm25()
    return {"status": "ok", "removed_chunks": removed}


# ================= RESET =================
@app.delete("/reset")
def reset():
    global index, stored_chunks, chunk_set
    index = faiss.IndexFlatIP(DIM)
    stored_chunks = []
    chunk_set = set()
    _emb_cache.clear()
    rebuild_bm25()
    for p in [INDEX_PATH, TEXTS_PATH]:
        if os.path.exists(p):
            os.remove(p)
    return {"status": "reset"}


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
