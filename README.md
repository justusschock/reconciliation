# go-reconciliation

`go-reconciliation` is a Go library for converging desired state toward
observed state. You describe your domain once, as an adapter that observes,
plans, applies and undoes; the library handles the control loop around it:
validated action graphs with a deterministic order, classified retries with
bounded backoff, dependency-safe rollback, keyed scheduling of many
reconciliations, non-blocking status reporting, and handoff of pending work
to a successor process. It is for authors of controllers, operators and
services that keep an external system in a desired state and do not want to
re-implement these mechanics, and their edge cases, for every resource.

## Features

- **Action DAGs.** `Plan[A]` actions declare runtime dependencies;
  `ValidatePlan` and `Ordered` reject invalid graphs and produce a stable
  topological order, including branches that later converge.
- **One-shot engine.** `Engine[S, O, A, U]` observes, plans, validates the
  whole plan before mutating, applies, and verifies with a fresh
  observation.
- **Classified retries.** Errors wrapped with `Transient` or `Conflict` are
  retried with exponential, capped, optionally jittered backoff;
  `Permanent`, unclassified and context errors are not.
- **Dependency-safe rollback.** Completed reversible actions are undone in
  reverse order, without removing anything a surviving action still needs,
  under a bounded cleanup deadline that caller cancellation cannot
  suppress.
- **Keyed manager.** `Manager[S, O]` runs one call per key at a time, bounds
  concurrently active keys, coalesces equal generations, supersedes stale
  pending ones, honours delayed requeues and shuts down gracefully.
- **Status events.** `WithStatusSink` and `EventStream` report every phase
  without letting a slow consumer block reconciliation.
- **Handoff.** The `handoff` package exports and adopts pending desired
  state and absolute retry deadlines as integrity-checked
  `go-lifecycle` artifacts.

The module defines no domain resources, persistence, leader election,
transport or daemon, and performs no logging.

## Installation

```sh
go get github.com/justusschock/go-reconciliation@latest
```

Requires Go 1.25 or newer. Dependencies: the standard library,
[`go-concurrency`](https://github.com/justusschock/go-concurrency) and
[`go-lifecycle`](https://github.com/justusschock/go-lifecycle).

## Quick start

Implement `Adapter` for your environment. Here the environment is a map of
settings; `Apply` returns the previous value so the engine can undo it
(from [`ExampleEngine_Reconcile`](example_test.go)):

```go
type settings struct {
	mu      sync.Mutex
	current map[string]string
}

type set struct{ Name, Value string }

type previous struct {
	Name, Value string
	Present     bool
}

func (s *settings) Observe(context.Context, reconciliation.Key) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.current), nil
}

func (s *settings) Plan(_ context.Context, desired reconciliation.Desired[map[string]string], observed map[string]string) (reconciliation.Plan[set], error) {
	var plan reconciliation.Plan[set]
	for _, name := range slices.Sorted(maps.Keys(desired.Spec)) {
		if value := desired.Spec[name]; observed[name] != value {
			plan.Actions = append(plan.Actions, reconciliation.Action[set]{
				ID:         reconciliation.ActionID("set/" + name),
				Value:      set{Name: name, Value: value},
				Reversible: true,
			})
		}
	}
	return plan, nil
}

func (s *settings) Apply(_ context.Context, action reconciliation.Action[set]) (reconciliation.Undo[previous], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, present := s.current[action.Value.Name]
	s.current[action.Value.Name] = action.Value.Value
	return reconciliation.Undo[previous]{Value: previous{Name: action.Value.Name, Value: value, Present: present}}, nil
}

func (s *settings) Undo(_ context.Context, undo reconciliation.Undo[previous]) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if undo.Value.Present {
		s.current[undo.Value.Name] = undo.Value.Value
	} else {
		delete(s.current, undo.Value.Name)
	}
	return nil
}
```

Then construct an engine and reconcile one generation:

```go
env := &settings{current: map[string]string{"replicas": "1"}}
engine := reconciliation.NewEngine(env, reconciliation.Policy{
	MaxAttempts:     3,
	BaseDelay:       100 * time.Millisecond,
	MaxDelay:        2 * time.Second,
	AttemptTimeout:  30 * time.Second,
	RollbackTimeout: 15 * time.Second,
})

result, err := engine.Reconcile(context.Background(), reconciliation.Desired[map[string]string]{
	Key:        "service/api",
	Generation: 1,
	Spec:       map[string]string{"image": "api:v2", "replicas": "3"},
})
if err != nil {
	panic(err)
}
fmt.Println(result.Changed, result.Observed["image"], result.Observed["replicas"])
// Output: true api:v2 3
```

### Plans

Dependencies are runtime prerequisites. Branches can fan out and a later
action can depend on every branch:

```go
plan := reconciliation.Plan[string]{Actions: []reconciliation.Action[string]{
	{ID: "fetch", Value: "read current state"},
	{ID: "configure", DependsOn: []reconciliation.ActionID{"fetch"}},
	{ID: "validate", DependsOn: []reconciliation.ActionID{"fetch"}},
	{ID: "publish", DependsOn: []reconciliation.ActionID{"configure", "validate"}},
}}

ordered, err := reconciliation.Ordered(plan) // fetch, configure, validate, publish
```

`Ordered` rejects empty or duplicate IDs, missing or duplicate dependencies
and cycles. Among ready actions it picks the lexicographically smallest ID,
so equivalent plans produce the same order ([`ExampleOrdered`](example_test.go)).

### Managing keyed work

`Manager` adds keyed serialization and a concurrency limit around any
`Reconciler`, including an `Engine`. A clone function is mandatory because
generic specifications may contain maps, slices or pointers:

```go
manager, err := reconciliation.NewManager[Spec, Observed](
	engine,
	8,
	func(spec Spec) Spec { return spec.Clone() },
)
if err != nil {
	return err
}
defer manager.Close(ctx)

future := manager.Submit(ctx, reconciliation.Desired[Spec]{
	Key: "service/api", Generation: 7, Spec: desired,
})
result, err := future.Await(ctx)
```

The `Submit` context controls admission only; the manager owns accepted
work until it completes or the manager shuts down. A newer generation
supersedes older pending work for the same key (`ErrSuperseded`); equal
generations coalesce. A successful result with `RequeueAfter` keeps the
generation pending until an absolute deadline, visible as
`Pending.RetryAt` in `Manager.Pending()` snapshots
([`ExampleManager_Pending`](example_test.go)). `Close` stops admission,
fails pending work with `concurrency.ErrClosed`, lets active work finish
until its context ends, then cancels it with one shutdown cause.

### Status

Carry a `StatusSink` through a call with `WithStatusSink`, or configure one
on a manager with `WithManagerStatusSink`. `EventStream` (from
`NewEventStream`) gives each subscriber an independent bounded queue;
publishers never wait, and a lagging subscriber later receives a
`StatusDropped` event with the number of omitted events.

### Handoff

The `handoff` package transfers only desired state that has not started,
with its absolute retry deadline. Convert `Manager.Pending()` to
`handoff.Pending`, export with `handoff.NewExporter`, adopt with
`handoff.NewAdopter`, and resubmit on the successor, honouring a future
`RetryAt` ([`handoff.ExampleExporter`](handoff/example_test.go)). Artifacts
use media type `application/vnd.justusschock.reconciliation+json`, schema
version 1, canonical key order and UTC deadlines. A `Codec` encodes
specifications: `Marshal` may reuse scratch storage, while `Unmarshal` must
return an independently owned value (for example `bytes.Clone(data)`).

## Packages

| Package | Description |
|---|---|
| [`github.com/justusschock/go-reconciliation`](https://pkg.go.dev/github.com/justusschock/go-reconciliation) | Plans, engine, retry policy, manager and status events. |
| [`github.com/justusschock/go-reconciliation/handoff`](https://pkg.go.dev/github.com/justusschock/go-reconciliation/handoff) | Export and adoption of pending work through lifecycle artifacts. |

Packages under `internal/` (none at present) are not part of the API.

## Design and guarantees

- **Concurrency.** `Engine` does not serialize calls; adapters and
  `Policy.Jitter` shared by concurrent calls must be safe for concurrent
  use. `Manager` and `EventStream` are safe for concurrent use. The test
  suite runs under the race detector.
- **Cancellation.** Every blocking operation takes a `context.Context` and
  reports `context.Cause`. Rollback deliberately outlives caller
  cancellation, bounded by `RollbackTimeout`.
- **Errors.** Failure categories are sentinels (`ErrInvalidPlan`,
  `ErrInvalidPolicy`, `ErrSuperseded`, …) matched with `errors.Is`. A failed
  rollback is joined to the primary error and stops retries. Messages may
  contain keys and action IDs but never specifications or undo values.
- **Ownership.** Specifications, observations, actions and undo values are
  shallow; the manager clones specifications with your `CloneSpec`, and the
  library copies the slices it owns.
- **Stability.** The module is `v0.x`: a minor release may break the API.
  Every break is listed in the [changelog](CHANGELOG.md) and backed by an
  ADR.

## Documentation

- [docs/README.md](docs/README.md): requirements (PRD) and architecture
  decision records.
- API reference on [pkg.go.dev](https://pkg.go.dev/github.com/justusschock/go-reconciliation).

## Contributing

Contributions go through pull requests; nothing is pushed to `main`
directly. Read [AGENTS.md](AGENTS.md) first: it describes the docs-first
process, the API rules and the checks to run before opening a pull request.

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
