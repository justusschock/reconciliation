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

Two kinds of exported API existed only for the library's own tests:

- `Clock`, `EngineOption`, `WithClock` (engine) and `ManagerClock`,
  `WithManagerClock` (manager), plus `ErrNilClock`, which let tests inject
  fake clocks. No consumer used them; `testing/synctest` now provides
  virtual time (ADR-0012).
- `internal/conformance`, a function-field `Adapter`. It was already
  internal and was not imported anywhere: root-package tests cannot import
  it without an import cycle.

## Decision drivers

- D1: The public surface should be exactly what users need.
- D2: Nothing a known consumer uses may move or disappear.
- D3: Test-only hooks should not become compatibility promises.

## Considered options

1. **Remove the test-only clock API and the unused internal package**
   - Pro: smaller surface; one way to construct an engine; no dead code.
   - Con: a breaking change for any unknown user of the clock options.
2. **Keep the clocks as a public time abstraction**
   - Pro: users could inject their own clocks.
   - Con: synctest covers the testing need; a clock interface invites
     partial implementations (for example `After` returning nil, which needed
     its own `ErrNilClock`).
3. **Move the clocks under `internal/`**
   - Pro: keeps the hook.
   - Con: options in the root package cannot take an internal type in a
     useful way, and the hook has no remaining caller.

## Decision

The public API is:

- Root package `reconciliation`: identity and result types (`Key`,
  `Generation`, `Desired`, `Condition`, `ConditionStatus` and its constants,
  `Result`), plans (`ActionID`, `Action`, `Undo`, `Plan`, `ValidatePlan`,
  `Ordered`), policy and errors (`Policy`, `Decision`,
  `DefaultRollbackTimeout`, `Permanent`, `Transient`, `Conflict`, all `Err*`
  sentinels), execution (`Adapter`, `Engine`, `NewEngine`), scheduling
  (`Reconciler`, `CloneSpec`, `Pending`, `Manager`, `NewManager`,
  `ManagerOption`, `WithManagerStatusSink`) and status (`StatusPhase` and
  its constants, `StatusEvent`, `StatusSink`, `WithStatusSink`,
  `EventStream`, `NewEventStream`).
- `handoff`: `Pending`, `Codec`, `Exporter`, `NewExporter`, `Adopter`,
  `NewAdopter`, `ErrUnsupportedVersion`.

We remove `Clock`, `EngineOption`, `WithClock`, `ManagerClock`,
`WithManagerClock` and `ErrNilClock`; `NewEngine` takes only an adapter and
a policy. We delete `internal/conformance`. There is no `internal/` package;
future implementation-only packages go there. No public conformance suite is
shipped: adapters are domain-specific and their contract is documented on
`Adapter`.

## Consequences

### Positive
- A smaller, documented surface; existing two-argument `NewEngine` calls
  compile unchanged.

### Negative
- Users can no longer inject a clock; tests of code built on this library
  should use `testing/synctest`.

### Follow-ups
- Record every future break in `CHANGELOG.md` with an ADR.

## Links

- [CHANGELOG.md](../../CHANGELOG.md)
