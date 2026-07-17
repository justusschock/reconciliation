package reconciliation

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/justusschock/go-concurrency"
)

var (
	// ErrNilReconciler identifies a nil or typed-nil Reconciler.
	ErrNilReconciler = errors.New("reconciliation: nil reconciler")
	// ErrNilCloneSpec identifies a missing specification clone function.
	ErrNilCloneSpec = errors.New("reconciliation: nil specification clone function")
	// ErrSuperseded identifies desired work replaced by a newer generation.
	ErrSuperseded = errors.New("reconciliation: superseded generation")
)

// Reconciler reconciles one desired value. Implementations may be called
// concurrently for different keys, but Manager serializes calls for one key.
type Reconciler[S, O any] interface {
	Reconcile(context.Context, Desired[S]) (Result[O], error)
}

// CloneSpec returns an independently owned specification. Manager requires a
// clone function because generic specifications may contain reference values.
type CloneSpec[S any] func(S) S

// ManagerClock supplies absolute time and retry deadlines. Implementations must
// be safe for concurrent use.
type ManagerClock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}

// ManagerOption configures a Manager.
type ManagerOption func(*managerConfig)

// WithManagerClock replaces the clock used for delayed requeues.
func WithManagerClock(clock ManagerClock) ManagerOption {
	return func(config *managerConfig) {
		config.clock = clock
	}
}

type managerConfig struct {
	clock ManagerClock
	sink  StatusSink
}

// WithManagerStatusSink publishes queued work and carries sink into Reconciler
// calls. Engine reads the sink from that per-call context.
func WithManagerStatusSink(sink StatusSink) ManagerOption {
	return func(config *managerConfig) {
		config.sink = sink
	}
}

// Pending describes accepted work that has not started. Desired.Spec is cloned
// for the caller. RetryAt is zero when the work is ready to run immediately.
type Pending[S any] struct {
	Desired Desired[S]
	RetryAt time.Time
}

// Manager serializes reconciliation by key and bounds concurrently active
// keys. Accepted work runs with a manager-owned context; Submit contexts govern
// admission only.
type Manager[S, O any] struct {
	reconciler Reconciler[S, O]
	cloneSpec  CloneSpec[S]
	limit      int
	clock      ManagerClock
	sink       StatusSink

	ctx    context.Context
	cancel context.CancelCauseFunc

	cloneMu  sync.Mutex
	mu       sync.Mutex
	states   map[Key]*managerKeyState[S, O]
	active   int
	closed   bool
	done     chan struct{}
	doneOnce sync.Once
	closeErr error
}

type managerKeyState[S, O any] struct {
	latest  Generation
	active  *managerWork[S, O]
	pending *managerWork[S, O]
}

type managerWork[S, O any] struct {
	desired Desired[S]
	waiters []*managerWaiter[O]
	ready   bool
	retryAt time.Time
	timer   *managerTimerControl
}

type managerTimerControl struct {
	stop chan struct{}
	once sync.Once
}

func (timer *managerTimerControl) cancel() {
	if timer == nil {
		return
	}
	timer.once.Do(func() { close(timer.stop) })
}

type managerOutcome[O any] struct {
	result Result[O]
	err    error
}

type managerWaiter[O any] struct {
	once    sync.Once
	outcome chan managerOutcome[O]
}

func newManagerWaiter[O any]() (*managerWaiter[O], *concurrency.Future[Result[O]]) {
	waiter := &managerWaiter[O]{outcome: make(chan managerOutcome[O], 1)}
	future := concurrency.Go(context.Background(), func(context.Context) (Result[O], error) {
		outcome := <-waiter.outcome
		return outcome.result, outcome.err
	})
	return waiter, future
}

func (waiter *managerWaiter[O]) resolve(result Result[O], err error) {
	waiter.once.Do(func() {
		waiter.outcome <- managerOutcome[O]{result: result, err: err}
	})
}

// NewManager constructs a keyed reconciliation manager. CloneSpec is required
// and is used both when accepting work and when returning Pending snapshots.
func NewManager[S, O any](
	reconciler Reconciler[S, O],
	limit int,
	cloneSpec CloneSpec[S],
	options ...ManagerOption,
) (*Manager[S, O], error) {
	if isNilInterface(reconciler) {
		return nil, ErrNilReconciler
	}
	if limit <= 0 {
		return nil, concurrency.ErrInvalidLimit
	}
	if cloneSpec == nil {
		return nil, ErrNilCloneSpec
	}
	config := managerConfig{clock: systemManagerClock{}}
	for _, option := range options {
		if option != nil {
			option(&config)
		}
	}
	if isNilInterface(config.clock) {
		return nil, ErrNilClock
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	return &Manager[S, O]{
		reconciler: reconciler,
		cloneSpec:  cloneSpec,
		limit:      limit,
		clock:      config.clock,
		sink:       config.sink,
		ctx:        ctx,
		cancel:     cancel,
		states:     make(map[Key]*managerKeyState[S, O]),
		done:       make(chan struct{}),
	}, nil
}

// Submit validates and clones desired before acceptance. Invalid, closed,
// canceled, stale, and superseded submissions are represented by failed
// futures; Submit never returns a nil future. Coalesced futures own independent
// Conditions slices, while Result.Observed ownership remains deliberately
// shallow.
func (manager *Manager[S, O]) Submit(ctx context.Context, desired Desired[S]) *concurrency.Future[Result[O]] {
	if err := desired.Validate(); err != nil {
		return concurrency.Failed[Result[O]](err)
	}
	if err := context.Cause(ctx); err != nil {
		return concurrency.Failed[Result[O]](err)
	}
	owned := manager.cloneDesired(desired)
	if err := context.Cause(ctx); err != nil {
		return concurrency.Failed[Result[O]](err)
	}

	manager.mu.Lock()
	if manager.closed {
		manager.mu.Unlock()
		return concurrency.Failed[Result[O]](concurrency.ErrClosed)
	}
	if err := context.Cause(ctx); err != nil {
		manager.mu.Unlock()
		return concurrency.Failed[Result[O]](err)
	}

	state := manager.states[owned.Key]
	if state == nil {
		state = &managerKeyState[S, O]{latest: owned.Generation}
		manager.states[owned.Key] = state
	}
	if owned.Generation < state.latest {
		err := supersededGenerationError(owned.Key, owned.Generation, state.latest)
		manager.mu.Unlock()
		return concurrency.Failed[Result[O]](err)
	}

	waiter, future := newManagerWaiter[O]()
	if owned.Generation == state.latest {
		if state.active != nil && state.active.desired.Generation == owned.Generation {
			state.active.waiters = append(state.active.waiters, waiter)
			manager.mu.Unlock()
			manager.publish(owned, StatusQueued, 0, nil)
			return future
		}
		if state.pending != nil && state.pending.desired.Generation == owned.Generation {
			state.pending.waiters = append(state.pending.waiters, waiter)
			manager.mu.Unlock()
			manager.publish(owned, StatusQueued, 0, nil)
			manager.startReady()
			return future
		}
	}

	var superseded *managerWork[S, O]
	if owned.Generation > state.latest {
		state.latest = owned.Generation
		if state.pending != nil {
			superseded = state.pending
			superseded.timer.cancel()
			state.pending = nil
		}
	}
	work := &managerWork[S, O]{desired: owned, waiters: []*managerWaiter[O]{waiter}}
	state.pending = work
	manager.mu.Unlock()

	if superseded != nil {
		resolveManagerWaiters(
			superseded.waiters,
			Result[O]{},
			supersededGenerationError(owned.Key, superseded.desired.Generation, owned.Generation),
		)
	}
	manager.publish(owned, StatusQueued, 0, nil)
	manager.startQueuedWork(work)
	return future
}

// Pending returns key-sorted, independently cloned snapshots of work that has
// not started.
func (manager *Manager[S, O]) Pending() []Pending[S] {
	manager.mu.Lock()
	pending := make([]Pending[S], 0)
	for _, state := range manager.states {
		if state.pending == nil {
			continue
		}
		retryAt := state.pending.retryAt
		if state.pending.ready {
			retryAt = time.Time{}
		}
		pending = append(pending, Pending[S]{Desired: state.pending.desired, RetryAt: retryAt})
	}
	manager.mu.Unlock()

	slices.SortFunc(pending, func(left, right Pending[S]) int {
		switch {
		case left.Desired.Key < right.Desired.Key:
			return -1
		case left.Desired.Key > right.Desired.Key:
			return 1
		default:
			return 0
		}
	})
	for index := range pending {
		pending[index].Desired = manager.cloneDesired(pending[index].Desired)
	}
	return pending
}

// Close atomically stops admission and fails work that has not started. Active
// calls may finish normally until ctx expires. On expiry, their manager-owned
// context and futures fail with the shutdown cause.
func (manager *Manager[S, O]) Close(ctx context.Context) error {
	manager.mu.Lock()
	pending := make([]*managerWork[S, O], 0)
	if !manager.closed {
		manager.closed = true
		for _, state := range manager.states {
			if state.pending == nil {
				continue
			}
			state.pending.timer.cancel()
			pending = append(pending, state.pending)
			state.pending = nil
		}
		manager.finishCloseLocked()
	}
	done := manager.done
	manager.mu.Unlock()

	for _, work := range pending {
		resolveManagerWaiters(work.waiters, Result[O]{}, concurrency.ErrClosed)
	}
	select {
	case <-done:
		return manager.closeError()
	default:
	}
	if err := context.Cause(ctx); err != nil {
		return manager.abort(err)
	}
	select {
	case <-done:
		return manager.closeError()
	case <-ctx.Done():
		return manager.abort(context.Cause(ctx))
	}
}

func (manager *Manager[S, O]) cloneDesired(desired Desired[S]) Desired[S] {
	manager.cloneMu.Lock()
	desired.Spec = manager.cloneSpec(desired.Spec)
	manager.cloneMu.Unlock()
	return desired
}

func (manager *Manager[S, O]) startReadyLocked() {
	for manager.active < manager.limit {
		keys := make([]Key, 0)
		for key, state := range manager.states {
			if state.active == nil && state.pending != nil && state.pending.ready {
				keys = append(keys, key)
			}
		}
		if len(keys) == 0 {
			return
		}
		slices.Sort(keys)
		state := manager.states[keys[0]]
		work := state.pending
		state.pending = nil
		state.active = work
		manager.active++
		go manager.run(work)
	}
}

func (manager *Manager[S, O]) startReady() {
	manager.mu.Lock()
	if !manager.closed {
		manager.startReadyLocked()
	}
	manager.mu.Unlock()
}

func (manager *Manager[S, O]) startQueuedWork(work *managerWork[S, O]) {
	manager.mu.Lock()
	state := manager.states[work.desired.Key]
	if !manager.closed && state != nil && state.pending == work {
		work.ready = true
		manager.startReadyLocked()
	}
	manager.mu.Unlock()
}

func (manager *Manager[S, O]) run(work *managerWork[S, O]) {
	desired := manager.cloneDesired(work.desired)
	ctx := WithStatusSink(manager.ctx, manager.sink)
	result, err := manager.reconciler.Reconcile(ctx, desired)
	manager.complete(work, result, err)
}

func (manager *Manager[S, O]) complete(work *managerWork[S, O], result Result[O], reconcileErr error) {
	resolve := true
	requeued := false
	var timerChannel <-chan time.Time
	manager.mu.Lock()
	state := manager.states[work.desired.Key]
	if state == nil || state.active != work {
		manager.mu.Unlock()
		return
	}
	state.active = nil
	manager.active--

	if reconcileErr == nil && result.RequeueAfter > 0 && !manager.closed &&
		state.latest == work.desired.Generation && state.pending == nil {
		work.ready = false
		work.retryAt = manager.clock.Now().Add(result.RequeueAfter)
		work.timer = &managerTimerControl{stop: make(chan struct{})}
		timerChannel = manager.clock.After(result.RequeueAfter)
		if timerChannel == nil {
			reconcileErr = ErrNilClock
		} else {
			state.pending = work
			resolve = false
			requeued = true
		}
	}

	if manager.closed {
		manager.finishCloseLocked()
	} else {
		manager.startReadyLocked()
	}
	manager.mu.Unlock()

	if resolve {
		resolveManagerWaiters(work.waiters, result, reconcileErr)
		return
	}
	if requeued {
		manager.publish(work.desired, StatusRetrying, 0, nil)
	}
	go manager.waitUntilReady(work, timerChannel)
}

func (manager *Manager[S, O]) waitUntilReady(work *managerWork[S, O], deadline <-chan time.Time) {
	select {
	case <-deadline:
		manager.mu.Lock()
		state := manager.states[work.desired.Key]
		if !manager.closed && state != nil && state.pending == work {
			work.ready = true
			work.retryAt = time.Time{}
			work.timer = nil
			manager.startReadyLocked()
		}
		manager.mu.Unlock()
	case <-work.timer.stop:
	case <-manager.ctx.Done():
	}
}

func (manager *Manager[S, O]) abort(cause error) error {
	manager.mu.Lock()
	if manager.closeErr != nil {
		selected := manager.closeErr
		manager.mu.Unlock()
		return selected
	}
	if manager.active == 0 {
		manager.finishCloseLocked()
		manager.mu.Unlock()
		return nil
	}
	manager.closeErr = cause
	manager.cancel(cause)
	active := make([]*managerWork[S, O], 0, manager.active)
	for _, state := range manager.states {
		if state.active != nil {
			active = append(active, state.active)
		}
	}
	manager.finishCloseLocked()
	manager.mu.Unlock()
	for _, work := range active {
		resolveManagerWaiters(work.waiters, Result[O]{}, cause)
	}
	return cause
}

func (manager *Manager[S, O]) finishCloseLocked() {
	if manager.closed && manager.active == 0 {
		manager.doneOnce.Do(func() { close(manager.done) })
	}
}

func (manager *Manager[S, O]) closeError() error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.closeErr
}

func resolveManagerWaiters[O any](waiters []*managerWaiter[O], result Result[O], err error) {
	for _, waiter := range waiters {
		waiter.resolve(result.Clone(), err)
	}
}

func supersededGenerationError(key Key, generation, latest Generation) error {
	return fmt.Errorf("%w: key %q generation %d was replaced by generation %d", ErrSuperseded, key, generation, latest)
}

func (manager *Manager[S, O]) publish(desired Desired[S], phase StatusPhase, attempt int, err error) {
	if isNilInterface(manager.sink) {
		return
	}
	manager.sink.Publish(StatusEvent{
		Key:        desired.Key,
		Generation: desired.Generation,
		Phase:      phase,
		Attempt:    attempt,
		Err:        err,
	})
}

type systemManagerClock struct{}

func (systemManagerClock) Now() time.Time {
	return time.Now()
}

func (systemManagerClock) After(delay time.Duration) <-chan time.Time {
	return time.After(delay)
}

var _ Reconciler[struct{}, struct{}] = (*Engine[struct{}, struct{}, struct{}, struct{}])(nil)
