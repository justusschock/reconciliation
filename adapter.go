package reconciliation

import "context"

// Adapter observes a value, plans changes, applies actions, and undoes
// reversible actions. Apply and undo payloads are independently typed. An
// Apply error means that action did not complete and its returned Undo value is
// unusable; implementations must make a subsequent observation safe. Engine
// does not serialize calls: Adapter implementations shared by concurrent
// Reconcile calls must be concurrency-safe. Keyed serialization belongs to a
// Manager or another caller-owned admission layer.
type Adapter[S, O, A, U any] interface {
	Observe(context.Context, Key) (O, error)
	Plan(context.Context, Desired[S], O) (Plan[A], error)
	Apply(context.Context, Action[A]) (Undo[U], error)
	Undo(context.Context, Undo[U]) error
}
