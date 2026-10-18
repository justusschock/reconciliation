# ADR 0005: Validated action DAGs and dependency-safe rollback

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-10-18 |
| Deciders | Justus Perillieux |
| Supersedes | — |
| Related | PRD-0001, ADR-0004 |

## Context

Recorded retrospectively. A plan is a set of actions with `DependsOn` edges.
The code validates the whole plan before applying anything, orders it with a
deterministic Kahn-style topological sort (smallest ready ID first), reports
cycles as a canonical path, and on failure undoes completed reversible
actions in reverse order while retaining the transitive dependencies of any
action that cannot be undone.

## Decision drivers

- D1: No mutation may start from a plan that cannot complete structurally.
- D2: The same plan must always apply in the same order, so behaviour and
  diagnostics are reproducible.
- D3: Rollback must never remove something a surviving change depends on.
- D4: Partial failure must be visible in the result.

## Considered options

1. **Dependency DAG, validated up front, deterministic order, dependency-aware rollback**
   - Pro: fan-out and convergence are expressible; ordering is reproducible;
     rollback respects runtime prerequisites.
   - Con: adapters must declare dependencies explicitly.
2. **Ordered list of actions**
   - Pro: simplest model.
   - Con: no way to express which earlier actions a later one requires, so
     rollback cannot know what to retain.
3. **Parallel execution of independent branches**
   - Pro: faster for large plans.
   - Con: non-deterministic interleaving and harder rollback; adapters would
     need to be concurrency-safe per action. Not needed by the current users.

## Decision

We will treat `DependsOn` as runtime prerequisites, validate plans with
`ValidatePlan`/`Ordered` (rejecting empty IDs, duplicates, missing or
duplicate dependencies and cycles), apply actions sequentially in the
deterministic order, and roll back in reverse order: reversible actions are
undone unless a retained action depends on them; irreversible actions, failed
undos and actions skipped after the rollback deadline retain their dependency
closure. `Result.Changed` is true while any change remains, and the residual
change is carried across retries.

## Consequences

### Positive
- Failures leave the system either rolled back or with a reported,
  dependency-consistent residual change.
- Diagnostics are stable across runs.

### Negative
- Plans execute sequentially.
- Rollback is best-effort: an undo that fails leaves its closure in place.

### Follow-ups
- None.

## Links

- `plan.go`, `engine.go`
