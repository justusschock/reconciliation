# ADR 0009: Handoff artifact format

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-10-18 |
| Deciders | Justus Perillieux |
| Supersedes | — |
| Related | PRD-0001, ADR-0003, ADR-0007 |

## Context

Recorded retrospectively. When a process hands over to a successor, pending
work should not be lost. The `handoff` package exports `[]Pending[S]` (desired
value plus absolute retry deadline) into a `lifecycle/handoff` artifact
and adopts it on the other side. This is the library's only persistence or
wire format.

## Decision drivers

- D1: Transfer only what is safe to transfer: work that has not started.
- D2: Artifacts must be integrity-checked and versioned.
- D3: Equal inputs must produce identical bytes (stable IDs and digests).
- D4: Specifications are generic; their encoding belongs to the caller.

## Considered options

1. **JSON document inside a lifecycle artifact, caller-supplied codec for specs**
   - Pro: reuses the lifecycle envelope (identity, digest, validation);
     JSON is inspectable; codecs keep domain encoding with the domain.
   - Con: specifications are opaque base64 bytes inside the JSON.
2. **Transfer running work, plans and undo values too**
   - Pro: more complete handover.
   - Con: running work and undo values are not portable across processes,
     may hold resource rights, and could be replayed twice.
3. **gob or another binary format**
   - Pro: compact.
   - Con: Go-specific, harder to inspect and version.

## Decision

We will encode `{"pending":[{"key","generation","spec","retry_at"}]}` with
media type `application/vnd.justusschock.reconciliation+json` and schema
version 1, sorted by key, with UTC deadlines (zero means ready now) and
specifications encoded by a caller `Codec`. Adoption validates the artifact
envelope and digest, the media type and version (`ErrUnsupportedVersion`),
rejects unknown fields, trailing data, missing or duplicate entries and
invalid deadlines, and wraps failures in `lifecycle.ErrInvalidArtifact`.
Running work, plans, undo values, errors and resource rights are never
exported. A new schema version requires a new ADR.

## Consequences

### Positive
- Handoff composes with lifecycle coordination and is safe to replay.

### Negative
- The successor must schedule adopted work itself (honouring `RetryAt`).
- The media type is part of the compatibility surface.

### Follow-ups
- PRD-0001 Q2: consider a manager helper for resubmitting adopted work.

## Links

- `handoff/handoff.go`
