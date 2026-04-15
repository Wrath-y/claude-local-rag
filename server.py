import os
import pickle
import re
import numpy as np
import faiss
import yaml
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
from sentence_transformers import SentenceTransformer
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
OVERLAP_RATIO = 0.2

# ================= INIT =================
app = FastAPI(title="Local RAG Plugin")

print(f"[1/3] 加载 embedding 模型：{MODEL_NAME} ...")
model = SentenceTransformer(MODEL_NAME)
DIM = model.get_embedding_dimension()
print(f"[1/3] 模型加载完成，向量维度：{DIM}")

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
    current = []
    current_len = 0
    overlap_size = max(1, int(len(sentences) * OVERLAP_RATIO)) if sentences else 0

    for sentence in sentences:
        # 用字符数估算 token，对中英文均适用
        est_tokens = len(sentence)

        if current_len + est_tokens > CHUNK_MAX and current:
            chunks.append("".join(current))
            current = current[-overlap_size:]
            current_len = sum(len(s) for s in current)

        current.append(sentence)
        current_len += est_tokens

        if current_len >= CHUNK_MIN:
            chunks.append("".join(current))
            current = current[-overlap_size:]
            current_len = sum(len(s) for s in current)

    if current:
        chunks.append("".join(current))

    return [c for c in chunks if c.strip()]


# ================= HYBRID SEARCH =================
def keyword_score(query: str, text: str) -> float:
    q_words = query.lower().split()
    t_words = text.lower().split()
    if not q_words:
        return 0.0
    counter = Counter(t_words)
    return sum(counter[w] for w in q_words) / len(q_words)


# ================= STARTUP =================
@app.on_event("startup")
def startup():
    load_store()
    print(f"[3/3] 服务就绪，监听 http://127.0.0.1:{config['server']['port']}")


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

    if index.ntotal == 0:
        return RetrieveResponse(chunks=[])

    query = f"{QUERY_PREFIX}{req.text}"
    embedding = model.encode([query], normalize_embeddings=True, show_progress_bar=False)
    embedding = np.array(embedding, dtype=np.float32)

    k = min(TOP_K * 3, index.ntotal)
    scores, indices = index.search(embedding, k)

    candidates = []
    for score, i in zip(scores[0], indices[0]):
        if i >= len(stored_chunks):
            continue
        if score < SCORE_THRESHOLD:
            continue
        chunk = stored_chunks[i]
        kw = keyword_score(req.text, chunk["text"])
        final_score = score * 0.7 + kw * 0.3
        candidates.append((final_score, chunk))

    candidates.sort(key=lambda x: x[0], reverse=True)

    results = [
        f"[来源: {c['source']}]\n{c['text']}"
        for _, c in candidates[:TOP_K]
    ]
    return RetrieveResponse(chunks=results)


# ================= HEALTH =================
@app.get("/health")
def health():
    return {"status": "ok", "total_chunks": len(stored_chunks)}


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
