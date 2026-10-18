package reconciliation

import (
	"context"
	"errors"
	"testing"
	"time"
)

// FR-RECON-07
func TestPolicyValidation(t *testing.T) {
	valid := Policy{MaxAttempts: 3, BaseDelay: time.Second, MaxDelay: 2 * time.Second, AttemptTimeout: time.Minute}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Policy.Validate() = %v, want nil", err)
	}
	if err := (Policy{MaxAttempts: 1}).Validate(); err != nil {
		t.Fatalf("zero-duration Policy.Validate() = %v, want nil", err)
	}

	tests := []struct {
		name   string
		policy Policy
	}{
		{name: "zero attempts", policy: Policy{}},
		{name: "negative attempts", policy: Policy{MaxAttempts: -1}},
		{name: "negative base delay", policy: Policy{MaxAttempts: 1, BaseDelay: -time.Nanosecond}},
		{name: "negative max delay", policy: Policy{MaxAttempts: 1, MaxDelay: -time.Nanosecond}},
		{name: "negative attempt timeout", policy: Policy{MaxAttempts: 1, AttemptTimeout: -time.Nanosecond}},
		{name: "negative rollback timeout", policy: Policy{MaxAttempts: 1, RollbackTimeout: -time.Nanosecond}},
		{name: "max below base", policy: Policy{MaxAttempts: 1, BaseDelay: 2 * time.Second, MaxDelay: time.Second}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.policy.Validate(); !errors.Is(err, ErrInvalidPolicy) {
				t.Fatalf("Policy.Validate() = %v, want %v", err, ErrInvalidPolicy)
			}
		})
	}
}

// FR-RECON-06
func TestClassificationWrappersPreserveIdentity(t *testing.T) {
	cause := errors.New("cause")
	tests := []struct {
		name     string
		wrap     func(error) error
		category error
	}{
		{name: "permanent", wrap: Permanent, category: ErrPermanent},
		{name: "transient", wrap: Transient, category: ErrTransient},
		{name: "conflict", wrap: Conflict, category: ErrConflict},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.wrap(cause)
			if !errors.Is(err, cause) || !errors.Is(err, test.category) {
				t.Fatalf("wrapped error = %v, want identities %v and %v", err, cause, test.category)
			}
			if err.Error() != cause.Error() {
				t.Fatalf("wrapped error text = %q, want %q", err, cause)
			}
			if test.wrap(nil) != nil {
				t.Fatal("classification wrapper changed nil into an error")
			}
		})
	}
}

// FR-RECON-06
func TestPolicyRetriesOnlyTransientAndConflictErrors(t *testing.T) {
	policy := Policy{MaxAttempts: 3, BaseDelay: time.Second, MaxDelay: 4 * time.Second}
	tests := []struct {
		name string
		err  error
	}{
		{name: "nil"},
		{name: "plain", err: errors.New("plain")},
		{name: "permanent", err: Permanent(errors.New("invalid"))},
		{name: "canceled", err: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded},
		{name: "classified canceled", err: Transient(context.Canceled)},
		{name: "classified deadline", err: Conflict(context.DeadlineExceeded)},
		{name: "permanent wins over transient", err: Permanent(Transient(errors.New("invalid")))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := policy.Decide(1, test.err); got != (Decision{}) {
				t.Fatalf("Policy.Decide() = %#v, want no retry", got)
			}
		})
	}

	for _, err := range []error{Transient(errors.New("temporary")), Conflict(errors.New("changed"))} {
		if got := policy.Decide(1, err); !got.Retry || got.Delay != time.Second {
			t.Fatalf("Policy.Decide(1) = %#v, want retry after %v", got, time.Second)
		}
		if got := policy.Decide(2, err); !got.Retry || got.Delay != 2*time.Second {
			t.Fatalf("Policy.Decide(2) = %#v, want retry after %v", got, 2*time.Second)
		}
		if got := policy.Decide(3, err); got != (Decision{}) {
			t.Fatalf("Policy.Decide(3) = %#v, want exhausted policy", got)
		}
	}
}

// FR-RECON-07
func TestPolicyRequiresOneBasedAttemptAndValidPolicy(t *testing.T) {
	retryable := Transient(errors.New("temporary"))
	policy := Policy{MaxAttempts: 3, MaxDelay: time.Second}
	for _, attempt := range []int{-1, 0} {
		if got := policy.Decide(attempt, retryable); got != (Decision{}) {
			t.Fatalf("Policy.Decide(%d) = %#v, want no retry", attempt, got)
		}
	}
	if got := (Policy{}).Decide(1, retryable); got != (Decision{}) {
		t.Fatalf("invalid Policy.Decide() = %#v, want no retry", got)
	}
}

// FR-RECON-07
func TestPolicyExponentialDelayCapsAndSaturates(t *testing.T) {
	retryable := Transient(errors.New("temporary"))
	policy := Policy{MaxAttempts: 10, BaseDelay: 10 * time.Millisecond, MaxDelay: 100 * time.Millisecond}
	for attempt, want := range []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond, 100 * time.Millisecond, 100 * time.Millisecond} {
		if got := policy.Decide(attempt+1, retryable); got != (Decision{Retry: true, Delay: want}) {
			t.Fatalf("Policy.Decide(%d) = %#v, want delay %v", attempt+1, got, want)
		}
	}

	maximum := time.Duration(1<<63 - 1)
	overflowPolicy := Policy{MaxAttempts: int(^uint(0) >> 1), BaseDelay: maximum/2 + 1, MaxDelay: maximum}
	if got := overflowPolicy.Decide(2, retryable); got.Delay != maximum {
		t.Fatalf("overflow Policy.Decide() = %#v, want saturated delay %v", got, maximum)
	}
	if got := (Policy{MaxAttempts: int(^uint(0) >> 1), BaseDelay: 1, MaxDelay: maximum}).Decide(int(^uint(0)>>1)-1, retryable); got.Delay != maximum {
		t.Fatalf("large-attempt Policy.Decide() = %#v, want saturated delay %v", got, maximum)
	}
}

// FR-RECON-07
func TestPolicyAppliesJitterAfterCapAndClampsResult(t *testing.T) {
	retryable := Conflict(errors.New("changed"))
	t.Run("after cap", func(t *testing.T) {
		var input time.Duration
		policy := Policy{
			MaxAttempts: 3,
			BaseDelay:   80 * time.Millisecond,
			MaxDelay:    100 * time.Millisecond,
			Jitter: func(delay time.Duration) time.Duration {
				input = delay
				return delay / 2
			},
		}
		got := policy.Decide(2, retryable)
		if input != 100*time.Millisecond || got != (Decision{Retry: true, Delay: 50 * time.Millisecond}) {
			t.Fatalf("Policy.Decide() input=%v decision=%#v", input, got)
		}
	})

	for _, test := range []struct {
		name   string
		jitter time.Duration
		want   time.Duration
	}{
		{name: "negative", jitter: -time.Second, want: 0},
		{name: "above maximum", jitter: 2 * time.Second, want: time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := Policy{MaxAttempts: 2, BaseDelay: time.Second, MaxDelay: time.Second, Jitter: func(time.Duration) time.Duration { return test.jitter }}
			if got := policy.Decide(1, retryable); got != (Decision{Retry: true, Delay: test.want}) {
				t.Fatalf("Policy.Decide() = %#v, want delay %v", got, test.want)
			}
		})
	}
}

// FR-RECON-07
func TestPolicyDoesNotApplyJitterWithoutRetry(t *testing.T) {
	called := false
	policy := Policy{MaxAttempts: 2, MaxDelay: time.Second, Jitter: func(delay time.Duration) time.Duration { called = true; return delay }}
	if got := policy.Decide(1, Permanent(errors.New("invalid"))); got != (Decision{}) {
		t.Fatalf("Policy.Decide() = %#v, want no retry", got)
	}
	if called {
		t.Fatal("Jitter called for a non-retryable error")
	}
}
