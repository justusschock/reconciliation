# ADR 0007: Value ownership: shallow by default, explicit clones

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-10-18 |
| Deciders | Justus Perillieux |
| Supersedes | — |
| Related | PRD-0001, ADR-0009 |

## Context

Recorded retrospectively. Generic specifications, observations, actions and
undo values may contain maps, slices or pointers. The library cannot deep
copy unknown types safely, yet `Manager` retains specifications beyond the
`Submit` call and `Pending` hands snapshots back to callers. The code makes
ownership shallow by default, requires a `CloneSpec` function for
`NewManager`, copies its own slice storage (`Plan` actions and dependencies,
`Result.Conditions`), and defines an asymmetric codec contract in `handoff`.

## Decision drivers

- D1: No reflection-based deep copying.
- D2: The manager must not observe caller mutations after acceptance, and
  callers must not corrupt pending work through a snapshot.
- D3: Coalesced futures must not share mutable result storage the library
  owns.

## Considered options

1. **Shallow by default; mandatory clone function where values are retained**
   - Pro: explicit, cheap, and correct for any type the caller can clone.
   - Con: callers must write a clone function.
2. **Reflection-based deep copy**
   - Pro: no caller code.
   - Con: slow, cannot handle unexported fields, channels or functions, and
     hides aliasing bugs.
3. **Document "do not mutate" only**
   - Pro: no copying.
   - Con: aliasing bugs in long-lived manager state would be silent.

## Decision

We will keep `Desired.Spec`, `Result.Observed`, action values and undo values
shallow. `NewManager` requires a non-nil `CloneSpec`, applied on acceptance,
before each run and for every `Pending` snapshot. Library-owned slices are
copied (`Ordered` copies actions and dependencies; `Result.Clone` copies
`Conditions`; each coalesced future gets its own clone). In `handoff`,
`Codec.Marshal` may reuse scratch storage because `Export` copies each
result immediately, while `Codec.Unmarshal` must return an independently
owned value.

## Consequences

### Positive
- Ownership is predictable and tested.

### Negative
- `Result.Observed` is shared between coalesced futures; callers that
  mutate it must clone it themselves.

### Follow-ups
- PRD-0001 Q1: a public codec conformance check could enforce the
  `Unmarshal` ownership rule.

## Links

- `types.go`, `plan.go`, `manager.go`, `handoff/handoff.go`
