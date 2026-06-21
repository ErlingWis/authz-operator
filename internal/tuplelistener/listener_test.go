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

package tuplelistener

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/nats-io/nats.go"

	"erli.ng/authz-operator/internal/tuple"
)

func TestProcessAppliesValidMessageAndAcks(t *testing.T) {
	ctx := context.Background()
	applier := &fakeApplier{}
	message := &fakeMessage{data: []byte(`{"operations":[{"op":"write","object":{"type":"document","id":"1"},"relation":"viewer","subject":{"type":"user","id":"alice"}}]}`)}
	listener := &Listener{Applier: applier}

	if err := listener.Process(ctx, message); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if !message.acked {
		t.Fatal("message was not acked")
	}
	if message.nacked {
		t.Fatal("message was nacked")
	}
	want := tuple.Changes{
		Writes: []tuple.TupleKey{{
			User:     "user:alice",
			Relation: "viewer",
			Object:   "document:1",
		}},
	}
	if diff := cmp.Diff(want, applier.changes); diff != "" {
		t.Fatalf("changes mismatch (-want +got):\n%s", diff)
	}
}

func TestProcessDeadLettersInvalidMessageAndAcks(t *testing.T) {
	ctx := context.Background()
	dlq := &fakeDeadLetterPublisher{}
	message := &fakeMessage{data: []byte(`{"operations":[]}`)}
	listener := &Listener{Applier: &fakeApplier{}, DeadLetterPublisher: dlq}

	if err := listener.Process(ctx, message); err != nil {
		t.Fatalf("Process() error = %v", err)
	}
	if !message.acked {
		t.Fatal("message was not acked")
	}
	if message.nacked {
		t.Fatal("message was nacked")
	}
	if len(dlq.messages) != 1 {
		t.Fatalf("dead letter count = %d, want 1", len(dlq.messages))
	}
}

func TestProcessNacksWhenDeadLetterPublishFails(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("nats unavailable")
	message := &fakeMessage{data: []byte(`{"operations":[]}`)}
	listener := &Listener{
		Applier:             &fakeApplier{},
		DeadLetterPublisher: &fakeDeadLetterPublisher{err: wantErr},
	}

	if err := listener.Process(ctx, message); !errors.Is(err, wantErr) {
		t.Fatalf("Process() error = %v, want %v", err, wantErr)
	}
	if message.acked {
		t.Fatal("message was acked")
	}
	if !message.nacked {
		t.Fatal("message was not nacked")
	}
}

func TestProcessNacksWhenApplyFails(t *testing.T) {
	ctx := context.Background()
	wantErr := errors.New("openfga unavailable")
	message := &fakeMessage{data: []byte(`{"operations":[{"op":"delete","object":{"type":"document","id":"1"},"relation":"viewer","subject":{"type":"user","id":"alice"}}]}`)}
	listener := &Listener{Applier: &fakeApplier{err: wantErr}}

	if err := listener.Process(ctx, message); !errors.Is(err, wantErr) {
		t.Fatalf("Process() error = %v, want %v", err, wantErr)
	}
	if message.acked {
		t.Fatal("message was acked")
	}
	if !message.nacked {
		t.Fatal("message was not nacked")
	}
}

func TestEncodeDeadLetterPreservesMalformedJSONPayload(t *testing.T) {
	occurredAt := time.Date(2026, 6, 15, 12, 0, 0, 0, time.FixedZone("offset", 2*60*60))
	payload := []byte(`{"operations":[`)

	body, err := EncodeDeadLetter(payload, "invalid JSON", occurredAt)
	if err != nil {
		t.Fatalf("EncodeDeadLetter() error = %v", err)
	}

	var message DeadLetterMessage
	if err := json.Unmarshal(body, &message); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if message.Reason != "invalid JSON" {
		t.Fatalf("reason = %q, want %q", message.Reason, "invalid JSON")
	}
	if !message.OccurredAt.Equal(occurredAt.UTC()) {
		t.Fatalf("occurredAt = %s, want %s", message.OccurredAt, occurredAt.UTC())
	}
	if diff := cmp.Diff(payload, message.Payload); diff != "" {
		t.Fatalf("payload mismatch (-want +got):\n%s", diff)
	}
}

func TestNATSOptionsConfigureTokenAndConsumerName(t *testing.T) {
	config := NATSConfig{
		Consumer: "consumer-1",
		Token:    "token-1",
	}
	options := nats.GetDefaultOptions()
	for _, option := range natsOptions(config) {
		if err := option(&options); err != nil {
			t.Fatalf("option() error = %v", err)
		}
	}

	if options.Name != "consumer-1" {
		t.Fatalf("Name = %q, want %q", options.Name, "consumer-1")
	}
	if options.Token != "token-1" {
		t.Fatalf("Token = %q, want %q", options.Token, "token-1")
	}
}

type fakeApplier struct {
	changes tuple.Changes
	err     error
}

func (a *fakeApplier) Apply(_ context.Context, changes tuple.Changes) error {
	a.changes = changes
	return a.err
}

type fakeMessage struct {
	data   []byte
	acked  bool
	nacked bool
}

func (m *fakeMessage) Data() []byte {
	return m.data
}

func (m *fakeMessage) Ack() error {
	m.acked = true
	return nil
}

func (m *fakeMessage) Nak() error {
	m.nacked = true
	return nil
}

type fakeDeadLetterPublisher struct {
	messages [][]byte
	err      error
}

func (p *fakeDeadLetterPublisher) PublishDeadLetter(_ context.Context, data []byte, _ string) error {
	if p.err != nil {
		return p.err
	}
	p.messages = append(p.messages, data)
	return nil
}
