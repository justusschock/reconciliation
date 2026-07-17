# go-reconciliation

`go-reconciliation` provides building blocks for converging
generic desired state toward observed state. It models deterministic action
DAGs, one-shot reconciliation with retries and rollback, bounded keyed work
management, non-blocking status delivery, and lifecycle handoff of pending
work.

Module path: `github.com/justusschock/go-reconciliation`

## What it provides

- `Plan[A]` validates arbitrary dependency DAGs, including branches that later
  converge, and produces a stable topological order.
- `Engine[S,O,A,U]` observes, plans, applies, verifies, retries classified
  failures, and rolls back completed reversible actions in dependency-safe
  reverse order.
- `Manager[S,O]` serializes work per key, bounds concurrently active keys,
  coalesces identical generations, supersedes stale pending generations, and
  preserves delayed requeues.
- `EventStream` broadcasts phase changes without allowing a slow subscriber to
  block reconciliation.
- `handoff.Exporter` and `handoff.Adopter` transfer pending desired values and
  absolute retry deadlines through integrity-checked `go-lifecycle` artifacts.

The module does not define product resources, persistence, leader election,
remote transport, or a daemon. Adapters own the side effects and domain types.

## Install the private module

You need access to this repository and its private dependencies. Configure a
Git credential helper or SSH agent; do not place access tokens in Git URLs,
source files, `go env`, or shell history. GitHub CLI can configure Git's
credential helper after browser authentication:

```bash
gh auth login --hostname github.com --git-protocol https --web
gh auth setup-git --hostname github.com
```

Tell Go that the module family is private, then install it:

```bash
go env -w GOPRIVATE='github.com/justusschock/*'
go env -w GONOSUMDB='github.com/justusschock/*'
go get github.com/justusschock/go-reconciliation@latest
```

## Build and order an action DAG

Dependencies are runtime prerequisites. Branches can fan out and a later
action can depend on every branch:

```go
plan := reconciliation.Plan[string]{Actions: []reconciliation.Action[string]{
	{ID: "fetch", Value: "read current state"},
	{ID: "configure", DependsOn: []reconciliation.ActionID{"fetch"}},
	{ID: "validate", DependsOn: []reconciliation.ActionID{"fetch"}},
	{ID: "publish", DependsOn: []reconciliation.ActionID{"configure", "validate"}},
}}

ordered, err := reconciliation.Ordered(plan)
```

`Ordered` rejects empty or duplicate IDs, missing or duplicate dependencies,
and cycles. Among currently ready actions it chooses the lexicographically
smallest ID, so equivalent plans produce the same order. See the
compile-checked [`ExampleOrdered`](example_test.go).

## Reconcile once

Implement the generic adapter around your environment:

```go
type Adapter[S, O, A, U any] interface {
	Observe(context.Context, reconciliation.Key) (O, error)
	Plan(context.Context, reconciliation.Desired[S], O) (reconciliation.Plan[A], error)
	Apply(context.Context, reconciliation.Action[A]) (reconciliation.Undo[U], error)
	Undo(context.Context, reconciliation.Undo[U]) error
}
```

Then construct an engine and reconcile one generation:

```go
engine := reconciliation.NewEngine(adapter, reconciliation.Policy{
	MaxAttempts:     3,
	BaseDelay:       100 * time.Millisecond,
	MaxDelay:        2 * time.Second,
	AttemptTimeout:  30 * time.Second,
	RollbackTimeout: 15 * time.Second,
})

result, err := engine.Reconcile(ctx, reconciliation.Desired[Spec]{
	Key: "service/api", Generation: 7, Spec: desired,
})
```

Wrap retryable adapter errors with `reconciliation.Transient` or
`reconciliation.Conflict`. `Permanent` failures, unclassified failures,
context cancellation, and exhausted attempts are not retried. Every retry
starts with a fresh observation and plan.

An `Apply` error means that action did not complete and its returned undo value
is unusable. On later failure, the engine attempts to undo completed reversible
actions. If an action cannot be undone, the result remains changed and the
engine retains its completed dependency closure rather than undoing state that
the surviving action still requires.

## Manage keyed work

`Manager` adds keyed serialization and a concurrency limit around any
`Reconciler` (including an `Engine`). A clone function is mandatory because
generic specifications may contain maps, slices, or pointers:

```go
manager, err := reconciliation.NewManager[Spec, Observed](
	engine,
	8,
	func(spec Spec) Spec { return spec.Clone() },
)
if err != nil {
	return err
}

future := manager.Submit(ctx, reconciliation.Desired[Spec]{
	Key: "service/api", Generation: 7, Spec: desired,
})
result, err := future.Await(ctx)
```

The context passed to `Submit` controls admission, not the accepted work's
lifetime. The manager owns execution until completion or shutdown. A newer
generation supersedes older pending work for the same key; submissions of the
same accepted generation coalesce but receive independently owned condition
slices.

If a successful result sets `RequeueAfter`, the same desired generation becomes
pending until its absolute retry deadline. `Manager.Pending()` returns
key-sorted, cloned snapshots; `RetryAt.IsZero()` means ready immediately. See
the deterministic [`ExampleManager_Pending`](example_test.go).

Always close a manager. `Close` stops admission and pending work immediately,
allows active work to finish until its context expires, then cancels remaining
work with the selected shutdown cause.

## Transfer pending work

The `handoff` package exports only desired state that has not started and its
absolute retry deadline. It deliberately excludes running calls, action plans,
undo values, errors, and resource rights.

Convert the result of `Manager.Pending()` to `handoff.Pending`, export it, and
reverse the mapping after adoption. Preserve a future `RetryAt` when scheduling
work on the destination; submit zero or elapsed deadlines immediately. The
compile-checked [`handoff.ExampleExporter`](handoff/example_test.go) demonstrates
the complete conversion and UTC deadline round trip.

`Codec.Marshal` may reuse scratch bytes because the exporter copies each result
immediately. `Codec.Unmarshal` has the opposite contract: it must return an
independently owned specification that remains stable across future codec
calls. A byte-slice codec must clone decoder input, for example:

```go
func (codec) Unmarshal(data []byte) ([]byte, error) {
	return bytes.Clone(data), nil
}
```

Artifacts use media type
`application/vnd.justusschock.reconciliation+json`, schema version 1,
canonical key ordering, UTC deadlines, and the identity/digest validation from
`go-lifecycle/handoff`.

## Observe status

Carry a `StatusSink` through a reconciliation call with `WithStatusSink`, or
configure one on a manager with `WithManagerStatusSink`. `EventStream` gives
each subscriber an independent bounded queue. Publishers never wait for slow
consumers; after a consumer catches up it receives a `StatusDropped` event with
the number of omitted events.

## Ownership and errors

- `Desired.Spec`, `Result.Observed`, action values, and undo values are shallow
  by default. Domain adapters define their ownership.
- Plan functions copy action/dependency slice storage; managers use the
  required `CloneSpec`; handoff codecs follow the ownership contract above.
- Validation and lifecycle errors wrap stable sentinels, so use `errors.Is`
  rather than matching text.
- Error messages may contain generic keys/action IDs for diagnosis but do not
  serialize specifications or undo values.

## Development

Run the focused repository gates:

```bash
go test -race ./...
go vet ./...
go mod verify
pre-commit run --all-files
```

Implementation plans live under `docs/` and are intentionally ignored. They
must never be committed.

## License

Apache License 2.0. See [LICENSE](LICENSE).
