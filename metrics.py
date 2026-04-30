"""Prometheus metric definitions for the RAG service.

Scope (change: health-metrics-observability):
- Dedicated CollectorRegistry so pytest re-imports don't duplicate-register
- Lazily exported via `render()` for the /metrics endpoint
"""

from __future__ import annotations

from prometheus_client import (
    CollectorRegistry,
    Counter,
    Gauge,
    Histogram,
    generate_latest,
)
from prometheus_client.exposition import CONTENT_TYPE_LATEST


registry = CollectorRegistry()

ingest_total = Counter(
    "rag_ingest_total",
    "Total number of /ingest requests, labelled by outcome.",
    ["result"],  # ok | skip | error
    registry=registry,
)

retrieve_total = Counter(
    "rag_retrieve_total",
    "Total number of /retrieve requests, labelled by whether any chunk was returned.",
    ["hit"],  # true | false
    registry=registry,
)

retrieve_latency_seconds = Histogram(
    "rag_retrieve_latency_seconds",
    "Wall-clock latency of /retrieve (end-to-end, including reranking if enabled).",
    registry=registry,
)

chunk_total = Gauge(
    "rag_chunk_total",
    "Current number of chunks in the vector store.",
    registry=registry,
)

index_bytes = Gauge(
    "rag_index_bytes",
    "Current size of the FAISS index file in bytes.",
    registry=registry,
)

model_load_seconds = Gauge(
    "rag_model_load_seconds",
    "Time taken to load the embedding model at startup.",
    registry=registry,
)

wal_replaying = Gauge(
    "rag_wal_replaying",
    "1 while the service is replaying WAL at startup, 0 otherwise.",
    registry=registry,
)

last_commit_timestamp_seconds = Gauge(
    "rag_last_commit_timestamp_seconds",
    "Unix timestamp of the most recent successful save_store commit.",
    registry=registry,
)


def render() -> bytes:
    """Return Prometheus text format bytes suitable for /metrics."""
    return generate_latest(registry)


# Convenience export so server.py can set Content-Type on responses
content_type = CONTENT_TYPE_LATEST
