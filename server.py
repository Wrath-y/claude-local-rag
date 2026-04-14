import os
import pickle
import re
import numpy as np
import faiss
import yaml
from fastapi import FastAPI, HTTPException
from pydantic import BaseModel
from sentence_transformers import SentenceTransformer
from typing import List

# Load config
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

app = FastAPI(title="Local RAG Plugin")

print(f"[1/3] 加载 embedding 模型：{MODEL_NAME} ...")
model = SentenceTransformer(MODEL_NAME)
DIM = model.get_embedding_dimension()
print(f"[1/3] 模型加载完成，向量维度：{DIM}")

index: faiss.IndexFlatIP = None
stored_chunks: List[str] = []


def load_store():
    global index, stored_chunks
    if os.path.exists(INDEX_PATH) and os.path.exists(TEXTS_PATH):
        index = faiss.read_index(INDEX_PATH)
        with open(TEXTS_PATH, "rb") as f:
            stored_chunks = pickle.load(f)
        print(f"[2/3] 向量库加载完成，已有 {len(stored_chunks)} 个 chunk")
    else:
        index = faiss.IndexFlatIP(DIM)
        stored_chunks = []
        print("[2/3] 向量库初始化（空库）")


def save_store():
    faiss.write_index(index, INDEX_PATH)
    with open(TEXTS_PATH, "wb") as f:
        pickle.dump(stored_chunks, f)


def chunk_text(text: str) -> List[str]:
    sentences = re.split(r'(?<=[。！？.!?\n])\s*', text)
    sentences = [s.strip() for s in sentences if s.strip()]

    chunks = []
    current = []
    current_len = 0

    for sentence in sentences:
        est_tokens = len(sentence)
        if current_len + est_tokens > CHUNK_MAX and current:
            chunks.append("".join(current))
            current = []
            current_len = 0
        current.append(sentence)
        current_len += est_tokens
        if current_len >= CHUNK_MIN:
            chunks.append("".join(current))
            current = []
            current_len = 0

    if current:
        chunks.append("".join(current))

    return [c for c in chunks if c.strip()]


@app.on_event("startup")
def startup():
    load_store()
    print(f"[3/3] 服务就绪，监听 http://127.0.0.1:{config['server']['port']}")


class IngestRequest(BaseModel):
    text: str


class RetrieveRequest(BaseModel):
    text: str


class RetrieveResponse(BaseModel):
    chunks: List[str]


@app.post("/ingest")
def ingest(req: IngestRequest):
    if not req.text.strip():
        raise HTTPException(status_code=400, detail="text is empty")

    chunks = chunk_text(req.text)
    if not chunks:
        raise HTTPException(status_code=400, detail="no chunks generated")

    prefixed = [f"{DOC_PREFIX}{c}" for c in chunks]
    embeddings = model.encode(prefixed, normalize_embeddings=True, show_progress_bar=False)
    embeddings = np.array(embeddings, dtype=np.float32)

    index.add(embeddings)
    stored_chunks.extend(chunks)
    save_store()

    return {"status": "ok", "chunks_added": len(chunks)}


@app.post("/retrieve", response_model=RetrieveResponse)
def retrieve(req: RetrieveRequest):
    if not req.text.strip():
        raise HTTPException(status_code=400, detail="text is empty")
    if index.ntotal == 0:
        return RetrieveResponse(chunks=[])

    prefixed = f"{QUERY_PREFIX}{req.text}"
    embedding = model.encode([prefixed], normalize_embeddings=True, show_progress_bar=False)
    embedding = np.array(embedding, dtype=np.float32)

    k = min(TOP_K, index.ntotal)
    _, indices = index.search(embedding, k)

    results = [stored_chunks[i] for i in indices[0] if i < len(stored_chunks)]
    return RetrieveResponse(chunks=results)


@app.get("/health")
def health():
    return {"status": "ok", "total_chunks": len(stored_chunks)}


@app.delete("/reset")
def reset():
    global index, stored_chunks
    index = faiss.IndexFlatIP(DIM)
    stored_chunks = []
    for path in [INDEX_PATH, TEXTS_PATH]:
        if os.path.exists(path):
            os.remove(path)
    return {"status": "reset"}
