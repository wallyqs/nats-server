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

import "testing"

// Benchmarks for message arg processing functions to measure heap allocations.
// These functions parse incoming protocol messages and split arguments.

func BenchmarkProcessRoutedMsgArgs(b *testing.B) {
	// RMSG format: account subject [reply] size
	arg := []byte("$G foo.bar _INBOX.xxx 1024")
	c := &client{kind: ROUTER, route: &route{}}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := c.processRoutedMsgArgs(arg); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkProcessRoutedHeaderMsgArgs(b *testing.B) {
	// HMSG format: account subject [reply] headerSize totalSize
	arg := []byte("$G foo.bar 12 1024")
	c := &client{kind: ROUTER, route: &route{}}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := c.processRoutedHeaderMsgArgs(arg); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkProcessRoutedOriginClusterMsgArgs(b *testing.B) {
	// Origin cluster HMSG format: origin account subject [reply] headerSize totalSize
	arg := []byte("ORIGIN MY_ACCOUNT foo.bar 12 345")
	c := &client{kind: ROUTER, route: &route{}}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := c.processRoutedOriginClusterMsgArgs(arg); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkProcessLeafMsgArgs(b *testing.B) {
	// LMSG format: subject [reply] size
	arg := []byte("foo.bar _INBOX.xxx 1024")
	c := &client{kind: LEAF}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := c.processLeafMsgArgs(arg); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkProcessLeafHeaderMsgArgs(b *testing.B) {
	// Leaf HMSG format: subject headerSize totalSize
	arg := []byte("foo.bar 12 1024")
	c := &client{kind: LEAF}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := c.processLeafHeaderMsgArgs(arg); err != nil {
			b.Fatal(err)
		}
	}
}

// Benchmarks with queue subscribers to exercise the larger arg paths.

func BenchmarkProcessRoutedMsgArgs_Queues(b *testing.B) {
	// RMSG format with queues: account subject replyIndicator reply queue1 queue2 size
	arg := []byte("$G foo.bar + _INBOX.xxx queue1 queue2 1024")
	c := &client{kind: ROUTER, route: &route{}}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := c.processRoutedMsgArgs(arg); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkProcessRoutedHeaderMsgArgs_Queues(b *testing.B) {
	// HMSG format with queues: account subject replyIndicator reply queue1 queue2 headerSize totalSize
	arg := []byte("$G foo.bar + _INBOX.xxx queue1 queue2 12 1024")
	c := &client{kind: ROUTER, route: &route{}}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := c.processRoutedHeaderMsgArgs(arg); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkProcessLeafMsgArgs_Queues(b *testing.B) {
	// LMSG format with queues: subject replyIndicator reply queue1 queue2 size
	arg := []byte("foo.bar + _INBOX.xxx queue1 queue2 1024")
	c := &client{kind: LEAF}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if err := c.processLeafMsgArgs(arg); err != nil {
			b.Fatal(err)
		}
	}
}
