package reconciliation

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"
)

var (
	// ErrNilAdapter identifies a nil or typed-nil engine adapter.
	ErrNilAdapter = errors.New("reconciliation: nil adapter")
	// ErrNilClock identifies a nil or typed-nil retry clock.
	ErrNilClock = errors.New("reconciliation: nil clock")
)

// Clock supplies retry deadlines. After must return a non-nil channel.
// Implementations must be safe for concurrent use by reconciliation calls.
type Clock interface {
	After(time.Duration) <-chan time.Time
}

// EngineOption configures an Engine.
type EngineOption func(*engineConfig)

// WithClock replaces the clock used for retry delays.
func WithClock(clock Clock) EngineOption {
	return func(config *engineConfig) {
		config.clock = clock
	}
}

type engineConfig struct {
	clock Clock
}

// Engine executes validated action plans and rolls back completed reversible
// actions when an attempt fails. Engine does not serialize Reconcile calls;
// callers that share an Engine must satisfy the Adapter, Clock, and Jitter
// concurrency contracts and serialize calls by key when required.
type Engine[S, O, A, U any] struct {
	adapter Adapter[S, O, A, U]
	policy  Policy
	clock   Clock
}

// NewEngine constructs a one-shot reconciliation engine.
func NewEngine[S, O, A, U any](adapter Adapter[S, O, A, U], policy Policy, options ...EngineOption) *Engine[S, O, A, U] {
	config := engineConfig{clock: systemClock{}}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	return &Engine[S, O, A, U]{adapter: adapter, policy: policy, clock: config.clock}
}

// Reconcile observes, plans, validates, applies, and verifies one desired
// value. A retry always starts with a fresh observation and plan.
func (engine *Engine[S, O, A, U]) Reconcile(ctx context.Context, desired Desired[S]) (result Result[O], reconcileErr error) {
	result = Result[O]{Key: desired.Key, Generation: desired.Generation}
	attemptNumber := 0
	defer func() {
		phase := StatusSucceeded
		if reconcileErr != nil {
			phase = StatusFailed
		}
		publishStatus(ctx, StatusEvent{
			Key:        desired.Key,
			Generation: desired.Generation,
			Phase:      phase,
			Attempt:    attemptNumber,
			Err:        reconcileErr,
		})
	}()
	if err := desired.Validate(); err != nil {
		return result, err
	}
	if err := engine.policy.Validate(); err != nil {
		return result, err
	}
	if isNilInterface(engine.adapter) {
		return result, ErrNilAdapter
	}
	if isNilInterface(engine.clock) {
		return result, ErrNilClock
	}
	if err := context.Cause(ctx); err != nil {
		return result, err
	}

	var lastErr error
	residualChange := false
	for attempt := 1; attempt <= engine.policy.MaxAttempts; attempt++ {
		attemptNumber = attempt
		attemptContext := ctx
		cancelAttempt := func() {}
		if engine.policy.AttemptTimeout > 0 {
			attemptContext, cancelAttempt = context.WithTimeout(ctx, engine.policy.AttemptTimeout)
		}

		attemptResult, attemptErr, retrySafe := engine.runAttempt(attemptContext, ctx, desired, attempt)
		cancelAttempt()
		residualChange = residualChange || attemptResult.Changed
		attemptResult.Changed = residualChange
		if attemptErr == nil {
			return attemptResult, nil
		}
		lastErr = attemptErr
		result = attemptResult
		decision := engine.policy.Decide(attempt, attemptErr)
		if !retrySafe || !decision.Retry {
			return result, attemptErr
		}
		publishStatus(ctx, StatusEvent{
			Key:        desired.Key,
			Generation: desired.Generation,
			Phase:      StatusRetrying,
			Attempt:    attempt,
			Err:        attemptErr,
		})
		if err := engine.waitForRetry(ctx, decision.Delay); err != nil {
			return result, err
		}
	}

	return result, lastErr
}

type completedAction[U any] struct {
	id         ActionID
	dependsOn  []ActionID
	reversible bool
	undo       Undo[U]
}

func (engine *Engine[S, O, A, U]) runAttempt(
	attemptContext context.Context,
	rollbackContext context.Context,
	desired Desired[S],
	attempt int,
) (Result[O], error, bool) {
	result := Result[O]{Key: desired.Key, Generation: desired.Generation}

	publishStatus(attemptContext, StatusEvent{
		Key: desired.Key, Generation: desired.Generation, Phase: StatusObserving, Attempt: attempt,
	})
	observed, observeErr := engine.adapter.Observe(attemptContext, desired.Key)
	result.Observed = observed
	if err := operationError(attemptContext, observeErr); err != nil {
		return result, fmt.Errorf("reconciliation: observe attempt %d: %w", attempt, err), true
	}

	publishStatus(attemptContext, StatusEvent{
		Key: desired.Key, Generation: desired.Generation, Phase: StatusPlanning, Attempt: attempt,
	})
	plan, planErr := engine.adapter.Plan(attemptContext, desired, observed)
	if err := operationError(attemptContext, planErr); err != nil {
		return result, fmt.Errorf("reconciliation: plan attempt %d: %w", attempt, err), true
	}
	ordered, err := Ordered(plan)
	if err != nil {
		return result, fmt.Errorf("reconciliation: validate attempt %d: %w", attempt, err), true
	}

	completed := make([]completedAction[U], 0, len(ordered))
	for _, action := range ordered {
		if err := context.Cause(attemptContext); err != nil {
			return engine.failAttempt(rollbackContext, result, completed, attempt, fmt.Errorf("reconciliation: apply action %s: %w", formatActionID(action.ID), err))
		}
		dependencies := slices.Clone(action.DependsOn)
		publishStatus(attemptContext, StatusEvent{
			Key: desired.Key, Generation: desired.Generation, Phase: StatusApplying, Attempt: attempt, ActionID: action.ID,
		})
		undo, applyErr := engine.adapter.Apply(attemptContext, action)
		if applyErr != nil {
			err := operationError(attemptContext, applyErr)
			return engine.failAttempt(rollbackContext, result, completed, attempt, fmt.Errorf("reconciliation: apply action %s: %w", formatActionID(action.ID), err))
		}
		undo.ActionID = action.ID
		completed = append(completed, completedAction[U]{
			id:         action.ID,
			dependsOn:  dependencies,
			reversible: action.Reversible,
			undo:       undo,
		})
		if err := context.Cause(attemptContext); err != nil {
			return engine.failAttempt(rollbackContext, result, completed, attempt, fmt.Errorf("reconciliation: apply action %s: %w", formatActionID(action.ID), err))
		}
	}

	publishStatus(attemptContext, StatusEvent{
		Key: desired.Key, Generation: desired.Generation, Phase: StatusObserving, Attempt: attempt,
	})
	finalObserved, finalObserveErr := engine.adapter.Observe(attemptContext, desired.Key)
	if err := operationError(attemptContext, finalObserveErr); err != nil {
		return engine.failAttempt(rollbackContext, result, completed, attempt, fmt.Errorf("reconciliation: verify attempt %d: %w", attempt, err))
	}
	result.Observed = finalObserved
	result.Changed = len(completed) > 0
	return result, nil, true
}

func (engine *Engine[S, O, A, U]) failAttempt(
	parentContext context.Context,
	result Result[O],
	completed []completedAction[U],
	attempt int,
	primary error,
) (Result[O], error, bool) {
	if len(completed) == 0 {
		return result, primary, true
	}
	publishStatus(parentContext, StatusEvent{
		Key: result.Key, Generation: result.Generation, Phase: StatusRollingBack, Attempt: attempt, Err: primary,
	})
	rollbackContext, cancelRollback := context.WithTimeout(
		context.WithoutCancel(parentContext),
		engine.policy.rollbackTimeout(),
	)
	defer cancelRollback()

	rollbackErr, residualChange := engine.rollback(rollbackContext, completed)
	result.Changed = residualChange
	if rollbackErr != nil {
		return result, errors.Join(primary, rollbackErr), false
	}
	return result, primary, true
}

func (engine *Engine[S, O, A, U]) rollback(ctx context.Context, completed []completedAction[U]) (error, bool) {
	byID := make(map[ActionID]completedAction[U], len(completed))
	for _, action := range completed {
		byID[action.id] = action
	}
	retained := make(map[ActionID]struct{})
	rollbackErrors := make([]error, 0)
	contextReported := false
	for index := len(completed) - 1; index >= 0; index-- {
		action := completed[index]
		if _, keep := retained[action.id]; keep {
			continue
		}
		if !action.reversible {
			retainDependencyClosure(action.id, byID, retained)
			continue
		}
		if contextErr := context.Cause(ctx); contextErr != nil {
			retainDependencyClosure(action.id, byID, retained)
			if !contextReported {
				rollbackErrors = append(rollbackErrors, fmt.Errorf("reconciliation: rollback context: %w", contextErr))
				contextReported = true
			}
			continue
		}
		if err := operationError(ctx, engine.adapter.Undo(ctx, action.undo)); err != nil {
			retainDependencyClosure(action.id, byID, retained)
			rollbackErrors = append(rollbackErrors, fmt.Errorf("reconciliation: undo action %s: %w", formatActionID(action.id), err))
			if context.Cause(ctx) != nil {
				contextReported = true
			}
		}
	}
	return errors.Join(rollbackErrors...), len(retained) > 0
}

func retainDependencyClosure[U any](
	id ActionID,
	completed map[ActionID]completedAction[U],
	retained map[ActionID]struct{},
) {
	if _, exists := retained[id]; exists {
		return
	}
	retained[id] = struct{}{}
	action, exists := completed[id]
	if !exists {
		return
	}
	for _, dependency := range action.dependsOn {
		retainDependencyClosure(dependency, completed, retained)
	}
}

func (engine *Engine[S, O, A, U]) waitForRetry(ctx context.Context, delay time.Duration) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	if delay <= 0 {
		return nil
	}
	deadline := engine.clock.After(delay)
	if deadline == nil {
		return ErrNilClock
	}
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-deadline:
		return nil
	}
}

func operationError(ctx context.Context, operationErr error) error {
	contextErr := context.Cause(ctx)
	if operationErr == nil {
		return contextErr
	}
	if contextErr == nil || errors.Is(operationErr, contextErr) {
		return operationErr
	}
	return errors.Join(operationErr, contextErr)
}

func isNilInterface(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}

type systemClock struct{}

func (systemClock) After(delay time.Duration) <-chan time.Time {
	return time.After(delay)
}
