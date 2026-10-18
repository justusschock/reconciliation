package handoff_test

import (
	"bytes"
	"context"
	"fmt"
	"time"

	reconciliation "github.com/justusschock/go-reconciliation"
	reconciliationhandoff "github.com/justusschock/go-reconciliation/handoff"
)

// FR-RECON-17, NFR-RECON-01
func ExampleExporter() {
	retryAt := time.Date(2026, time.July, 17, 12, 0, 0, 0, time.FixedZone("offset", 2*60*60))
	managerSnapshot := []reconciliation.Pending[[]byte]{{
		Desired: reconciliation.Desired[[]byte]{
			Key: "job", Generation: 3, Spec: []byte("desired state"),
		},
		RetryAt: retryAt,
	}}

	exporter, err := reconciliationhandoff.NewExporter[[]byte](ownedBytesCodec{})
	if err != nil {
		panic(err)
	}
	artifact, err := exporter.Export(context.Background(), toTransfer(managerSnapshot))
	if err != nil {
		panic(err)
	}

	adopter, err := reconciliationhandoff.NewAdopter[[]byte](ownedBytesCodec{})
	if err != nil {
		panic(err)
	}
	transferred, err := adopter.Adopt(context.Background(), artifact)
	if err != nil {
		panic(err)
	}
	restored := toManagerPending(transferred)

	fmt.Println(restored[0].Desired.Key, restored[0].Desired.Generation)
	fmt.Println(string(restored[0].Desired.Spec))
	fmt.Println(restored[0].RetryAt.Format(time.RFC3339))

	// Output:
	// job 3
	// desired state
	// 2026-07-17T10:00:00Z
}

type ownedBytesCodec struct{}

func (ownedBytesCodec) Marshal(value []byte) ([]byte, error) {
	return value, nil
}

func (ownedBytesCodec) Unmarshal(data []byte) ([]byte, error) {
	return bytes.Clone(data), nil
}

func toTransfer[S any](pending []reconciliation.Pending[S]) []reconciliationhandoff.Pending[S] {
	transferred := make([]reconciliationhandoff.Pending[S], len(pending))
	for index, value := range pending {
		transferred[index] = reconciliationhandoff.Pending[S]{
			Desired: value.Desired,
			RetryAt: value.RetryAt,
		}
	}
	return transferred
}

func toManagerPending[S any](pending []reconciliationhandoff.Pending[S]) []reconciliation.Pending[S] {
	restored := make([]reconciliation.Pending[S], len(pending))
	for index, value := range pending {
		restored[index] = reconciliation.Pending[S]{
			Desired: value.Desired,
			RetryAt: value.RetryAt,
		}
	}
	return restored
}
