package main

import (
	"testing"

	"erli.ng/authz-operator/internal/tuplelistener"
)

func TestDefaultsAreSet(t *testing.T) {
	if tupleListenerBatchSizeEnv == "" || tuplelistener.DefaultBatchSize <= 0 {
		t.Fatal("tuple listener defaults must be set")
	}
}
