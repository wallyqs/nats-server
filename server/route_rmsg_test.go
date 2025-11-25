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
	"bytes"
	"fmt"
	"testing"
)

// TestProcessRoutedMsgArgsWithDoubleAtReply tests parsing RMSG with double @ in reply
// This investigates the error: processRoutedMsgArgs Bad or Missing Size
// where the args contain a reply with double @ and 's-p' as invalid size
func TestProcessRoutedMsgArgsWithDoubleAtReply(t *testing.T) {
	// This is the actual problematic arg from the logs:
	// AAAPA5K5DMZ2CWPDJDBBYBM3RIRDFPDHL6IOFSOFHM6UEQURGRMJDGKS DELIVER.dims-proxy.saptooling.eu + $JS.ACK.saptooling_eu.dimsproxy_saptooling_eu.1.13266774.12547470.1708328638063395796.1@saptooling.eu.de.2961.tool.orderconf.93810634.110.342263425@DELIVER.dims-proxy.saptooling.eu dims_proxy s-p

	// Create a mock client for testing
	c := &client{
		kind: ROUTER,
		route: &route{},
	}

	// The malformed RMSG arg from the logs
	malformedArg := []byte(`AAAPA5K5DMZ2CWPDJDBBYBM3RIRDFPDHL6IOFSOFHM6UEQURGRMJDGKS DELIVER.dims-proxy.saptooling.eu + $JS.ACK.saptooling_eu.dimsproxy_saptooling_eu.1.13266774.12547470.1708328638063395796.1@saptooling.eu.de.2961.tool.orderconf.93810634.110.342263425@DELIVER.dims-proxy.saptooling.eu dims_proxy s-p`)

	err := c.processRoutedMsgArgs(malformedArg)
	if err == nil {
		t.Fatal("Expected error for malformed RMSG args with invalid size 's-p'")
	}
	t.Logf("Got expected error: %v", err)

	// Parse the args manually to understand the structure
	args := bytes.Fields(malformedArg)
	t.Logf("Number of args: %d", len(args))
	for i, arg := range args {
		t.Logf("  args[%d] = %q", i, string(arg))
	}

	// The args should be:
	// [0] = account
	// [1] = subject
	// [2] = '+' (reply indicator)
	// [3] = reply (with double @)
	// [4] = queue name
	// [5] = size (which is 's-p' - INVALID!)

	if len(args) != 6 {
		t.Fatalf("Expected 6 args, got %d", len(args))
	}

	// Verify the reply contains double @
	reply := string(args[3])
	atCount := bytes.Count(args[3], []byte("@"))
	t.Logf("Reply subject: %s", reply)
	t.Logf("Number of @ in reply: %d", atCount)

	if atCount != 2 {
		t.Errorf("Expected 2 @ in reply, got %d", atCount)
	}

	// Verify the size is invalid
	size := string(args[5])
	if size != "s-p" {
		t.Errorf("Expected size to be 's-p', got %q", size)
	}
}

// TestValidRMSGWithReplyIndicator tests a valid RMSG with + reply indicator
func TestValidRMSGWithReplyIndicator(t *testing.T) {
	c := &client{
		kind: ROUTER,
		route: &route{},
	}

	// Valid RMSG: account subject + reply queue size
	validArg := []byte(`$G foo.bar + _INBOX.123 myqueue 42`)

	err := c.processRoutedMsgArgs(validArg)
	if err != nil {
		t.Fatalf("Unexpected error for valid RMSG: %v", err)
	}

	// Verify parsed values
	if string(c.pa.account) != "$G" {
		t.Errorf("Expected account '$G', got %q", string(c.pa.account))
	}
	if string(c.pa.subject) != "foo.bar" {
		t.Errorf("Expected subject 'foo.bar', got %q", string(c.pa.subject))
	}
	if string(c.pa.reply) != "_INBOX.123" {
		t.Errorf("Expected reply '_INBOX.123', got %q", string(c.pa.reply))
	}
	if len(c.pa.queues) != 1 || string(c.pa.queues[0]) != "myqueue" {
		t.Errorf("Expected queues ['myqueue'], got %v", c.pa.queues)
	}
	if c.pa.size != 42 {
		t.Errorf("Expected size 42, got %d", c.pa.size)
	}
}

// TestRMSGWithSingleAtInReply tests RMSG with a single @ in reply (JetStream deliver suffix)
func TestRMSGWithSingleAtInReply(t *testing.T) {
	c := &client{
		kind: ROUTER,
		route: &route{},
	}

	// RMSG with JetStream style reply containing @deliver suffix
	// Format: account subject + reply@deliver queue size
	arg := []byte(`$G DELIVER.foo + $JS.ACK.stream.consumer.1.1.1.1@DELIVER.foo myqueue 100`)

	err := c.processRoutedMsgArgs(arg)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// The reply should include the @deliver suffix
	expectedReply := "$JS.ACK.stream.consumer.1.1.1.1@DELIVER.foo"
	if string(c.pa.reply) != expectedReply {
		t.Errorf("Expected reply %q, got %q", expectedReply, string(c.pa.reply))
	}
	if c.pa.size != 100 {
		t.Errorf("Expected size 100, got %d", c.pa.size)
	}
}

// TestDoubleAtReplyConstruction shows how double @ could be constructed
func TestDoubleAtReplyConstruction(t *testing.T) {
	// Simulate the flow that could create double @

	// Original ACK reply from consumer
	originalReply := []byte("$JS.ACK.stream.consumer.1.1.1.1")

	// First delivery - append @deliver (simulating gateway send)
	firstDeliver := []byte("SOME.DELIVER.SUBJECT")
	replyWithSingleAt := make([]byte, 0, len(originalReply)+1+len(firstDeliver))
	replyWithSingleAt = append(replyWithSingleAt, originalReply...)
	replyWithSingleAt = append(replyWithSingleAt, '@')
	replyWithSingleAt = append(replyWithSingleAt, firstDeliver...)

	t.Logf("After first @ append: %s", string(replyWithSingleAt))

	// If this gets processed again and @ is appended again...
	secondDeliver := []byte("DELIVER.dims-proxy.saptooling.eu")
	replyWithDoubleAt := make([]byte, 0, len(replyWithSingleAt)+1+len(secondDeliver))
	replyWithDoubleAt = append(replyWithDoubleAt, replyWithSingleAt...)
	replyWithDoubleAt = append(replyWithDoubleAt, '@')
	replyWithDoubleAt = append(replyWithDoubleAt, secondDeliver...)

	t.Logf("After second @ append (DOUBLE @): %s", string(replyWithDoubleAt))

	// Count @ symbols
	atCount := bytes.Count(replyWithDoubleAt, []byte("@"))
	if atCount != 2 {
		t.Errorf("Expected 2 @ symbols, got %d", atCount)
	}

	// This shows the problem: if c.pa.deliver is set and the reply already has @deliver,
	// we'll get a double @ in the reply
}

// TestAppendDeliverToReplyFlow shows the code path that appends @deliver
func TestAppendDeliverToReplyFlow(t *testing.T) {
	// This simulates what happens in processInboundClientMsg
	// when c.kind == JETSTREAM and c.pa.deliver > 0 and c.pa.reply > 0

	// Scenario: A message comes in with a reply that already has @deliver from a previous hop
	existingReply := []byte("$JS.ACK.stream.consumer.1.1.1.1@FIRST.DELIVER")
	newDeliver := []byte("SECOND.DELIVER")

	// The code does:
	// reply = append(reply, '@')
	// reply = append(reply, c.pa.deliver...)

	// If existingReply is c.pa.reply and newDeliver is c.pa.deliver:
	reply := existingReply
	reply = append(reply, '@')
	reply = append(reply, newDeliver...)

	t.Logf("Result: %s", string(reply))

	atCount := bytes.Count(reply, []byte("@"))
	t.Logf("Number of @ symbols: %d", atCount)

	// The question is: under what conditions would c.pa.reply already contain @
	// and c.pa.deliver also be set, causing this double append?
}

// TestRMSGSizeParsingWithInvalidInput tests various invalid size inputs
func TestRMSGSizeParsingWithInvalidInput(t *testing.T) {
	tests := []struct {
		name     string
		arg      []byte
		wantErr  bool
		errMatch string
	}{
		{
			name:     "valid size",
			arg:      []byte(`$G foo.bar + reply queue 42`),
			wantErr:  false,
		},
		{
			name:     "size with letters",
			arg:      []byte(`$G foo.bar + reply queue abc`),
			wantErr:  true,
			errMatch: "Bad or Missing Size",
		},
		{
			name:     "size with dash",
			arg:      []byte(`$G foo.bar + reply queue s-p`),
			wantErr:  true,
			errMatch: "Bad or Missing Size",
		},
		{
			name:     "size with special chars",
			arg:      []byte(`$G foo.bar + reply queue 1@2`),
			wantErr:  true,
			errMatch: "Bad or Missing Size",
		},
		{
			name:     "empty size",
			arg:      []byte(`$G foo.bar + reply queue`),
			wantErr:  false, // queue becomes size in 4-arg case
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &client{
				kind: ROUTER,
				route: &route{},
			}

			err := c.processRoutedMsgArgs(tt.arg)
			if tt.wantErr {
				if err == nil {
					t.Error("Expected error but got nil")
				} else if tt.errMatch != "" && !bytes.Contains([]byte(err.Error()), []byte(tt.errMatch)) {
					t.Errorf("Expected error containing %q, got %v", tt.errMatch, err)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}
		})
	}
}

// TestWhereDoubleAtCouldOriginateInGateway explores gateway message sending
func TestWhereDoubleAtCouldOriginateInGateway(t *testing.T) {
	// Looking at the log message, the double @ appears in the reply:
	// $JS.ACK.saptooling_eu.dimsproxy_saptooling_eu.1.13266774.12547470.1708328638063395796.1@saptooling.eu.de.2961.tool.orderconf.93810634.110.342263425@DELIVER.dims-proxy.saptooling.eu

	// Breaking this down:
	// Base ACK: $JS.ACK.saptooling_eu.dimsproxy_saptooling_eu.1.13266774.12547470.1708328638063395796.1
	// First @: @saptooling.eu.de.2961.tool.orderconf.93810634.110.342263425
	// Second @: @DELIVER.dims-proxy.saptooling.eu

	// The first @ part looks like some kind of routing/mapping info
	// The second @ part is clearly the DELIVER subject

	baseAck := "$JS.ACK.saptooling_eu.dimsproxy_saptooling_eu.1.13266774.12547470.1708328638063395796.1"
	firstAppend := "saptooling.eu.de.2961.tool.orderconf.93810634.110.342263425"
	secondAppend := "DELIVER.dims-proxy.saptooling.eu"

	t.Logf("Base ACK subject: %s", baseAck)
	t.Logf("First @ append: %s", firstAppend)
	t.Logf("Second @ append: %s", secondAppend)

	// The first append doesn't look like a DELIVER subject
	// It looks more like: domain.route.seq.something
	// saptooling.eu (domain) . de.2961.tool.orderconf.93810634.110.342263425 (routing info?)

	// This could be from some kind of service import or export mapping
	// or from a gateway cluster hash/routing mechanism

	fullReply := fmt.Sprintf("%s@%s@%s", baseAck, firstAppend, secondAppend)
	t.Logf("Full malformed reply: %s", fullReply)
}

// TestDoubleAtAppendInProcessMsgResults documents the potential issue where
// @deliver could be appended twice if:
// 1. processMsgResults appends @deliver to reply (line 5345-5348 in client.go)
// 2. Then the gateway code also appends @deliver (line 4315-4317 in client.go)
//
// The key code paths are:
// In processInboundClientMsg:
//   - Line 4309: c.processMsgResults(acc, r, msg, c.pa.deliver, c.pa.subject, c.pa.reply, flag)
//     This calls processMsgResults with c.pa.deliver
//   - Inside processMsgResults at line 5345-5348:
//     if len(deliver) > 0 && len(reply) > 0 {
//         reply = append(reply, '@')
//         reply = append(reply, deliver...)
//     }
//     This appends @deliver to reply when sending to routes/leafnodes
//
//   - Then back in processInboundClientMsg at lines 4314-4318:
//     reply := c.pa.reply
//     if len(c.pa.deliver) > 0 && c.kind == JETSTREAM && len(c.pa.reply) > 0 {
//         reply = append(reply, '@')
//         reply = append(reply, c.pa.deliver...)
//     }
//     This ALSO appends @deliver when sending to gateways
//
// Note: Since these operate on different variables (the first modifies a local
// copy of reply, the second modifies c.pa.reply), a double @ would require
// the message to flow through multiple hops where @deliver is appended each time.
func TestDoubleAtAppendInProcessMsgResults(t *testing.T) {
	// Scenario that could cause double @:
	// 1. Server A (with JETSTREAM client) sends to routes with @deliver appended
	// 2. Server B receives the routed message with @deliver already in reply
	// 3. If Server B somehow re-processes with c.pa.deliver set, another @ would be added

	// The protection against this is that when messages come from routes,
	// processMsgResults is called with deliver=nil (see route.go:498)
	// This prevents the second @deliver append.

	// However, if there's a scenario where a routed message gets reprocessed
	// through a JETSTREAM client path (e.g., via service imports or mirrors),
	// the double @ could occur.

	t.Log("Double @ append is possible if a message flows through multiple JetStream hops")
	t.Log("Each hop that has c.pa.deliver set will append @deliver to the reply")
	t.Log("")
	t.Log("Key code paths that append @:")
	t.Log("1. client.go:5345-5348 - in processMsgResults for routes/leafnodes")
	t.Log("2. client.go:4315-4318 - in processInboundClientMsg for gateways")
	t.Log("3. client.go:4363-4366 - in handleGWReplyMap for gateways")
	t.Log("")
	t.Log("Protection: When receiving from routes, deliver is set to nil (route.go:498)")
}

// TestDoubleAtWithMirrorPushConsumerAndImports documents the specific scenario
// that can cause double @ with mirror + push consumer + service imports.
//
// The flow is:
// 1. Original stream on account A has a push consumer with deliver subject
// 2. Consumer sends message with reply=$JS.ACK...@DELIVER.subject
// 3. Message goes through a service import to account B
// 4. In processServiceImport (client.go:4792), it calls:
//    c.processMsgResults(siAcc, rr, msg, c.pa.deliver, []byte(to), nrr, flags)
//    This passes c.pa.deliver, which will append ANOTHER @deliver!
//
// The problem is that processServiceImport doesn't check if the reply
// already contains an @deliver suffix before passing c.pa.deliver.
//
// There IS extraction code at client.go:4913-4930 that extracts @deliver
// from replies for ROUTER/GATEWAY/LEAF connections, but this extraction
// happens in processMsgResults, and if processServiceImport has already
// appended another @, the reply would have double @.
func TestDoubleAtWithMirrorPushConsumerAndImports(t *testing.T) {
	// Analyzing the actual log message:
	// $JS.ACK.saptooling_eu.dimsproxy_saptooling_eu.1.13266774.12547470.1708328638063395796.1@saptooling.eu.de.2961.tool.orderconf.93810634.110.342263425@DELIVER.dims-proxy.saptooling.eu
	//
	// Breaking it down:
	// Base ACK (8 dots): $JS.ACK.saptooling_eu.dimsproxy_saptooling_eu.1.13266774.12547470.1708328638063395796.1
	// First @: saptooling.eu.de.2961.tool.orderconf.93810634.110.342263425 (not a deliver subject!)
	// Second @: DELIVER.dims-proxy.saptooling.eu (the actual deliver subject)
	//
	// The first @ content looks like it could be:
	// - A mapped subject from service import/export
	// - Some routing metadata
	// - Part of an account/domain mapping

	baseAck := "$JS.ACK.saptooling_eu.dimsproxy_saptooling_eu.1.13266774.12547470.1708328638063395796.1"
	firstAt := "saptooling.eu.de.2961.tool.orderconf.93810634.110.342263425"
	secondAt := "DELIVER.dims-proxy.saptooling.eu"

	// Count dots in baseAck - should be 8
	dotCount := bytes.Count([]byte(baseAck), []byte("."))
	t.Logf("Base ACK has %d dots (expected 8)", dotCount)

	// The extraction code at client.go:4919-4926 looks for @ AFTER 8 dots
	// So if the reply is: baseAck@firstAt@secondAt
	// It would find the first @ and extract everything after it as the subject
	// This means subj = "firstAt@secondAt" which contains @!

	fullReply := fmt.Sprintf("%s@%s@%s", baseAck, firstAt, secondAt)
	t.Logf("Full malformed reply: %s", fullReply)

	// Find first @ after 8 dots
	counter := 0
	li := bytes.IndexFunc([]byte(fullReply), func(rn rune) bool {
		if rn == '.' {
			counter++
		} else if rn == '@' {
			return counter >= 8
		}
		return false
	})

	if li != -1 {
		extractedSubj := string([]byte(fullReply)[li+1:])
		truncatedReply := string([]byte(fullReply)[:li])
		t.Logf("Extracted subject (contains @!): %s", extractedSubj)
		t.Logf("Truncated reply: %s", truncatedReply)

		// Check if extracted subject contains @
		if bytes.Contains([]byte(extractedSubj), []byte("@")) {
			t.Log("PROBLEM: Extracted subject contains @ which indicates double @ in original reply")
		}
	}

	// Key finding: processServiceImport at line 4792 passes c.pa.deliver
	// without checking if the reply already has @deliver appended.
	// This is the likely source of the double @ issue.
	t.Log("")
	t.Log("LIKELY CAUSE: processServiceImport (client.go:4792) calls")
	t.Log("  c.processMsgResults(..., c.pa.deliver, ...)")
	t.Log("without checking if reply already contains @deliver suffix.")
	t.Log("When combined with mirror + push consumer + imports, this causes double @.")
}
