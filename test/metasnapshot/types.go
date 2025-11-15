// Copyright 2025 The NATS Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:generate go tool cborgen -i $GOFILE

// Package metasnapshot defines a dedicated, trimmed meta snapshot model
// used for CBOR/JSON codec benchmarks. It mirrors the JSON wire shape of
// JetStream meta snapshots but is self-contained and independent of the
// server's internal types.
package metasnapshot

import (
	"encoding/json"
	"fmt"
	"time"

	cbor "github.com/delaneyj/cbor/runtime"
)

// StorageType matches the JSON "store" field: "memory" / "file".
type StorageType int

const (
	FileStorage   StorageType = 22
	MemoryStorage StorageType = 33
)

func (st StorageType) MarshalCBOR(b []byte) ([]byte, error) {
	// Encode as small int as in the cborgen examples.
	return cbor.AppendInt(b, int(st)), nil
}

func (st *StorageType) UnmarshalCBOR(b []byte) ([]byte, error) {
	v, rest, err := cbor.ReadIntBytes(b)
	if err != nil {
		return b, err
	}
	*st = StorageType(v)
	return rest, nil
}

// ClientInfo is the minimal assignment snapshot view.
type ClientInfo struct {
	Account string `json:"acc,omitempty"`
	Service string `json:"svc,omitempty"`
	Cluster string `json:"cluster,omitempty"`
}

// ForAssignmentSnap mirrors the server's forAssignmentSnap behaviour.
func (ci *ClientInfo) ForAssignmentSnap() *ClientInfo {
	if ci == nil {
		return nil
	}
	return &ClientInfo{
		Account: ci.Account,
		Service: ci.Service,
		Cluster: ci.Cluster,
	}
}

// RaftGroup placement information.
type RaftGroup struct {
	Name      string      `json:"name"`
	Peers     []string    `json:"peers"`
	Storage   StorageType `json:"store"`
	Cluster   string      `json:"cluster,omitempty"`
	Preferred string      `json:"preferred,omitempty"`
	ScaleUp   bool        `json:"scale_up,omitempty"`
}

// SequencePair tracks both consumer and stream sequence.
type SequencePair struct {
	Consumer uint64 `json:"consumer_seq"`
	Stream   uint64 `json:"stream_seq"`
}

// Pending mirrors the JSON "pending" map value.
type Pending struct {
	Sequence  uint64 `json:"sequence"`
	Timestamp int64  `json:"ts"`
}

// ConsumerState is the nested state payload.
type ConsumerState struct {
	Delivered   SequencePair         `json:"delivered"`
	AckFloor    SequencePair         `json:"ack_floor"`
	Pending     map[uint64]*Pending  `json:"pending,omitempty"`
	Redelivered map[uint64]uint64    `json:"redelivered,omitempty"`
}

// WriteableConsumerAssignment is the on-wire consumer snapshot.
type WriteableConsumerAssignment struct {
	Client     *ClientInfo    `json:"client,omitempty"`
	Created    time.Time      `json:"created"`
	Name       string         `json:"name"`
	Stream     string         `json:"stream"`
	ConfigJSON json.RawMessage `json:"consumer"`
	Group      *RaftGroup     `json:"group"`
	State      *ConsumerState `json:"state,omitempty"`
}

// WriteableStreamAssignment is the on-wire stream snapshot.
type WriteableStreamAssignment struct {
	Client     *ClientInfo                    `json:"client,omitempty"`
	Created    time.Time                      `json:"created"`
	ConfigJSON json.RawMessage                `json:"stream"`
	Group      *RaftGroup                     `json:"group"`
	Sync       string                         `json:"sync"`
	Consumers  []*WriteableConsumerAssignment `json:"consumers,omitempty"`
}

// MetaSnapshot holds the full snapshot, mirroring the server's JSON
// metaSnapshot wire shape.
type MetaSnapshot struct {
	Streams []WriteableStreamAssignment `json:"streams"`
}

const (
	DefaultNumStreams   = 200
	DefaultNumConsumers = 500
)

// BuildMetaSnapshotFixture constructs a MetaSnapshot fixture similar in
// scale and shape to the JetStream meta snapshot benchmark.
func BuildMetaSnapshotFixture(numStreams, numConsumers int) MetaSnapshot {
	if numStreams <= 0 {
		numStreams = DefaultNumStreams
	}
	if numConsumers <= 0 {
		numConsumers = DefaultNumConsumers
	}

	client := &ClientInfo{
		Account: "G",
		Service: "JS",
		Cluster: "R3S",
	}

	rg := &RaftGroup{
		Name:    "rg-meta",
		Peers:   []string{"n1", "n2", "n3"},
		Storage: MemoryStorage,
		Cluster: "R3S",
	}

	metadata := map[string]string{
		"required_api": "0",
	}

	streamsByName := make(map[string]*WriteableStreamAssignment, numStreams)
	baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < numStreams; i++ {
		streamName := fmt.Sprintf("STREAM-%d", i)
		subject := fmt.Sprintf("SUBJECT-%d", i)

		cfg := struct {
			Name     string            `json:"name"`
			Subjects []string          `json:"subjects"`
			Storage  StorageType       `json:"storage"`
			Metadata map[string]string `json:"metadata,omitempty"`
		}{
			Name:     streamName,
			Subjects: []string{subject},
			Storage:  MemoryStorage,
			Metadata: metadata,
		}
		cfgJSON, _ := json.Marshal(cfg)

		sa := &WriteableStreamAssignment{
			Client:     client.ForAssignmentSnap(),
			Created:    baseTime.Add(time.Duration(i) * time.Millisecond),
			ConfigJSON: json.RawMessage(cfgJSON),
			Group:      rg,
			Sync:       "_INBOX.meta.sync",
			Consumers:  make([]*WriteableConsumerAssignment, 0, numConsumers),
		}

		for j := 0; j < numConsumers; j++ {
			consumerName := fmt.Sprintf("CONSUMER-%d", j)
			ccfg := struct {
				Durable       string            `json:"durable"`
				MemoryStorage bool              `json:"mem_storage"`
				Metadata      map[string]string `json:"metadata,omitempty"`
			}{
				Durable:       consumerName,
				MemoryStorage: true,
				Metadata:      metadata,
			}
			ccfgJSON, _ := json.Marshal(ccfg)

			state := &ConsumerState{
				Delivered: SequencePair{
					Consumer: uint64(j + 1),
					Stream:   uint64(j + 1),
				},
				AckFloor: SequencePair{
					Consumer: uint64(j),
					Stream:   uint64(j),
				},
				Pending: map[uint64]*Pending{
					1: {
						Sequence:  uint64(j + 1),
						Timestamp: baseTime.Add(time.Duration(i*j) * time.Millisecond).UnixNano(),
					},
				},
				Redelivered: map[uint64]uint64{
					1: 2,
				},
			}

			ca := &WriteableConsumerAssignment{
				Client:     client.ForAssignmentSnap(),
				Created:    sa.Created,
				Name:       consumerName,
				Stream:     streamName,
				ConfigJSON: json.RawMessage(ccfgJSON),
				Group:      rg,
				State:      state,
			}

			sa.Consumers = append(sa.Consumers, ca)
		}

		streamsByName[streamName] = sa
	}

	streams := make([]WriteableStreamAssignment, 0, len(streamsByName))
	for _, sa := range streamsByName {
		streams = append(streams, *sa)
	}

	return MetaSnapshot{Streams: streams}
}
