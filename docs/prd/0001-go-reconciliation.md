# PRD 0001: go-reconciliation

| Field | Value |
|---|---|
| Status | Accepted |
| Implementation | Complete |
| Owners | Justus Perillieux |
| Related ADRs | ADR-0002, ADR-0003, ADR-0004, ADR-0005, ADR-0006, ADR-0007, ADR-0008, ADR-0009, ADR-0010, ADR-0011, ADR-0012, ADR-0013 |
| Last updated | 2026-10-18 |

Recorded retrospectively for the first public release; describes the library
as implemented.

## 1. Problem and context

Services that keep some external system in a desired state (configuration,
infrastructure, job queues, recovered work) repeatedly write the same control
loop: observe what exists, compute the difference, apply changes in a safe
order, retry what failed for a transient reason, undo partial work when an
attempt fails, and avoid running two loops for the same object at once. Each
hand-written loop gets the edge cases slightly wrong: retries reuse a stale
plan, a rollback undoes something a surviving change still depends on, a
cancelled caller suppresses cleanup, a slow status consumer stalls the loop,
or pending work is lost when a process hands over to its successor.

`go-reconciliation` provides these mechanics once, generically, so that a
caller only writes the domain-specific part: an `Adapter` that observes,
plans, applies and undoes. It does not model any particular resource, store
or transport.

## 2. Users and use cases

- As a service author, I want to describe one reconciliation as observe,
  plan, apply and undo functions so that retries, ordering and rollback are
  handled for me.
- As a service author, I want plans to be dependency graphs so that
  independent changes can branch and later converge without hand-written
  ordering.
- As an operator, I want a failed attempt to leave the system either fully
  rolled back or with a clearly reported residual change so that I can reason
  about partial failure.
- As a controller author, I want many keys reconciled concurrently with a
  bound, one call per key at a time, and newer desired generations replacing
  stale pending work.
- As a controller author, I want a reconciliation to ask to be repeated later
  (`RequeueAfter`) without holding a goroutine per key.
- As an operator, I want to observe reconciliation phases without the
  observer ever slowing reconciliation down.
- As a platform engineer, I want to hand pending (not yet started) work and
  its retry deadlines to a successor process through an integrity-checked
  artifact.

## 3. Goals and non-goals

### Goals

- G1: A generic, type-safe adapter contract with independent types for the
  desired specification, observation, action and undo payloads.
- G2: Deterministic, validated action DAGs and deterministic diagnostics.
- G3: One-shot reconciliation with classified retries, bounded backoff,
  per-attempt deadlines and dependency-safe, bounded rollback.
- G4: Keyed, bounded, generation-aware management of many reconciliations
  with explicit shutdown semantics.
- G5: Non-blocking status reporting.
- G6: Lifecycle handoff of pending desired state.

### Non-goals

- NG1: Domain resources, persistence, leader election, remote transport or a
  daemon. These belong to the caller; the library has no I/O of its own.
- NG2: Watching or event sources. Callers decide when to submit work.
- NG3: Handoff of running work, plans, undo values, errors or resource
  rights. Only state that has not started is transferable (ADR-0009).
- NG4: Deep copying of caller values. Ownership is shallow unless a clone
  function is supplied (ADR-0007).
- NG5: Logging. The library reports through return values and status sinks.

## 4. Functional requirements

| ID | Requirement | Priority |
|---|---|---|
| FR-RECON-01 | `Key`, `Generation` and `Desired` validate: a key is non-empty, a generation is positive, and failures wrap `ErrInvalidKey`, `ErrInvalidGeneration` and `ErrInvalidDesired`. | MUST |
| FR-RECON-02 | `Condition` has a tri-state status and rejects empty or malformed type, reason and message text; `Result` validates its identity, a non-negative `RequeueAfter` and every condition; `Result.Clone` gives independent `Conditions` storage while `Observed` stays shallow. | MUST |
| FR-RECON-03 | `ValidatePlan` and `Ordered` reject empty IDs, duplicate actions, missing or duplicate dependencies and cycles. Errors wrap `ErrInvalidPlan` plus a category sentinel; cycles are reported as a canonical path starting at the smallest ID, with unsafe IDs quoted. | MUST |
| FR-RECON-04 | `Ordered` returns a deterministic topological order that picks the lexicographically smallest ready action, without aliasing or mutating the input plan. | MUST |
| FR-RECON-05 | `Engine.Reconcile` validates its input, policy and adapter before any adapter call, then observes, plans, validates the whole plan before mutating, applies actions in order, and observes again to verify. `Result.Changed` reports whether any change remains. | MUST |
| FR-RECON-06 | Errors wrapped with `Transient` or `Conflict` are retried while attempts remain; `Permanent`, unclassified and context errors are not. Every retry starts with a fresh observation and plan. | MUST |
| FR-RECON-07 | Retry delays grow exponentially from `BaseDelay`, saturate at `MaxDelay` without overflow, and pass through an optional `Jitter` whose result is clamped to `[0, MaxDelay]`. Invalid policies are rejected with `ErrInvalidPolicy`. | MUST |
| FR-RECON-08 | When an attempt fails after mutating, completed reversible actions are undone in reverse order; irreversible actions and the transitive dependencies of any action that survives are retained; a failed rollback is joined to the primary error and stops further retries. | MUST |
| FR-RECON-09 | `AttemptTimeout` bounds each attempt; rollback runs detached from caller cancellation but bounded by `RollbackTimeout` (falling back to `AttemptTimeout`, then `DefaultRollbackTimeout`); cancellation interrupts a retry wait with the context cause. | MUST |
| FR-RECON-10 | `NewManager` requires a reconciler, a positive limit and a clone function; a `Manager` never runs two calls for one key concurrently and bounds the number of active keys. | MUST |
| FR-RECON-11 | Submitting the generation already pending or active coalesces; a newer generation supersedes older pending work with `ErrSuperseded` but never cancels active work; an older generation is rejected with `ErrSuperseded`. | MUST |
| FR-RECON-12 | A `Manager` clones specifications on acceptance and in `Pending` snapshots (sorted by key); coalesced futures receive independent `Conditions` storage. | MUST |
| FR-RECON-13 | A successful result with `RequeueAfter` keeps the same generation pending until an absolute deadline reported as `Pending.RetryAt`, then reruns it and resolves the original futures; a newer generation replaces a scheduled requeue. | MUST |
| FR-RECON-14 | The `Submit` context controls admission only. `Close` stops admission and fails pending work with `concurrency.ErrClosed`, lets active work finish until its context ends, then cancels active work with one selected shutdown cause that every `Close` call returns. | MUST |
| FR-RECON-15 | A `StatusSink` carried by `WithStatusSink` (or configured with `WithManagerStatusSink`) receives queued, observing, planning, applying, retrying, rolling-back, succeeded and failed events; a manager publishes `StatusQueued` before the work can start. | MUST |
| FR-RECON-16 | `EventStream` gives each subscriber an independent bounded queue, never blocks publishers, reports omitted events with a `StatusDropped` count, closes idempotently and safely under concurrent publishing, and rejects a non-positive capacity. | MUST |
| FR-RECON-17 | `handoff.Exporter` and `handoff.Adopter` round-trip pending desired values and absolute retry deadlines through a `go-lifecycle` artifact with a fixed media type and schema version 1, canonical key order and UTC deadlines, carrying no other state. | MUST |
| FR-RECON-18 | Adoption rejects wrong media types, unsupported versions, digest mismatches, invalid or duplicate entries, invalid deadlines, unknown fields and trailing data; export rejects invalid or duplicate entries and unencodable deadlines; nil codecs are rejected; codec errors and context causes are preserved. | MUST |
| FR-RECON-19 | Export copies each codec output before the next `Marshal` call; adopted specifications do not alias artifact bytes when the codec honours its ownership contract. | MUST |

## 5. Non-functional requirements

| ID | Requirement | Measure |
|---|---|---|
| NFR-RECON-01 | Determinism: plan order, cycle diagnostics, `Pending` snapshots and handoff artifact bytes do not depend on input or map order. | `TestOrderedUsesStableTopologicalOrder`, `TestValidatePlanReportsCanonicalCyclePath`, `TestHandoffRoundTripIsCanonicalAndStateOnly` |
| NFR-RECON-02 | Concurrency safety: every exported type documents its concurrency contract; the suite passes under the race detector. | `go test -race -count=3 ./...` |
| NFR-RECON-03 | Error stability: failure categories are sentinels matched with `errors.Is`; messages never serialize specifications or undo values. | Sentinel assertions throughout the suite |
| NFR-RECON-04 | Toolchain: the module's floor is Go 1.25 with no `toolchain` or `replace` directive. | `TestModuleCompatibilityPolicy` |
| NFR-RECON-05 | Supply chain: CI uses only `push`/`pull_request` triggers, read-only permissions, SHA-pinned actions and no secrets. | `TestCIWorkflowPolicy` |
| NFR-RECON-06 | Dependencies: standard library plus `go-concurrency` and `go-lifecycle` only; no logging or global state. | `go.mod` review (ADR-0003) |
| NFR-RECON-07 | Test determinism: time- and concurrency-dependent tests use `testing/synctest` virtual time; no real sleeps. | ADR-0012 |

## 6. API sketch

```go
package reconciliation

type Adapter[S, O, A, U any] interface {
	Observe(context.Context, Key) (O, error)
	Plan(context.Context, Desired[S], O) (Plan[A], error)
	Apply(context.Context, Action[A]) (Undo[U], error)
	Undo(context.Context, Undo[U]) error
}

func ValidatePlan[A any](Plan[A]) error
func Ordered[A any](Plan[A]) ([]Action[A], error)

func NewEngine[S, O, A, U any](Adapter[S, O, A, U], Policy) *Engine[S, O, A, U]
func (*Engine[S, O, A, U]) Reconcile(context.Context, Desired[S]) (Result[O], error)

func Permanent(error) error
func Transient(error) error
func Conflict(error) error

type Reconciler[S, O any] interface {
	Reconcile(context.Context, Desired[S]) (Result[O], error)
}
func NewManager[S, O any](Reconciler[S, O], int, CloneSpec[S], ...ManagerOption) (*Manager[S, O], error)
func (*Manager[S, O]) Submit(context.Context, Desired[S]) *concurrency.Future[Result[O]]
func (*Manager[S, O]) Pending() []Pending[S]
func (*Manager[S, O]) Close(context.Context) error

func WithStatusSink(context.Context, StatusSink) context.Context
func NewEventStream(capacity int) (*EventStream, error)

package handoff

func NewExporter[S any](Codec[S]) (*Exporter[S], error)
func (*Exporter[S]) Export(context.Context, []Pending[S]) (lifecyclehandoff.Artifact, error)
func NewAdopter[S any](Codec[S]) (*Adopter[S], error)
func (*Adopter[S]) Adopt(context.Context, lifecyclehandoff.Artifact) ([]Pending[S], error)
```

## 7. Acceptance criteria

| Criterion | Requirement(s) | Test(s) |
|---|---|---|
| Identity values validate with stable sentinels | FR-RECON-01 | `TestKeyValidation`, `TestGenerationValidation`, `TestDesiredRequiresKeyAndGeneration` |
| Conditions and results validate; clones own conditions | FR-RECON-02 | `TestConditionStatusValues`, `TestConditionValidation`, `TestConditionValidationAcceptsUnicodeAndMessageNewlines`, `TestResultValidation`, `TestResultCloneCopiesConditionsAndKeepsObservedShallow` |
| Invalid plans are rejected with categorized, canonical diagnostics | FR-RECON-03, NFR-RECON-01 | `TestValidatePlanRejectsInvalidReferences`, `TestValidatePlanReportsCanonicalCyclePath`, `TestValidatePlanReportsSelfCycle`, `TestValidatePlanEscapesUnsafeCycleIDs`, `TestOrderedReturnsValidationErrorWithoutPartialOrder` |
| Order is deterministic and input is not aliased | FR-RECON-04, NFR-RECON-01 | `TestOrderedUsesStableTopologicalOrder`, `TestOrderedDoesNotAliasOrMutateInput`, `ExampleOrdered` |
| Engine runs observe, plan, apply, verify safely | FR-RECON-05 | `TestEngineNoOpObservesBeforeAndAfter`, `TestEngineAppliesStablePlanAndOwnsUndoProvenance`, `TestEngineRejectsInvalidConfigurationBeforeAdapterCalls`, `TestEngineValidatesWholePlanBeforeMutation`, `TestEngineResultShapeIsStable`, `ExampleEngine_Reconcile` |
| Only classified failures retry, with fresh state | FR-RECON-06 | `TestEngineRetriesClassifiedFailureWithFreshState`, `TestEngineDoesNotRetryPermanentOrUnclassifiedErrors`, `TestPolicyRetriesOnlyTransientAndConflictErrors`, `TestClassificationWrappersPreserveIdentity` |
| Backoff is bounded, saturating and jitter-clamped | FR-RECON-07 | `TestPolicyValidation`, `TestPolicyRequiresOneBasedAttemptAndValidPolicy`, `TestPolicyExponentialDelayCapsAndSaturates`, `TestPolicyAppliesJitterAfterCapAndClampsResult`, `TestPolicyDoesNotApplyJitterWithoutRetry` |
| Rollback is reverse-ordered and dependency-safe | FR-RECON-08 | `TestEngineRollsBackCompletedActionsInReverseOrder`, `TestEngineLeavesIrreversibleActionsOutOfRollback`, `TestEngineRetainsDependenciesOfResidualActions`, `TestEngineJoinsRollbackErrorsAndDoesNotRetry`, `TestEngineRetriesAfterSuccessfulRollback`, `TestEngineCarriesResidualChangeAcrossRetries`, `TestEngineRollsBackWhenFinalObservationFails` |
| Deadlines and cancellation are honoured; cleanup is bounded | FR-RECON-09 | `TestEngineAttemptTimeoutRollsBackWithParentContext`, `TestEngineParentCancellationDoesNotSuppressRollback`, `TestEngineBoundsRollbackAndPreservesCleanupDeadline`, `TestEngineCancellationInterruptsRetryWait` |
| Manager configuration, keyed serialization and bound | FR-RECON-10 | `TestNewManagerValidatesRequiredConfiguration`, `TestManagerSerializesKeysAndBoundsActiveWork` |
| Generations coalesce and supersede without cancelling active work | FR-RECON-11 | `TestManagerCoalescesEqualAndSupersedesPendingGenerations`, `TestManagerDoesNotCancelActiveWorkOnSupersession` |
| Specifications and conditions are owned | FR-RECON-12 | `TestManagerOwnsAcceptedAndPendingSpecs`, `TestManagerCoalescedResultsOwnConditionsButObservedIsShallow`, `ExampleManager_Pending` |
| Requeues run at absolute deadlines and are replaceable | FR-RECON-13 | `TestManagerRequeuesAtAbsoluteDeadlineAndResolvesOriginalFuture`, `TestManagerNewGenerationReplacesScheduledRequeue` |
| Admission and shutdown semantics | FR-RECON-14 | `TestManagerSubmitContextOnlyControlsAdmission`, `TestManagerCloseStopsAdmissionFailsPendingAndLetsActiveFinish`, `TestManagerCloseDeadlineCancelsActiveWorkAndResolvesEveryFuture`, `TestManagerConcurrentCloseDeadlinesUseOneShutdownCause`, `TestManagerLaterCloseReturnsSelectedCauseBeforeActiveCallExits`, `TestManagerWaitingCloseObservesSelectedShutdownCause` |
| Status phases are published in order | FR-RECON-15 | `TestEnginePublishesContextCarriedStatusPhases`, `TestEnginePublishesRetryingBeforeAnotherAttempt`, `TestEnginePublishesRollbackAndFailure`, `TestManagerPublishesQueuedBeforeEnginePhases`, `TestManagerDoesNotStartBeforeInitialQueuedPublicationCompletes` |
| Event streams are bounded and non-blocking | FR-RECON-16 | `TestEventStreamPublishesWithoutBlockingAndMarksDroppedEvents`, `TestEventStreamKeepsSubscribersIndependent`, `TestEventStreamConcurrentPublishAndCloseIsSafe`, `TestEventStreamRejectsUnboundedCapacity` |
| Handoff round-trips canonical, state-only artifacts | FR-RECON-17, NFR-RECON-01 | `TestHandoffRoundTripIsCanonicalAndStateOnly`, `TestAdoptRejectsUnknownVersion`, `TestAdoptRejectsWrongMediaTypeAndDigestMismatch`, `ExampleExporter` |
| Handoff rejects invalid input and preserves causes | FR-RECON-18 | `TestExportRejectsInvalidPendingState`, `TestAdoptRejectsInvalidPendingStateAndTrailingData`, `TestConstructorsRejectNilCodecs`, `TestCodecErrorsArePreserved`, `TestHandoffHonorsCanceledContexts` |
| Codec buffers are owned correctly | FR-RECON-19 | `TestExportOwnsEachCodecBufferBeforeMarshalingTheNextSpec`, `TestAdoptedCodecBytesDoNotAliasArtifactData` |
| Go floor and module hygiene | NFR-RECON-04 | `TestModuleCompatibilityPolicy` |
| CI supply-chain policy | NFR-RECON-05 | `TestCIWorkflowPolicy` |

## 8. Source references

None.

## 9. Open questions

- Q1: Should the library ship a public conformance suite for `handoff.Codec`
  implementations (ownership of `Unmarshal` results)? Today the contract is
  documented and tested only with the library's own codecs.
- Q2: Should `Manager` offer a way to resubmit adopted `handoff.Pending` values
  with their retry deadline directly, instead of callers scheduling the
  submission themselves?
