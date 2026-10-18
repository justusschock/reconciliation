# ADR 0008: Status reporting through context-carried, non-blocking sinks

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-10-18 |
| Deciders | Justus Perillieux |
| Supersedes | — |
| Related | PRD-0001, ADR-0006 |

## Context

Recorded retrospectively. Operators want to see reconciliation phases, but
the library must not log, and an observer must never slow reconciliation.
The code defines a `StatusSink` interface, carries a sink per call through
the context (`WithStatusSink`), lets a manager publish `StatusQueued` and
pass its sink into each call (`WithManagerStatusSink`), and provides
`EventStream`, a fan-out sink with an independent bounded queue per
subscriber and explicit `StatusDropped` markers.

## Decision drivers

- D1: One `Engine` must serve concurrent calls with different observers.
- D2: Publishing must not block on slow consumers.
- D3: Consumers must learn that they missed events.

## Considered options

1. **Context-carried sink plus a bounded, dropping fan-out stream**
   - Pro: per-call observers without mutating the engine; publishers never
     wait; loss is visible as a count.
   - Con: context values are implicit; events can be dropped.
2. **Sink configured on the Engine**
   - Pro: explicit.
   - Con: one observer per engine; concurrent calls cannot be observed
     separately without separate engines.
3. **Blocking channel or unbounded queue**
   - Pro: no loss.
   - Con: either stalls reconciliation or grows without bound.

## Decision

We will publish `StatusEvent` values to a `StatusSink` found in the call
context; nil and typed-nil sinks are ignored. `StatusSink.Publish` must not
block. `EventStream` keeps a queue of the configured capacity per subscriber;
when full it counts further events and delivers one `StatusDropped` event
with that count once the subscriber catches up. `Close` closes every
subscriber channel idempotently; `Publish` after `Close` is a no-op.

## Consequences

### Positive
- Observability without logging and without back-pressure on the engine.

### Negative
- Observers may miss individual events; they see how many.

### Follow-ups
- None.

## Links

- `status.go`
