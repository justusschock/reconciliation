package reconciliation_test

import (
	"bytes"
	"context"
	"fmt"
	"time"

	reconciliation "github.com/justusschock/go-reconciliation"
)

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
