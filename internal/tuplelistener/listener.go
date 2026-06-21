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
	"fmt"
	"time"

	"erli.ng/authz-operator/internal/tuple"
)

type Applier interface {
	Apply(ctx context.Context, changes tuple.Changes) error
}

type Message interface {
	Data() []byte
	Ack() error
	Nak() error
}

type DeadLetterPublisher interface {
	PublishDeadLetter(ctx context.Context, data []byte, reason string) error
}

type Listener struct {
	Applier             Applier
	DeadLetterPublisher DeadLetterPublisher
}

func (l *Listener) Process(ctx context.Context, message Message) error {
	changes, err := tuple.ParseMessage(message.Data())
	if err != nil {
		if l.DeadLetterPublisher != nil {
			if dlqErr := l.DeadLetterPublisher.PublishDeadLetter(ctx, message.Data(), err.Error()); dlqErr != nil {
				if nakErr := message.Nak(); nakErr != nil {
					return fmt.Errorf("publish dead letter failed: %w; nak failed: %v", dlqErr, nakErr)
				}
				return dlqErr
			}
		}
		return message.Ack()
	}

	if err := l.Applier.Apply(ctx, changes); err != nil {
		if nakErr := message.Nak(); nakErr != nil {
			return fmt.Errorf("apply tuple changes failed: %w; nak failed: %v", err, nakErr)
		}
		return err
	}

	return message.Ack()
}

type DeadLetterMessage struct {
	Reason     string    `json:"reason"`
	OccurredAt time.Time `json:"occurredAt"`
	Payload    []byte    `json:"payload"`
}

func EncodeDeadLetter(data []byte, reason string, occurredAt time.Time) ([]byte, error) {
	message := DeadLetterMessage{
		Reason:     reason,
		OccurredAt: occurredAt.UTC(),
		Payload:    data,
	}
	return json.Marshal(message)
}
