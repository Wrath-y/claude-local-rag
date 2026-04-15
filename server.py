import os
import pickle
import re
import numpy as np
import faiss
import yaml
from contextlib import asynccontextmanager
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
from sentence_transformers import SentenceTransformer, CrossEncoder
from typing import List, Dict
from collections import Counter

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

# ================= STORAGE =================
def load_store():
    global index, stored_chunks, chunk_set
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
        print(f"[2/3] 向量库加载完成，已有 {len(stored_chunks)} 个 chunk")
    else:
        index = faiss.IndexFlatIP(DIM)
        stored_chunks = []
        chunk_set = set()
        print("[2/3] 向量库初始化（空库）")


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
def _bigrams(s: str) -> List[str]:
    s = s.lower()
    return [s[i:i+2] for i in range(len(s) - 1)]


def keyword_score(query: str, text: str) -> float:
    """计算 query 的 bigram 在 text 中的覆盖率，范围 [0, 1]。"""
    q_bg = _bigrams(query)
    if not q_bg:
        return 0.0
    t_counter = Counter(_bigrams(text))
    # 命中数 / query bigram 总数，衡量关键词覆盖度
    hits = sum(1 for bg in q_bg if t_counter[bg] > 0)
    return hits / len(q_bg)


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

    texts = [f"{DOC_PREFIX}{c['text']}" for c in new_chunks]
    embeddings = model.encode(texts, normalize_embeddings=True, show_progress_bar=False)
    embeddings = np.array(embeddings, dtype=np.float32)

    index.add(embeddings)
    stored_chunks.extend(new_chunks)
    save_store()

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

    candidates = []
    dropped_threshold = 0
    for score, i in zip(scores[0], indices[0]):
        if i >= len(stored_chunks):
            continue
        if score < SCORE_THRESHOLD:
            dropped_threshold += 1
            continue
        chunk = stored_chunks[i]
        kw = keyword_score(req.text, chunk["text"])
        final_score = score * 0.7 + kw * 0.3
        candidates.append((final_score, chunk, score, kw))

    log(f"[retrieve] 阈值过滤（< {SCORE_THRESHOLD}）丢弃 {dropped_threshold} 个，剩余 {len(candidates)} 个")

    candidates.sort(key=lambda x: x[0], reverse=True)
    top_candidates = candidates[:TOP_K]

    for final_score, chunk, vec_score, kw in top_candidates:
        src = chunk.get("source", "unknown")
        preview = chunk["text"][:40].replace("\n", " ")
        log(f"  vec={vec_score:.3f} kw={kw:.3f} final={final_score:.3f} [{src}] {preview!r}")

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
        texts = [f"{DOC_PREFIX}{c['text']}" for c in remaining]
        embeddings = model.encode(texts, normalize_embeddings=True, show_progress_bar=False)
        new_index.add(np.array(embeddings, dtype=np.float32))

    index = new_index
    stored_chunks = remaining
    chunk_set = set(c["text"] for c in remaining)
    save_store()
    return {"status": "ok", "removed_chunks": removed}


# ================= RESET =================
@app.delete("/reset")
def reset():
    global index, stored_chunks, chunk_set
    index = faiss.IndexFlatIP(DIM)
    stored_chunks = []
    chunk_set = set()
    for p in [INDEX_PATH, TEXTS_PATH]:
        if os.path.exists(p):
            os.remove(p)
    return {"status": "reset"}
