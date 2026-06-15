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
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type Operation string

const (
	OperationWrite  Operation = "write"
	OperationDelete Operation = "delete"
)

type Message struct {
	ID         string             `json:"id,omitempty"`
	Operations []MessageOperation `json:"operations"`
}

type MessageOperation struct {
	Operation Operation  `json:"op"`
	Object    ObjectRef  `json:"object"`
	Relation  string     `json:"relation"`
	Subject   SubjectRef `json:"subject"`
}

type ObjectRef struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type SubjectRef struct {
	Type     string `json:"type"`
	ID       string `json:"id"`
	Relation string `json:"relation,omitempty"`
}

type TupleKey struct {
	User     string
	Relation string
	Object   string
}

type Changes struct {
	Writes  []TupleKey
	Deletes []TupleKey
}

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func ParseMessage(data []byte) (Changes, error) {
	var message Message
	if err := json.Unmarshal(data, &message); err != nil {
		return Changes{}, fmt.Errorf("invalid JSON: %w", err)
	}
	return message.Changes()
}

func (m Message) Changes() (Changes, error) {
	if len(m.Operations) == 0 {
		return Changes{}, fmt.Errorf("operations must contain at least one tuple operation")
	}

	changes := Changes{}
	for i, operation := range m.Operations {
		tupleKey, err := operation.TupleKey()
		if err != nil {
			return Changes{}, fmt.Errorf("operation %d: %w", i, err)
		}

		switch operation.Operation {
		case OperationWrite:
			changes.Writes = append(changes.Writes, tupleKey)
		case OperationDelete:
			changes.Deletes = append(changes.Deletes, tupleKey)
		default:
			return Changes{}, fmt.Errorf("operation %d: op must be write or delete", i)
		}
	}
	return changes, nil
}

func (o MessageOperation) TupleKey() (TupleKey, error) {
	object, err := o.Object.String()
	if err != nil {
		return TupleKey{}, fmt.Errorf("object: %w", err)
	}
	subject, err := o.Subject.String()
	if err != nil {
		return TupleKey{}, fmt.Errorf("subject: %w", err)
	}
	if err := validateName("relation", o.Relation); err != nil {
		return TupleKey{}, err
	}
	return TupleKey{
		User:     subject,
		Relation: o.Relation,
		Object:   object,
	}, nil
}

func (o ObjectRef) String() (string, error) {
	if err := validateName("type", o.Type); err != nil {
		return "", err
	}
	if err := validateID("id", o.ID); err != nil {
		return "", err
	}
	return o.Type + ":" + o.ID, nil
}

func (s SubjectRef) String() (string, error) {
	if err := validateName("type", s.Type); err != nil {
		return "", err
	}
	if err := validateID("id", s.ID); err != nil {
		return "", err
	}

	subject := s.Type + ":" + s.ID
	if s.Relation == "" {
		return subject, nil
	}
	if err := validateName("relation", s.Relation); err != nil {
		return "", err
	}
	return subject + "#" + s.Relation, nil
}

func validateName(field, value string) error {
	if !namePattern.MatchString(value) {
		return fmt.Errorf("%s must match %s", field, namePattern.String())
	}
	return nil
}

func validateID(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	if strings.ContainsAny(value, ":# \t\r\n") {
		return fmt.Errorf("%s must not contain ':', '#', or whitespace", field)
	}
	return nil
}
