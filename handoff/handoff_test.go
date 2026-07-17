package handoff

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	lifecycle "github.com/justusschock/go-lifecycle"
	lifecyclehandoff "github.com/justusschock/go-lifecycle/handoff"
	reconciliation "github.com/justusschock/go-reconciliation"
)

const testMediaType = "application/vnd.justusschock.reconciliation+json"

type testSpec struct {
	Name string `json:"name"`
}

type testDocument struct {
	Pending []testEncodedPending `json:"pending"`
}

type testEncodedPending struct {
	Key        reconciliation.Key        `json:"key"`
	Generation reconciliation.Generation `json:"generation"`
	Spec       []byte                    `json:"spec"`
	RetryAt    time.Time                 `json:"retry_at"`
}

type jsonCodec[S any] struct{}

func (jsonCodec[S]) Marshal(value S) ([]byte, error) {
	return json.Marshal(value)
}

func (jsonCodec[S]) Unmarshal(data []byte) (S, error) {
	var value S
	err := json.Unmarshal(data, &value)
	return value, err
}

type pointerCodec struct{}

func (*pointerCodec) Marshal(value testSpec) ([]byte, error) {
	return json.Marshal(value)
}

func (*pointerCodec) Unmarshal(data []byte) (testSpec, error) {
	var value testSpec
	err := json.Unmarshal(data, &value)
	return value, err
}

type codecFuncs[S any] struct {
	marshal   func(S) ([]byte, error)
	unmarshal func([]byte) (S, error)
}

func (codec codecFuncs[S]) Marshal(value S) ([]byte, error) {
	return codec.marshal(value)
}

func (codec codecFuncs[S]) Unmarshal(data []byte) (S, error) {
	return codec.unmarshal(data)
}

var _ lifecyclehandoff.Exporter[[]Pending[testSpec]] = (*Exporter[testSpec])(nil)
var _ lifecyclehandoff.Adopter[[]Pending[testSpec]] = (*Adopter[testSpec])(nil)

func TestHandoffRoundTripIsCanonicalAndStateOnly(t *testing.T) {
	deadline := time.Date(2026, time.July, 17, 10, 30, 0, 123456789, time.FixedZone("source", 2*60*60))
	alpha := Pending[testSpec]{
		Desired: reconciliation.Desired[testSpec]{Key: "alpha", Generation: 1, Spec: testSpec{Name: "first"}},
	}
	beta := Pending[testSpec]{
		Desired: reconciliation.Desired[testSpec]{Key: "beta", Generation: 2, Spec: testSpec{Name: "second"}},
		RetryAt: deadline,
	}
	exporter, err := NewExporter[testSpec](jsonCodec[testSpec]{})
	if err != nil {
		t.Fatal(err)
	}
	adopter, err := NewAdopter[testSpec](jsonCodec[testSpec]{})
	if err != nil {
		t.Fatal(err)
	}

	artifact, err := exporter.Export(context.Background(), []Pending[testSpec]{beta, alpha})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := exporter.Export(context.Background(), []Pending[testSpec]{alpha, beta})
	if err != nil {
		t.Fatal(err)
	}
	if artifact.ID != canonical.ID || artifact.Digest != canonical.Digest || !bytes.Equal(artifact.Data, canonical.Data) {
		t.Fatalf("input order changed artifact identity: first=%q second=%q", artifact.ID, canonical.ID)
	}
	if artifact.MediaType != testMediaType || artifact.Version != 1 {
		t.Fatalf("artifact envelope = (%q, %d), want (%q, 1)", artifact.MediaType, artifact.Version, testMediaType)
	}
	if err := artifact.Validate(); err != nil {
		t.Fatalf("Artifact.Validate() = %v", err)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(artifact.Data, &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 1 || raw["pending"] == nil {
		t.Fatalf("top-level fields = %v, want only pending", raw)
	}
	var rawPending []map[string]json.RawMessage
	if err := json.Unmarshal(raw["pending"], &rawPending); err != nil {
		t.Fatal(err)
	}
	wantFields := map[string]bool{"key": true, "generation": true, "spec": true, "retry_at": true}
	for index, item := range rawPending {
		if len(item) != len(wantFields) {
			t.Fatalf("pending %d fields = %v", index, item)
		}
		for field := range item {
			if !wantFields[field] {
				t.Fatalf("pending %d contains unexpected field %q", index, field)
			}
		}
	}

	var encoded testDocument
	if err := json.Unmarshal(artifact.Data, &encoded); err != nil {
		t.Fatal(err)
	}
	if len(encoded.Pending) != 2 || encoded.Pending[0].Key != "alpha" || encoded.Pending[1].Key != "beta" {
		t.Fatalf("encoded key order = %#v", encoded.Pending)
	}
	if encoded.Pending[0].Generation != 1 || encoded.Pending[1].Generation != 2 {
		t.Fatalf("encoded generations = %d, %d", encoded.Pending[0].Generation, encoded.Pending[1].Generation)
	}
	for index, want := range []testSpec{{Name: "first"}, {Name: "second"}} {
		got, err := (jsonCodec[testSpec]{}).Unmarshal(encoded.Pending[index].Spec)
		if err != nil || got != want {
			t.Fatalf("encoded spec %d = (%#v, %v), want %#v", index, got, err, want)
		}
	}
	if !encoded.Pending[0].RetryAt.IsZero() || !encoded.Pending[1].RetryAt.Equal(deadline) || encoded.Pending[1].RetryAt.Location() != time.UTC {
		t.Fatalf("encoded retry deadlines = %v, %v", encoded.Pending[0].RetryAt, encoded.Pending[1].RetryAt)
	}

	adopted, err := adopter.Adopt(context.Background(), artifact)
	if err != nil {
		t.Fatal(err)
	}
	if len(adopted) != 2 || adopted[0].Desired != alpha.Desired || adopted[1].Desired != beta.Desired {
		t.Fatalf("adopted desired values = %#v", adopted)
	}
	if !adopted[0].RetryAt.IsZero() || !adopted[1].RetryAt.Equal(deadline) || adopted[1].RetryAt.Location() != time.UTC {
		t.Fatalf("adopted retry deadlines = %v, %v", adopted[0].RetryAt, adopted[1].RetryAt)
	}
}

func TestConstructorsRejectNilCodecs(t *testing.T) {
	var nilCodec Codec[testSpec]
	var typedNil *pointerCodec
	for name, codec := range map[string]Codec[testSpec]{"nil": nilCodec, "typed nil": typedNil} {
		t.Run(name, func(t *testing.T) {
			exporter, exportErr := NewExporter[testSpec](codec)
			if exporter != nil || !errors.Is(exportErr, lifecycle.ErrInvalidArtifact) {
				t.Fatalf("NewExporter() = (%#v, %v), want nil ErrInvalidArtifact", exporter, exportErr)
			}
			adopter, adoptErr := NewAdopter[testSpec](codec)
			if adopter != nil || !errors.Is(adoptErr, lifecycle.ErrInvalidArtifact) {
				t.Fatalf("NewAdopter() = (%#v, %v), want nil ErrInvalidArtifact", adopter, adoptErr)
			}
		})
	}
}

func TestAdoptRejectsUnknownVersion(t *testing.T) {
	artifact, err := lifecyclehandoff.NewArtifact(testMediaType, 99, []byte(`{"pending":[]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	adopter, err := NewAdopter[testSpec](jsonCodec[testSpec]{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adopter.Adopt(context.Background(), artifact); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("Adopt() error = %v, want ErrUnsupportedVersion", err)
	}
}

func TestAdoptRejectsWrongMediaTypeAndDigestMismatch(t *testing.T) {
	adopter, err := NewAdopter[testSpec](jsonCodec[testSpec]{})
	if err != nil {
		t.Fatal(err)
	}
	validData := mustDocumentData(t, []testEncodedPending{{
		Key: "alpha", Generation: 1, Spec: mustSpecData(t, testSpec{Name: "first"}), RetryAt: time.Time{},
	}})

	t.Run("media type", func(t *testing.T) {
		artifact, err := lifecyclehandoff.NewArtifact("application/other", 1, validData, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := adopter.Adopt(context.Background(), artifact); !errors.Is(err, lifecycle.ErrInvalidArtifact) {
			t.Fatalf("Adopt() error = %v, want ErrInvalidArtifact", err)
		}
	})

	t.Run("digest", func(t *testing.T) {
		artifact := mustArtifact(t, validData)
		artifact.Data[0]++
		if _, err := adopter.Adopt(context.Background(), artifact); !errors.Is(err, lifecycle.ErrIntegrity) {
			t.Fatalf("Adopt() error = %v, want ErrIntegrity", err)
		}
	})
}

func TestExportRejectsInvalidPendingState(t *testing.T) {
	exporter, err := NewExporter[testSpec](jsonCodec[testSpec]{})
	if err != nil {
		t.Fatal(err)
	}
	valid := Pending[testSpec]{Desired: reconciliation.Desired[testSpec]{Key: "alpha", Generation: 1}}

	t.Run("duplicate key", func(t *testing.T) {
		duplicate := valid
		duplicate.Desired.Generation = 2
		if _, err := exporter.Export(context.Background(), []Pending[testSpec]{valid, duplicate}); !errors.Is(err, lifecycle.ErrInvalidArtifact) {
			t.Fatalf("Export() error = %v, want ErrInvalidArtifact", err)
		}
	})

	for name, desired := range map[string]reconciliation.Desired[testSpec]{
		"empty key":       {Generation: 1},
		"zero generation": {Key: "alpha"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := exporter.Export(context.Background(), []Pending[testSpec]{{Desired: desired}})
			if !errors.Is(err, lifecycle.ErrInvalidArtifact) || !errors.Is(err, reconciliation.ErrInvalidDesired) {
				t.Fatalf("Export() error = %v, want ErrInvalidArtifact and ErrInvalidDesired", err)
			}
		})
	}

	t.Run("deadline outside RFC3339 range", func(t *testing.T) {
		invalid := valid
		invalid.RetryAt = time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)
		if _, err := exporter.Export(context.Background(), []Pending[testSpec]{invalid}); !errors.Is(err, lifecycle.ErrInvalidArtifact) {
			t.Fatalf("Export() error = %v, want ErrInvalidArtifact", err)
		}
	})
}

func TestAdoptRejectsInvalidPendingStateAndTrailingData(t *testing.T) {
	adopter, err := NewAdopter[testSpec](jsonCodec[testSpec]{})
	if err != nil {
		t.Fatal(err)
	}
	validSpec := mustSpecData(t, testSpec{Name: "first"})

	t.Run("duplicate key", func(t *testing.T) {
		artifact := mustArtifact(t, mustDocumentData(t, []testEncodedPending{
			{Key: "alpha", Generation: 1, Spec: validSpec},
			{Key: "alpha", Generation: 2, Spec: validSpec},
		}))
		if _, err := adopter.Adopt(context.Background(), artifact); !errors.Is(err, lifecycle.ErrInvalidArtifact) {
			t.Fatalf("Adopt() error = %v, want ErrInvalidArtifact", err)
		}
	})

	t.Run("invalid desired", func(t *testing.T) {
		artifact := mustArtifact(t, mustDocumentData(t, []testEncodedPending{{Generation: 1, Spec: validSpec}}))
		_, err := adopter.Adopt(context.Background(), artifact)
		if !errors.Is(err, lifecycle.ErrInvalidArtifact) || !errors.Is(err, reconciliation.ErrInvalidDesired) {
			t.Fatalf("Adopt() error = %v, want ErrInvalidArtifact and ErrInvalidDesired", err)
		}
	})

	t.Run("invalid deadline", func(t *testing.T) {
		artifact := mustArtifact(t, []byte(`{"pending":[{"key":"alpha","generation":1,"spec":"e30=","retry_at":"not-a-deadline"}]}`))
		if _, err := adopter.Adopt(context.Background(), artifact); !errors.Is(err, lifecycle.ErrInvalidArtifact) {
			t.Fatalf("Adopt() error = %v, want ErrInvalidArtifact", err)
		}
	})

	t.Run("trailing data", func(t *testing.T) {
		data := append(mustDocumentData(t, nil), []byte("\n{}")...)
		artifact := mustArtifact(t, data)
		if _, err := adopter.Adopt(context.Background(), artifact); !errors.Is(err, lifecycle.ErrInvalidArtifact) {
			t.Fatalf("Adopt() error = %v, want ErrInvalidArtifact", err)
		}
	})

	t.Run("unknown action payload", func(t *testing.T) {
		artifact := mustArtifact(t, []byte(`{"pending":[],"actions":[{"secret":"value"}]}`))
		if _, err := adopter.Adopt(context.Background(), artifact); !errors.Is(err, lifecycle.ErrInvalidArtifact) {
			t.Fatalf("Adopt() error = %v, want ErrInvalidArtifact", err)
		}
	})
}

func TestCodecErrorsArePreserved(t *testing.T) {
	marshalErr := errors.New("marshal failed")
	exporter, err := NewExporter[testSpec](codecFuncs[testSpec]{
		marshal: func(testSpec) ([]byte, error) { return nil, marshalErr },
		unmarshal: func([]byte) (testSpec, error) {
			return testSpec{}, errors.New("unexpected unmarshal")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = exporter.Export(context.Background(), []Pending[testSpec]{{
		Desired: reconciliation.Desired[testSpec]{Key: "alpha", Generation: 1},
	}})
	if !errors.Is(err, marshalErr) || !errors.Is(err, lifecycle.ErrInvalidArtifact) {
		t.Fatalf("Export() error = %v, want codec error and ErrInvalidArtifact", err)
	}

	unmarshalErr := errors.New("unmarshal failed")
	adopter, err := NewAdopter[testSpec](codecFuncs[testSpec]{
		marshal: func(testSpec) ([]byte, error) { return nil, errors.New("unexpected marshal") },
		unmarshal: func([]byte) (testSpec, error) {
			return testSpec{}, unmarshalErr
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact := mustArtifact(t, mustDocumentData(t, []testEncodedPending{{
		Key: "alpha", Generation: 1, Spec: []byte("state"),
	}}))
	if _, err := adopter.Adopt(context.Background(), artifact); !errors.Is(err, unmarshalErr) || !errors.Is(err, lifecycle.ErrInvalidArtifact) {
		t.Fatalf("Adopt() error = %v, want codec error and ErrInvalidArtifact", err)
	}
}

func TestHandoffHonorsCanceledContexts(t *testing.T) {
	wantErr := errors.New("handoff canceled")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(wantErr)
	exporter, err := NewExporter[testSpec](jsonCodec[testSpec]{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exporter.Export(ctx, nil); !errors.Is(err, wantErr) {
		t.Fatalf("Export() error = %v, want context cause", err)
	}
	adopter, err := NewAdopter[testSpec](jsonCodec[testSpec]{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adopter.Adopt(ctx, mustArtifact(t, mustDocumentData(t, nil))); !errors.Is(err, wantErr) {
		t.Fatalf("Adopt() error = %v, want context cause", err)
	}
}

func mustSpecData(t *testing.T, spec testSpec) []byte {
	t.Helper()
	data, err := (jsonCodec[testSpec]{}).Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustDocumentData(t *testing.T, pending []testEncodedPending) []byte {
	t.Helper()
	if pending == nil {
		pending = []testEncodedPending{}
	}
	data, err := json.Marshal(testDocument{Pending: pending})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func mustArtifact(t *testing.T, data []byte) lifecyclehandoff.Artifact {
	t.Helper()
	artifact, err := lifecyclehandoff.NewArtifact(testMediaType, 1, data, nil)
	if err != nil {
		t.Fatal(err)
	}
	return artifact
}
