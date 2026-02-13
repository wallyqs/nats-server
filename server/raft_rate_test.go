// Copyright 2026 The NATS Authors
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
	"time"
)

func TestRaftRatesFirstSampleReturnsZero(t *testing.T) {
	var r raftRates
	cr, ar := r.sampleRates(100, 50)
	if cr != 0 || ar != 0 {
		t.Fatalf("Expected zero rates on first sample, got commit=%v apply=%v", cr, ar)
	}
}

func TestRaftRatesComputesRates(t *testing.T) {
	var r raftRates

	// First sample initializes state.
	r.sampleRates(100, 50)

	// Simulate time passing by backdating the sample.
	r.Lock()
	r.sampleTime = time.Now().Add(-2 * time.Second)
	r.Unlock()

	// Second sample should compute rates.
	cr, ar := r.sampleRates(200, 150)
	// 100 commits over ~2 seconds = ~50/sec
	if cr < 40 || cr > 60 {
		t.Fatalf("Expected commit rate ~50/sec, got %v", cr)
	}
	// 100 applies over ~2 seconds = ~50/sec
	if ar < 40 || ar > 60 {
		t.Fatalf("Expected apply rate ~50/sec, got %v", ar)
	}
}

func TestRaftRatesIgnoresRapidPolling(t *testing.T) {
	var r raftRates

	// First sample.
	r.sampleRates(100, 50)

	// Immediate second sample (within minRateSampleInterval) should
	// not update rates and should return the previously stored values (zero).
	cr, ar := r.sampleRates(200, 150)
	if cr != 0 || ar != 0 {
		t.Fatalf("Expected zero rates for rapid polling, got commit=%v apply=%v", cr, ar)
	}
}

func TestRaftRatesClampsOnIndexDecrease(t *testing.T) {
	var r raftRates

	// First sample at a high index.
	r.sampleRates(1000, 900)

	// Backdate the sample time.
	r.Lock()
	r.sampleTime = time.Now().Add(-2 * time.Second)
	r.Unlock()

	// Second sample with lower indices (e.g. after WAL truncation).
	cr, ar := r.sampleRates(500, 400)
	if cr != 0 || ar != 0 {
		t.Fatalf("Expected zero rates on index decrease, got commit=%v apply=%v", cr, ar)
	}
}

func TestRaftRatesUpdatesOverTime(t *testing.T) {
	var r raftRates

	// Initialize.
	r.sampleRates(0, 0)

	// Simulate a series of samples over time.
	r.Lock()
	r.sampleTime = time.Now().Add(-1 * time.Second)
	r.Unlock()

	// 10 commits, 5 applies in 1 second.
	cr, ar := r.sampleRates(10, 5)
	if cr < 8 || cr > 12 {
		t.Fatalf("Expected commit rate ~10/sec, got %v", cr)
	}
	if ar < 4 || ar > 6 {
		t.Fatalf("Expected apply rate ~5/sec, got %v", ar)
	}

	// Backdate again and sample with higher values.
	r.Lock()
	r.sampleTime = time.Now().Add(-1 * time.Second)
	r.Unlock()

	cr, ar = r.sampleRates(110, 105)
	// 100 commits, 100 applies in 1 second.
	if cr < 90 || cr > 110 {
		t.Fatalf("Expected commit rate ~100/sec, got %v", cr)
	}
	if ar < 90 || ar > 110 {
		t.Fatalf("Expected apply rate ~100/sec, got %v", ar)
	}
}
