# ADR 0006: Concurrency, cancellation and shutdown model

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-10-18 |
| Deciders | Justus Perillieux |
| Supersedes | — |
| Related | PRD-0001, ADR-0002, ADR-0005 |

## Context

Recorded retrospectively. The code separates execution from scheduling.
`Engine` does not serialize calls; `Manager` serializes per key, bounds
active keys, runs accepted work under a manager-owned context, and treats the
`Submit` context as an admission context only. Attempts run under
`AttemptTimeout`; rollback runs under `context.WithoutCancel` of the caller's
context with its own deadline. `Close` drains gracefully until its context
ends, then cancels active work with one selected cause.

## Decision drivers

- D1: A caller's request lifetime must not cancel work the manager has
  accepted; otherwise a returning HTTP handler could abort a half-applied
  plan.
- D2: Cleanup after a failure must run even when the caller has given up,
  but must be bounded.
- D3: Shutdown must resolve every future and report one consistent cause.
- D4: One key must never be reconciled concurrently with itself.

## Considered options

1. **Admission-only submit context, manager-owned execution context, detached bounded rollback**
   - Pro: accepted work completes or is cancelled only by shutdown; cleanup
     cannot be suppressed by caller cancellation.
   - Con: callers cannot cancel a single accepted reconciliation.
2. **Propagate the submit context into execution**
   - Pro: per-call cancellation.
   - Con: coalesced submissions share one execution, so whose context wins
     is ambiguous; a cancelled caller could abort shared work.
3. **Rollback under the attempt or caller context**
   - Pro: simpler.
   - Con: the very timeout or cancellation that failed the attempt would
     immediately cancel its cleanup.

## Decision

We will document `Engine` as not serializing calls (adapters shared by
concurrent calls must be concurrency-safe), serialize per key and bound
active keys in `Manager` (ready keys start in key order), and use the
`Submit` context for admission only. Rollback runs detached from caller
cancellation, bounded by `RollbackTimeout`, else `AttemptTimeout`, else
`DefaultRollbackTimeout`. Supersession never cancels active work. `Close`
stops admission and fails pending work with `concurrency.ErrClosed`, waits
for active work until its context ends, then cancels the manager context
with the first selected cause; every concurrent and later `Close` returns
that cause, and every future is resolved. Delayed requeues use standard
library timers that are stopped when superseded or closed.

## Consequences

### Positive
- No goroutine or future is left dangling after `Close`.
- Shutdown behaviour is deterministic and testable.

### Negative
- An individual accepted reconciliation cannot be cancelled by its
  submitter.

### Follow-ups
- None.

## Links

- `engine.go`, `manager.go`
