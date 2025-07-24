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

package elastic

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testData struct {
	value int
	data  []byte
}

func TestElasticPointerBasics(t *testing.T) {
	data := &testData{value: 42, data: make([]byte, 1024)}

	// Test Make
	ep := Make(data)
	if ep == nil {
		t.Fatal("Make returned nil")
	}

	// Test Value
	retrieved := ep.Value()
	if retrieved != data {
		t.Fatal("Value() returned wrong pointer")
	}
	if retrieved.value != 42 {
		t.Fatal("Retrieved data has wrong value")
	}

	// Test Strong initially false
	if ep.Strong() {
		t.Fatal("New elastic pointer should not be strong initially")
	}
}

func TestElasticPointerStrengthen(t *testing.T) {
	data := &testData{value: 100}
	ep := Make(data)

	// Strengthen
	strong := ep.Strengthen()
	if strong != data {
		t.Fatal("Strengthen returned wrong pointer")
	}
	if !ep.Strong() {
		t.Fatal("Pointer should be strong after Strengthen")
	}

	// Value should still work
	retrieved := ep.Value()
	if retrieved != data {
		t.Fatal("Value() failed after strengthen")
	}
}

func TestElasticPointerWeaken(t *testing.T) {
	data := &testData{value: 200}
	ep := Make(data)

	// Strengthen first
	ep.Strengthen()
	if !ep.Strong() {
		t.Fatal("Failed to strengthen")
	}

	// Weaken
	weakened := ep.Weaken()
	if !weakened {
		t.Fatal("Weaken should return true when weakening a strong pointer")
	}
	if ep.Strong() {
		t.Fatal("Pointer should not be strong after Weaken")
	}

	// Value should still work (until GC)
	retrieved := ep.Value()
	if retrieved != data {
		t.Fatal("Value() failed immediately after weaken")
	}

	// Weaken again should return false
	weakened = ep.Weaken()
	if weakened {
		t.Fatal("Weaken should return false when already weak")
	}
}

func TestElasticPointerSet(t *testing.T) {
	data1 := &testData{value: 1}
	data2 := &testData{value: 2}

	ep := Make(data1)

	// Verify initial value
	if ep.Value().value != 1 {
		t.Fatal("Initial value wrong")
	}

	// Set new value
	ep.Set(data2)
	retrieved := ep.Value()
	if retrieved != data2 || retrieved.value != 2 {
		t.Fatal("Set failed to update value")
	}

	// Test Set with strong reference
	ep.Strengthen()
	data3 := &testData{value: 3}
	ep.Set(data3)

	if !ep.Strong() {
		t.Fatal("Strong reference should be maintained after Set")
	}
	if ep.Value().value != 3 {
		t.Fatal("Set failed with strong reference")
	}
}

func TestElasticPointerNilHandling(t *testing.T) {
	var ep *Pointer[testData]

	// Test nil pointer
	if ep.Value() != nil {
		t.Fatal("Nil elastic pointer should return nil value")
	}
	if ep.Strong() {
		t.Fatal("Nil elastic pointer should not be strong")
	}
	if ep.Strengthen() != nil {
		t.Fatal("Nil elastic pointer Strengthen should return nil")
	}
	if ep.Weaken() {
		t.Fatal("Nil elastic pointer Weaken should return false")
	}
}

func TestElasticPointerGarbageCollection(t *testing.T) {
	// This test verifies that weak references can be collected
	createAndWeaken := func() *Pointer[testData] {
		data := &testData{
			value: 42,
			data:  make([]byte, 10*1024), // Larger allocation
		}
		ep := Make(data)
		// Don't strengthen, leave as weak reference
		return ep
	}

	ep := createAndWeaken()

	// Value should initially be available
	if ep.Value() == nil {
		t.Fatal("Value should be available initially")
	}

	// Force garbage collection multiple times
	for i := 0; i < 5; i++ {
		runtime.GC()
		runtime.GC() // Double GC to ensure cleanup
		time.Sleep(10 * time.Millisecond)
	}

	// The weak reference might be collected (not guaranteed, but possible)
	// This test mainly ensures no crashes occur
	val := ep.Value()
	t.Logf("After GC, weak reference available: %v", val != nil)

	// Test should not crash regardless of GC behavior
}

func TestElasticPointerConcurrency(t *testing.T) {
	data := &testData{value: 1000, data: make([]byte, 1024)}
	ep := Make(data)

	var wg sync.WaitGroup
	var accessCount int64
	var strengthenCount int64
	var weakenCount int64

	// Concurrent Value() calls
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				val := ep.Value()
				if val != nil && val.value == 1000 {
					atomic.AddInt64(&accessCount, 1)
				}
			}
		}()
	}

	// Concurrent Strengthen/Weaken calls
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if ep.Strong() {
					if ep.Weaken() {
						atomic.AddInt64(&weakenCount, 1)
					}
				} else {
					if ep.Strengthen() != nil {
						atomic.AddInt64(&strengthenCount, 1)
					}
				}
				time.Sleep(time.Microsecond)
			}
		}()
	}

	wg.Wait()

	t.Logf("Concurrent test results: accesses=%d, strengthens=%d, weakens=%d",
		accessCount, strengthenCount, weakenCount)

	// Should have successful accesses and state changes
	if accessCount == 0 {
		t.Fatal("No successful concurrent accesses")
	}
	if strengthenCount == 0 && weakenCount == 0 {
		t.Fatal("No successful state changes occurred")
	}
}

func TestElasticPointerStrongWeakToggle(t *testing.T) {
	data := &testData{value: 500}
	ep := Make(data)

	// Test multiple strengthen/weaken cycles
	for i := 0; i < 10; i++ {
		// Strengthen
		strong := ep.Strengthen()
		if strong == nil || strong != data {
			t.Fatalf("Strengthen failed on iteration %d", i)
		}
		if !ep.Strong() {
			t.Fatalf("Should be strong on iteration %d", i)
		}

		// Verify value access works
		val := ep.Value()
		if val != data {
			t.Fatalf("Value access failed when strong on iteration %d", i)
		}

		// Weaken
		if !ep.Weaken() {
			t.Fatalf("Weaken failed on iteration %d", i)
		}
		if ep.Strong() {
			t.Fatalf("Should not be strong after weaken on iteration %d", i)
		}

		// Value should still work immediately after weaken
		val = ep.Value()
		if val != data {
			t.Fatalf("Value access failed immediately after weaken on iteration %d", i)
		}
	}
}

func BenchmarkElasticPointerValue(b *testing.B) {
	data := &testData{value: 42, data: make([]byte, 1024)}
	ep := Make(data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		val := ep.Value()
		if val == nil {
			b.Fatal("Value returned nil")
		}
	}
}

func BenchmarkElasticPointerStrengthen(b *testing.B) {
	data := &testData{value: 42, data: make([]byte, 1024)}
	ep := Make(data)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ep.Strengthen()
		ep.Weaken()
	}
}

func BenchmarkElasticPointerConcurrentAccess(b *testing.B) {
	data := &testData{value: 42, data: make([]byte, 1024)}
	ep := Make(data)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			val := ep.Value()
			if val == nil {
				b.Fatal("Value returned nil")
			}
		}
	})
}

func BenchmarkElasticPointerToggleState(b *testing.B) {
	data := &testData{value: 42, data: make([]byte, 1024)}
	ep := Make(data)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if ep.Strong() {
				ep.Weaken()
			} else {
				ep.Strengthen()
			}
		}
	})
}
