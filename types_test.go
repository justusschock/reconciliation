package reconciliation

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// FR-RECON-01
func TestKeyValidation(t *testing.T) {
	if err := Key("resource").Validate(); err != nil {
		t.Fatalf("Key.Validate() = %v, want nil", err)
	}
	if err := Key("").Validate(); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("Key.Validate() = %v, want %v", err, ErrInvalidKey)
	}
}

// FR-RECON-01
func TestGenerationValidation(t *testing.T) {
	if err := Generation(1).Validate(); err != nil {
		t.Fatalf("Generation.Validate() = %v, want nil", err)
	}
	if err := Generation(0).Validate(); !errors.Is(err, ErrInvalidGeneration) {
		t.Fatalf("Generation.Validate() = %v, want %v", err, ErrInvalidGeneration)
	}
}

// FR-RECON-01
func TestDesiredRequiresKeyAndGeneration(t *testing.T) {
	tests := []struct {
		name       string
		desired    Desired[string]
		underlying error
	}{
		{name: "key", desired: Desired[string]{Generation: 1, Spec: "x"}, underlying: ErrInvalidKey},
		{name: "generation", desired: Desired[string]{Key: "resource", Spec: "x"}, underlying: ErrInvalidGeneration},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.desired.Validate()
			if !errors.Is(err, ErrInvalidDesired) || !errors.Is(err, test.underlying) {
				t.Fatalf("Desired.Validate() = %v, want %v and %v", err, ErrInvalidDesired, test.underlying)
			}
		})
	}

	if err := (Desired[string]{Key: "resource", Generation: 1, Spec: "x"}).Validate(); err != nil {
		t.Fatalf("Desired.Validate() = %v, want nil", err)
	}
}

// FR-RECON-02
func TestConditionStatusValues(t *testing.T) {
	if ConditionTrue != "true" || ConditionFalse != "false" || ConditionUnknown != "unknown" {
		t.Fatalf("condition statuses = (%q, %q, %q)", ConditionTrue, ConditionFalse, ConditionUnknown)
	}
}

// FR-RECON-02
func TestConditionValidation(t *testing.T) {
	valid := Condition{
		Type:               "Ready",
		Status:             ConditionTrue,
		Reason:             "Reconciled",
		Message:            "first line\nsecond line",
		LastTransitionTime: time.Unix(123, 0),
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Condition.Validate() = %v, want nil", err)
	}

	invalidUTF8 := string([]byte{utf8.RuneSelf})
	tests := []struct {
		name   string
		mutate func(*Condition)
	}{
		{name: "empty type", mutate: func(condition *Condition) { condition.Type = "" }},
		{name: "empty status", mutate: func(condition *Condition) { condition.Status = "" }},
		{name: "unknown status", mutate: func(condition *Condition) { condition.Status = "sometimes" }},
		{name: "type newline", mutate: func(condition *Condition) { condition.Type = "Not\nReady" }},
		{name: "type carriage return", mutate: func(condition *Condition) { condition.Type = "Not\rReady" }},
		{name: "type NUL", mutate: func(condition *Condition) { condition.Type = "Not\x00Ready" }},
		{name: "reason newline", mutate: func(condition *Condition) { condition.Reason = "Not\nReady" }},
		{name: "reason carriage return", mutate: func(condition *Condition) { condition.Reason = "Not\rReady" }},
		{name: "reason NUL", mutate: func(condition *Condition) { condition.Reason = "Not\x00Ready" }},
		{name: "message NUL", mutate: func(condition *Condition) { condition.Message = "Not\x00Ready" }},
		{name: "type invalid UTF-8", mutate: func(condition *Condition) { condition.Type = invalidUTF8 }},
		{name: "reason invalid UTF-8", mutate: func(condition *Condition) { condition.Reason = invalidUTF8 }},
		{name: "message invalid UTF-8", mutate: func(condition *Condition) { condition.Message = invalidUTF8 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			condition := valid
			test.mutate(&condition)
			if err := condition.Validate(); !errors.Is(err, ErrInvalidCondition) {
				t.Fatalf("Condition.Validate() = %v, want %v", err, ErrInvalidCondition)
			}
		})
	}
}

// FR-RECON-02
func TestResultValidation(t *testing.T) {
	valid := Result[string]{
		Key:          "resource",
		Generation:   1,
		Observed:     "state",
		Changed:      true,
		Conditions:   []Condition{{Type: "Ready", Status: ConditionTrue}},
		RequeueAfter: time.Second,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("Result.Validate() = %v, want nil", err)
	}

	tests := []struct {
		name       string
		mutate     func(*Result[string])
		underlying error
	}{
		{name: "key", mutate: func(result *Result[string]) { result.Key = "" }, underlying: ErrInvalidKey},
		{name: "generation", mutate: func(result *Result[string]) { result.Generation = 0 }, underlying: ErrInvalidGeneration},
		{name: "negative requeue", mutate: func(result *Result[string]) { result.RequeueAfter = -time.Nanosecond }},
		{name: "condition", mutate: func(result *Result[string]) { result.Conditions[0].Status = "invalid" }, underlying: ErrInvalidCondition},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := valid.Clone()
			test.mutate(&result)
			err := result.Validate()
			if !errors.Is(err, ErrInvalidResult) {
				t.Fatalf("Result.Validate() = %v, want %v", err, ErrInvalidResult)
			}
			if test.underlying != nil && !errors.Is(err, test.underlying) {
				t.Fatalf("Result.Validate() = %v, also want %v", err, test.underlying)
			}
		})
	}
}

// FR-RECON-02
func TestResultCloneCopiesConditionsAndKeepsObservedShallow(t *testing.T) {
	observed := map[string]int{"value": 1}
	original := Result[map[string]int]{
		Key:        "resource",
		Generation: 1,
		Observed:   observed,
		Conditions: []Condition{{Type: "Ready", Status: ConditionTrue, Message: "original"}},
	}

	clone := original.Clone()
	clone.Conditions[0].Message = "changed"
	if original.Conditions[0].Message != "original" {
		t.Fatalf("original condition = %q, want defensive copy", original.Conditions[0].Message)
	}

	clone.Observed["value"] = 2
	if original.Observed["value"] != 2 {
		t.Fatalf("original observed value = %d, want shallow ownership", original.Observed["value"])
	}

	var nilConditions Result[string]
	if nilConditions.Clone().Conditions != nil {
		t.Fatal("Clone changed nil Conditions into a non-nil slice")
	}
}

// FR-RECON-02
func TestConditionValidationAcceptsUnicodeAndMessageNewlines(t *testing.T) {
	condition := Condition{Type: "Bereit", Status: ConditionUnknown, Reason: "Überprüfung", Message: strings.Repeat("状態\n", 2)}
	if err := condition.Validate(); err != nil {
		t.Fatalf("Condition.Validate() = %v, want nil", err)
	}
}
