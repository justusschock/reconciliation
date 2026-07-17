package reconciliation

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrInvalidPolicy = errors.New("reconciliation: invalid policy")
	ErrPermanent     = errors.New("reconciliation: permanent error")
	ErrTransient     = errors.New("reconciliation: transient error")
	ErrConflict      = errors.New("reconciliation: conflict error")
)

// Policy bounds retry attempts, timeouts, and backoff.
type Policy struct {
	MaxAttempts    int
	BaseDelay      time.Duration
	MaxDelay       time.Duration
	AttemptTimeout time.Duration
	Jitter         func(time.Duration) time.Duration
}

// Decision reports whether another attempt should run and after what delay.
type Decision struct {
	Retry bool
	Delay time.Duration
}

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
	if policy.MaxDelay < policy.BaseDelay {
		return fmt.Errorf("%w: max delay is below base delay", ErrInvalidPolicy)
	}
	return nil
}

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

func Permanent(err error) error {
	return classify(ErrPermanent, err)
}

func Transient(err error) error {
	return classify(ErrTransient, err)
}

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
