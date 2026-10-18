package reconciliation

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type engineSpec string
type engineObserved int
type engineAction string
type engineUndo string

type engineAdapter struct {
	observe func(context.Context, Key) (engineObserved, error)
	plan    func(context.Context, Desired[engineSpec], engineObserved) (Plan[engineAction], error)
	apply   func(context.Context, Action[engineAction]) (Undo[engineUndo], error)
	undo    func(context.Context, Undo[engineUndo]) error
}

func (adapter *engineAdapter) Observe(ctx context.Context, key Key) (engineObserved, error) {
	return adapter.observe(ctx, key)
}

func (adapter *engineAdapter) Plan(ctx context.Context, desired Desired[engineSpec], observed engineObserved) (Plan[engineAction], error) {
	return adapter.plan(ctx, desired, observed)
}

func (adapter *engineAdapter) Apply(ctx context.Context, action Action[engineAction]) (Undo[engineUndo], error) {
	return adapter.apply(ctx, action)
}

func (adapter *engineAdapter) Undo(ctx context.Context, undo Undo[engineUndo]) error {
	return adapter.undo(ctx, undo)
}

// FR-RECON-05
func TestEngineNoOpObservesBeforeAndAfter(t *testing.T) {
	observations := 0
	plans := 0
	adapter := &engineAdapter{
		observe: func(context.Context, Key) (engineObserved, error) {
			observations++
			return engineObserved(observations), nil
		},
		plan: func(context.Context, Desired[engineSpec], engineObserved) (Plan[engineAction], error) {
			plans++
			return Plan[engineAction]{}, nil
		},
		apply: unexpectedApply(t),
		undo:  unexpectedUndo(t),
	}

	result, err := NewEngine(adapter, enginePolicy(1)).Reconcile(context.Background(), engineDesired())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if observations != 2 || plans != 1 {
		t.Fatalf("calls observe=%d plan=%d, want 2/1", observations, plans)
	}
	if result.Key != "resource" || result.Generation != 1 || result.Observed != 2 || result.Changed {
		t.Fatalf("Reconcile() result = %#v", result)
	}
}

// FR-RECON-05
func TestEngineAppliesStablePlanAndOwnsUndoProvenance(t *testing.T) {
	var applied []ActionID
	observations := 0
	adapter := &engineAdapter{
		observe: func(context.Context, Key) (engineObserved, error) {
			observations++
			return engineObserved(observations), nil
		},
		plan: func(context.Context, Desired[engineSpec], engineObserved) (Plan[engineAction], error) {
			return Plan[engineAction]{Actions: []Action[engineAction]{
				{ID: "c", DependsOn: []ActionID{"b", "a"}, Value: "C", Reversible: true},
				{ID: "b", Value: "B", Reversible: true},
				{ID: "a", Value: "A", Reversible: true},
			}}, nil
		},
		apply: func(_ context.Context, action Action[engineAction]) (Undo[engineUndo], error) {
			applied = append(applied, action.ID)
			return Undo[engineUndo]{ActionID: "adapter-supplied-wrong-ID", Value: engineUndo(action.Value)}, nil
		},
		undo: unexpectedUndo(t),
	}

	result, err := NewEngine(adapter, enginePolicy(1)).Reconcile(context.Background(), engineDesired())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if !slices.Equal(applied, []ActionID{"a", "b", "c"}) {
		t.Fatalf("applied = %v, want stable topological order", applied)
	}
	if !result.Changed || result.Observed != 2 {
		t.Fatalf("Reconcile() result = %#v", result)
	}
}

// FR-RECON-05
func TestEngineRejectsInvalidConfigurationBeforeAdapterCalls(t *testing.T) {
	calls := 0
	adapter := &engineAdapter{
		observe: func(context.Context, Key) (engineObserved, error) { calls++; return 0, nil },
		plan: func(context.Context, Desired[engineSpec], engineObserved) (Plan[engineAction], error) {
			calls++
			return Plan[engineAction]{}, nil
		},
		apply: func(context.Context, Action[engineAction]) (Undo[engineUndo], error) {
			calls++
			return Undo[engineUndo]{}, nil
		},
		undo: func(context.Context, Undo[engineUndo]) error { calls++; return nil },
	}

	if _, err := NewEngine(adapter, enginePolicy(1)).Reconcile(context.Background(), Desired[engineSpec]{}); !errors.Is(err, ErrInvalidDesired) {
		t.Fatalf("invalid desired error = %v, want ErrInvalidDesired", err)
	}
	if _, err := NewEngine(adapter, Policy{}).Reconcile(context.Background(), engineDesired()); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("invalid policy error = %v, want ErrInvalidPolicy", err)
	}
	var nilAdapter *engineAdapter
	if _, err := NewEngine[engineSpec, engineObserved, engineAction, engineUndo](nilAdapter, enginePolicy(1)).Reconcile(context.Background(), engineDesired()); !errors.Is(err, ErrNilAdapter) {
		t.Fatalf("nil adapter error = %v, want ErrNilAdapter", err)
	}
	var nilClock *recordingClock
	if _, err := NewEngine(adapter, enginePolicy(1), WithClock(nilClock)).Reconcile(context.Background(), engineDesired()); !errors.Is(err, ErrNilClock) {
		t.Fatalf("nil clock error = %v, want ErrNilClock", err)
	}
	if calls != 0 {
		t.Fatalf("adapter called %d times for invalid input", calls)
	}
}

// FR-RECON-05
func TestEngineValidatesWholePlanBeforeMutation(t *testing.T) {
	applyCalls := 0
	adapter := &engineAdapter{
		observe: constantObservation(1),
		plan: func(context.Context, Desired[engineSpec], engineObserved) (Plan[engineAction], error) {
			return Plan[engineAction]{Actions: []Action[engineAction]{
				{ID: "valid", Reversible: true},
				{ID: "broken", DependsOn: []ActionID{"absent"}, Reversible: true},
			}}, nil
		},
		apply: func(context.Context, Action[engineAction]) (Undo[engineUndo], error) {
			applyCalls++
			return Undo[engineUndo]{}, nil
		},
		undo: unexpectedUndo(t),
	}

	_, err := NewEngine(adapter, enginePolicy(1)).Reconcile(context.Background(), engineDesired())
	if !errors.Is(err, ErrInvalidPlan) || !errors.Is(err, ErrMissingDependency) {
		t.Fatalf("Reconcile() error = %v, want invalid missing dependency", err)
	}
	if applyCalls != 0 {
		t.Fatalf("Apply called %d times for invalid plan", applyCalls)
	}
}

// FR-RECON-08
func TestEngineRollsBackCompletedActionsInReverseOrder(t *testing.T) {
	wantApplyErr := errors.New("apply c failed")
	var applied []ActionID
	var undone []Undo[engineUndo]
	adapter := rollbackAdapter(&applied, &undone, wantApplyErr, nil, true)

	result, err := NewEngine(adapter, enginePolicy(1)).Reconcile(context.Background(), engineDesired())
	if !errors.Is(err, wantApplyErr) {
		t.Fatalf("Reconcile() error = %v, want apply error", err)
	}
	if !slices.Equal(applied, []ActionID{"a", "b", "c"}) {
		t.Fatalf("applied = %v", applied)
	}
	gotUndone := []ActionID{undone[0].ActionID, undone[1].ActionID}
	if !slices.Equal(gotUndone, []ActionID{"b", "a"}) {
		t.Fatalf("undone = %v, want [b a]", gotUndone)
	}
	if undone[0].Value != "B" || undone[1].Value != "A" {
		t.Fatalf("undo payloads = %#v", undone)
	}
	if result.Changed {
		t.Fatalf("fully rolled-back result = %#v, want Changed=false", result)
	}
}

// FR-RECON-08
func TestEngineLeavesIrreversibleActionsOutOfRollback(t *testing.T) {
	wantApplyErr := errors.New("apply c failed")
	var applied []ActionID
	var undone []Undo[engineUndo]
	adapter := rollbackAdapter(&applied, &undone, wantApplyErr, nil, false)

	result, err := NewEngine(adapter, enginePolicy(1)).Reconcile(context.Background(), engineDesired())
	if !errors.Is(err, wantApplyErr) {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if len(undone) != 1 || undone[0].ActionID != "b" {
		t.Fatalf("undone = %#v, want only b", undone)
	}
	if !result.Changed {
		t.Fatalf("residual irreversible mutation not reflected: %#v", result)
	}
}

// FR-RECON-08
func TestEngineRetainsDependenciesOfResidualActions(t *testing.T) {
	wantApplyErr := errors.New("apply z failed")
	var undone []ActionID
	adapter := &engineAdapter{
		observe: constantObservation(1),
		plan: func(context.Context, Desired[engineSpec], engineObserved) (Plan[engineAction], error) {
			return Plan[engineAction]{Actions: []Action[engineAction]{
				{ID: "a", Value: "dependency", Reversible: true},
				{ID: "b", DependsOn: []ActionID{"a"}, Value: "retained", Reversible: false},
				{ID: "x", Value: "independent", Reversible: true},
				{ID: "z", DependsOn: []ActionID{"b", "x"}, Value: "failure", Reversible: true},
			}}, nil
		},
		apply: func(_ context.Context, action Action[engineAction]) (Undo[engineUndo], error) {
			if action.ID == "z" {
				return Undo[engineUndo]{}, wantApplyErr
			}
			return Undo[engineUndo]{Value: engineUndo(action.Value)}, nil
		},
		undo: func(_ context.Context, undo Undo[engineUndo]) error {
			undone = append(undone, undo.ActionID)
			return nil
		},
	}

	result, err := NewEngine(adapter, enginePolicy(1)).Reconcile(context.Background(), engineDesired())
	if !errors.Is(err, wantApplyErr) {
		t.Fatalf("Reconcile() error = %v, want %v", err, wantApplyErr)
	}
	if !slices.Equal(undone, []ActionID{"x"}) {
		t.Fatalf("undone = %v, want only independent action x", undone)
	}
	if !result.Changed {
		t.Fatalf("retained dependency closure not reflected: %#v", result)
	}
}

// FR-RECON-08
func TestEngineJoinsRollbackErrorsAndDoesNotRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wantApplyErr := Transient(errors.New("apply failed"))
		wantUndoErr := errors.New("undo failed")
		var applied []ActionID
		var undone []Undo[engineUndo]
		adapter := rollbackAdapter(&applied, &undone, wantApplyErr, wantUndoErr, true)
		policy := Policy{MaxAttempts: 3, BaseDelay: time.Second, MaxDelay: time.Second}
		start := time.Now()

		result, err := NewEngine(adapter, policy).Reconcile(context.Background(), engineDesired())
		if !errors.Is(err, wantApplyErr) || !errors.Is(err, wantUndoErr) {
			t.Fatalf("Reconcile() error = %v, want apply and undo errors", err)
		}
		if elapsed := time.Since(start); len(applied) != 3 || len(undone) != 1 || undone[0].ActionID != "b" || elapsed != 0 {
			t.Fatalf("rollback/retry mismatch: applied=%v undone=%v waited=%v", applied, undone, elapsed)
		}
		if !result.Changed {
			t.Fatalf("failed rollback result = %#v, want Changed=true", result)
		}
	})
}

// FR-RECON-08
func TestEngineRetriesAfterSuccessfulRollback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wantTransient := Transient(errors.New("temporary apply failure"))
		observations := 0
		plans := 0
		var applied []ActionID
		var undone []ActionID
		adapter := &engineAdapter{
			observe: func(context.Context, Key) (engineObserved, error) {
				observations++
				return engineObserved(observations), nil
			},
			plan: func(context.Context, Desired[engineSpec], engineObserved) (Plan[engineAction], error) {
				plans++
				if plans > 1 {
					return Plan[engineAction]{}, nil
				}
				return Plan[engineAction]{Actions: []Action[engineAction]{
					{ID: "a", Value: "A", Reversible: true},
					{ID: "b", DependsOn: []ActionID{"a"}, Value: "B", Reversible: true},
				}}, nil
			},
			apply: func(_ context.Context, action Action[engineAction]) (Undo[engineUndo], error) {
				applied = append(applied, action.ID)
				if action.ID == "b" {
					return Undo[engineUndo]{}, wantTransient
				}
				return Undo[engineUndo]{Value: "undo-a"}, nil
			},
			undo: func(_ context.Context, undo Undo[engineUndo]) error {
				undone = append(undone, undo.ActionID)
				return nil
			},
		}
		policy := Policy{MaxAttempts: 2, BaseDelay: time.Millisecond, MaxDelay: time.Millisecond}
		start := time.Now()

		result, err := NewEngine(adapter, policy).Reconcile(context.Background(), engineDesired())
		if err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
		if elapsed := time.Since(start); elapsed != time.Millisecond {
			t.Fatalf("retry wait = %v, want %v", elapsed, time.Millisecond)
		}
		if !slices.Equal(applied, []ActionID{"a", "b"}) || !slices.Equal(undone, []ActionID{"a"}) {
			t.Fatalf("applied=%v undone=%v", applied, undone)
		}
		if plans != 2 || observations != 3 || result.Observed != 3 || result.Changed {
			t.Fatalf("plans=%d observations=%d result=%#v", plans, observations, result)
		}
	})
}

// FR-RECON-08
func TestEngineCarriesResidualChangeAcrossRetries(t *testing.T) {
	wantTransient := Transient(errors.New("temporary apply failure"))
	plans := 0
	adapter := &engineAdapter{
		observe: constantObservation(1),
		plan: func(context.Context, Desired[engineSpec], engineObserved) (Plan[engineAction], error) {
			plans++
			if plans > 1 {
				return Plan[engineAction]{}, nil
			}
			return Plan[engineAction]{Actions: []Action[engineAction]{
				{ID: "a", Value: "retained", Reversible: false},
				{ID: "b", DependsOn: []ActionID{"a"}, Value: "fails", Reversible: true},
			}}, nil
		},
		apply: func(_ context.Context, action Action[engineAction]) (Undo[engineUndo], error) {
			if action.ID == "b" {
				return Undo[engineUndo]{}, wantTransient
			}
			return Undo[engineUndo]{Value: engineUndo(action.Value)}, nil
		},
		undo: unexpectedUndo(t),
	}
	result, err := NewEngine(adapter, enginePolicy(2)).Reconcile(context.Background(), engineDesired())
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if plans != 2 || !result.Changed {
		t.Fatalf("plans=%d result=%#v, want residual change preserved", plans, result)
	}
}

// FR-RECON-06
func TestEngineRetriesClassifiedFailureWithFreshState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wantConflict := Conflict(errors.New("stale observation"))
		observations := 0
		plans := 0
		adapter := &engineAdapter{
			observe: func(context.Context, Key) (engineObserved, error) {
				observations++
				return engineObserved(observations), nil
			},
			plan: func(context.Context, Desired[engineSpec], engineObserved) (Plan[engineAction], error) {
				plans++
				if plans == 1 {
					return Plan[engineAction]{}, wantConflict
				}
				return Plan[engineAction]{}, nil
			},
			apply: unexpectedApply(t),
			undo:  unexpectedUndo(t),
		}
		policy := Policy{MaxAttempts: 3, BaseDelay: 5 * time.Millisecond, MaxDelay: 20 * time.Millisecond}
		start := time.Now()

		result, err := NewEngine(adapter, policy).Reconcile(context.Background(), engineDesired())
		if err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
		if plans != 2 || observations != 3 || result.Observed != 3 {
			t.Fatalf("fresh-state calls plan=%d observe=%d result=%#v", plans, observations, result)
		}
		if elapsed := time.Since(start); elapsed != 5*time.Millisecond {
			t.Fatalf("retry wait = %v, want %v", elapsed, 5*time.Millisecond)
		}
	})
}

// FR-RECON-06
func TestEngineDoesNotRetryPermanentOrUnclassifiedErrors(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "permanent", err: Permanent(errors.New("invalid"))},
		{name: "unclassified", err: errors.New("unknown")},
	} {
		t.Run(test.name, func(t *testing.T) {
			plans := 0
			adapter := &engineAdapter{
				observe: constantObservation(1),
				plan: func(context.Context, Desired[engineSpec], engineObserved) (Plan[engineAction], error) {
					plans++
					return Plan[engineAction]{}, test.err
				},
				apply: unexpectedApply(t),
				undo:  unexpectedUndo(t),
			}
			_, err := NewEngine(adapter, enginePolicy(3)).Reconcile(context.Background(), engineDesired())
			if !errors.Is(err, test.err) || plans != 1 {
				t.Fatalf("Reconcile() error=%v plans=%d", err, plans)
			}
		})
	}
}

// FR-RECON-09
func TestEngineAttemptTimeoutRollsBackWithParentContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var undone []Undo[engineUndo]
		adapter := &engineAdapter{
			observe: constantObservation(1),
			plan: func(context.Context, Desired[engineSpec], engineObserved) (Plan[engineAction], error) {
				return Plan[engineAction]{Actions: []Action[engineAction]{
					{ID: "a", Value: "A", Reversible: true},
					{ID: "b", DependsOn: []ActionID{"a"}, Value: "B", Reversible: true},
				}}, nil
			},
			apply: func(ctx context.Context, action Action[engineAction]) (Undo[engineUndo], error) {
				if action.ID == "b" {
					<-ctx.Done()
					return Undo[engineUndo]{}, context.Cause(ctx)
				}
				return Undo[engineUndo]{Value: engineUndo(action.Value)}, nil
			},
			undo: func(ctx context.Context, undo Undo[engineUndo]) error {
				if err := context.Cause(ctx); err != nil {
					t.Fatalf("rollback received canceled attempt context: %v", err)
				}
				undone = append(undone, undo)
				return nil
			},
		}
		policy := enginePolicy(2)
		policy.AttemptTimeout = 10 * time.Millisecond
		start := time.Now()

		result, err := NewEngine(adapter, policy).Reconcile(context.Background(), engineDesired())
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Reconcile() error = %v, want deadline", err)
		}
		if elapsed := time.Since(start); elapsed != policy.AttemptTimeout {
			t.Fatalf("attempt ran for %v, want %v", elapsed, policy.AttemptTimeout)
		}
		if len(undone) != 1 || undone[0].ActionID != "a" || result.Changed {
			t.Fatalf("rollback=%#v result=%#v", undone, result)
		}
	})
}

// FR-RECON-09
func TestEngineParentCancellationDoesNotSuppressRollback(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	wantCancel := errors.New("caller stopped")
	var rollbackCause error
	adapter := &engineAdapter{
		observe: constantObservation(1),
		plan: func(context.Context, Desired[engineSpec], engineObserved) (Plan[engineAction], error) {
			return Plan[engineAction]{Actions: []Action[engineAction]{
				{ID: "a", Value: "A", Reversible: true},
				{ID: "b", DependsOn: []ActionID{"a"}, Value: "B", Reversible: true},
			}}, nil
		},
		apply: func(ctx context.Context, action Action[engineAction]) (Undo[engineUndo], error) {
			if action.ID == "b" {
				cancel(wantCancel)
				return Undo[engineUndo]{}, context.Cause(ctx)
			}
			return Undo[engineUndo]{Value: engineUndo(action.Value)}, nil
		},
		undo: func(ctx context.Context, _ Undo[engineUndo]) error {
			rollbackCause = context.Cause(ctx)
			return nil
		},
	}

	result, err := NewEngine(adapter, enginePolicy(1)).Reconcile(ctx, engineDesired())
	if !errors.Is(err, wantCancel) {
		t.Fatalf("Reconcile() error = %v, want %v", err, wantCancel)
	}
	if rollbackCause != nil {
		t.Fatalf("rollback inherited parent cancellation: %v", rollbackCause)
	}
	if result.Changed {
		t.Fatalf("successful cleanup result = %#v, want Changed=false", result)
	}
}

// FR-RECON-09
func TestEngineBoundsRollbackAndPreservesCleanupDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wantApplyErr := errors.New("apply failed")
		adapter := &engineAdapter{
			observe: constantObservation(1),
			plan: func(context.Context, Desired[engineSpec], engineObserved) (Plan[engineAction], error) {
				return Plan[engineAction]{Actions: []Action[engineAction]{
					{ID: "a", Value: "A", Reversible: true},
					{ID: "b", DependsOn: []ActionID{"a"}, Value: "B", Reversible: true},
				}}, nil
			},
			apply: func(_ context.Context, action Action[engineAction]) (Undo[engineUndo], error) {
				if action.ID == "b" {
					return Undo[engineUndo]{}, wantApplyErr
				}
				return Undo[engineUndo]{Value: engineUndo(action.Value)}, nil
			},
			undo: func(ctx context.Context, _ Undo[engineUndo]) error {
				<-ctx.Done()
				return context.Cause(ctx)
			},
		}
		policy := enginePolicy(1)
		policy.RollbackTimeout = 10 * time.Millisecond
		start := time.Now()

		result, err := NewEngine(adapter, policy).Reconcile(context.Background(), engineDesired())
		if !errors.Is(err, wantApplyErr) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Reconcile() error = %v, want apply and cleanup deadline", err)
		}
		if elapsed := time.Since(start); elapsed != policy.RollbackTimeout {
			t.Fatalf("rollback ran for %v, want %v", elapsed, policy.RollbackTimeout)
		}
		if !result.Changed {
			t.Fatalf("timed-out rollback result = %#v, want Changed=true", result)
		}
	})
}

// FR-RECON-09
func TestEngineCancellationInterruptsRetryWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wantFailure := Transient(errors.New("retry me"))
		adapter := &engineAdapter{
			observe: constantObservation(1),
			plan: func(context.Context, Desired[engineSpec], engineObserved) (Plan[engineAction], error) {
				return Plan[engineAction]{}, wantFailure
			},
			apply: unexpectedApply(t),
			undo:  unexpectedUndo(t),
		}
		ctx, cancel := context.WithCancelCause(context.Background())
		wantCancel := errors.New("stop retrying")
		done := make(chan error, 1)
		go func() {
			_, err := NewEngine(adapter, Policy{MaxAttempts: 3, BaseDelay: time.Hour, MaxDelay: time.Hour}).Reconcile(ctx, engineDesired())
			done <- err
		}()

		// Every goroutine in the bubble is now blocked; the engine sits in
		// its one-hour retry wait.
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("Reconcile() returned before cancellation: %v", err)
		default:
		}
		start := time.Now()
		cancel(wantCancel)
		synctest.Wait()
		select {
		case err := <-done:
			if !errors.Is(err, wantCancel) {
				t.Fatalf("Reconcile() error = %v, want cancellation cause", err)
			}
		default:
			t.Fatal("engine did not leave retry wait")
		}
		if elapsed := time.Since(start); elapsed != 0 {
			t.Fatalf("cancellation took %v of virtual time, want 0", elapsed)
		}
	})
}

// FR-RECON-08
func TestEngineRollsBackWhenFinalObservationFails(t *testing.T) {
	wantObserveErr := errors.New("final observation failed")
	observations := 0
	var undone []Undo[engineUndo]
	adapter := &engineAdapter{
		observe: func(context.Context, Key) (engineObserved, error) {
			observations++
			if observations == 2 {
				return 0, wantObserveErr
			}
			return 1, nil
		},
		plan: func(context.Context, Desired[engineSpec], engineObserved) (Plan[engineAction], error) {
			return Plan[engineAction]{Actions: []Action[engineAction]{{ID: "a", Value: "A", Reversible: true}}}, nil
		},
		apply: func(context.Context, Action[engineAction]) (Undo[engineUndo], error) {
			return Undo[engineUndo]{Value: "undo-a"}, nil
		},
		undo: func(_ context.Context, undo Undo[engineUndo]) error {
			undone = append(undone, undo)
			return nil
		},
	}

	result, err := NewEngine(adapter, enginePolicy(1)).Reconcile(context.Background(), engineDesired())
	if !errors.Is(err, wantObserveErr) || len(undone) != 1 || undone[0].ActionID != "a" || result.Changed {
		t.Fatalf("Reconcile() result=%#v error=%v undone=%#v", result, err, undone)
	}
}

func rollbackAdapter(applied *[]ActionID, undone *[]Undo[engineUndo], applyErr, undoErr error, firstReversible bool) *engineAdapter {
	return &engineAdapter{
		observe: constantObservation(1),
		plan: func(context.Context, Desired[engineSpec], engineObserved) (Plan[engineAction], error) {
			return Plan[engineAction]{Actions: []Action[engineAction]{
				{ID: "a", Value: "A", Reversible: firstReversible},
				{ID: "b", DependsOn: []ActionID{"a"}, Value: "B", Reversible: true},
				{ID: "c", DependsOn: []ActionID{"b"}, Value: "C", Reversible: true},
			}}, nil
		},
		apply: func(_ context.Context, action Action[engineAction]) (Undo[engineUndo], error) {
			*applied = append(*applied, action.ID)
			if action.ID == "c" {
				return Undo[engineUndo]{}, applyErr
			}
			return Undo[engineUndo]{ActionID: "wrong", Value: engineUndo(action.Value)}, nil
		},
		undo: func(_ context.Context, undo Undo[engineUndo]) error {
			*undone = append(*undone, undo)
			if undo.ActionID == "b" {
				return undoErr
			}
			return nil
		},
	}
}

func constantObservation(value engineObserved) func(context.Context, Key) (engineObserved, error) {
	return func(context.Context, Key) (engineObserved, error) { return value, nil }
}

func unexpectedApply(t *testing.T) func(context.Context, Action[engineAction]) (Undo[engineUndo], error) {
	t.Helper()
	return func(context.Context, Action[engineAction]) (Undo[engineUndo], error) {
		t.Fatal("unexpected Apply call")
		return Undo[engineUndo]{}, nil
	}
}

func unexpectedUndo(t *testing.T) func(context.Context, Undo[engineUndo]) error {
	t.Helper()
	return func(context.Context, Undo[engineUndo]) error {
		t.Fatal("unexpected Undo call")
		return nil
	}
}

func engineDesired() Desired[engineSpec] {
	return Desired[engineSpec]{Key: "resource", Generation: 1, Spec: "desired"}
}

func enginePolicy(maxAttempts int) Policy {
	return Policy{MaxAttempts: maxAttempts}
}

type recordingClock struct {
	mu        sync.Mutex
	delays    []time.Duration
	immediate bool
	block     bool
	called    chan struct{}
	callOnce  sync.Once
}

func (clock *recordingClock) After(delay time.Duration) <-chan time.Time {
	clock.mu.Lock()
	clock.delays = append(clock.delays, delay)
	clock.mu.Unlock()
	if clock.called != nil {
		clock.callOnce.Do(func() { close(clock.called) })
	}
	channel := make(chan time.Time, 1)
	if clock.immediate {
		channel <- time.Time{}
	}
	if !clock.block && !clock.immediate {
		channel <- time.Time{}
	}
	return channel
}

func (clock *recordingClock) Delays() []time.Duration {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return slices.Clone(clock.delays)
}

var _ Clock = (*recordingClock)(nil)

// FR-RECON-05
func TestEngineResultShapeIsStable(t *testing.T) {
	want := Result[engineObserved]{Key: "resource", Generation: 1, Observed: 2, Changed: true}
	adapter := &engineAdapter{
		observe: func() func(context.Context, Key) (engineObserved, error) {
			value := engineObserved(0)
			return func(context.Context, Key) (engineObserved, error) { value++; return value, nil }
		}(),
		plan: func(context.Context, Desired[engineSpec], engineObserved) (Plan[engineAction], error) {
			return Plan[engineAction]{Actions: []Action[engineAction]{{ID: "a"}}}, nil
		},
		apply: func(context.Context, Action[engineAction]) (Undo[engineUndo], error) { return Undo[engineUndo]{}, nil },
		undo:  unexpectedUndo(t),
	}
	got, err := NewEngine(adapter, enginePolicy(1)).Reconcile(context.Background(), engineDesired())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Reconcile()=(%#v,%v), want (%#v,nil)", got, err, want)
	}
}
