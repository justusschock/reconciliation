package reconciliation

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// The tests in this file run inside synctest bubbles so that any use of the
// standard library timers instead of the supplied clock shows up as elapsed
// virtual time.

// FR-RECON-20
func TestEngineHonoursSuppliedClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wantTransient := Transient(errors.New("temporary observe failure"))
		observations := 0
		adapter := &engineAdapter{
			observe: func(context.Context, Key) (engineObserved, error) {
				observations++
				if observations <= 2 {
					return 0, wantTransient
				}
				return 1, nil
			},
			plan: func(context.Context, Desired[engineSpec], engineObserved) (Plan[engineAction], error) {
				return Plan[engineAction]{}, nil
			},
			apply: unexpectedApply(t),
			undo:  unexpectedUndo(t),
		}
		clock := &recordingClock{}
		policy := Policy{MaxAttempts: 3, BaseDelay: time.Hour, MaxDelay: 4 * time.Hour}
		start := time.Now()

		result, err := NewEngine(adapter, policy, WithClock(clock)).Reconcile(context.Background(), engineDesired())
		if err != nil {
			t.Fatalf("Reconcile() error = %v", err)
		}
		if result.Observed != 1 || observations != 4 {
			t.Fatalf("Reconcile() = %#v after %d observations, want Observed=1 after 4", result, observations)
		}
		if got, want := clock.Delays(), []time.Duration{time.Hour, 2 * time.Hour}; !slices.Equal(got, want) {
			t.Fatalf("clock delays = %v, want %v", got, want)
		}
		if elapsed := time.Since(start); elapsed != 0 {
			t.Fatalf("Reconcile() waited %v of virtual time, want 0 with a supplied clock", elapsed)
		}
	})
}

// FR-RECON-20
func TestEngineReportsNilClockChannel(t *testing.T) {
	adapter := &engineAdapter{
		observe: func(context.Context, Key) (engineObserved, error) {
			return 0, Transient(errors.New("temporary observe failure"))
		},
		plan: func(context.Context, Desired[engineSpec], engineObserved) (Plan[engineAction], error) {
			return Plan[engineAction]{}, nil
		},
		apply: unexpectedApply(t),
		undo:  unexpectedUndo(t),
	}
	policy := Policy{MaxAttempts: 2, BaseDelay: time.Second, MaxDelay: time.Second}
	clock := &recordingClock{nilChannel: true}

	_, err := NewEngine(adapter, policy, WithClock(clock)).Reconcile(context.Background(), engineDesired())
	if !errors.Is(err, ErrNilClock) {
		t.Fatalf("Reconcile() error = %v, want ErrNilClock", err)
	}
}

// FR-RECON-20
func TestManagerHonoursSuppliedClock(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clock := newFakeManagerClock(time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC))
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
		start := time.Now()
		future := manager.Submit(context.Background(), managerDesired("target", 1))
		if call := receiveInt(t, calls, "first reconciliation"); call != 1 {
			t.Fatalf("first call = %d", call)
		}
		pending := waitForManagerPending(t, manager, 1)
		if want := clock.Start().Add(10 * time.Second); !pending[0].RetryAt.Equal(want) {
			t.Fatalf("RetryAt = %v, want %v from the supplied clock", pending[0].RetryAt, want)
		}

		clock.Advance(9 * time.Second)
		synctest.Wait()
		if got := count.Load(); got != 1 {
			t.Fatalf("calls before the supplied clock's deadline = %d, want 1", got)
		}
		clock.Advance(time.Second)
		if call := receiveInt(t, calls, "requeued reconciliation"); call != 2 {
			t.Fatalf("second call = %d", call)
		}
		result, err := awaitManagerFuture(t, future)
		if err != nil || result.Observed != "final" {
			t.Fatalf("future = (%#v, %v), want final completed run", result, err)
		}
		if elapsed := time.Since(start); elapsed != 0 {
			t.Fatalf("requeue waited %v of virtual time, want 0 with a supplied clock", elapsed)
		}
		if err := manager.Close(context.Background()); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
}

// FR-RECON-20
func TestManagerReportsNilClockChannel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clock := newFakeManagerClock(time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC))
		clock.nilChannel = true
		reconciler := reconcilerFunc[managerSpec, string](func(_ context.Context, desired Desired[managerSpec]) (Result[string], error) {
			result := managerResult(desired, "requeue")
			result.RequeueAfter = time.Second
			return result, nil
		})
		manager, err := NewManager(reconciler, 1, cloneManagerSpec, WithManagerClock(clock))
		if err != nil {
			t.Fatalf("NewManager() error = %v", err)
		}
		future := manager.Submit(context.Background(), managerDesired("target", 1))
		if _, err := awaitManagerFuture(t, future); !errors.Is(err, ErrNilClock) {
			t.Fatalf("future error = %v, want ErrNilClock", err)
		}
		if pending := manager.Pending(); len(pending) != 0 {
			t.Fatalf("Pending() = %#v, want no scheduled requeue", pending)
		}
		if err := manager.Close(context.Background()); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
}

// recordingClock records requested delays and returns channels that are
// already ready, or nil channels when nilChannel is set.
type recordingClock struct {
	mu         sync.Mutex
	delays     []time.Duration
	nilChannel bool
}

func (clock *recordingClock) After(delay time.Duration) <-chan time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.delays = append(clock.delays, delay)
	if clock.nilChannel {
		return nil
	}
	channel := make(chan time.Time, 1)
	channel <- time.Time{}
	return channel
}

func (clock *recordingClock) Delays() []time.Duration {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return slices.Clone(clock.delays)
}

var _ Clock = (*recordingClock)(nil)

// fakeManagerClock is a manually advanced ManagerClock.
type fakeManagerClock struct {
	mu         sync.Mutex
	start      time.Time
	now        time.Time
	timers     []fakeManagerTimer
	nilChannel bool
}

type fakeManagerTimer struct {
	deadline time.Time
	channel  chan time.Time
}

func newFakeManagerClock(now time.Time) *fakeManagerClock {
	return &fakeManagerClock{start: now, now: now}
}

func (clock *fakeManagerClock) Start() time.Time {
	return clock.start
}

func (clock *fakeManagerClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *fakeManagerClock) After(delay time.Duration) <-chan time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	if clock.nilChannel {
		return nil
	}
	channel := make(chan time.Time, 1)
	clock.timers = append(clock.timers, fakeManagerTimer{deadline: clock.now.Add(delay), channel: channel})
	return channel
}

func (clock *fakeManagerClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.now = clock.now.Add(duration)
	pending := clock.timers[:0]
	for _, timer := range clock.timers {
		if timer.deadline.After(clock.now) {
			pending = append(pending, timer)
			continue
		}
		timer.channel <- clock.now
	}
	clock.timers = pending
}

var _ ManagerClock = (*fakeManagerClock)(nil)
