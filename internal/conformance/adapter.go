// Package conformance provides test adapters for this module's contract tests.
package conformance

import (
	"context"

	"github.com/justusschock/reconciliation"
)

// Adapter implements reconciliation.Adapter with independently supplied
// functions. It is intended for package conformance tests.
type Adapter[S, O, A, U any] struct {
	ObserveFunc func(context.Context, reconciliation.Key) (O, error)
	PlanFunc    func(context.Context, reconciliation.Desired[S], O) (reconciliation.Plan[A], error)
	ApplyFunc   func(context.Context, reconciliation.Action[A]) (reconciliation.Undo[U], error)
	UndoFunc    func(context.Context, reconciliation.Undo[U]) error
}

var _ reconciliation.Adapter[struct{}, struct{}, struct{}, struct{}] = Adapter[struct{}, struct{}, struct{}, struct{}]{}

// Observe calls ObserveFunc.
func (adapter Adapter[S, O, A, U]) Observe(ctx context.Context, key reconciliation.Key) (O, error) {
	return adapter.ObserveFunc(ctx, key)
}

// Plan calls PlanFunc.
func (adapter Adapter[S, O, A, U]) Plan(ctx context.Context, desired reconciliation.Desired[S], observed O) (reconciliation.Plan[A], error) {
	return adapter.PlanFunc(ctx, desired, observed)
}

// Apply calls ApplyFunc.
func (adapter Adapter[S, O, A, U]) Apply(ctx context.Context, action reconciliation.Action[A]) (reconciliation.Undo[U], error) {
	return adapter.ApplyFunc(ctx, action)
}

// Undo calls UndoFunc.
func (adapter Adapter[S, O, A, U]) Undo(ctx context.Context, undo reconciliation.Undo[U]) error {
	return adapter.UndoFunc(ctx, undo)
}
