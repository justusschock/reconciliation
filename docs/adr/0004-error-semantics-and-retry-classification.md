# ADR 0004: Error semantics and retry classification

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-10-18 |
| Deciders | Justus Perillieux |
| Supersedes | — |
| Related | PRD-0001, ADR-0005 |

## Context

Recorded retrospectively. The engine has to decide, from an adapter error
alone, whether another attempt is safe and useful, and callers have to
distinguish validation failures, supersession, shutdown and domain failures
programmatically. The code uses exported sentinel errors
(`ErrInvalidKey`, `ErrInvalidPlan` with category sentinels, `ErrInvalidPolicy`,
`ErrNilAdapter`, `ErrSuperseded`, …), classification wrappers `Permanent`,
`Transient` and `Conflict`, `Policy.Decide`, and `errors.Join` to combine a
primary failure with rollback failures.

## Decision drivers

- D1: Retrying must be opt-in; an unknown error is not assumed safe to retry.
- D2: Classification must not change the message or hide the cause.
- D3: Cancellation must always win over classification.
- D4: Errors must be testable with `errors.Is`, never by matching text.

## Considered options

1. **Opt-in classification wrappers plus sentinels**
   - Pro: adapters mark only what they know is retryable; the cause stays
     reachable with `errors.Is`/`errors.As`; the message is unchanged.
   - Con: adapters must remember to wrap retryable errors.
2. **Retry everything except a permanent marker**
   - Pro: less wrapping for adapters.
   - Con: retries programming errors and unknown failures, repeating side
     effects that were never meant to be repeated.
3. **Typed error structs with a retry field**
   - Pro: richer metadata.
   - Con: forces adapters to construct library types and couples domain
     errors to this module.

## Decision

We will classify with `Permanent`, `Transient` and `Conflict`, which wrap a
cause (nil stays nil, an already-classified error is not re-wrapped) and match
`ErrPermanent`, `ErrTransient` or `ErrConflict`. `Policy.Decide` retries only
`Transient` or `Conflict` errors while attempts remain; `Permanent`,
unclassified, `context.Canceled` and `context.DeadlineExceeded` errors are
never retried, even when wrapped as transient. Validation failures wrap a
stable sentinel with `%w` and add generic diagnostics (keys, action IDs) but
never specifications or undo values. A failed rollback is joined to the
primary error with `errors.Join` and stops retries, because the system is in
an unknown state. Context failures report `context.Cause`.

## Consequences

### Positive
- Callers and tests match errors with `errors.Is`.
- Unknown errors fail fast instead of repeating side effects.

### Negative
- Sentinels and wrappers are part of the API; renaming one is a breaking
  change.

### Follow-ups
- None.

## Links

- `types.go`, `plan.go`, `policy.go`, `engine.go`, `manager.go`
