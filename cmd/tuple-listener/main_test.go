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

func TestEnvHelpers(t *testing.T) {
	t.Setenv("TUPLE_LISTENER_TEST_ENV", "configured")
	t.Setenv("TUPLE_LISTENER_TEST_INT", "8")
	t.Setenv("TUPLE_LISTENER_TEST_BAD_INT", "bad")

	if got := envOrDefault("TUPLE_LISTENER_TEST_ENV", "default"); got != "configured" {
		t.Fatalf("envOrDefault configured = %q, want configured", got)
	}
	if got := envOrDefault("TUPLE_LISTENER_MISSING_ENV", "default"); got != "default" {
		t.Fatalf("envOrDefault missing = %q, want default", got)
	}
	if got := envIntOrDefault("TUPLE_LISTENER_TEST_INT", 1); got != 8 {
		t.Fatalf("envIntOrDefault configured = %d, want 8", got)
	}
	if got := envIntOrDefault("TUPLE_LISTENER_TEST_BAD_INT", 1); got != 1 {
		t.Fatalf("envIntOrDefault invalid = %d, want 1", got)
	}
}
