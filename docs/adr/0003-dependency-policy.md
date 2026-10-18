# ADR 0003: Dependency policy

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-10-18 |
| Deciders | Justus Perillieux |
| Supersedes | — |
| Related | PRD-0001, ADR-0009 |

## Context

Recorded retrospectively. The module depends on the standard library and two
sibling libraries by the same author:

- `github.com/justusschock/concurrency` for `Future` (the result type of
  `Manager.Submit`), `Failed`, `Go`, and the shared sentinels
  `ErrInvalidLimit` and `ErrClosed`.
- `github.com/justusschock/lifecycle` (root and `handoff` packages) for
  the integrity-checked artifact envelope, its validation, and the
  `ErrInvalidArtifact`/`ErrIntegrity` sentinels used by the `handoff`
  package.

There are no third-party dependencies.

## Decision drivers

- D1: A small, auditable dependency graph for a foundational library.
- D2: Consistent futures and error identities across the sibling libraries,
  so callers combining them can use one `errors.Is` vocabulary.
- D3: Handoff artifacts must interoperate with the lifecycle library's
  coordinator rather than define a second envelope.

## Considered options

1. **Standard library plus the two sibling libraries**
   - Pro: shared `Future` and sentinels; handoff artifacts plug into
     lifecycle coordination unchanged.
   - Con: the sibling libraries are pre-1.0, so their breaking changes
     propagate.
2. **Standard library only**
   - Pro: no external graph at all.
   - Con: would duplicate futures, artifact digests and validation, and
     callers would see different error identities for the same conditions.
3. **Third-party libraries (for example a workqueue or retry package)**
   - Pro: mature implementations.
   - Con: larger graph and semantics (generations, dependency-safe rollback)
     that do not match; would still need adapters.

## Decision

We will depend only on the standard library, `concurrency` and
`lifecycle`. Any new dependency, including another sibling library,
requires an ADR. The library performs no logging and keeps no global state.

## Consequences

### Positive
- Minimal attack surface and fast builds.
- `concurrency.ErrClosed`, `concurrency.ErrInvalidLimit` and
  `lifecycle.ErrInvalidArtifact` mean the same thing across libraries.

### Negative
- `Manager.Submit` exposes `*concurrency.Future`, so a breaking change there
  is a breaking change here.

### Follow-ups
- Dependabot proposes dependency updates weekly (ADR-0013).

## Links

- `go.mod`
