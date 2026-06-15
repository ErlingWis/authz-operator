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
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	DefaultStream     = "bridder-tuples"
	DefaultSubject    = "bridder.tuples.ops"
	DefaultConsumer   = "bridder-tuple-listener"
	DefaultDLQSubject = "bridder.tuples.ops.dlq"
	DefaultBatchSize  = 50
)

type NATSConfig struct {
	URL        string
	Token      string
	Stream     string
	Subject    string
	Consumer   string
	DLQSubject string
	BatchSize  int
}

func (c NATSConfig) WithDefaults() NATSConfig {
	if c.Stream == "" {
		c.Stream = DefaultStream
	}
	if c.Subject == "" {
		c.Subject = DefaultSubject
	}
	if c.Consumer == "" {
		c.Consumer = DefaultConsumer
	}
	if c.DLQSubject == "" {
		c.DLQSubject = DefaultDLQSubject
	}
	if c.BatchSize <= 0 {
		c.BatchSize = DefaultBatchSize
	}
	return c
}

func RunNATS(ctx context.Context, config NATSConfig, applier Applier) error {
	config = config.WithDefaults()
	if config.URL == "" {
		return fmt.Errorf("NATS URL is required")
	}

	nc, err := nats.Connect(config.URL, natsOptions(config)...)
	if err != nil {
		return err
	}
	defer nc.Close()

	js, err := jetstream.New(nc)
	if err != nil {
		return err
	}

	if _, err := js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     config.Stream,
		Subjects: []string{config.Subject, config.DLQSubject},
	}); err != nil {
		return err
	}

	consumer, err := js.CreateOrUpdateConsumer(ctx, config.Stream, jetstream.ConsumerConfig{
		Name:          config.Consumer,
		Durable:       config.Consumer,
		FilterSubject: config.Subject,
		AckPolicy:     jetstream.AckExplicitPolicy,
	})
	if err != nil {
		return err
	}

	messages, err := consumer.Messages(jetstream.PullMaxMessages(config.BatchSize))
	if err != nil {
		return err
	}
	defer messages.Stop()

	listener := &Listener{
		Applier: applier,
		DeadLetterPublisher: &NATSDeadLetterPublisher{
			JetStream: js,
			Subject:   config.DLQSubject,
			Now:       time.Now,
		},
	}

	for {
		message, err := messages.Next(jetstream.NextContext(ctx))
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, jetstream.ErrMsgIteratorClosed) {
				return nil
			}
			return err
		}
		if err := listener.Process(ctx, &NATSMessage{Message: message}); err != nil {
			log.FromContext(ctx).Error(err, "Failed to process tuple message")
		}
	}
}

func natsOptions(config NATSConfig) []nats.Option {
	options := []nats.Option{nats.Name(config.Consumer)}
	if config.Token != "" {
		options = append(options, nats.Token(config.Token))
	}
	return options
}

type NATSMessage struct {
	Message jetstream.Msg
}

func (m *NATSMessage) Data() []byte {
	return m.Message.Data()
}

func (m *NATSMessage) Ack() error {
	return m.Message.Ack()
}

func (m *NATSMessage) Nak() error {
	return m.Message.Nak()
}

type NATSDeadLetterPublisher struct {
	JetStream jetstream.JetStream
	Subject   string
	Now       func() time.Time
}

func (p *NATSDeadLetterPublisher) PublishDeadLetter(ctx context.Context, data []byte, reason string) error {
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}

	body, err := EncodeDeadLetter(data, reason, now())
	if err != nil {
		return err
	}
	_, err = p.JetStream.Publish(ctx, p.Subject, body)
	return err
}
