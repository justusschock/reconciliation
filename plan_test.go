package reconciliation

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

type adapterSpec struct{}
type adapterObserved struct{}
type adapterAction struct{}
type adapterUndo struct{}

type separatelyTypedAdapter struct{}

func (*separatelyTypedAdapter) Observe(context.Context, Key) (adapterObserved, error) {
	return adapterObserved{}, nil
}

func (*separatelyTypedAdapter) Plan(context.Context, Desired[adapterSpec], adapterObserved) (Plan[adapterAction], error) {
	return Plan[adapterAction]{}, nil
}

func (*separatelyTypedAdapter) Apply(context.Context, Action[adapterAction]) (Undo[adapterUndo], error) {
	return Undo[adapterUndo]{}, nil
}

func (*separatelyTypedAdapter) Undo(context.Context, Undo[adapterUndo]) error {
	return nil
}

var _ Adapter[adapterSpec, adapterObserved, adapterAction, adapterUndo] = (*separatelyTypedAdapter)(nil)

func TestValidatePlanRejectsInvalidReferences(t *testing.T) {
	tests := []struct {
		name string
		plan Plan[string]
		want error
	}{
		{
			name: "empty action ID",
			plan: Plan[string]{Actions: []Action[string]{{}}},
			want: ErrInvalidActionID,
		},
		{
			name: "duplicate action ID",
			plan: Plan[string]{Actions: []Action[string]{{ID: "same"}, {ID: "same"}}},
			want: ErrDuplicateAction,
		},
		{
			name: "missing dependency",
			plan: Plan[string]{Actions: []Action[string]{{ID: "a", DependsOn: []ActionID{"missing"}}}},
			want: ErrMissingDependency,
		},
		{
			name: "duplicate dependency",
			plan: Plan[string]{Actions: []Action[string]{{ID: "a"}, {ID: "b", DependsOn: []ActionID{"a", "a"}}}},
			want: ErrDuplicateDependency,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidatePlan(test.plan)
			if !errors.Is(err, ErrInvalidPlan) || !errors.Is(err, test.want) {
				t.Fatalf("ValidatePlan() error = %v, want ErrInvalidPlan and %v", err, test.want)
			}
		})
	}
}

func TestValidatePlanReportsCanonicalCyclePath(t *testing.T) {
	plan := Plan[string]{Actions: []Action[string]{
		{ID: "z", DependsOn: []ActionID{"y"}},
		{ID: "b", DependsOn: []ActionID{"c"}},
		{ID: "c", DependsOn: []ActionID{"b"}},
		{ID: "y", DependsOn: []ActionID{"z"}},
	}}

	err := ValidatePlan(plan)
	if !errors.Is(err, ErrPlanCycle) {
		t.Fatalf("ValidatePlan() error = %v, want ErrPlanCycle", err)
	}
	if !strings.Contains(err.Error(), "b -> c -> b") {
		t.Fatalf("ValidatePlan() error = %q, want canonical cycle path", err)
	}
}

func TestValidatePlanReportsSelfCycle(t *testing.T) {
	err := ValidatePlan(Plan[string]{Actions: []Action[string]{{ID: "a", DependsOn: []ActionID{"a"}}}})
	if !errors.Is(err, ErrPlanCycle) || !strings.Contains(err.Error(), "a -> a") {
		t.Fatalf("ValidatePlan() error = %v, want self-cycle path", err)
	}
}

func TestValidatePlanEscapesUnsafeCycleIDs(t *testing.T) {
	unsafe := ActionID("line\nbreak -> next")
	invalidUTF8 := ActionID(string([]byte{'b', 'a', 'd', 0xff}))
	err := ValidatePlan(Plan[string]{Actions: []Action[string]{
		{ID: unsafe, DependsOn: []ActionID{invalidUTF8}},
		{ID: invalidUTF8, DependsOn: []ActionID{unsafe}},
	}})
	if !errors.Is(err, ErrPlanCycle) {
		t.Fatalf("ValidatePlan() error = %v, want ErrPlanCycle", err)
	}
	if !utf8.ValidString(err.Error()) || strings.Contains(err.Error(), "line\nbreak") {
		t.Fatalf("ValidatePlan() emitted unsafe diagnostics %q", err)
	}
	for _, id := range []ActionID{unsafe, invalidUTF8} {
		if !strings.Contains(err.Error(), strconv.QuoteToASCII(string(id))) {
			t.Fatalf("ValidatePlan() error = %q, want escaped ID %s", err, strconv.QuoteToASCII(string(id)))
		}
	}
}

func TestOrderedUsesStableTopologicalOrder(t *testing.T) {
	plan := Plan[string]{Actions: []Action[string]{
		{ID: "publish", DependsOn: []ActionID{"compile", "lint"}},
		{ID: "lint"},
		{ID: "archive", DependsOn: []ActionID{"compile"}},
		{ID: "compile"},
		{ID: "announce", DependsOn: []ActionID{"publish"}},
	}}

	got, err := Ordered(plan)
	if err != nil {
		t.Fatalf("Ordered() error = %v", err)
	}
	want := []ActionID{"compile", "archive", "lint", "publish", "announce"}
	gotIDs := make([]ActionID, len(got))
	for index := range got {
		gotIDs[index] = got[index].ID
	}
	if !slices.Equal(gotIDs, want) {
		t.Fatalf("Ordered() IDs = %v, want %v", gotIDs, want)
	}
}

func TestOrderedDoesNotAliasOrMutateInput(t *testing.T) {
	dependencies := []ActionID{"b", "a"}
	plan := Plan[int]{Actions: []Action[int]{
		{ID: "c", DependsOn: dependencies, Value: 3},
		{ID: "b", Value: 2},
		{ID: "a", Value: 1},
	}}
	want := Plan[int]{Actions: []Action[int]{
		{ID: "c", DependsOn: []ActionID{"b", "a"}, Value: 3},
		{ID: "b", Value: 2},
		{ID: "a", Value: 1},
	}}

	got, err := Ordered(plan)
	if err != nil {
		t.Fatalf("Ordered() error = %v", err)
	}
	if !reflect.DeepEqual(plan, want) {
		t.Fatalf("Ordered() mutated input: got %#v, want %#v", plan, want)
	}

	if !slices.Equal(got[2].DependsOn, []ActionID{"a", "b"}) {
		t.Fatalf("Ordered() dependencies = %v, want canonical order", got[2].DependsOn)
	}
	got[2].DependsOn[0] = "changed"
	if !slices.Equal(dependencies, []ActionID{"b", "a"}) {
		t.Fatalf("Ordered() result aliases input dependencies: %v", dependencies)
	}
}

func TestOrderedReturnsValidationErrorWithoutPartialOrder(t *testing.T) {
	got, err := Ordered(Plan[string]{Actions: []Action[string]{
		{ID: "valid"},
		{ID: "broken", DependsOn: []ActionID{"absent"}},
	}})
	if !errors.Is(err, ErrMissingDependency) {
		t.Fatalf("Ordered() error = %v, want ErrMissingDependency", err)
	}
	if got != nil {
		t.Fatalf("Ordered() = %#v, want nil on invalid plan", got)
	}
}
