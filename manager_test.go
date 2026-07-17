package reconciliation

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/justusschock/go-concurrency"
)

type managerSpec struct {
	Values map[string]string
}

type reconcilerFunc[S, O any] func(context.Context, Desired[S]) (Result[O], error)

func (reconcile reconcilerFunc[S, O]) Reconcile(ctx context.Context, desired Desired[S]) (Result[O], error) {
	return reconcile(ctx, desired)
}

func cloneManagerSpec(spec managerSpec) managerSpec {
	return managerSpec{Values: maps.Clone(spec.Values)}
}

func managerDesired(key Key, generation Generation) Desired[managerSpec] {
	return Desired[managerSpec]{
		Key:        key,
		Generation: generation,
		Spec:       managerSpec{Values: map[string]string{"value": string(key)}},
	}
}

func managerResult(desired Desired[managerSpec], observed string) Result[string] {
	return Result[string]{Key: desired.Key, Generation: desired.Generation, Observed: observed}
}

func TestNewManagerValidatesRequiredConfiguration(t *testing.T) {
	reconciler := reconcilerFunc[managerSpec, string](func(_ context.Context, desired Desired[managerSpec]) (Result[string], error) {
		return managerResult(desired, "ok"), nil
	})

	if _, err := NewManager(reconciler, 0, cloneManagerSpec); !errors.Is(err, concurrency.ErrInvalidLimit) {
		t.Fatalf("NewManager() limit error = %v, want ErrInvalidLimit", err)
	}
	if _, err := NewManager(reconciler, 1, CloneSpec[managerSpec](nil)); !errors.Is(err, ErrNilCloneSpec) {
		t.Fatalf("NewManager() clone error = %v, want ErrNilCloneSpec", err)
	}
	var nilReconciler *stubReconciler
	if _, err := NewManager[managerSpec, string](nilReconciler, 1, cloneManagerSpec); !errors.Is(err, ErrNilReconciler) {
		t.Fatalf("NewManager() reconciler error = %v, want ErrNilReconciler", err)
	}
}

type stubReconciler struct{}

func (*stubReconciler) Reconcile(_ context.Context, desired Desired[managerSpec]) (Result[string], error) {
	return managerResult(desired, "ok"), nil
}

func TestManagerSerializesKeysAndBoundsActiveWork(t *testing.T) {
	type callID struct {
		key        Key
		generation Generation
	}
	gates := map[callID]chan struct{}{
		{key: "a", generation: 1}: make(chan struct{}),
		{key: "a", generation: 2}: make(chan struct{}),
		{key: "b", generation: 1}: make(chan struct{}),
		{key: "c", generation: 1}: make(chan struct{}),
	}
	started := make(chan callID, len(gates))
	var mu sync.Mutex
	activeByKey := make(map[Key]int)
	active := 0
	maximum := 0
	overlapped := false
	reconciler := reconcilerFunc[managerSpec, string](func(ctx context.Context, desired Desired[managerSpec]) (Result[string], error) {
		id := callID{key: desired.Key, generation: desired.Generation}
		mu.Lock()
		active++
		activeByKey[desired.Key]++
		maximum = max(maximum, active)
		overlapped = overlapped || activeByKey[desired.Key] > 1
		mu.Unlock()
		started <- id
		select {
		case <-gates[id]:
		case <-ctx.Done():
			return Result[string]{}, context.Cause(ctx)
		}
		mu.Lock()
		active--
		activeByKey[desired.Key]--
		mu.Unlock()
		return managerResult(desired, string(desired.Key)), nil
	})

	manager, err := NewManager(reconciler, 2, cloneManagerSpec)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	futures := []*concurrency.Future[Result[string]]{
		manager.Submit(context.Background(), managerDesired("a", 1)),
		manager.Submit(context.Background(), managerDesired("a", 2)),
		manager.Submit(context.Background(), managerDesired("b", 1)),
		manager.Submit(context.Background(), managerDesired("c", 1)),
	}

	first := receiveManagerCall(t, started)
	second := receiveManagerCall(t, started)
	if got := []Key{first.key, second.key}; !sameKeys(got, []Key{"a", "b"}) {
		t.Fatalf("first active keys = %v, want a and b", got)
	}
	close(gates[callID{key: "a", generation: 1}])
	third := receiveManagerCall(t, started)
	if third != (callID{key: "a", generation: 2}) {
		t.Fatalf("third call = %#v, want a generation 2", third)
	}
	close(gates[callID{key: "b", generation: 1}])
	fourth := receiveManagerCall(t, started)
	if fourth != (callID{key: "c", generation: 1}) {
		t.Fatalf("fourth call = %#v, want c generation 1", fourth)
	}
	close(gates[callID{key: "a", generation: 2}])
	close(gates[callID{key: "c", generation: 1}])
	for index, future := range futures {
		if _, err := awaitManagerFuture(t, future); err != nil {
			t.Fatalf("future %d error = %v", index, err)
		}
	}
	mu.Lock()
	gotMaximum, gotOverlap := maximum, overlapped
	mu.Unlock()
	if gotMaximum != 2 {
		t.Fatalf("maximum active = %d, want 2", gotMaximum)
	}
	if gotOverlap {
		t.Fatal("same key reconciled concurrently")
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestManagerCoalescesEqualAndSupersedesPendingGenerations(t *testing.T) {
	blockerRelease := make(chan struct{})
	targetCalls := make(chan Desired[managerSpec], 2)
	var targetCount atomic.Int32
	reconciler := reconcilerFunc[managerSpec, string](func(ctx context.Context, desired Desired[managerSpec]) (Result[string], error) {
		if desired.Key == "blocker" {
			select {
			case <-blockerRelease:
				return managerResult(desired, "blocker"), nil
			case <-ctx.Done():
				return Result[string]{}, context.Cause(ctx)
			}
		}
		targetCount.Add(1)
		targetCalls <- desired
		return managerResult(desired, "target"), nil
	})

	manager, err := NewManager(reconciler, 1, cloneManagerSpec)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	blocker := manager.Submit(context.Background(), managerDesired("blocker", 1))
	first := manager.Submit(context.Background(), managerDesired("target", 1))
	equalFirst := manager.Submit(context.Background(), managerDesired("target", 1))
	second := manager.Submit(context.Background(), managerDesired("target", 2))
	equalSecond := manager.Submit(context.Background(), managerDesired("target", 2))
	older := manager.Submit(context.Background(), managerDesired("target", 1))

	for name, future := range map[string]*concurrency.Future[Result[string]]{
		"first": first, "equal first": equalFirst, "older": older,
	} {
		if _, err := awaitManagerFuture(t, future); !errors.Is(err, ErrSuperseded) {
			t.Fatalf("%s error = %v, want ErrSuperseded", name, err)
		}
	}
	pending := manager.Pending()
	if len(pending) != 1 || pending[0].Desired.Key != "target" || pending[0].Desired.Generation != 2 || !pending[0].RetryAt.IsZero() {
		t.Fatalf("Pending() = %#v, want target generation 2 ready immediately", pending)
	}
	close(blockerRelease)
	if _, err := awaitManagerFuture(t, blocker); err != nil {
		t.Fatalf("blocker error = %v", err)
	}
	call := receiveManagerDesired(t, targetCalls)
	if call.Generation != 2 {
		t.Fatalf("target generation = %d, want 2", call.Generation)
	}
	for name, future := range map[string]*concurrency.Future[Result[string]]{"second": second, "equal second": equalSecond} {
		result, err := awaitManagerFuture(t, future)
		if err != nil || result.Generation != 2 {
			t.Fatalf("%s = (%#v, %v), want generation 2", name, result, err)
		}
	}
	if got := targetCount.Load(); got != 1 {
		t.Fatalf("target reconciliations = %d, want 1", got)
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestManagerDoesNotCancelActiveWorkOnSupersession(t *testing.T) {
	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	secondStarted := make(chan struct{})
	var firstCanceled atomic.Bool
	reconciler := reconcilerFunc[managerSpec, string](func(ctx context.Context, desired Desired[managerSpec]) (Result[string], error) {
		if desired.Generation == 1 {
			close(firstStarted)
			select {
			case <-firstRelease:
			case <-ctx.Done():
				firstCanceled.Store(true)
				return Result[string]{}, context.Cause(ctx)
			}
		} else {
			close(secondStarted)
		}
		return managerResult(desired, "ok"), nil
	})
	manager, err := NewManager(reconciler, 1, cloneManagerSpec)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	first := manager.Submit(context.Background(), managerDesired("target", 1))
	receiveSignal(t, firstStarted, "first reconciliation")
	second := manager.Submit(context.Background(), managerDesired("target", 2))
	close(firstRelease)
	if result, err := awaitManagerFuture(t, first); err != nil || result.Generation != 1 {
		t.Fatalf("first future = (%#v, %v), want successful generation 1", result, err)
	}
	receiveSignal(t, secondStarted, "second reconciliation")
	if result, err := awaitManagerFuture(t, second); err != nil || result.Generation != 2 {
		t.Fatalf("second future = (%#v, %v), want successful generation 2", result, err)
	}
	if firstCanceled.Load() {
		t.Fatal("active generation was canceled by supersession")
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestManagerOwnsAcceptedAndPendingSpecs(t *testing.T) {
	blockerStarted := make(chan struct{})
	blockerRelease := make(chan struct{})
	seen := make(chan Desired[managerSpec], 3)
	reconciler := reconcilerFunc[managerSpec, string](func(ctx context.Context, desired Desired[managerSpec]) (Result[string], error) {
		seen <- desired
		if desired.Key == "active" {
			close(blockerStarted)
			select {
			case <-blockerRelease:
			case <-ctx.Done():
				return Result[string]{}, context.Cause(ctx)
			}
		}
		return managerResult(desired, desired.Spec.Values["value"]), nil
	})
	manager, err := NewManager(reconciler, 1, cloneManagerSpec)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	active := manager.Submit(context.Background(), managerDesired("active", 1))
	receiveSignal(t, blockerStarted, "active reconciliation")
	originalB := managerDesired("b", 1)
	originalZ := managerDesired("z", 1)
	futureB := manager.Submit(context.Background(), originalB)
	futureZ := manager.Submit(context.Background(), originalZ)
	originalB.Spec.Values["value"] = "caller mutation"
	originalZ.Spec.Values["value"] = "caller mutation"

	pending := manager.Pending()
	if got := []Key{pending[0].Desired.Key, pending[1].Desired.Key}; !slices.Equal(got, []Key{"b", "z"}) {
		t.Fatalf("Pending() keys = %v, want [b z]", got)
	}
	if !pending[0].RetryAt.IsZero() || !pending[1].RetryAt.IsZero() {
		t.Fatalf("Pending() retry deadlines = %v, %v, want zero", pending[0].RetryAt, pending[1].RetryAt)
	}
	pending[0].Desired.Spec.Values["value"] = "snapshot mutation"
	if got := manager.Pending()[0].Desired.Spec.Values["value"]; got != "b" {
		t.Fatalf("second Pending() spec = %q, want owned value b", got)
	}

	close(blockerRelease)
	for index, future := range []*concurrency.Future[Result[string]]{active, futureB, futureZ} {
		if _, err := awaitManagerFuture(t, future); err != nil {
			t.Fatalf("future %d error = %v", index, err)
		}
	}
	close(seen)
	for desired := range seen {
		if desired.Key != "active" && desired.Spec.Values["value"] != string(desired.Key) {
			t.Fatalf("reconciler saw mutated desired = %#v", desired)
		}
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestManagerCoalescedResultsOwnConditionsButObservedIsShallow(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	observed := map[string]string{"value": "shared"}
	reconciler := reconcilerFunc[managerSpec, map[string]string](func(ctx context.Context, desired Desired[managerSpec]) (Result[map[string]string], error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return Result[map[string]string]{}, context.Cause(ctx)
		}
		return Result[map[string]string]{
			Key: desired.Key, Generation: desired.Generation, Observed: observed,
			Conditions: []Condition{{Type: "Ready", Status: ConditionTrue, Message: "owned"}},
		}, nil
	})
	manager, err := NewManager(reconciler, 1, cloneManagerSpec)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	first := manager.Submit(context.Background(), managerDesired("target", 1))
	receiveSignal(t, started, "reconciliation")
	second := manager.Submit(context.Background(), managerDesired("target", 1))
	close(release)
	firstResult, firstErr := awaitManagerFuture(t, first)
	secondResult, secondErr := awaitManagerFuture(t, second)
	if firstErr != nil || secondErr != nil {
		t.Fatalf("future errors = %v, %v", firstErr, secondErr)
	}
	firstResult.Conditions[0].Message = "changed"
	if secondResult.Conditions[0].Message != "owned" {
		t.Fatalf("coalesced conditions share storage: %#v", secondResult.Conditions)
	}
	firstResult.Observed["value"] = "shallow"
	if secondResult.Observed["value"] != "shallow" {
		t.Fatalf("Observed was deep-copied, want documented shallow ownership: %#v", secondResult.Observed)
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestManagerRequeuesAtAbsoluteDeadlineAndResolvesOriginalFuture(t *testing.T) {
	clock := newManagerClock(time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC))
	calls := make(chan int, 2)
	var count atomic.Int32
	reconciler := reconcilerFunc[managerSpec, string](func(_ context.Context, desired Desired[managerSpec]) (Result[string], error) {
		call := int(count.Add(1))
		calls <- call
		result := managerResult(desired, "final")
		if call == 1 {
			result.Observed = "intermediate"
			result.RequeueAfter = 10 * time.Second
		}
		return result, nil
	})
	manager, err := NewManager(reconciler, 1, cloneManagerSpec, WithManagerClock(clock))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	future := manager.Submit(context.Background(), managerDesired("target", 1))
	if call := receiveInt(t, calls, "first reconciliation"); call != 1 {
		t.Fatalf("first call = %d", call)
	}
	pending := waitForManagerPending(t, manager, 1)
	wantRetryAt := clock.Start().Add(10 * time.Second)
	if !pending[0].RetryAt.Equal(wantRetryAt) {
		t.Fatalf("RetryAt = %v, want %v", pending[0].RetryAt, wantRetryAt)
	}
	if _, _, ready := future.Try(); ready {
		t.Fatal("future resolved before requeued generation completed")
	}
	clock.Advance(9 * time.Second)
	if got := count.Load(); got != 1 {
		t.Fatalf("calls before deadline = %d, want 1", got)
	}
	clock.Advance(time.Second)
	if call := receiveInt(t, calls, "requeued reconciliation"); call != 2 {
		t.Fatalf("second call = %d", call)
	}
	result, err := awaitManagerFuture(t, future)
	if err != nil || result.Observed != "final" || result.RequeueAfter != 0 {
		t.Fatalf("future = (%#v, %v), want final completed run", result, err)
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestManagerNewGenerationReplacesScheduledRequeue(t *testing.T) {
	clock := newManagerClock(time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC))
	calls := make(chan Generation, 3)
	reconciler := reconcilerFunc[managerSpec, string](func(_ context.Context, desired Desired[managerSpec]) (Result[string], error) {
		calls <- desired.Generation
		result := managerResult(desired, "done")
		if desired.Generation == 1 {
			result.RequeueAfter = time.Hour
		}
		return result, nil
	})
	manager, err := NewManager(reconciler, 1, cloneManagerSpec, WithManagerClock(clock))
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	first := manager.Submit(context.Background(), managerDesired("target", 1))
	if generation := receiveGeneration(t, calls); generation != 1 {
		t.Fatalf("first generation = %d", generation)
	}
	waitForManagerPending(t, manager, 1)
	second := manager.Submit(context.Background(), managerDesired("target", 2))
	if _, err := awaitManagerFuture(t, first); !errors.Is(err, ErrSuperseded) {
		t.Fatalf("first future error = %v, want ErrSuperseded", err)
	}
	if generation := receiveGeneration(t, calls); generation != 2 {
		t.Fatalf("second generation = %d", generation)
	}
	if result, err := awaitManagerFuture(t, second); err != nil || result.Generation != 2 {
		t.Fatalf("second future = (%#v, %v), want generation 2", result, err)
	}
	clock.Advance(2 * time.Hour)
	if err := manager.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if got := len(calls); got != 0 {
		t.Fatalf("obsolete requeue produced %d extra calls", got)
	}
}

func TestManagerCloseStopsAdmissionFailsPendingAndLetsActiveFinish(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	activeCanceled := make(chan error, 1)
	reconciler := reconcilerFunc[managerSpec, string](func(ctx context.Context, desired Desired[managerSpec]) (Result[string], error) {
		close(started)
		select {
		case <-release:
			return managerResult(desired, "done"), nil
		case <-ctx.Done():
			activeCanceled <- context.Cause(ctx)
			return Result[string]{}, context.Cause(ctx)
		}
	})
	manager, err := NewManager(reconciler, 1, cloneManagerSpec)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	active := manager.Submit(context.Background(), managerDesired("active", 1))
	receiveSignal(t, started, "active reconciliation")
	pending := manager.Submit(context.Background(), managerDesired("pending", 1))
	closed := make(chan error, 1)
	go func() { closed <- manager.Close(context.Background()) }()
	if _, err := awaitManagerFuture(t, pending); !errors.Is(err, concurrency.ErrClosed) {
		t.Fatalf("pending future error = %v, want ErrClosed", err)
	}
	if _, err := awaitManagerFuture(t, manager.Submit(context.Background(), managerDesired("new", 1))); !errors.Is(err, concurrency.ErrClosed) {
		t.Fatalf("post-close future error = %v, want ErrClosed", err)
	}
	select {
	case cause := <-activeCanceled:
		t.Fatalf("active work canceled during graceful close: %v", cause)
	default:
	}
	select {
	case err := <-closed:
		t.Fatalf("Close returned before active work finished: %v", err)
	default:
	}
	close(release)
	if _, err := awaitManagerFuture(t, active); err != nil {
		t.Fatalf("active future error = %v", err)
	}
	if err := receiveError(t, closed, "graceful close"); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestManagerCloseDeadlineCancelsActiveWorkAndResolvesEveryFuture(t *testing.T) {
	started := make(chan struct{})
	reconciler := reconcilerFunc[managerSpec, string](func(ctx context.Context, _ Desired[managerSpec]) (Result[string], error) {
		close(started)
		<-ctx.Done()
		return Result[string]{}, context.Cause(ctx)
	})
	manager, err := NewManager(reconciler, 1, cloneManagerSpec)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	active := manager.Submit(context.Background(), managerDesired("active", 1))
	receiveSignal(t, started, "active reconciliation")
	pending := manager.Submit(context.Background(), managerDesired("pending", 1))
	shutdown, cancelShutdown := context.WithCancelCause(context.Background())
	shutdownCause := errors.New("shutdown expired")
	cancelShutdown(shutdownCause)
	if err := manager.Close(shutdown); !errors.Is(err, shutdownCause) {
		t.Fatalf("Close() error = %v, want shutdown cause", err)
	}
	if _, err := awaitManagerFuture(t, active); !errors.Is(err, shutdownCause) {
		t.Fatalf("active future error = %v, want shutdown cause", err)
	}
	if _, err := awaitManagerFuture(t, pending); !errors.Is(err, concurrency.ErrClosed) {
		t.Fatalf("pending future error = %v, want ErrClosed", err)
	}
	if err := manager.Close(context.Background()); !errors.Is(err, shutdownCause) {
		t.Fatalf("second Close() error = %v, want original shutdown cause", err)
	}
}

func TestManagerSubmitContextOnlyControlsAdmission(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	reconciler := reconcilerFunc[managerSpec, string](func(ctx context.Context, desired Desired[managerSpec]) (Result[string], error) {
		close(started)
		select {
		case <-release:
			return managerResult(desired, "done"), nil
		case <-ctx.Done():
			return Result[string]{}, context.Cause(ctx)
		}
	})
	manager, err := NewManager(reconciler, 1, cloneManagerSpec)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := awaitManagerFuture(t, manager.Submit(canceled, managerDesired("rejected", 1))); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled admission error = %v, want context.Canceled", err)
	}
	admission, cancelAdmission := context.WithCancel(context.Background())
	accepted := manager.Submit(admission, managerDesired("accepted", 1))
	cancelAdmission()
	receiveSignal(t, started, "accepted reconciliation")
	close(release)
	if result, err := awaitManagerFuture(t, accepted); err != nil || result.Key != "accepted" {
		t.Fatalf("accepted future = (%#v, %v)", result, err)
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

type managerTimer struct {
	deadline time.Time
	channel  chan time.Time
}

type managerClock struct {
	mu     sync.Mutex
	start  time.Time
	now    time.Time
	timers []managerTimer
}

func newManagerClock(now time.Time) *managerClock {
	return &managerClock{start: now, now: now}
}

func (clock *managerClock) Start() time.Time {
	return clock.start
}

func (clock *managerClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *managerClock) After(delay time.Duration) <-chan time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	channel := make(chan time.Time, 1)
	clock.timers = append(clock.timers, managerTimer{deadline: clock.now.Add(delay), channel: channel})
	return channel
}

func (clock *managerClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(duration)
	now := clock.now
	pending := clock.timers[:0]
	for _, timer := range clock.timers {
		if timer.deadline.After(now) {
			pending = append(pending, timer)
			continue
		}
		timer.channel <- now
	}
	clock.timers = pending
	clock.mu.Unlock()
}

func receiveManagerCall[T any](t *testing.T, channel <-chan T) T {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for reconciliation call")
		var zero T
		return zero
	}
}

func receiveManagerDesired(t *testing.T, channel <-chan Desired[managerSpec]) Desired[managerSpec] {
	t.Helper()
	return receiveManagerCall(t, channel)
}

func receiveGeneration(t *testing.T, channel <-chan Generation) Generation {
	t.Helper()
	return receiveManagerCall(t, channel)
}

func receiveInt(t *testing.T, channel <-chan int, description string) int {
	t.Helper()
	select {
	case value := <-channel:
		return value
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
		return 0
	}
}

func receiveSignal(t *testing.T, channel <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-channel:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func receiveError(t *testing.T, channel <-chan error, description string) error {
	t.Helper()
	select {
	case err := <-channel:
		return err
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
		return nil
	}
}

func awaitManagerFuture[O any](t *testing.T, future *concurrency.Future[Result[O]]) (Result[O], error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return future.Await(ctx)
}

func waitForManagerPending[S, O any](t *testing.T, manager *Manager[S, O], count int) []Pending[S] {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		pending := manager.Pending()
		if len(pending) == count {
			return pending
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("Pending() never reached length %d", count)
	return nil
}

func sameKeys(left, right []Key) bool {
	slices.Sort(left)
	slices.Sort(right)
	return slices.Equal(left, right)
}

var _ ManagerClock = (*managerClock)(nil)
