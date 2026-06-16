package main

import (
	"testing"

	"my.domain/fga/internal/authzproxy"
)

func TestDefaultsAreSet(t *testing.T) {
	if authzProxyAddressEnv == "" || authzproxy.DefaultBindAddress == "" {
		t.Fatal("authorization proxy defaults must be set")
	}
}
