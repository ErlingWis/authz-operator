package main

import (
	"testing"

	"erli.ng/authz-operator/internal/authzproxy"
)

func TestDefaultsAreSet(t *testing.T) {
	if authzProxyAddressEnv == "" || authzproxy.DefaultBindAddress == "" {
		t.Fatal("authorization proxy defaults must be set")
	}
}
