/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import "testing"

func TestEnvOrDefault(t *testing.T) {
	t.Setenv("AUTHZ_PROXY_TEST_ENV", "configured")

	if got := envOrDefault("AUTHZ_PROXY_TEST_ENV", "default"); got != "configured" {
		t.Fatalf("envOrDefault configured = %q, want configured", got)
	}
	if got := envOrDefault("AUTHZ_PROXY_MISSING_ENV", "default"); got != "default" {
		t.Fatalf("envOrDefault missing = %q, want default", got)
	}
}
