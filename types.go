package reconciliation

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	ErrInvalidKey        = errors.New("reconciliation: invalid key")
	ErrInvalidGeneration = errors.New("reconciliation: invalid generation")
	ErrInvalidDesired    = errors.New("reconciliation: invalid desired state")
	ErrInvalidCondition  = errors.New("reconciliation: invalid condition")
	ErrInvalidResult     = errors.New("reconciliation: invalid result")
)

// Key identifies one independently reconciled value.
type Key string

func (key Key) Validate() error {
	if key == "" {
		return fmt.Errorf("%w: empty value", ErrInvalidKey)
	}
	return nil
}

// Generation identifies a positive revision of desired state.
type Generation uint64

func (generation Generation) Validate() error {
	if generation == 0 {
		return fmt.Errorf("%w: zero value", ErrInvalidGeneration)
	}
	return nil
}

// Desired holds a desired specification. Spec ownership is shallow.
type Desired[S any] struct {
	Key        Key
	Generation Generation
	Spec       S
}

func (desired Desired[S]) Validate() error {
	if err := desired.Key.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidDesired, err)
	}
	if err := desired.Generation.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidDesired, err)
	}
	return nil
}

// ConditionStatus is the tri-state value of a condition.
type ConditionStatus string

const (
	ConditionTrue    ConditionStatus = "true"
	ConditionFalse   ConditionStatus = "false"
	ConditionUnknown ConditionStatus = "unknown"
)

// Condition reports one generic aspect of reconciliation state.
type Condition struct {
	Type               string
	Status             ConditionStatus
	Reason             string
	Message            string
	LastTransitionTime time.Time
}

func (condition Condition) Validate() error {
	if condition.Type == "" {
		return fmt.Errorf("%w: empty type", ErrInvalidCondition)
	}
	switch condition.Status {
	case ConditionTrue, ConditionFalse, ConditionUnknown:
	default:
		return fmt.Errorf("%w: unknown status %q", ErrInvalidCondition, condition.Status)
	}
	if !utf8.ValidString(condition.Type) || strings.ContainsAny(condition.Type, "\x00\r\n") {
		return fmt.Errorf("%w: malformed type", ErrInvalidCondition)
	}
	if !utf8.ValidString(condition.Reason) || strings.ContainsAny(condition.Reason, "\x00\r\n") {
		return fmt.Errorf("%w: malformed reason", ErrInvalidCondition)
	}
	if !utf8.ValidString(condition.Message) || strings.ContainsRune(condition.Message, '\x00') {
		return fmt.Errorf("%w: malformed message", ErrInvalidCondition)
	}
	return nil
}

// Result holds observed reconciliation output. Observed ownership is shallow.
type Result[O any] struct {
	Key          Key
	Generation   Generation
	Observed     O
	Changed      bool
	Conditions   []Condition
	RequeueAfter time.Duration
}

func (result Result[O]) Validate() error {
	if err := result.Key.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidResult, err)
	}
	if err := result.Generation.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidResult, err)
	}
	if result.RequeueAfter < 0 {
		return fmt.Errorf("%w: negative requeue delay", ErrInvalidResult)
	}
	for index, condition := range result.Conditions {
		if err := condition.Validate(); err != nil {
			return fmt.Errorf("%w: condition %d: %w", ErrInvalidResult, index, err)
		}
	}
	return nil
}

// Clone returns a shallow value copy with independent Conditions storage.
func (result Result[O]) Clone() Result[O] {
	result.Conditions = slices.Clone(result.Conditions)
	return result
}
