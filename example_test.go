package reconciliation_test

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	reconciliation "github.com/justusschock/go-reconciliation"
)

// settings is a toy environment whose observed state is a map of names to
// values. Apply returns the previous value so the engine can roll it back.
type settings struct {
	mu      sync.Mutex
	current map[string]string
}

// set changes one setting; previous restores it.
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

// FR-RECON-05
func ExampleEngine_Reconcile() {
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

	// Output:
	// true api:v2 3
}

// FR-RECON-04, NFR-RECON-01
func ExampleOrdered() {
	plan := reconciliation.Plan[string]{Actions: []reconciliation.Action[string]{
		{ID: "fetch", Value: "read current state"},
		{ID: "configure", DependsOn: []reconciliation.ActionID{"fetch"}, Value: "apply configuration"},
		{ID: "validate", DependsOn: []reconciliation.ActionID{"fetch"}, Value: "validate inputs"},
		{ID: "publish", DependsOn: []reconciliation.ActionID{"configure", "validate"}, Value: "publish result"},
	}}

	ordered, err := reconciliation.Ordered(plan)
	if err != nil {
		panic(err)
	}
	for _, action := range ordered {
		fmt.Println(action.ID)
	}

	// Output:
	// fetch
	// configure
	// validate
	// publish
}

// FR-RECON-12
func ExampleManager_Pending() {
	reconciler := &blockingReconciler{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	manager, err := reconciliation.NewManager[[]byte, string](
		reconciler,
		1,
		func(spec []byte) []byte { return bytes.Clone(spec) },
	)
	if err != nil {
		panic(err)
	}

	active := manager.Submit(context.Background(), reconciliation.Desired[[]byte]{
		Key: "active", Generation: 1, Spec: []byte("first"),
	})
	<-reconciler.started
	queued := manager.Submit(context.Background(), reconciliation.Desired[[]byte]{
		Key: "queued", Generation: 1, Spec: []byte("second"),
	})

	pending := manager.Pending()
	fmt.Println(pending[0].Desired.Key, pending[0].RetryAt.IsZero())

	close(reconciler.release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := active.Await(ctx); err != nil {
		panic(err)
	}
	if _, err := queued.Await(ctx); err != nil {
		panic(err)
	}
	if err := manager.Close(ctx); err != nil {
		panic(err)
	}

	// Output:
	// queued true
}

type blockingReconciler struct {
	started chan struct{}
	release chan struct{}
}

func (reconciler *blockingReconciler) Reconcile(
	ctx context.Context,
	desired reconciliation.Desired[[]byte],
) (reconciliation.Result[string], error) {
	if desired.Key == "active" {
		close(reconciler.started)
		select {
		case <-ctx.Done():
			return reconciliation.Result[string]{}, context.Cause(ctx)
		case <-reconciler.release:
		}
	}
	return reconciliation.Result[string]{
		Key: desired.Key, Generation: desired.Generation, Observed: string(desired.Spec),
	}, nil
}
