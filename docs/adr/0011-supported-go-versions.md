# ADR 0011: Supported Go versions

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-10-18 |
| Deciders | Justus Perillieux |
| Supersedes | — |
| Related | PRD-0001, ADR-0012, ADR-0013 |

## Context

Recorded retrospectively. Version 0.2.0 declared `go 1.22` and tested Go
1.22 through 1.26. The test suite now relies on `testing/synctest`, whose
stable API (`synctest.Test`, `synctest.Wait`) first shipped in Go 1.25. The
Go project supports the two most recent releases (currently 1.26 and 1.27).

## Decision drivers

- D1: Tests must compile on every supported version.
- D2: Consumers should not be forced onto the newest release.
- D3: The CI matrix should be small and follow upstream support
  automatically.

## Considered options

1. **Floor at 1.25; test 1.25, `oldstable` and `stable`**
   - Pro: synctest available everywhere; the matrix follows upstream
     releases without edits.
   - Con: drops Go 1.22 to 1.24, which are out of upstream support.
2. **Keep 1.22 and gate synctest tests behind build tags**
   - Pro: older toolchains still build the module.
   - Con: two test variants to maintain; the gated tests would not run on
     the old versions anyway.
3. **Only the two upstream-supported releases (floor 1.26)**
   - Pro: smallest matrix.
   - Con: excludes 1.25 users without a technical reason.

## Decision

We will declare `go 1.25` in `go.mod`, without a `toolchain` directive, and
test the floor (`1.25.x`) plus `oldstable` and `stable` in CI. The floor
rises only with an ADR, when a needed language or library feature requires
it.

## Consequences

### Positive
- Supported versions are explicit and enforced by `TestModuleCompatibilityPolicy`.

### Negative
- Go 1.22–1.24 users must stay on v0.2.0.

### Follow-ups
- Revisit the floor when 1.25 is two releases behind `oldstable`.

## Links

- `go.mod`, `.github/workflows/ci.yml`
