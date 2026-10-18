package reconciliation

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	// ErrInvalidPolicy is wrapped by Policy validation failures.
	ErrInvalidPolicy = errors.New("reconciliation: invalid policy")
	// ErrPermanent marks errors wrapped with Permanent; they are never retried.
	ErrPermanent = errors.New("reconciliation: permanent error")
	// ErrTransient marks errors wrapped with Transient; they may be retried.
	ErrTransient = errors.New("reconciliation: transient error")
	// ErrConflict marks errors wrapped with Conflict; they may be retried.
	ErrConflict = errors.New("reconciliation: conflict error")
)

// DefaultRollbackTimeout bounds best-effort cleanup when Policy does not set
// RollbackTimeout or AttemptTimeout.
const DefaultRollbackTimeout = 30 * time.Second

// Policy bounds retry attempts, timeouts, and backoff. MaxAttempts counts
// the first attempt and must be positive. The delay after failed attempt n is
// BaseDelay doubled n-1 times, capped at MaxDelay, then passed through Jitter
// and clamped to [0, MaxDelay]. Zero AttemptTimeout means no per-attempt
// deadline; RollbackTimeout falls back to AttemptTimeout and then to
// DefaultRollbackTimeout. A Policy is a value and safe for concurrent use if
// Jitter is.
type Policy struct {
	MaxAttempts     int
	BaseDelay       time.Duration
	MaxDelay        time.Duration
	AttemptTimeout  time.Duration
	RollbackTimeout time.Duration
	// Jitter may be called concurrently by Reconcile calls on the same Engine.
	Jitter func(time.Duration) time.Duration
}

// Decision reports whether another attempt should run and after what delay.
type Decision struct {
	Retry bool
	Delay time.Duration
}

// Validate reports an error wrapping ErrInvalidPolicy if MaxAttempts is not
// positive, a duration is negative, or MaxDelay is below BaseDelay.
func (policy Policy) Validate() error {
	if policy.MaxAttempts <= 0 {
		return fmt.Errorf("%w: max attempts must be positive", ErrInvalidPolicy)
	}
	if policy.BaseDelay < 0 {
		return fmt.Errorf("%w: negative base delay", ErrInvalidPolicy)
	}
	if policy.MaxDelay < 0 {
		return fmt.Errorf("%w: negative max delay", ErrInvalidPolicy)
	}
	if policy.AttemptTimeout < 0 {
		return fmt.Errorf("%w: negative attempt timeout", ErrInvalidPolicy)
	}
	if policy.RollbackTimeout < 0 {
		return fmt.Errorf("%w: negative rollback timeout", ErrInvalidPolicy)
	}
	if policy.MaxDelay < policy.BaseDelay {
		return fmt.Errorf("%w: max delay is below base delay", ErrInvalidPolicy)
	}
	return nil
}

func (policy Policy) rollbackTimeout() time.Duration {
	if policy.RollbackTimeout > 0 {
		return policy.RollbackTimeout
	}
	if policy.AttemptTimeout > 0 {
		return policy.AttemptTimeout
	}
	return DefaultRollbackTimeout
}

// Decide reports whether attempt (one-based) failing with err should be
// retried and after what delay. Only errors matching ErrTransient or
// ErrConflict are retried, and only while attempts remain; context
// cancellation and deadline errors and ErrPermanent always stop. An invalid
// policy never retries.
func (policy Policy) Decide(attempt int, err error) Decision {
	if err == nil || attempt < 1 || attempt >= policy.MaxAttempts || policy.Validate() != nil {
		return Decision{}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrPermanent) {
		return Decision{}
	}
	if !errors.Is(err, ErrTransient) && !errors.Is(err, ErrConflict) {
		return Decision{}
	}
	return Decision{Retry: true, Delay: policy.retryDelay(attempt)}
}

func (policy Policy) retryDelay(attempt int) time.Duration {
	delay := policy.BaseDelay
	if delay > 0 {
		for exponent := 1; exponent < attempt && delay < policy.MaxDelay; exponent++ {
			if delay > policy.MaxDelay/2 {
				delay = policy.MaxDelay
				break
			}
			delay *= 2
		}
	}
	if delay > policy.MaxDelay {
		delay = policy.MaxDelay
	}
	if policy.Jitter != nil {
		delay = policy.Jitter(delay)
	}
	if delay < 0 {
		return 0
	}
	if delay > policy.MaxDelay {
		return policy.MaxDelay
	}
	return delay
}

type classifiedError struct {
	category error
	cause    error
}

func (err *classifiedError) Error() string {
	return err.cause.Error()
}

func (err *classifiedError) Unwrap() error {
	return err.cause
}

func (err *classifiedError) Is(target error) bool {
	return target == err.category
}

// Permanent marks err as not retryable. The result matches both err and
// ErrPermanent with errors.Is and has err's message. Permanent(nil) is nil.
func Permanent(err error) error {
	return classify(ErrPermanent, err)
}

// Transient marks err as retryable, for example a temporary outage. The
// result matches both err and ErrTransient with errors.Is and has err's
// message. Transient(nil) is nil.
func Transient(err error) error {
	return classify(ErrTransient, err)
}

// Conflict marks err as retryable because the observed state changed
// concurrently. The result matches both err and ErrConflict with errors.Is
// and has err's message. Conflict(nil) is nil.
func Conflict(err error) error {
	return classify(ErrConflict, err)
}

func classify(category, cause error) error {
	if cause == nil {
		return nil
	}
	if errors.Is(cause, category) {
		return cause
	}
	return &classifiedError{category: category, cause: cause}
}
