// Package handoff transfers pending reconciliation state through lifecycle
// artifacts.
package handoff

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"time"

	lifecycle "github.com/justusschock/go-lifecycle"
	lifecyclehandoff "github.com/justusschock/go-lifecycle/handoff"
	reconciliation "github.com/justusschock/go-reconciliation"
)

const (
	mediaType     = "application/vnd.justusschock.reconciliation+json"
	schemaVersion = 1
)

// ErrUnsupportedVersion reports an artifact schema this package cannot adopt.
var ErrUnsupportedVersion = errors.New("reconciliation handoff: unsupported version")

// Pending is a desired value that has not started reconciling. RetryAt is the
// absolute time at which a delayed value may be submitted again.
type Pending[S any] struct {
	Desired reconciliation.Desired[S]
	RetryAt time.Time
}

// Codec serializes and deserializes desired specifications. Marshal may return
// scratch storage because Export copies each result before the next call.
// Unmarshal must return an independently owned value whose mutable state stays
// stable across later Codec calls; implementations must clone input-backed
// slices, maps, pointers, and other reference values as needed.
type Codec[S any] interface {
	Marshal(S) ([]byte, error)
	Unmarshal([]byte) (S, error)
}

// Exporter creates lifecycle artifacts from pending reconciliation state.
type Exporter[S any] struct {
	codec Codec[S]
}

// NewExporter creates an Exporter using codec for desired specifications.
func NewExporter[S any](codec Codec[S]) (*Exporter[S], error) {
	if isNilCodec(codec) {
		return nil, fmt.Errorf("%w: nil reconciliation handoff codec", lifecycle.ErrInvalidArtifact)
	}
	return &Exporter[S]{codec: codec}, nil
}

// Export encodes pending state in canonical key order.
func (exporter *Exporter[S]) Export(ctx context.Context, pending []Pending[S]) (lifecyclehandoff.Artifact, error) {
	if err := context.Cause(ctx); err != nil {
		return lifecyclehandoff.Artifact{}, err
	}

	ordered := slices.Clone(pending)
	slices.SortFunc(ordered, comparePending[S])
	document := wireDocument{Pending: make([]wirePending, len(ordered))}
	var previous reconciliation.Key
	for index, value := range ordered {
		if err := context.Cause(ctx); err != nil {
			return lifecyclehandoff.Artifact{}, err
		}
		if err := value.Desired.Validate(); err != nil {
			return lifecyclehandoff.Artifact{}, fmt.Errorf("%w: pending %d: %w", lifecycle.ErrInvalidArtifact, index, err)
		}
		if index > 0 && value.Desired.Key == previous {
			return lifecyclehandoff.Artifact{}, fmt.Errorf("%w: duplicate reconciliation key %q", lifecycle.ErrInvalidArtifact, value.Desired.Key)
		}
		previous = value.Desired.Key

		retryAt, err := canonicalDeadline(value.RetryAt)
		if err != nil {
			return lifecyclehandoff.Artifact{}, fmt.Errorf("%w: pending %q deadline: %w", lifecycle.ErrInvalidArtifact, value.Desired.Key, err)
		}
		spec, err := exporter.codec.Marshal(value.Desired.Spec)
		if err != nil {
			return lifecyclehandoff.Artifact{}, fmt.Errorf("%w: pending %q specification: %w", lifecycle.ErrInvalidArtifact, value.Desired.Key, err)
		}
		document.Pending[index] = wirePending{
			Key:        value.Desired.Key,
			Generation: value.Desired.Generation,
			Spec:       slices.Clone(spec),
			RetryAt:    retryAt,
		}
	}

	data, err := json.Marshal(document)
	if err != nil {
		return lifecyclehandoff.Artifact{}, fmt.Errorf("%w: encode reconciliation handoff: %w", lifecycle.ErrInvalidArtifact, err)
	}
	if err := context.Cause(ctx); err != nil {
		return lifecyclehandoff.Artifact{}, err
	}
	artifact, err := lifecyclehandoff.NewArtifact(mediaType, schemaVersion, data, nil)
	if err != nil {
		return lifecyclehandoff.Artifact{}, fmt.Errorf("create reconciliation handoff: %w", err)
	}
	return artifact, nil
}

// Adopter restores pending reconciliation state from lifecycle artifacts.
type Adopter[S any] struct {
	codec Codec[S]
}

// NewAdopter creates an Adopter using codec for desired specifications.
func NewAdopter[S any](codec Codec[S]) (*Adopter[S], error) {
	if isNilCodec(codec) {
		return nil, fmt.Errorf("%w: nil reconciliation handoff codec", lifecycle.ErrInvalidArtifact)
	}
	return &Adopter[S]{codec: codec}, nil
}

// Adopt validates and decodes pending reconciliation state.
func (adopter *Adopter[S]) Adopt(ctx context.Context, artifact lifecyclehandoff.Artifact) ([]Pending[S], error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	if err := artifact.Validate(); err != nil {
		return nil, fmt.Errorf("validate reconciliation handoff: %w", err)
	}
	if artifact.MediaType != mediaType {
		return nil, fmt.Errorf("%w: media type %q", lifecycle.ErrInvalidArtifact, artifact.MediaType)
	}
	if artifact.Version != schemaVersion {
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedVersion, artifact.Version)
	}

	var document wireDocument
	decoder := json.NewDecoder(bytes.NewReader(artifact.Data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("%w: decode reconciliation handoff: %w", lifecycle.ErrInvalidArtifact, err)
	}
	if document.Pending == nil {
		return nil, fmt.Errorf("%w: missing pending values", lifecycle.ErrInvalidArtifact)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("additional JSON value")
		}
		return nil, fmt.Errorf("%w: trailing artifact data: %w", lifecycle.ErrInvalidArtifact, err)
	}

	pending := make([]Pending[S], len(document.Pending))
	seen := make(map[reconciliation.Key]struct{}, len(document.Pending))
	for index, encoded := range document.Pending {
		if err := context.Cause(ctx); err != nil {
			return nil, err
		}
		desired := reconciliation.Desired[S]{Key: encoded.Key, Generation: encoded.Generation}
		if err := desired.Validate(); err != nil {
			return nil, fmt.Errorf("%w: pending %d: %w", lifecycle.ErrInvalidArtifact, index, err)
		}
		if _, duplicate := seen[desired.Key]; duplicate {
			return nil, fmt.Errorf("%w: duplicate reconciliation key %q", lifecycle.ErrInvalidArtifact, desired.Key)
		}
		seen[desired.Key] = struct{}{}

		retryAt, err := canonicalDeadline(encoded.RetryAt)
		if err != nil {
			return nil, fmt.Errorf("%w: pending %q deadline: %w", lifecycle.ErrInvalidArtifact, desired.Key, err)
		}
		desired.Spec, err = adopter.codec.Unmarshal(encoded.Spec)
		if err != nil {
			return nil, fmt.Errorf("%w: pending %q specification: %w", lifecycle.ErrInvalidArtifact, desired.Key, err)
		}
		pending[index] = Pending[S]{Desired: desired, RetryAt: retryAt}
	}
	slices.SortFunc(pending, comparePending[S])
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	return pending, nil
}

type wireDocument struct {
	Pending []wirePending `json:"pending"`
}

type wirePending struct {
	Key        reconciliation.Key        `json:"key"`
	Generation reconciliation.Generation `json:"generation"`
	Spec       []byte                    `json:"spec"`
	RetryAt    time.Time                 `json:"retry_at"`
}

func comparePending[S any](left, right Pending[S]) int {
	switch {
	case left.Desired.Key < right.Desired.Key:
		return -1
	case left.Desired.Key > right.Desired.Key:
		return 1
	default:
		return 0
	}
}

func canonicalDeadline(deadline time.Time) (time.Time, error) {
	if deadline.IsZero() {
		return time.Time{}, nil
	}
	deadline = deadline.UTC()
	if _, err := deadline.MarshalJSON(); err != nil {
		return time.Time{}, err
	}
	return deadline, nil
}

func isNilCodec(codec any) bool {
	if codec == nil {
		return true
	}
	value := reflect.ValueOf(codec)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

var _ lifecyclehandoff.Exporter[[]Pending[any]] = (*Exporter[any])(nil)
var _ lifecyclehandoff.Adopter[[]Pending[any]] = (*Adopter[any])(nil)
