# ADR 0010: Public API boundary

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-10-18 |
| Deciders | Justus Perillieux |
| Supersedes | — |
| Related | PRD-0001, ADR-0002, ADR-0012 |

## Context

Recorded retrospectively while preparing the first public release. Every
exported identifier becomes a compatibility promise, so the surface was
reviewed package by package. Known consumers use `Adapter`, `Action`,
`ActionID`, `Desired`, `Engine`, `NewEngine`, `Key`, `Plan`, `Policy` and
`Undo` from the root package.

Two parts of the surface needed a deliberate decision:

- `Clock`, `EngineOption`, `WithClock` (engine) and `ManagerClock`,
  `WithManagerClock` (manager), plus `ErrNilClock`, which let callers supply
  their own source of time for retry delays and delayed requeues. No known
  consumer uses them today, and `testing/synctest` now provides virtual time
  for this repository's own tests (ADR-0012).
- `internal/conformance`, a function-field `Adapter` that implements
  `reconciliation.Adapter` from independently supplied functions. It is
  internal, so it is not part of the API either way.

## Decision drivers

- D1: The public surface should be exactly what users need.
- D2: Nothing a known consumer uses may move or disappear.
- D3: Callers that schedule against a time source other than the process
  clock (simulations, deterministic replays, tests outside synctest) should
  not have to wrap the library.
- D4: Avoid breaking changes that are not required for the release.

## Considered options

1. **Remove the clock API and the internal package**
   - Pro: smaller surface; one way to construct an engine; no dead code.
   - Con: a breaking change for any unknown user of the clock options;
     callers lose the ability to supply their own time source.
2. **Keep the clocks as a public time abstraction and keep
   `internal/conformance`**
   - Pro: no breaking change; callers can inject their own clocks; the
     variadic `NewEngine` options leave room for future engine settings
     without another signature change.
   - Con: a larger surface; a clock interface admits partial
     implementations (for example `After` returning nil), which the library
     must detect and report with `ErrNilClock`.
3. **Move the clocks under `internal/`**
   - Pro: keeps the hook for this repository.
   - Con: options in the root package cannot take an internal type in a
     useful way, so external callers would lose it anyway.

## Decision

We keep the clock API (option 2). The public API is:

- Root package `reconciliation`: identity and result types (`Key`,
  `Generation`, `Desired`, `Condition`, `ConditionStatus` and its constants,
  `Result`), plans (`ActionID`, `Action`, `Undo`, `Plan`, `ValidatePlan`,
  `Ordered`), policy and errors (`Policy`, `Decision`,
  `DefaultRollbackTimeout`, `Permanent`, `Transient`, `Conflict`, all `Err*`
  sentinels including `ErrNilClock`), execution (`Adapter`, `Engine`,
  `NewEngine`, `EngineOption`, `Clock`, `WithClock`), scheduling
  (`Reconciler`, `CloneSpec`, `Pending`, `Manager`, `NewManager`,
  `ManagerOption`, `ManagerClock`, `WithManagerClock`,
  `WithManagerStatusSink`) and status (`StatusPhase` and its constants,
  `StatusEvent`, `StatusSink`, `WithStatusSink`, `EventStream`,
  `NewEventStream`).
- `handoff`: `Pending`, `Codec`, `Exporter`, `NewExporter`, `Adopter`,
  `NewAdopter`, `ErrUnsupportedVersion`.

`NewEngine(adapter, policy, options...)` keeps its variadic `EngineOption`
parameter. Without options the engine and the manager use the standard
library clock (`time.After`, `time.Now`), so code built on this library can
also be tested inside a `testing/synctest` bubble without supplying a clock.
A nil or typed-nil clock is reported with `ErrNilClock` (by
`Engine.Reconcile` and `NewManager`), as is a clock whose `After` returns a
nil channel.

`internal/conformance` stays as an internal test adapter. It is not part of
the API and is not shown in examples. No public conformance suite is shipped:
adapters are domain-specific and their contract is documented on `Adapter`.

## Consequences

### Positive
- No breaking change to the existing surface; existing `NewEngine` and
  `NewManager` calls, with or without clock options, compile unchanged.
- Callers can run the engine and the manager against their own time source.

### Negative
- The clock interfaces are compatibility promises; changing their method
  sets is a breaking change.

### Follow-ups
- Record every future break in `CHANGELOG.md` with an ADR.

## Links

- [CHANGELOG.md](../../CHANGELOG.md)
