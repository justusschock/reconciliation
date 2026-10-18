# ADR 0002: Generic adapter-driven API shape

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-10-18 |
| Deciders | Justus Perillieux |
| Supersedes | — |
| Related | PRD-0001, ADR-0005, ADR-0006 |

## Context

Recorded retrospectively. The library must drive reconciliation of arbitrary
domains (configuration, infrastructure, recovered jobs) without knowing their
types. The code expresses this with one generic `Adapter[S, O, A, U]`
interface (desired specification, observation, action and undo payloads), an
`Engine` that runs one reconciliation through it, a `Reconciler[S, O]`
interface that `Engine` satisfies, and a `Manager[S, O]` that schedules any
`Reconciler`. Configuration is a plain `Policy` struct for the engine and
functional options (`ManagerOption`) for the manager.

## Decision drivers

- D1: Type safety for domain values without casts or reflection.
- D2: The domain-specific part should be as small as possible.
- D3: Scheduling (manager) and execution (engine) should be usable
  independently.
- D4: Configuration should be easy to validate and to construct literally.

## Considered options

1. **Generic adapter interface with four type parameters**
   - Pro: every payload is statically typed; undo values can differ from
     actions (for example the previous value of a setting).
   - Pro: adapters are ordinary types; no registration or reflection.
   - Con: four type parameters make signatures long; type arguments sometimes
     have to be spelled out.
2. **`any`-typed payloads**
   - Pro: short signatures.
   - Con: casts in every adapter and no compile-time checking.
3. **Callback struct instead of an interface**
   - Pro: no named adapter type needed.
   - Con: less idiomatic; nil callbacks become a runtime concern; harder to
     share state across the four operations.
4. **Single type combining scheduling and execution**
   - Pro: one entry point.
   - Con: callers who only need one-shot reconciliation (for example a
     periodic recovery pass) pay for keyed scheduling, and alternative
     executors could not be plugged into the scheduler.

For configuration, a validated `Policy` struct was preferred over options for
the engine because every field is a plain bound that callers want to write
literally; the manager uses options for its optional status sink because its
required arguments (reconciler, limit, clone function) are positional.

## Decision

We will expose `Adapter[S, O, A, U]`, `Engine[S, O, A, U]` with
`NewEngine(adapter, policy)`, the `Reconciler[S, O]` interface and
`Manager[S, O]` with `NewManager(reconciler, limit, cloneSpec, options...)`.
Inputs are validated at use: `Engine.Reconcile` validates the desired value,
policy and adapter before calling the adapter; `NewManager` validates its
arguments and returns an error.

## Consequences

### Positive
- Adapters are small, typed and testable in isolation.
- `Manager` can schedule any `Reconciler`, not only `Engine`.

### Negative
- Long generic signatures; inference does not always apply.
- `NewEngine` cannot report an invalid policy; the error appears on the first
  `Reconcile`.

### Follow-ups
- None.

## Links

- `adapter.go`, `engine.go`, `manager.go`
