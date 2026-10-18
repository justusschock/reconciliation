package conformance_test

import (
	"context"
	"errors"
	"testing"

	"github.com/justusschock/go-reconciliation"
	"github.com/justusschock/go-reconciliation/internal/conformance"
)

func TestAdapterDelegatesToFunctions(t *testing.T) {
	var calls []string
	wantUndoErr := errors.New("undo failed")
	adapter := conformance.Adapter[string, int, string, string]{
		ObserveFunc: func(_ context.Context, key reconciliation.Key) (int, error) {
			calls = append(calls, "observe:"+string(key))
			return 1, nil
		},
		PlanFunc: func(_ context.Context, desired reconciliation.Desired[string], observed int) (reconciliation.Plan[string], error) {
			calls = append(calls, "plan:"+desired.Spec)
			if observed != 1 {
				t.Fatalf("Plan observed = %d, want 1", observed)
			}
			return reconciliation.Plan[string]{Actions: []reconciliation.Action[string]{{ID: "a", Value: "A", Reversible: true}}}, nil
		},
		ApplyFunc: func(_ context.Context, action reconciliation.Action[string]) (reconciliation.Undo[string], error) {
			calls = append(calls, "apply:"+string(action.ID))
			return reconciliation.Undo[string]{ActionID: action.ID, Value: "undo-" + action.Value}, nil
		},
		UndoFunc: func(_ context.Context, undo reconciliation.Undo[string]) error {
			calls = append(calls, "undo:"+undo.Value)
			return wantUndoErr
		},
	}
	var _ reconciliation.Adapter[string, int, string, string] = adapter

	ctx := context.Background()
	observed, err := adapter.Observe(ctx, "key")
	if err != nil || observed != 1 {
		t.Fatalf("Observe() = (%d, %v), want (1, nil)", observed, err)
	}
	plan, err := adapter.Plan(ctx, reconciliation.Desired[string]{Key: "key", Generation: 1, Spec: "spec"}, observed)
	if err != nil || len(plan.Actions) != 1 {
		t.Fatalf("Plan() = (%#v, %v), want one action", plan, err)
	}
	undo, err := adapter.Apply(ctx, plan.Actions[0])
	if err != nil || undo.Value != "undo-A" {
		t.Fatalf("Apply() = (%#v, %v), want undo-A", undo, err)
	}
	if err := adapter.Undo(ctx, undo); !errors.Is(err, wantUndoErr) {
		t.Fatalf("Undo() error = %v, want %v", err, wantUndoErr)
	}
	want := []string{"observe:key", "plan:spec", "apply:a", "undo:undo-A"}
	if len(calls) != len(want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Fatalf("calls = %v, want %v", calls, want)
		}
	}
}
