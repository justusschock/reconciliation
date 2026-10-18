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

	"github.com/justusschock/concurrency"
)

type recordingStatusSink struct {
	mu     sync.Mutex
	events []StatusEvent
}

func (sink *recordingStatusSink) Publish(event StatusEvent) {
	sink.mu.Lock()
	sink.events = append(sink.events, event)
	sink.mu.Unlock()
}

func (sink *recordingStatusSink) Events() []StatusEvent {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return slices.Clone(sink.events)
}

// FR-RECON-15
func TestEnginePublishesContextCarriedStatusPhases(t *testing.T) {
	sink := &recordingStatusSink{}
	observations := 0
	adapter := &engineAdapter{
		observe: func(context.Context, Key) (engineObserved, error) {
			observations++
			return engineObserved(observations), nil
		},
		plan: func(context.Context, Desired[engineSpec], engineObserved) (Plan[engineAction], error) {
			return Plan[engineAction]{Actions: []Action[engineAction]{{ID: "apply", Reversible: true}}}, nil
		},
		apply: func(context.Context, Action[engineAction]) (Undo[engineUndo], error) {
			return Undo[engineUndo]{Value: "undo"}, nil
		},
		undo: unexpectedUndo(t),
	}
	ctx := WithStatusSink(context.Background(), sink)

	if _, err := NewEngine(adapter, enginePolicy(1)).Reconcile(ctx, engineDesired()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	events := sink.Events()
	wantPhases := []StatusPhase{StatusObserving, StatusPlanning, StatusApplying, StatusObserving, StatusSucceeded}
	if got := statusPhases(events); !slices.Equal(got, wantPhases) {
		t.Fatalf("status phases = %v, want %v", got, wantPhases)
	}
	if events[2].Attempt != 1 || events[2].ActionID != "apply" {
		t.Fatalf("applying event = %#v, want attempt 1 action apply", events[2])
	}
	for _, event := range events {
		if event.Key != "resource" || event.Generation != 1 {
			t.Fatalf("event identity = %#v", event)
		}
	}
}

// FR-RECON-15
func TestEnginePublishesRetryingBeforeAnotherAttempt(t *testing.T) {
	sink := &recordingStatusSink{}
	wantErr := errors.New("observe transiently unavailable")
	observations := 0
	adapter := &engineAdapter{
		observe: func(context.Context, Key) (engineObserved, error) {
			observations++
			if observations == 1 {
				return 0, Transient(wantErr)
			}
			return engineObserved(observations), nil
		},
		plan: func(context.Context, Desired[engineSpec], engineObserved) (Plan[engineAction], error) {
			return Plan[engineAction]{}, nil
		},
		apply: unexpectedApply(t),
		undo:  unexpectedUndo(t),
	}
	ctx := WithStatusSink(context.Background(), sink)

	if _, err := NewEngine(adapter, enginePolicy(2)).Reconcile(ctx, engineDesired()); err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	events := sink.Events()
	wantPhases := []StatusPhase{
		StatusObserving,
		StatusRetrying,
		StatusObserving,
		StatusPlanning,
		StatusObserving,
		StatusSucceeded,
	}
	if got := statusPhases(events); !slices.Equal(got, wantPhases) {
		t.Fatalf("status phases = %v, want %v", got, wantPhases)
	}
	if events[1].Attempt != 1 || !errors.Is(events[1].Err, wantErr) {
		t.Fatalf("retrying event = %#v, want attempt 1 transient error", events[1])
	}
}

// FR-RECON-15
func TestEnginePublishesRollbackAndFailure(t *testing.T) {
	sink := &recordingStatusSink{}
	wantErr := errors.New("apply failed")
	adapter := &engineAdapter{
		observe: constantObservation(1),
		plan: func(context.Context, Desired[engineSpec], engineObserved) (Plan[engineAction], error) {
			return Plan[engineAction]{Actions: []Action[engineAction]{
				{ID: "first", Reversible: true},
				{ID: "second", DependsOn: []ActionID{"first"}, Reversible: true},
			}}, nil
		},
		apply: func(_ context.Context, action Action[engineAction]) (Undo[engineUndo], error) {
			if action.ID == "second" {
				return Undo[engineUndo]{}, wantErr
			}
			return Undo[engineUndo]{Value: "first"}, nil
		},
		undo: func(context.Context, Undo[engineUndo]) error { return nil },
	}
	ctx := WithStatusSink(context.Background(), sink)

	if _, err := NewEngine(adapter, enginePolicy(1)).Reconcile(ctx, engineDesired()); !errors.Is(err, wantErr) {
		t.Fatalf("Reconcile() error = %v, want apply failure", err)
	}
	events := sink.Events()
	wantPhases := []StatusPhase{
		StatusObserving,
		StatusPlanning,
		StatusApplying,
		StatusApplying,
		StatusRollingBack,
		StatusFailed,
	}
	if got := statusPhases(events); !slices.Equal(got, wantPhases) {
		t.Fatalf("status phases = %v, want %v", got, wantPhases)
	}
	if !errors.Is(events[len(events)-1].Err, wantErr) {
		t.Fatalf("failed event error = %v, want apply failure", events[len(events)-1].Err)
	}
}

// FR-RECON-15
func TestManagerPublishesQueuedBeforeEnginePhases(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := &recordingStatusSink{}
		adapter := &engineAdapter{
			observe: constantObservation(1),
			plan: func(context.Context, Desired[engineSpec], engineObserved) (Plan[engineAction], error) {
				return Plan[engineAction]{}, nil
			},
			apply: unexpectedApply(t),
			undo:  unexpectedUndo(t),
		}
		manager, err := NewManager[engineSpec, engineObserved](
			NewEngine(adapter, enginePolicy(1)),
			1,
			func(spec engineSpec) engineSpec { return spec },
			WithManagerStatusSink(sink),
		)
		if err != nil {
			t.Fatalf("NewManager() error = %v", err)
		}

		future := manager.Submit(context.Background(), engineDesired())
		if _, err := awaitManagerFuture(t, future); err != nil {
			t.Fatalf("future error = %v", err)
		}
		if err := manager.Close(context.Background()); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		want := []StatusPhase{StatusQueued, StatusObserving, StatusPlanning, StatusObserving, StatusSucceeded}
		if got := statusPhases(sink.Events()); !slices.Equal(got, want) {
			t.Fatalf("status phases = %v, want %v", got, want)
		}
	})
}

// FR-RECON-15
func TestManagerDoesNotStartBeforeInitialQueuedPublicationCompletes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := &gatedQueuedSink{entered: make(chan struct{}), release: make(chan struct{})}
		reconciler := reconcilerFunc[managerSpec, string](func(_ context.Context, desired Desired[managerSpec]) (Result[string], error) {
			return managerResult(desired, "done"), nil
		})
		manager, err := NewManager(reconciler, 1, cloneManagerSpec, WithManagerStatusSink(sink))
		if err != nil {
			t.Fatalf("NewManager() error = %v", err)
		}
		firstSubmitted := make(chan *concurrency.Future[Result[string]], 1)
		go func() {
			firstSubmitted <- manager.Submit(context.Background(), managerDesired("target", 1))
		}()
		receiveSignal(t, sink.entered, "initial queued publication")
		second := manager.Submit(context.Background(), managerDesired("target", 1))
		if pending := manager.Pending(); len(pending) != 1 {
			t.Fatalf("Pending() length = %d, want work held until initial queued publication completes", len(pending))
		}
		close(sink.release)
		first := receiveManagerCall(t, firstSubmitted)
		for name, future := range map[string]*concurrency.Future[Result[string]]{"first": first, "second": second} {
			if _, err := awaitManagerFuture(t, future); err != nil {
				t.Fatalf("%s future error = %v", name, err)
			}
		}
		if err := manager.Close(context.Background()); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	})
}

type gatedQueuedSink struct {
	entered chan struct{}
	release chan struct{}
	claimed atomic.Bool
}

func (sink *gatedQueuedSink) Publish(event StatusEvent) {
	if event.Phase != StatusQueued {
		return
	}
	if sink.claimed.CompareAndSwap(false, true) {
		close(sink.entered)
		<-sink.release
	}
}

// FR-RECON-16
func TestEventStreamPublishesWithoutBlockingAndMarksDroppedEvents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stream, err := NewEventStream(1)
		if err != nil {
			t.Fatalf("NewEventStream() error = %v", err)
		}
		events := stream.Subscribe()
		published := make(chan struct{})
		go func() {
			for generation := Generation(1); generation <= 100; generation++ {
				stream.Publish(StatusEvent{Key: "resource", Generation: generation, Phase: StatusQueued})
			}
			close(published)
		}()
		receiveSignal(t, published, "non-blocking status publication")

		foundMarker := false
		deadline := time.After(5 * time.Second)
		for !foundMarker {
			select {
			case event := <-events:
				if event.Phase == StatusDropped {
					if event.Dropped == 0 {
						t.Fatalf("drop marker = %#v, want positive count", event)
					}
					foundMarker = true
				}
			case <-deadline:
				t.Fatal("slow subscriber never received a drop marker")
			}
		}
		stream.Close()
		stream.Close()
		waitForStatusStreamClose(t, events)
	})
}

// FR-RECON-16
func TestEventStreamKeepsSubscribersIndependent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stream, err := NewEventStream(2)
		if err != nil {
			t.Fatalf("NewEventStream() error = %v", err)
		}
		first := stream.Subscribe()
		second := stream.Subscribe()
		event := StatusEvent{Key: "resource", Generation: 1, Phase: StatusQueued}
		stream.Publish(event)
		if got := receiveStatusEvent(t, first); got != event {
			t.Fatalf("first subscriber event = %#v", got)
		}
		if got := receiveStatusEvent(t, second); got != event {
			t.Fatalf("second subscriber event = %#v", got)
		}
		stream.Close()
		waitForStatusStreamClose(t, first)
		waitForStatusStreamClose(t, second)
	})
}

// FR-RECON-16
func TestEventStreamConcurrentPublishAndCloseIsSafe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		stream, err := NewEventStream(4)
		if err != nil {
			t.Fatalf("NewEventStream() error = %v", err)
		}
		events := stream.Subscribe()
		var publishers sync.WaitGroup
		for publisher := 0; publisher < 8; publisher++ {
			publishers.Add(1)
			go func() {
				defer publishers.Done()
				for generation := Generation(1); generation <= 100; generation++ {
					stream.Publish(StatusEvent{Key: "resource", Generation: generation, Phase: StatusApplying})
				}
			}()
		}
		stream.Close()
		stream.Close()
		publishers.Wait()
		waitForStatusStreamClose(t, events)
		afterClose := stream.Subscribe()
		waitForStatusStreamClose(t, afterClose)
	})
}

// FR-RECON-16
func TestEventStreamRejectsUnboundedCapacity(t *testing.T) {
	if _, err := NewEventStream(0); !errors.Is(err, concurrency.ErrInvalidLimit) {
		t.Fatalf("NewEventStream() error = %v, want ErrInvalidLimit", err)
	}
}

func statusPhases(events []StatusEvent) []StatusPhase {
	phases := make([]StatusPhase, len(events))
	for index, event := range events {
		phases[index] = event.Phase
	}
	return phases
}

func receiveStatusEvent(t *testing.T, events <-chan StatusEvent) StatusEvent {
	t.Helper()
	select {
	case event, open := <-events:
		if !open {
			t.Fatal("status stream closed before event")
		}
		return event
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for status event")
		return StatusEvent{}
	}
}

func waitForStatusStreamClose(t *testing.T, events <-chan StatusEvent) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case _, open := <-events:
			if !open {
				return
			}
		case <-deadline:
			t.Fatal("status stream did not close")
		}
	}
}

var _ StatusSink = (*recordingStatusSink)(nil)
