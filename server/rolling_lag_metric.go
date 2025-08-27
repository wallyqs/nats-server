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
	"sync"
	"time"
)

const (
	// Default window size for rolling average (5 minutes)
	DefaultLagMetricWindowSize = 5 * time.Minute
	// Default sample interval (30 seconds)
	DefaultLagMetricSampleInterval = 30 * time.Second
	// Maximum number of samples to keep in memory
	MaxLagMetricSamples = 20
)

// LagSample represents a single lag measurement
type LagSample struct {
	Timestamp time.Time
	Lag       time.Duration // Time-based lag (age of oldest unprocessed message)
	Count     uint64        // Number of pending messages at this sample
}

// RollingLagMetric tracks rolling average of message lag
type RollingLagMetric struct {
	mu                sync.RWMutex
	samples           []LagSample
	windowSize        time.Duration
	sampleInterval    time.Duration
	lastSampleTime    time.Time
	
	// Cached values to avoid recalculation
	cachedAvgLag      time.Duration
	cachedMaxLag      time.Duration
	cachedMinLag      time.Duration
	cacheValidUntil   time.Time
}

// NewRollingLagMetric creates a new rolling lag metric tracker
func NewRollingLagMetric(windowSize, sampleInterval time.Duration) *RollingLagMetric {
	if windowSize == 0 {
		windowSize = DefaultLagMetricWindowSize
	}
	if sampleInterval == 0 {
		sampleInterval = DefaultLagMetricSampleInterval
	}
	
	return &RollingLagMetric{
		samples:        make([]LagSample, 0, MaxLagMetricSamples),
		windowSize:     windowSize,
		sampleInterval: sampleInterval,
	}
}

// AddSample adds a new lag sample and maintains the rolling window
func (rlm *RollingLagMetric) AddSample(lag time.Duration, pendingCount uint64) {
	rlm.mu.Lock()
	defer rlm.mu.Unlock()
	
	now := time.Now()
	
	// Check if we should add a new sample based on interval
	if now.Sub(rlm.lastSampleTime) < rlm.sampleInterval {
		return
	}
	
	// Add new sample
	sample := LagSample{
		Timestamp: now,
		Lag:       lag,
		Count:     pendingCount,
	}
	
	rlm.samples = append(rlm.samples, sample)
	rlm.lastSampleTime = now
	
	// Remove samples outside the window
	cutoff := now.Add(-rlm.windowSize)
	validStart := 0
	for i, s := range rlm.samples {
		if s.Timestamp.After(cutoff) {
			validStart = i
			break
		}
	}
	
	if validStart > 0 {
		rlm.samples = rlm.samples[validStart:]
	}
	
	// Limit the number of samples to prevent unbounded growth
	if len(rlm.samples) > MaxLagMetricSamples {
		excess := len(rlm.samples) - MaxLagMetricSamples
		rlm.samples = rlm.samples[excess:]
	}
	
	// Invalidate cache
	rlm.cacheValidUntil = time.Time{}
}

// GetAverageLag returns the rolling average lag over the window
func (rlm *RollingLagMetric) GetAverageLag() time.Duration {
	rlm.mu.RLock()
	defer rlm.mu.RUnlock()
	
	if len(rlm.samples) == 0 {
		return 0
	}
	
	// Check if cached value is still valid
	if !rlm.cacheValidUntil.IsZero() && time.Now().Before(rlm.cacheValidUntil) {
		return rlm.cachedAvgLag
	}
	
	// Calculate average
	var total time.Duration
	validSamples := 0
	cutoff := time.Now().Add(-rlm.windowSize)
	
	for _, sample := range rlm.samples {
		if sample.Timestamp.After(cutoff) {
			total += sample.Lag
			validSamples++
		}
	}
	
	if validSamples == 0 {
		return 0
	}
	
	avg := total / time.Duration(validSamples)
	
	// Cache the result for 1 second to avoid frequent recalculation
	rlm.cachedAvgLag = avg
	rlm.cacheValidUntil = time.Now().Add(time.Second)
	
	return avg
}

// GetMaxLag returns the maximum lag over the window
func (rlm *RollingLagMetric) GetMaxLag() time.Duration {
	rlm.mu.RLock()
	defer rlm.mu.RUnlock()
	
	if len(rlm.samples) == 0 {
		return 0
	}
	
	var maxLag time.Duration
	cutoff := time.Now().Add(-rlm.windowSize)
	
	for _, sample := range rlm.samples {
		if sample.Timestamp.After(cutoff) && sample.Lag > maxLag {
			maxLag = sample.Lag
		}
	}
	
	return maxLag
}

// GetMinLag returns the minimum lag over the window
func (rlm *RollingLagMetric) GetMinLag() time.Duration {
	rlm.mu.RLock()
	defer rlm.mu.RUnlock()
	
	if len(rlm.samples) == 0 {
		return 0
	}
	
	minLag := time.Duration(^uint64(0) >> 1) // max duration
	cutoff := time.Now().Add(-rlm.windowSize)
	validSamples := 0
	
	for _, sample := range rlm.samples {
		if sample.Timestamp.After(cutoff) {
			if sample.Lag < minLag {
				minLag = sample.Lag
			}
			validSamples++
		}
	}
	
	if validSamples == 0 {
		return 0
	}
	
	return minLag
}

// GetLagMetrics returns comprehensive lag metrics
type LagMetrics struct {
	AverageLag    time.Duration `json:"average_lag"`
	MaxLag        time.Duration `json:"max_lag"`
	MinLag        time.Duration `json:"min_lag"`
	SampleCount   int           `json:"sample_count"`
	WindowSize    time.Duration `json:"window_size"`
	LastSample    time.Time     `json:"last_sample"`
	CurrentLag    time.Duration `json:"current_lag,omitempty"`
	PendingCount  uint64        `json:"pending_count,omitempty"`
}

func (rlm *RollingLagMetric) GetLagMetrics() LagMetrics {
	rlm.mu.RLock()
	defer rlm.mu.RUnlock()
	
	metrics := LagMetrics{
		AverageLag:  rlm.GetAverageLag(),
		MaxLag:      rlm.GetMaxLag(),
		MinLag:      rlm.GetMinLag(),
		SampleCount: len(rlm.samples),
		WindowSize:  rlm.windowSize,
	}
	
	if len(rlm.samples) > 0 {
		lastSample := rlm.samples[len(rlm.samples)-1]
		metrics.LastSample = lastSample.Timestamp
		metrics.CurrentLag = lastSample.Lag
		metrics.PendingCount = lastSample.Count
	}
	
	return metrics
}

// Reset clears all samples
func (rlm *RollingLagMetric) Reset() {
	rlm.mu.Lock()
	defer rlm.mu.Unlock()
	
	rlm.samples = rlm.samples[:0]
	rlm.cacheValidUntil = time.Time{}
}

// ConsumerLagTracker extends consumer with lag tracking capability
type ConsumerLagTracker struct {
	lagMetric *RollingLagMetric
	enabled   bool
}

// NewConsumerLagTracker creates a new consumer lag tracker
func NewConsumerLagTracker() *ConsumerLagTracker {
	return &ConsumerLagTracker{
		lagMetric: NewRollingLagMetric(DefaultLagMetricWindowSize, DefaultLagMetricSampleInterval),
		enabled:   true,
	}
}

// CalculateTimeLag calculates time-based lag for a consumer
// by looking at the timestamp of the oldest unprocessed message
func (clt *ConsumerLagTracker) CalculateTimeLag(o *consumer) time.Duration {
	if !clt.enabled || o == nil || o.mset == nil {
		return 0
	}
	
	// Get the sequence of the next message to be delivered
	nextSeq := o.sseq
	if nextSeq == 0 {
		return 0
	}
	
	// Try to get the message at this sequence
	sm, err := o.mset.store.LoadMsg(nextSeq, nil)
	if err != nil || sm == nil {
		return 0
	}
	
	// Calculate lag based on message timestamp
	msgTime := time.Unix(0, sm.ts)
	lag := time.Since(msgTime)
	
	// Ensure lag is not negative (clock skew protection)
	if lag < 0 {
		lag = 0
	}
	
	return lag
}

// UpdateLagMetric updates the rolling lag metric for a consumer
func (clt *ConsumerLagTracker) UpdateLagMetric(o *consumer) {
	if !clt.enabled {
		return
	}
	
	lag := clt.CalculateTimeLag(o)
	pendingCount := o.numPending()
	
	clt.lagMetric.AddSample(lag, pendingCount)
}

// GetLagMetrics returns the current lag metrics
func (clt *ConsumerLagTracker) GetLagMetrics() LagMetrics {
	if !clt.enabled {
		return LagMetrics{}
	}
	return clt.lagMetric.GetLagMetrics()
}

// Enable enables lag tracking
func (clt *ConsumerLagTracker) Enable() {
	clt.enabled = true
}

// Disable disables lag tracking
func (clt *ConsumerLagTracker) Disable() {
	clt.enabled = false
}

// IsEnabled returns whether lag tracking is enabled
func (clt *ConsumerLagTracker) IsEnabled() bool {
	return clt.enabled
}