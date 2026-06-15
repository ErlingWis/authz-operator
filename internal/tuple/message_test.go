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

package tuple

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParseMessageConvertsWritesDeletesAndUsersets(t *testing.T) {
	message := []byte(`{
		"id": "01KV",
		"operations": [
			{
				"op": "write",
				"object": {"type": "document", "id": "123"},
				"relation": "viewer",
				"subject": {"type": "user", "id": "alice"}
			},
			{
				"op": "delete",
				"object": {"type": "document", "id": "123"},
				"relation": "editor",
				"subject": {"type": "group", "id": "eng", "relation": "member"}
			}
		]
	}`)

	changes, err := ParseMessage(message)
	if err != nil {
		t.Fatalf("ParseMessage() error = %v", err)
	}

	want := Changes{
		Writes: []TupleKey{{
			User:     "user:alice",
			Relation: "viewer",
			Object:   "document:123",
		}},
		Deletes: []TupleKey{{
			User:     "group:eng#member",
			Relation: "editor",
			Object:   "document:123",
		}},
	}
	if diff := cmp.Diff(want, changes); diff != "" {
		t.Fatalf("ParseMessage() mismatch (-want +got):\n%s", diff)
	}
}

func TestParseMessageRejectsInvalidMessages(t *testing.T) {
	tests := map[string]string{
		"empty operations": `{"operations":[]}`,
		"unknown op":       `{"operations":[{"op":"touch","object":{"type":"document","id":"1"},"relation":"viewer","subject":{"type":"user","id":"alice"}}]}`,
		"bad object type":  `{"operations":[{"op":"write","object":{"type":"Document","id":"1"},"relation":"viewer","subject":{"type":"user","id":"alice"}}]}`,
		"bad relation":     `{"operations":[{"op":"write","object":{"type":"document","id":"1"},"relation":"can-view","subject":{"type":"user","id":"alice"}}]}`,
		"bad subject id":   `{"operations":[{"op":"write","object":{"type":"document","id":"1"},"relation":"viewer","subject":{"type":"user","id":"alice smith"}}]}`,
	}

	for name, message := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseMessage([]byte(message)); err == nil {
				t.Fatal("ParseMessage() error = nil, want error")
			}
		})
	}
}
