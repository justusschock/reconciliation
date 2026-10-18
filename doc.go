// Package reconciliation converges desired state toward observed state.
//
// A caller implements [Adapter] for its domain: Observe reads the current
// state, Plan returns a dependency graph of actions ([Plan]), Apply performs
// one action and returns the value needed to undo it, and Undo reverses it.
// [Engine] runs one reconciliation through an adapter: it validates the plan
// before mutating anything, applies actions in a deterministic order,
// verifies the result with a fresh observation, retries failures classified
// with [Transient] or [Conflict] according to a [Policy], and rolls back
// completed reversible actions in dependency-safe reverse order when an
// attempt fails.
//
// [Manager] schedules many reconciliations: one call per [Key] at a time, a
// bound on concurrently active keys, coalescing of equal generations,
// supersession of stale pending generations, delayed requeues and graceful
// shutdown. [EventStream] and [WithStatusSink] report progress without ever
// blocking reconciliation.
//
// The handoff subpackage transfers pending work to a successor process.
//
// Values of generic types (specifications, observations, actions and undo
// values) are owned shallowly; see the documentation of each type.
package reconciliation
