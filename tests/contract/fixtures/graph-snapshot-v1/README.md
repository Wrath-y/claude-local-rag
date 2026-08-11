# Graph Snapshot v1 fixtures

This directory is the versioned provider/consumer contract for Eco Guardian
Graph Snapshot lifecycle integration. Consumers vendor it unchanged under
`tests/contract/fixtures/graph-snapshot-v1/`.

Rules:

- Keep stable UUIDs, paths, and wire field names intact within fixture version
  `1.0`.
- Update `fixture_version` only for a reviewed contract version change.
- Every content change requires updating `manifest.json` with the SHA-256 of
  every payload file (the manifest does not hash itself).
- CI runs the manifest verification test; a missing, added, or modified
  fixture fails verification until its digest is reviewed and recorded.
- Consumers should replay `eco-guardian-consumer-transcript.json` in order;
  it documents the safe full-import fallback after a missing delta base.
