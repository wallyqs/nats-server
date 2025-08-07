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
	"strings"
	"testing"
)

// Current approach - switch with string prefix checks
func (js *jetStream) getAPISubjectPatternCurrent(subject string) string {
	switch {
	case subject == JSApiAccountInfo:
		return JSApiAccountInfo
	case strings.HasPrefix(subject, "$JS.API.STREAM.CREATE."):
		return JSApiStreamCreate
	case strings.HasPrefix(subject, "$JS.API.STREAM.UPDATE."):
		return JSApiStreamUpdate
	case subject == JSApiStreams:
		return JSApiStreams
	case subject == JSApiStreamList:
		return JSApiStreamList
	case strings.HasPrefix(subject, "$JS.API.STREAM.INFO."):
		return JSApiStreamInfo
	case strings.HasPrefix(subject, "$JS.API.STREAM.DELETE."):
		return JSApiStreamDelete
	case strings.HasPrefix(subject, "$JS.API.STREAM.PURGE."):
		return JSApiStreamPurge
	case strings.HasPrefix(subject, "$JS.API.STREAM.SNAPSHOT."):
		return JSApiStreamSnapshot
	case strings.HasPrefix(subject, "$JS.API.STREAM.RESTORE."):
		return JSApiStreamRestore
	case strings.HasPrefix(subject, "$JS.API.STREAM.MSG.DELETE."):
		return JSApiMsgDelete
	case strings.HasPrefix(subject, "$JS.API.STREAM.MSG.GET."):
		return JSApiMsgGet
	case strings.HasPrefix(subject, "$JS.API.DIRECT.GET."):
		return JSDirectMsgGet
	case strings.HasPrefix(subject, "$JS.API.CONSUMER.CREATE."):
		return JSApiConsumerCreate
	case strings.HasPrefix(subject, "$JS.API.CONSUMER.DURABLE.CREATE."):
		return JSApiDurableCreate
	case strings.HasPrefix(subject, "$JS.API.CONSUMER.NAMES."):
		return JSApiConsumers
	case strings.HasPrefix(subject, "$JS.API.CONSUMER.LIST."):
		return JSApiConsumerList
	case strings.HasPrefix(subject, "$JS.API.CONSUMER.INFO."):
		return JSApiConsumerInfo
	case strings.HasPrefix(subject, "$JS.API.CONSUMER.DELETE."):
		return JSApiConsumerDelete
	case strings.HasPrefix(subject, "$JS.API.CONSUMER.PAUSE."):
		return JSApiConsumerPause
	case strings.HasPrefix(subject, "$JS.API.CONSUMER.UNPIN."):
		return JSApiConsumerUnpin
	case strings.HasPrefix(subject, "$JS.API.STREAM.TEMPLATE.CREATE."):
		return JSApiTemplateCreate
	case subject == JSApiTemplates:
		return JSApiTemplates
	case strings.HasPrefix(subject, "$JS.API.STREAM.TEMPLATE.INFO."):
		return JSApiTemplateInfo
	case strings.HasPrefix(subject, "$JS.API.STREAM.TEMPLATE.DELETE."):
		return JSApiTemplateDelete
	case strings.HasPrefix(subject, "$JS.API.STREAM.PEER.REMOVE."):
		return JSApiStreamRemovePeer
	case strings.HasPrefix(subject, "$JS.API.STREAM.LEADER.STEPDOWN."):
		return JSApiStreamLeaderStepDown
	case strings.HasPrefix(subject, "$JS.API.CONSUMER.LEADER.STEPDOWN."):
		return JSApiConsumerLeaderStepDown
	default:
		return "unknown"
	}
}

// Optimized approach using string parsing and early returns
func (js *jetStream) getAPISubjectPatternOptimized(subject string) string {
	// Quick length and prefix checks
	if len(subject) < 10 || !strings.HasPrefix(subject, "$JS.API.") {
		return "unknown"
	}

	// Skip "$JS.API." (8 chars)
	rest := subject[8:]

	// Parse the next component
	switch {
	case rest == "INFO":
		return JSApiAccountInfo
	case strings.HasPrefix(rest, "STREAM."):
		return js.parseStreamAPI(rest[7:]) // Skip "STREAM."
	case strings.HasPrefix(rest, "CONSUMER."):
		return js.parseConsumerAPI(rest[9:]) // Skip "CONSUMER."
	case strings.HasPrefix(rest, "DIRECT.GET."):
		return JSDirectMsgGet
	default:
		return "unknown"
	}
}

// Map-based approach - precompute exact matches
var apiSubjectPatternsMap = map[string]string{
	"$JS.API.INFO":                  JSApiAccountInfo,
	"$JS.API.STREAM.NAMES":          JSApiStreams,
	"$JS.API.STREAM.LIST":           JSApiStreamList,
	"$JS.API.STREAM.TEMPLATE.NAMES": JSApiTemplates,
}

// Prefix patterns for efficient lookup
var apiPrefixPatterns = []struct {
	prefix  string
	pattern string
}{
	{"$JS.API.STREAM.CREATE.", JSApiStreamCreate},
	{"$JS.API.STREAM.UPDATE.", JSApiStreamUpdate},
	{"$JS.API.STREAM.INFO.", JSApiStreamInfo},
	{"$JS.API.STREAM.DELETE.", JSApiStreamDelete},
	{"$JS.API.STREAM.PURGE.", JSApiStreamPurge},
	{"$JS.API.STREAM.SNAPSHOT.", JSApiStreamSnapshot},
	{"$JS.API.STREAM.RESTORE.", JSApiStreamRestore},
	{"$JS.API.STREAM.MSG.DELETE.", JSApiMsgDelete},
	{"$JS.API.STREAM.MSG.GET.", JSApiMsgGet},
	{"$JS.API.DIRECT.GET.", JSDirectMsgGet},
	{"$JS.API.CONSUMER.CREATE.", JSApiConsumerCreate},
	{"$JS.API.CONSUMER.DURABLE.CREATE.", JSApiDurableCreate},
	{"$JS.API.CONSUMER.NAMES.", JSApiConsumers},
	{"$JS.API.CONSUMER.LIST.", JSApiConsumerList},
	{"$JS.API.CONSUMER.INFO.", JSApiConsumerInfo},
	{"$JS.API.CONSUMER.DELETE.", JSApiConsumerDelete},
	{"$JS.API.CONSUMER.PAUSE.", JSApiConsumerPause},
	{"$JS.API.CONSUMER.UNPIN.", JSApiConsumerUnpin},
	{"$JS.API.STREAM.TEMPLATE.CREATE.", JSApiTemplateCreate},
	{"$JS.API.STREAM.TEMPLATE.INFO.", JSApiTemplateInfo},
	{"$JS.API.STREAM.TEMPLATE.DELETE.", JSApiTemplateDelete},
	{"$JS.API.STREAM.PEER.REMOVE.", JSApiStreamRemovePeer},
	{"$JS.API.STREAM.LEADER.STEPDOWN.", JSApiStreamLeaderStepDown},
	{"$JS.API.CONSUMER.LEADER.STEPDOWN.", JSApiConsumerLeaderStepDown},
}

func (js *jetStream) getAPISubjectPatternMap(subject string) string {
	// Check exact matches first (fastest)
	if pattern, exists := apiSubjectPatternsMap[subject]; exists {
		return pattern
	}

	// Check prefix matches
	for _, p := range apiPrefixPatterns {
		if strings.HasPrefix(subject, p.prefix) {
			return p.pattern
		}
	}

	return "unknown"
}

// Benchmark the current approach
func BenchmarkAPIPatternCurrent(b *testing.B) {
	js := &jetStream{}
	subjects := []string{
		"$JS.API.STREAM.CREATE.mystream",
		"$JS.API.CONSUMER.INFO.mystream.myconsumer",
		"$JS.API.STREAM.INFO.mystream",
		"$JS.API.INFO",
		"$JS.API.STREAM.NAMES",
		"$JS.API.CONSUMER.CREATE.mystream",
		"$JS.API.STREAM.DELETE.mystream",
		"$JS.API.DIRECT.GET.mystream",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		subject := subjects[i%len(subjects)]
		_ = js.getAPISubjectPatternCurrent(subject)
	}
}

// Benchmark the optimized approach
func BenchmarkAPIPatternOptimized(b *testing.B) {
	js := &jetStream{}
	subjects := []string{
		"$JS.API.STREAM.CREATE.mystream",
		"$JS.API.CONSUMER.INFO.mystream.myconsumer",
		"$JS.API.STREAM.INFO.mystream",
		"$JS.API.INFO",
		"$JS.API.STREAM.NAMES",
		"$JS.API.CONSUMER.CREATE.mystream",
		"$JS.API.STREAM.DELETE.mystream",
		"$JS.API.DIRECT.GET.mystream",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		subject := subjects[i%len(subjects)]
		_ = js.getAPISubjectPatternOptimized(subject)
	}
}

// Benchmark the map-based approach
func BenchmarkAPIPatternMap(b *testing.B) {
	js := &jetStream{}
	subjects := []string{
		"$JS.API.STREAM.CREATE.mystream",
		"$JS.API.CONSUMER.INFO.mystream.myconsumer",
		"$JS.API.STREAM.INFO.mystream",
		"$JS.API.INFO",
		"$JS.API.STREAM.NAMES",
		"$JS.API.CONSUMER.CREATE.mystream",
		"$JS.API.STREAM.DELETE.mystream",
		"$JS.API.DIRECT.GET.mystream",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		subject := subjects[i%len(subjects)]
		_ = js.getAPISubjectPatternMap(subject)
	}
}
