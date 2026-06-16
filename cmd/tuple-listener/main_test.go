package main

import (
	"testing"

	"my.domain/fga/internal/tuplelistener"
)

func TestDefaultsAreSet(t *testing.T) {
	if tupleListenerBatchSizeEnv == "" || tuplelistener.DefaultBatchSize <= 0 {
		t.Fatal("tuple listener defaults must be set")
	}
}
