## 1. Prerequisite Gate and Operability Migration

- [x] 1.1 Verify the implemented `graph-snapshot-lifecycle`, `constrained-graph-query`, and `hybrid-graph-retrieval` changes expose checksummed `graph/1..3` migrations, immutable source rows, the durable graph task/worker, selected FTS/Vector generation catalog, callback-scoped graph read view, StoreLifecycle lease, shared strict `/v1` errors/request IDs, OpenAPI, and digested fixtures; adapt through narrow interfaces without duplicating them.
- [x] 1.2 Add transactional checksummed migration `graph/4` for missing graph-task operation/phase/progress/request/warning/error/result fields, rebuild idempotency records, durable component-step records, and `graph_indexes` generation metadata with all namespace/version/task foreign keys and bounded state constraints.
- [x] 1.3 Add generation-addressable graph adjacency/filter acceleration rows and indexes only if the applied graph-query store lacks an equivalent generation seam, retain direct immutable-edge reads for pre-`graph/4` ready snapshots, and make newly built snapshots create an initial selected graph-index generation.
- [x] 1.4 Add fresh, legacy-only, `graph/1..3`-populated, repeated-open, checksum-mismatch, partial-migration-failure, and rollback tests proving `graph/4` preserves every legacy record, snapshot/hash/head, task, generation, query, and retrieval result.
- [x] 1.5 Add store query-plan and constraint tests for task claiming, idempotency lookup, private-generation cleanup, component selection, adjacency by namespace/version/generation/direction/filter, and proof that no unscoped graph read can cross a namespace or version.

## 2. Shared Operability Types, Build Identity, and Errors

- [x] 2.1 Add transport-neutral health, dependency, capability, limit, rebuild request/submission, task phase/progress/warning/result, generation identity, and safe diagnostic types under the graph operability boundary, reusing lifecycle/query/retrieval types instead of defining wire-incompatible copies.
- [x] 2.2 Add `internal/buildinfo` with release linker injection and the valid `0.0.0-dev` fallback, plus tests for SemVer output and deterministic service/API/Graph Schema version reporting.
- [x] 2.3 Extend the shared graph error catalog and HTTP mapping with `INVALID_REBUILD_REQUEST`, `IDEMPOTENCY_CONFLICT`, and `REIMPORT_REQUIRED`; audit all prerequisite stable codes against the required 400/404/409/422/500/503 table and add retryability/sanitization tests without changing legacy error envelopes.
- [x] 2.4 Define one normalized rebuild component order and bounded `Idempotency-Key` validation, reject duplicate/unknown JSON members and empty/unknown component sets, and unit-test canonical fingerprints for reordered/duplicated component inputs.
- [x] 2.5 Expose the effective snapshot, traverse, paths, retrieval, and rebuild ceilings through shared configuration/registry values so health and request validation cannot drift; add default, override, and invalid-limit tests.

## 3. Durable Graph Tasks and Restart Recovery

- [x] 3.1 Extend the graph task repository to create/load/update typed snapshot-build and snapshot-rebuild tasks with only queued/running/succeeded/failed states, stable phases, integer durable progress, canonical ordered warnings, timestamps, source hash, and typed terminal result/error.
- [x] 3.2 Implement atomic oldest-queued claim and operation dispatch in the existing single graph worker, persist each phase/component boundary with monotonic progress, and keep embedding/provider calls outside SQLite transactions.
- [x] 3.3 Implement one startup recovery transaction that changes running tasks back to queued under the same IDs, preserves queued and terminal tasks, reconciles selected generations, marks incomplete private generations for cleanup, and wakes work only after recovery commits.
- [x] 3.4 Implement graceful graph-worker close that stops claims, cancels provider work, waits for the current database boundary, and leaves unfinished work requeueable without inventing canceled/interrupted states.
- [x] 3.5 Extend `GET /v1/tasks/{task_id}` to return the common four-state resource, stable rebuild phases, nondecreasing progress, warnings, typed result/error, and `TASK_NOT_FOUND`, while keeping existing snapshot-build task fields and exposing no cancel route.
- [x] 3.6 Add repository/service tests for every legal transition, rejected terminal mutation, concurrent claims, progress monotonicity, warning order, missing task, queued/running close-reopen recovery, post-promotion restart, and byte-equivalent terminal resources after restart.

## 4. Rebuild Admission and Trusted Source Verification

- [x] 4.1 Add a strict `POST /v1/graphs/:namespace/snapshots/:version/rebuild` handler and transport-neutral service that validates the body/header before graph work and returns 202 with task ID, state, Location/task URL, normalized components, namespace, and version.
- [x] 4.2 Implement the short admission transaction that reads the target/source hash, returns immediate `REIMPORT_REQUIRED` for a known-missing source, replays an exact target/key/fingerprint/hash as the original task, rejects changed work with `IDEMPOTENCY_CONFLICT`, or atomically inserts one queued task, component steps, and idempotency row before waking the worker.
- [x] 4.3 Implement a consistent immutable source reader that loads only the target Graph Snapshot's canonical Nodes, Edges, provenance, search-document inputs, content hash, and retained/evicted generation identities; prove it never calls Eco Guardian, legacy chunk storage, inference, activation, or retrieval submission paths.
- [x] 4.4 Reuse the lifecycle JCS canonicalizer to verify counts, IDs, endpoints, canonical hash, and namespace/version isolation before component building, mapping missing/incomplete/corrupt source data to sanitized non-retryable `REIMPORT_REQUIRED` without changing the snapshot.
- [x] 4.5 Add admission/source tests for exact replay before/during/after completion, component reordering, key conflict, concurrent same-key races, absent snapshot, deleted source rows, dangling/corrupt rows, hash mismatch, and proof that every rejection creates no task/generation or preserves the original one as specified.

## 5. Shadow Generations and Atomic Rebuild

- [x] 5.1 Implement a private `graph_indexes` generation builder from immutable Nodes/Edges with outgoing/incoming/filter coverage and content digests, then adapt the prerequisite graph read view to capture/use one selected generation while hydrating canonical evidence from immutable rows.
- [x] 5.2 Implement a private FTS generation builder from the lifecycle deterministic search-document formatter and recorded tokenizer/algorithm identity, with generation-scoped virtual rows and exact Node/document coverage validation.
- [x] 5.3 Implement a private Vector generation builder that requires the recorded compatible provider/model/prefix/dimension identity, embeds in bounded batches outside transactions, writes only generation-scoped rows, and fails safely on provider, count, dimension, or identity mismatch.
- [x] 5.4 Implement shared structural, digest, namespace/version, identity, and representative-query validation for each component, using fixed traverse/paths/BM25/Vector/retrieve fixtures in tests and keeping every unvalidated generation unreadable.
- [x] 5.5 Implement one promotion transaction that rechecks source hash/task state, selects all requested validated generations together, records task success/result and generation identities/digests, leaves unrequested selectors unchanged, and rolls back the entire selection on any fault.
- [x] 5.6 Integrate retired/private generation cleanup with the existing retention path and startup recovery so cleanup is idempotent, read-lease safe, and never deletes snapshots, Nodes, Edges, active heads, content hashes, tasks, or the last selected generation.
- [x] 5.7 Add store/service failure injection before and after every graph-index, FTS, Vector, validation, and promotion boundary; assert failure/restart preserves old selected generations, hides partial rows, avoids duplicate provider work after durable boundaries, and eventually yields one terminal task.
- [x] 5.8 Add equivalence and concurrency tests proving fixed traverse/paths and retrieval results before/after rebuild, all-requested-component atomic visibility, unrequested-component preservation, identical IDs across namespaces/versions, and old-or-new consistency during promotion/retention/activation/deletion.

## 6. Structured Health and Runtime Wiring

- [x] 6.1 Implement a typed capability/limit/dependency registry that reports snapshot lifecycle, traverse, paths, retrieval modes, rebuild components, task polling, SQLite, `graph/1..4` migrations, graph worker, core query, BM25, Vector, and Rerank with deterministic ordering and safe identities.
- [x] 6.2 Implement bounded probes and one health reducer: live SQLite/migration/core-query failures produce unavailable/503; expected worker or optional retrieval dependency failures produce degraded/200; disabled optional Rerank is neutral; and health never embeds content, invokes an LLM, enqueues work, creates generations, or depends on a namespace.
- [x] 6.3 Add optional provider capability probes plus short-TTL/last-known-state caching and invalidation on real provider outcomes, exposing safe provider/model identities and timestamps without secrets, raw provider payloads, SQL, or paths.
- [x] 6.4 Replace the current embedder-only `internal/handler/health.go` response with the exact versioned health DTO and 200/503 mapping, and add matrix tests for ok/degraded/unavailable, disabled Rerank, Vector/Rerank faults, worker faults, migration/store/core-query faults, deterministic encoding, sanitization, and repeated-call side-effect freedom.
- [x] 6.5 Wire build info, operability repository/service, shared graph worker, health registry, rebuild route, and expanded task route explicitly in `cmd/server/main.go` under the existing `/v1` middleware; gate acceptance/readiness on migration/recovery and preserve all flat legacy routes.
- [ ] 6.6 Implement ordered shutdown for HTTP admission, graph worker, other workers, sidecar, and StoreLifecycle, then add process-level tests proving shutdown at each rebuild phase yields a safely requeued task on reopen rather than store use after close.

## 7. Request Correlation, Metrics, Logs, and Local Runbook

- [x] 7.1 Audit snapshot/activation/deletion/traverse/paths/retrieve/rebuild/task handlers to use the one shared `/v1` request-ID middleware and error writer, propagate `X-Request-ID`, persist the causal submission request ID on tasks, and test valid/missing/invalid IDs plus background failures.
- [x] 7.2 Add graph health-state, task queue/transition/duration, rebuild component outcome/duration, and recovery Prometheus collectors in `internal/observe`, with only bounded enum labels and registry tests that reject namespaces, versions, generations, task/request IDs, graph/query text, and fixture secrets.
- [x] 7.3 Add safe structured events for health transitions, task accept/claim/requeue/phase/terminal state, rebuild validation/promotion, and stable errors; test correlation fields and prove logs omit bodies, graph properties/text, embeddings, provider payloads, credentials, raw SQL, and filesystem paths.
- [x] 7.4 Add a local graph operations runbook under `docs/` covering health compatibility and 200/503 interpretation, dependency/capability states, task polling, explicit idempotent rebuild, evicted generations, `REIMPORT_REQUIRED`, metrics, logs, clean shutdown/restart recovery, and rollback without documenting a remote control plane.

## 8. OpenAPI and Provider/Eco Guardian Contracts

- [x] 8.1 Extend the prerequisite `api/openapi.yaml` with the health schema, capability/dependency/limit DTOs, rebuild request/submission, expanded four-state task union/phases, Idempotency-Key/Location behavior, generation results, and every stable error/status/retryability mapping; validate it in tests/CI.
- [x] 8.2 Create `tests/contract/fixtures/graph-service-operability-v1/` with a versioned SHA-256 manifest and deterministic health ok/degraded/unavailable, rebuild submission/replay/conflict, task phases/terminal states, restart, atomic generation, failure preservation, and `REIMPORT_REQUIRED` fixtures.
- [x] 8.3 Add Eco Guardian consumer transcripts spanning health negotiation, full/delta sync, activation, traverse, paths, hybrid/degraded retrieval, evicted-index detection, explicit rebuild, polling, restart, fixed-result equivalence, reimport, and the shared request-ID/error envelope.
- [ ] 8.4 Add real Gin plus temporary `graph/1..4` SQLite provider tests with deterministic fake Embedder/Reranker that replay all #1–#4 fixtures, validate bodies against OpenAPI, verify manifest digests, and compare canonical results independently of request-specific IDs/timestamps.
- [x] 8.5 Add contract compatibility tests proving service SemVer is diagnostic while API/schema/capability fields control compatibility, unknown additive health fields are tolerated by the consumer fixture, and legacy non-`/v1` envelopes/tasks remain unchanged.

## 9. Recovery, Capacity, Regression, and Final Verification

- [x] 9.1 Add generated 10,000-Node/100,000-Edge cases measuring source verification, graph-index/FTS/Vector build, validation, promotion, batch counts, transaction duration, memory bounds, and fixed-result equivalence without introducing an unapproved public latency guarantee.
- [ ] 9.2 Add targeted race/cancellation tests for task admission/claim, worker close/reopen, source reads, generation writes/selection/cleanup, health cache/state, StoreLifecycle restore leases, activation/deletion, and concurrent query/retrieval readers.
- [ ] 9.3 Run graph lifecycle/query/retrieval suites plus legacy ingest/retrieve/evaluation, durable source sync, feedback, management tasks, backup/restore, MCP parity, old `/index/rebuild`, health, metrics, and startup/shutdown suites against a database containing multiple graph namespaces and generations.
- [ ] 9.4 Run `go test ./...`, targeted `go test -race` for store/graph lifecycle/query/retrieval/operability/handler/contract packages, OpenAPI validation, all fixture digest checks, and `openspec validate graph-service-operability --strict`; fix every failure before marking implementation complete.
