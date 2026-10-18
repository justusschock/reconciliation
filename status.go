package reconciliation

import (
	"context"
	"sync"

	"github.com/justusschock/concurrency"
)

// StatusPhase identifies a resource-independent reconciliation phase.
type StatusPhase string

// Status phases. StatusQueued is published by a Manager; StatusDropped is
// synthesized by EventStream; the others are published by Engine.
const (
	StatusQueued      StatusPhase = "queued"
	StatusObserving   StatusPhase = "observing"
	StatusPlanning    StatusPhase = "planning"
	StatusApplying    StatusPhase = "applying"
	StatusRetrying    StatusPhase = "retrying"
	StatusRollingBack StatusPhase = "rolling-back"
	StatusSucceeded   StatusPhase = "succeeded"
	StatusFailed      StatusPhase = "failed"
	StatusDropped     StatusPhase = "dropped"
)

// StatusEvent reports one reconciliation phase boundary. Dropped is positive
// only for a StatusDropped marker and counts events omitted for one subscriber.
type StatusEvent struct {
	Key        Key
	Generation Generation
	Phase      StatusPhase
	Attempt    int
	ActionID   ActionID
	Err        error
	Dropped    uint64
}

// StatusSink receives reconciliation events. Publish implementations must not
// block reconciliation. EventStream provides a bounded non-blocking sink.
type StatusSink interface {
	Publish(StatusEvent)
}

type statusSinkContextKey struct{}

// WithStatusSink carries sink through one reconciliation call. It does not
// mutate Engine configuration, so one Engine may serve calls with distinct
// sinks concurrently. A nil or typed-nil sink is a no-op.
func WithStatusSink(ctx context.Context, sink StatusSink) context.Context {
	if isNilInterface(sink) {
		return ctx
	}
	return context.WithValue(ctx, statusSinkContextKey{}, sink)
}

func publishStatus(ctx context.Context, event StatusEvent) {
	sink, _ := ctx.Value(statusSinkContextKey{}).(StatusSink)
	if isNilInterface(sink) {
		return
	}
	sink.Publish(event)
}

// EventStream broadcasts status through independently bounded subscriber
// streams. A slow subscriber loses events without delaying publishers and
// eventually receives a StatusDropped count after it resumes consumption.
type EventStream struct {
	mu          sync.RWMutex
	capacity    int
	closed      bool
	subscribers map[*statusSubscriber]struct{}
}

// NewEventStream constructs a stream with the per-subscriber queue capacity.
func NewEventStream(capacity int) (*EventStream, error) {
	if capacity <= 0 {
		return nil, concurrency.ErrInvalidLimit
	}
	return &EventStream{
		capacity:    capacity,
		subscribers: make(map[*statusSubscriber]struct{}),
	}, nil
}

// Subscribe returns a new independently bounded event channel. The channel is
// closed when EventStream.Close is called.
func (stream *EventStream) Subscribe() <-chan StatusEvent {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.closed {
		closed := make(chan StatusEvent)
		close(closed)
		return closed
	}
	subscriber := newStatusSubscriber(stream.capacity)
	stream.subscribers[subscriber] = struct{}{}
	go subscriber.run()
	return subscriber.output
}

// Publish offers event to every current subscriber without waiting for a
// subscriber to consume it. Publish after Close is a no-op.
func (stream *EventStream) Publish(event StatusEvent) {
	stream.mu.RLock()
	defer stream.mu.RUnlock()
	if stream.closed {
		return
	}
	for subscriber := range stream.subscribers {
		subscriber.enqueue(event)
	}
}

// Close idempotently closes every subscriber stream. Concurrent Publish and
// Close calls are safe.
func (stream *EventStream) Close() {
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if stream.closed {
		return
	}
	stream.closed = true
	for subscriber := range stream.subscribers {
		subscriber.close()
	}
	clear(stream.subscribers)
}

type statusSubscriber struct {
	capacity  int
	output    chan StatusEvent
	wake      chan struct{}
	done      chan struct{}
	closeOnce sync.Once

	mu      sync.Mutex
	queue   []StatusEvent
	dropped uint64
}

func newStatusSubscriber(capacity int) *statusSubscriber {
	return &statusSubscriber{
		capacity: capacity,
		output:   make(chan StatusEvent, capacity),
		wake:     make(chan struct{}, 1),
		done:     make(chan struct{}),
		queue:    make([]StatusEvent, 0, capacity),
	}
}

func (subscriber *statusSubscriber) enqueue(event StatusEvent) {
	subscriber.mu.Lock()
	if subscriber.dropped > 0 {
		subscriber.dropped++
	} else if len(subscriber.queue) == subscriber.capacity {
		subscriber.dropped = 1
	} else {
		subscriber.queue = append(subscriber.queue, event)
	}
	subscriber.mu.Unlock()
	select {
	case subscriber.wake <- struct{}{}:
	default:
	}
}

func (subscriber *statusSubscriber) run() {
	defer close(subscriber.output)
	for {
		select {
		case <-subscriber.done:
			return
		case <-subscriber.wake:
		}
		for {
			event, available := subscriber.next()
			if !available {
				break
			}
			select {
			case <-subscriber.done:
				return
			case subscriber.output <- event:
			}
		}
	}
}

func (subscriber *statusSubscriber) next() (StatusEvent, bool) {
	subscriber.mu.Lock()
	defer subscriber.mu.Unlock()
	if len(subscriber.queue) > 0 {
		event := subscriber.queue[0]
		copy(subscriber.queue, subscriber.queue[1:])
		var zero StatusEvent
		subscriber.queue[len(subscriber.queue)-1] = zero
		subscriber.queue = subscriber.queue[:len(subscriber.queue)-1]
		return event, true
	}
	if subscriber.dropped > 0 {
		event := StatusEvent{Phase: StatusDropped, Dropped: subscriber.dropped}
		subscriber.dropped = 0
		return event, true
	}
	return StatusEvent{}, false
}

func (subscriber *statusSubscriber) close() {
	subscriber.closeOnce.Do(func() { close(subscriber.done) })
}

var _ StatusSink = (*EventStream)(nil)
