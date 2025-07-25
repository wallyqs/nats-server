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
	"time"
	"weak"
)

func Make[T any](ptr *T) *Pointer[T] {
	return MakeWithMetrics(ptr, GetGlobalMetrics())
}

func MakeWithMetrics[T any](ptr *T, metrics *CacheMetrics) *Pointer[T] {
	p := &Pointer[T]{
		weak:    weak.Make(ptr),
		metrics: metrics,
	}
	if metrics != nil {
		metrics.UpdateRefCounts(0, 1) // Start as weak reference
	}
	return p
}

type Pointer[T any] struct {
	weak    weak.Pointer[T]
	strong  *T
	metrics *CacheMetrics // Optional metrics collection
}

func (e *Pointer[T]) Set(ptr *T) {
	if e == nil {
		return
	}
	e.weak = weak.Make(ptr)
	if e.strong != nil {
		e.strong = ptr
	}
}

func (e *Pointer[T]) Strengthen() *T {
	if e == nil {
		return nil
	}
	if e.strong != nil {
		return e.strong
	}

	start := time.Now()
	e.strong = e.weak.Value()
	duration := time.Since(start)

	if e.metrics != nil {
		e.metrics.RecordStrengthen(duration)
	}

	return e.strong
}

func (e *Pointer[T]) Weaken() bool {
	if e == nil {
		return false
	}
	if e.strong == nil {
		return false
	}

	start := time.Now()
	e.strong = nil
	duration := time.Since(start)

	if e.metrics != nil {
		e.metrics.RecordWeaken(duration)
	}

	return true
}

func (e *Pointer[T]) Value() *T {
	if e == nil {
		return nil
	}

	start := time.Now()
	var result *T
	if e.strong != nil {
		result = e.strong
	} else {
		result = e.weak.Value()
	}
	duration := time.Since(start)

	if e.metrics != nil {
		if result != nil {
			e.metrics.RecordHit(duration)
		} else {
			e.metrics.RecordMiss()
		}
	}

	return result
}

func (e *Pointer[T]) Strong() bool {
	if e == nil {
		return false
	}
	return e.strong != nil
}
