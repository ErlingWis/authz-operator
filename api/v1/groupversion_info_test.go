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

package v1

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
)

func TestAddToScheme(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() error = %v", err)
	}

	kinds, _, err := scheme.ObjectKinds(&AuthorizationComponent{})
	if err != nil {
		t.Fatalf("ObjectKinds() error = %v", err)
	}
	if len(kinds) != 1 || kinds[0].Group != "auth.bridder.io" || kinds[0].Version != "v1" || kinds[0].Kind != "AuthorizationComponent" {
		t.Fatalf("ObjectKinds() = %#v, want auth.bridder.io/v1 AuthorizationComponent", kinds)
	}
}
