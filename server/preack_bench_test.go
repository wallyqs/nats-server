// Copyright 2024 The NATS Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package server

import (
	"testing"
)

// Old implementation using nested maps for comparison
type oldPreAckMap map[uint64]map[*consumer]struct{}

func (m oldPreAckMap) register(o *consumer, seq uint64) {
	if o == nil {
		return
	}
	if m[seq] == nil {
		m[seq] = make(map[*consumer]struct{})
	}
	m[seq][o] = struct{}{}
}

func (m oldPreAckMap) has(o *consumer, seq uint64) bool {
	if o == nil || len(m) == 0 {
		return false
	}
	consumers := m[seq]
	if len(consumers) == 0 {
		return false
	}
	_, found := consumers[o]
	return found
}

func (m oldPreAckMap) clear(o *consumer, seq uint64) {
	if o == nil || len(m) == 0 {
		return
	}
	if consumers := m[seq]; len(consumers) > 0 {
		delete(consumers, o)
		if len(consumers) == 0 {
			delete(m, seq)
		}
	}
}

// New implementation using preAckEntry
type newPreAckMap map[uint64]preAckEntry

func (m newPreAckMap) register(o *consumer, seq uint64) {
	if o == nil {
		return
	}
	entry, exists := m[seq]
	if !exists {
		m[seq] = preAckEntry{single: o}
		return
	}
	if entry.single == o {
		return
	}
	if entry.multi != nil {
		entry.multi[o] = struct{}{}
		return
	}
	entry.multi = make(map[*consumer]struct{}, 2)
	if entry.single != nil {
		entry.multi[entry.single] = struct{}{}
	}
	entry.multi[o] = struct{}{}
	entry.single = nil
	m[seq] = entry
}

func (m newPreAckMap) has(o *consumer, seq uint64) bool {
	if o == nil || len(m) == 0 {
		return false
	}
	entry, exists := m[seq]
	if !exists {
		return false
	}
	if entry.single != nil {
		return entry.single == o
	}
	if entry.multi != nil {
		_, found := entry.multi[o]
		return found
	}
	return false
}

func (m newPreAckMap) clear(o *consumer, seq uint64) {
	if o == nil || len(m) == 0 {
		return
	}
	entry, exists := m[seq]
	if !exists {
		return
	}
	if entry.single != nil {
		if entry.single == o {
			delete(m, seq)
		}
		return
	}
	if entry.multi != nil {
		delete(entry.multi, o)
		if len(entry.multi) == 0 {
			delete(m, seq)
		}
	}
}

// Create fake consumers for benchmarking
func createFakeConsumers(n int) []*consumer {
	consumers := make([]*consumer, n)
	for i := 0; i < n; i++ {
		consumers[i] = &consumer{}
	}
	return consumers
}

// Benchmark: Single consumer per sequence (common case)
func BenchmarkPreAck_SingleConsumer_Old(b *testing.B) {
	consumers := createFakeConsumers(1)
	c := consumers[0]

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		m := make(oldPreAckMap)
		seq := uint64(i % 1000)

		m.register(c, seq)
		_ = m.has(c, seq)
		m.clear(c, seq)
	}
}

func BenchmarkPreAck_SingleConsumer_New(b *testing.B) {
	consumers := createFakeConsumers(1)
	c := consumers[0]

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		m := make(newPreAckMap)
		seq := uint64(i % 1000)

		m.register(c, seq)
		_ = m.has(c, seq)
		m.clear(c, seq)
	}
}

// Benchmark: Register many sequences with single consumer each
func BenchmarkPreAck_ManySeqs_SingleConsumer_Old(b *testing.B) {
	consumers := createFakeConsumers(1)
	c := consumers[0]
	numSeqs := 1000

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		m := make(oldPreAckMap)

		// Register
		for seq := uint64(0); seq < uint64(numSeqs); seq++ {
			m.register(c, seq)
		}

		// Check
		for seq := uint64(0); seq < uint64(numSeqs); seq++ {
			_ = m.has(c, seq)
		}

		// Clear
		for seq := uint64(0); seq < uint64(numSeqs); seq++ {
			m.clear(c, seq)
		}
	}
}

func BenchmarkPreAck_ManySeqs_SingleConsumer_New(b *testing.B) {
	consumers := createFakeConsumers(1)
	c := consumers[0]
	numSeqs := 1000

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		m := make(newPreAckMap)

		// Register
		for seq := uint64(0); seq < uint64(numSeqs); seq++ {
			m.register(c, seq)
		}

		// Check
		for seq := uint64(0); seq < uint64(numSeqs); seq++ {
			_ = m.has(c, seq)
		}

		// Clear
		for seq := uint64(0); seq < uint64(numSeqs); seq++ {
			m.clear(c, seq)
		}
	}
}

// Benchmark: Two consumers per sequence (transition case)
func BenchmarkPreAck_TwoConsumers_Old(b *testing.B) {
	consumers := createFakeConsumers(2)
	c1, c2 := consumers[0], consumers[1]

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		m := make(oldPreAckMap)
		seq := uint64(i % 1000)

		m.register(c1, seq)
		m.register(c2, seq)
		_ = m.has(c1, seq)
		_ = m.has(c2, seq)
		m.clear(c1, seq)
		m.clear(c2, seq)
	}
}

func BenchmarkPreAck_TwoConsumers_New(b *testing.B) {
	consumers := createFakeConsumers(2)
	c1, c2 := consumers[0], consumers[1]

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		m := make(newPreAckMap)
		seq := uint64(i % 1000)

		m.register(c1, seq)
		m.register(c2, seq)
		_ = m.has(c1, seq)
		_ = m.has(c2, seq)
		m.clear(c1, seq)
		m.clear(c2, seq)
	}
}

// Benchmark: Three consumers per sequence
func BenchmarkPreAck_ThreeConsumers_Old(b *testing.B) {
	consumers := createFakeConsumers(3)
	c1, c2, c3 := consumers[0], consumers[1], consumers[2]

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		m := make(oldPreAckMap)
		seq := uint64(i % 1000)

		m.register(c1, seq)
		m.register(c2, seq)
		m.register(c3, seq)
		_ = m.has(c1, seq)
		_ = m.has(c2, seq)
		_ = m.has(c3, seq)
		m.clear(c1, seq)
		m.clear(c2, seq)
		m.clear(c3, seq)
	}
}

func BenchmarkPreAck_ThreeConsumers_New(b *testing.B) {
	consumers := createFakeConsumers(3)
	c1, c2, c3 := consumers[0], consumers[1], consumers[2]

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		m := make(newPreAckMap)
		seq := uint64(i % 1000)

		m.register(c1, seq)
		m.register(c2, seq)
		m.register(c3, seq)
		_ = m.has(c1, seq)
		_ = m.has(c2, seq)
		_ = m.has(c3, seq)
		m.clear(c1, seq)
		m.clear(c2, seq)
		m.clear(c3, seq)
	}
}

// Benchmark: Mixed workload (80% single consumer, 20% two consumers)
func BenchmarkPreAck_MixedWorkload_Old(b *testing.B) {
	consumers := createFakeConsumers(2)
	c1, c2 := consumers[0], consumers[1]

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		m := make(oldPreAckMap)
		seq := uint64(i % 1000)

		m.register(c1, seq)
		if i%5 == 0 { // 20% have second consumer
			m.register(c2, seq)
		}
		_ = m.has(c1, seq)
		m.clear(c1, seq)
		if i%5 == 0 {
			m.clear(c2, seq)
		}
	}
}

func BenchmarkPreAck_MixedWorkload_New(b *testing.B) {
	consumers := createFakeConsumers(2)
	c1, c2 := consumers[0], consumers[1]

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		m := make(newPreAckMap)
		seq := uint64(i % 1000)

		m.register(c1, seq)
		if i%5 == 0 { // 20% have second consumer
			m.register(c2, seq)
		}
		_ = m.has(c1, seq)
		m.clear(c1, seq)
		if i%5 == 0 {
			m.clear(c2, seq)
		}
	}
}

// Benchmark: Register only (to isolate allocation cost)
func BenchmarkPreAck_RegisterOnly_SingleConsumer_Old(b *testing.B) {
	consumers := createFakeConsumers(1)
	c := consumers[0]
	m := make(oldPreAckMap)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		seq := uint64(i)
		m.register(c, seq)
	}
}

func BenchmarkPreAck_RegisterOnly_SingleConsumer_New(b *testing.B) {
	consumers := createFakeConsumers(1)
	c := consumers[0]
	m := make(newPreAckMap)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		seq := uint64(i)
		m.register(c, seq)
	}
}
