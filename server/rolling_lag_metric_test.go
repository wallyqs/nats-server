// Copyright 2025 The NATS Authors
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

func TestRollingLagMetric(t *testing.T) {
	// Create a rolling lag metric with small window for testing
	rlm := NewRollingLagMetric(1*time.Minute, 100*time.Millisecond)
	
	// Test that initial values are zero
	if avg := rlm.GetAverageLag(); avg != 0 {
		t.Fatalf("Expected initial average lag to be 0, got %v", avg)
	}
	
	// Add some samples
	rlm.AddSample(100*time.Millisecond, 10)
	time.Sleep(150 * time.Millisecond) // Wait for sample interval
	rlm.AddSample(200*time.Millisecond, 20)
	time.Sleep(150 * time.Millisecond)
	rlm.AddSample(300*time.Millisecond, 30)
	
	// Test average calculation
	avg := rlm.GetAverageLag()
	expected := (100 + 200 + 300) * time.Millisecond / 3
	if avg != expected {
		t.Fatalf("Expected average lag %v, got %v", expected, avg)
	}
	
	// Test max lag
	maxLag := rlm.GetMaxLag()
	if maxLag != 300*time.Millisecond {
		t.Fatalf("Expected max lag 300ms, got %v", maxLag)
	}
	
	// Test min lag
	minLag := rlm.GetMinLag()
	if minLag != 100*time.Millisecond {
		t.Fatalf("Expected min lag 100ms, got %v", minLag)
	}
	
	// Test metrics structure
	metrics := rlm.GetLagMetrics()
	if metrics.SampleCount != 3 {
		t.Fatalf("Expected 3 samples, got %d", metrics.SampleCount)
	}
	if metrics.WindowSize != 1*time.Minute {
		t.Fatalf("Expected window size 1m, got %v", metrics.WindowSize)
	}
	if metrics.CurrentLag != 300*time.Millisecond {
		t.Fatalf("Expected current lag 300ms, got %v", metrics.CurrentLag)
	}
	if metrics.PendingCount != 30 {
		t.Fatalf("Expected pending count 30, got %d", metrics.PendingCount)
	}
}

func TestRollingLagMetricWindowCleanup(t *testing.T) {
	// Create a rolling lag metric with very short window
	rlm := NewRollingLagMetric(200*time.Millisecond, 50*time.Millisecond)
	
	// Add samples
	rlm.AddSample(100*time.Millisecond, 10)
	time.Sleep(60 * time.Millisecond)
	rlm.AddSample(200*time.Millisecond, 20)
	time.Sleep(60 * time.Millisecond)
	rlm.AddSample(300*time.Millisecond, 30)
	
	// Wait for window to expire
	time.Sleep(250 * time.Millisecond)
	
	// Add new sample after window expiration
	rlm.AddSample(400*time.Millisecond, 40)
	
	// Should only have the latest sample
	avg := rlm.GetAverageLag()
	if avg != 400*time.Millisecond {
		t.Fatalf("Expected average lag 400ms (only recent sample), got %v", avg)
	}
	
	metrics := rlm.GetLagMetrics()
	if metrics.SampleCount != 1 {
		t.Fatalf("Expected 1 sample after cleanup, got %d", metrics.SampleCount)
	}
}

func TestConsumerLagTracker(t *testing.T) {
	tracker := NewConsumerLagTracker()
	
	if !tracker.IsEnabled() {
		t.Fatal("Expected lag tracker to be enabled by default")
	}
	
	// Test enable/disable
	tracker.Disable()
	if tracker.IsEnabled() {
		t.Fatal("Expected lag tracker to be disabled")
	}
	
	tracker.Enable()
	if !tracker.IsEnabled() {
		t.Fatal("Expected lag tracker to be enabled")
	}
	
	// Test metrics when disabled
	tracker.Disable()
	metrics := tracker.GetLagMetrics()
	if metrics.SampleCount != 0 {
		t.Fatalf("Expected empty metrics when disabled, got %+v", metrics)
	}
}

func TestLagMetricsSampleInterval(t *testing.T) {
	// Create metric with longer sample interval
	rlm := NewRollingLagMetric(1*time.Minute, 200*time.Millisecond)
	
	// Add sample
	rlm.AddSample(100*time.Millisecond, 10)
	
	// Try to add another sample immediately (should be ignored)
	rlm.AddSample(200*time.Millisecond, 20)
	
	// Should still have only the first sample
	metrics := rlm.GetLagMetrics()
	if metrics.SampleCount != 1 {
		t.Fatalf("Expected 1 sample due to interval, got %d", metrics.SampleCount)
	}
	if metrics.CurrentLag != 100*time.Millisecond {
		t.Fatalf("Expected current lag 100ms, got %v", metrics.CurrentLag)
	}
	
	// Wait for interval and add another sample
	time.Sleep(250 * time.Millisecond)
	rlm.AddSample(200*time.Millisecond, 20)
	
	// Should now have 2 samples
	metrics = rlm.GetLagMetrics()
	if metrics.SampleCount != 2 {
		t.Fatalf("Expected 2 samples after interval, got %d", metrics.SampleCount)
	}
}

func TestLagMetricMaxSamples(t *testing.T) {
	// Create metric with very short interval to test max samples
	rlm := NewRollingLagMetric(10*time.Minute, 1*time.Millisecond)
	
	// Add more than MaxLagMetricSamples
	for i := 0; i < MaxLagMetricSamples+5; i++ {
		rlm.AddSample(time.Duration(i)*time.Millisecond, uint64(i))
		time.Sleep(2 * time.Millisecond) // Ensure different timestamps
	}
	
	metrics := rlm.GetLagMetrics()
	if metrics.SampleCount > MaxLagMetricSamples {
		t.Fatalf("Expected max %d samples, got %d", MaxLagMetricSamples, metrics.SampleCount)
	}
}