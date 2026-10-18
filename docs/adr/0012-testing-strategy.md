# ADR 0012: Testing strategy

| Field | Value |
|---|---|
| Status | Accepted |
| Date | 2026-10-18 |
| Deciders | Justus Perillieux |
| Supersedes | — |
| Related | PRD-0001, ADR-0010, ADR-0011 |

## Context

Recorded retrospectively. The library is mostly about time and concurrency:
retry backoff, attempt and rollback deadlines, delayed requeues, keyed
serialization, shutdown races and non-blocking streams. Earlier tests
injected fake clocks through the public clock options (`WithClock`,
`WithManagerClock`), polled with `time.Sleep`, and used wall-clock timeouts
as deadlock guards, which made them slower and dependent on scheduling.

## Decision drivers

- D1: Deterministic tests that pass with `-race -count=3`.
- D2: Tests should exercise the default (standard library) time source that
  most callers use, and separately prove that supplied clocks are honoured.
- D3: Requirements traceable to tests.

## Considered options

1. **`testing/synctest` for every time- or concurrency-dependent test**
   - Pro: virtual time makes delays exact and instant; `synctest.Wait`
     replaces polling and makes negative assertions ("not cancelled", "not
     returned") deterministic; production code uses real timers.
   - Con: requires Go 1.25; tests must not block on real I/O in a bubble.
2. **Injected fake clocks for every timing test**
   - Pro: works on older Go versions; the clock options exist anyway
     (ADR-0010).
   - Con: tests would only cover the fake clock, not the default one, and
     fake clocks cannot control timers inside dependencies or
     `context.WithTimeout`.
3. **Real time with generous timeouts**
   - Pro: simplest.
   - Con: slow and flaky.

## Decision

We will test with:

- **Unit tests** in-package for every behaviour, citing requirement IDs
  (`// FR-RECON-NN`) where a test verifies one.
- **synctest** for engine retry, attempt and rollback deadlines, manager
  scheduling, requeues and shutdown, and event streams. Assertions on
  durations compare exact virtual elapsed time. Receive helpers keep a
  timeout, which inside a bubble fires only when every goroutine is blocked,
  turning a deadlock into a failure.
- **Clock option tests** (`clock_test.go`) that supply fake `Clock` and
  `ManagerClock` implementations inside a bubble and assert that no virtual
  time elapses, proving the supplied clock rather than the standard library
  drives retries and requeues; nil clocks and nil `After` channels report
  `ErrNilClock`.
- **Real time** only where nothing waits on time: plan, type, policy and
  handoff tests, and examples (examples cannot use synctest).
- **Examples** (`example_test.go`) for each main entry point, kept in sync
  with the README.
- **Policy tests** for repository invariants: `TestModuleCompatibilityPolicy`
  and `TestCIWorkflowPolicy`.
- **Race detector** on every CI run.

`internal/conformance` has its own unit test for its delegating `Adapter`.
There is no fuzz test and no public conformance suite (ADR-0010).

## Consequences

### Positive
- The suite runs in about a second and is deterministic.

### Negative
- Contributors need to understand synctest bubbles (for example, that every
  goroutine started in a test must exit before the bubble ends).

### Follow-ups
- Consider fuzzing `ValidatePlan` and `Adopter.Adopt`.

## Links

- `engine_test.go`, `manager_test.go`, `clock_test.go`, `status_test.go`
