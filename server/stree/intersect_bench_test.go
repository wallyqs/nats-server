// Copyright 2023-2025 The NATS Authors
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

package stree

import (
	"fmt"
	"testing"

	"github.com/nats-io/nats-server/v2/server/gsl"
)

// BenchmarkIntersectGSL benchmarks the subject tree / sublist intersection.
func BenchmarkIntersectGSL(b *testing.B) {
	// Setup: create a subject tree with various subjects
	scenarios := []struct {
		name     string
		subjects []string
		patterns []string
	}{
		{
			name: "SmallLiterals",
			subjects: []string{
				"one.two.three.four",
				"one.two.three.five",
				"one.two.six",
				"one.two.seven",
				"eight.nine",
				"stream.A",
				"stream.A.child",
			},
			patterns: []string{
				"one.two.six",
				"eight.nine",
			},
		},
		{
			name: "SmallPWC",
			subjects: []string{
				"one.two.three.four",
				"one.two.three.five",
				"one.two.six",
				"one.two.seven",
				"eight.nine",
				"stream.A",
				"stream.A.child",
			},
			patterns: []string{
				"one.two.*.*",
			},
		},
		{
			name: "SmallPWCOverlapping",
			subjects: []string{
				"one.two.three.four",
				"one.two.three.five",
				"one.two.six",
				"one.two.seven",
				"eight.nine",
				"stream.A",
				"stream.A.child",
			},
			patterns: []string{
				"one.two.*.four",
				"one.two.*.*",
			},
		},
		{
			name: "SmallFWC",
			subjects: []string{
				"one.two.three.four",
				"one.two.three.five",
				"one.two.six",
				"one.two.seven",
				"eight.nine",
				"stream.A",
				"stream.A.child",
			},
			patterns: []string{
				"one.>",
			},
		},
		{
			name: "SmallMixed",
			subjects: []string{
				"one.two.three.four",
				"one.two.three.five",
				"one.two.six",
				"one.two.seven",
				"eight.nine",
				"stream.A",
				"stream.A.child",
			},
			patterns: []string{
				"stream.*.child",
				"stream.A",
			},
		},
		{
			name: "SmallDisjointWildcards",
			subjects: []string{
				"one.two.three.four",
				"one.two.three.five",
				"one.two.six",
				"one.two.seven",
				"eight.nine",
				"stream.A",
				"stream.A.child",
			},
			patterns: []string{
				"one.two.*.*",
				"*.two.three.four",
			},
		},
		{
			name: "SmallAggressiveOverlap",
			subjects: []string{
				"one.two.three.four",
				"one.two.three.five",
				"one.two.six",
				"one.two.seven",
				"eight.nine",
				"stream.A",
				"stream.A.child",
			},
			patterns: []string{
				"stream.A.child",
				"*.A.child",
				"stream.*.child",
				"stream.A.*",
				"stream.*.*",
				"*.A.*",
				"*.*.child",
			},
		},
		{
			name: "MediumMixed",
			subjects: func() []string {
				var subjs []string
				for i := 0; i < 100; i++ {
					subjs = append(subjs, fmt.Sprintf("orders.%d.created", i))
					subjs = append(subjs, fmt.Sprintf("orders.%d.updated", i))
					subjs = append(subjs, fmt.Sprintf("orders.%d.deleted", i))
				}
				return subjs
			}(),
			patterns: []string{
				"orders.*.created",
				"orders.50.>",
				"orders.99.deleted",
			},
		},
		{
			name: "LargeFWC",
			subjects: func() []string {
				var subjs []string
				for i := 0; i < 1000; i++ {
					subjs = append(subjs, fmt.Sprintf("events.region%d.service%d.action%d", i%10, i%50, i))
				}
				return subjs
			}(),
			patterns: []string{
				"events.region0.>",
			},
		},
		{
			name: "LargePWCMultiple",
			subjects: func() []string {
				var subjs []string
				for i := 0; i < 1000; i++ {
					subjs = append(subjs, fmt.Sprintf("events.region%d.service%d.action%d", i%10, i%50, i))
				}
				return subjs
			}(),
			patterns: []string{
				"events.*.service0.*",
				"events.*.service1.*",
				"events.*.service2.*",
			},
		},
		{
			name: "LargeOverlappingPatterns",
			subjects: func() []string {
				var subjs []string
				for i := 0; i < 1000; i++ {
					subjs = append(subjs, fmt.Sprintf("events.region%d.service%d.action%d", i%10, i%50, i))
				}
				return subjs
			}(),
			patterns: []string{
				"events.region0.service0.action0",
				"events.region0.service0.*",
				"events.region0.*.*",
				"events.*.service0.*",
				"*.region0.service0.*",
			},
		},
	}

	for _, sc := range scenarios {
		b.Run(sc.name, func(b *testing.B) {
			// Build the subject tree
			st := NewSubjectTree[int]()
			for i, subj := range sc.subjects {
				st.Insert([]byte(subj), i)
			}

			// Build the sublist
			sl := gsl.NewSublist[int]()
			for i, pat := range sc.patterns {
				sl.Insert(pat, i)
			}

			b.ResetTimer()
			b.ReportAllocs()

			for i := 0; i < b.N; i++ {
				count := 0
				IntersectGSL(st, sl, func(subj []byte, val *int) {
					count++
				})
			}
		})
	}
}

// BenchmarkIntersectGSLParallel benchmarks the intersection with concurrent access.
func BenchmarkIntersectGSLParallel(b *testing.B) {
	// Build a medium-sized subject tree
	st := NewSubjectTree[int]()
	for i := 0; i < 500; i++ {
		st.Insert([]byte(fmt.Sprintf("orders.%d.created", i)), i)
		st.Insert([]byte(fmt.Sprintf("orders.%d.updated", i)), i)
		st.Insert([]byte(fmt.Sprintf("orders.%d.deleted", i)), i)
	}

	// Build a sublist with overlapping patterns
	sl := gsl.NewSublist[int]()
	sl.Insert("orders.*.created", 1)
	sl.Insert("orders.*.updated", 2)
	sl.Insert("orders.100.>", 3)
	sl.Insert("orders.200.>", 4)

	b.ResetTimer()
	b.ReportAllocs()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			count := 0
			IntersectGSL(st, sl, func(subj []byte, val *int) {
				count++
			})
		}
	})
}
