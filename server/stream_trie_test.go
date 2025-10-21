package server

import (
	"testing"
)

func TestJsHdrTrieMatching(t *testing.T) {
	// Test all header keys we care about
	tests := []struct {
		key   string
		field jsHdrField
	}{
		{JSMsgId, jsHdrFieldMsgId},
		{JSExpectedStream, jsHdrFieldExpStream},
		{JSExpectedLastSeq, jsHdrFieldExpLastSeq},
		{JSExpectedLastSubjSeq, jsHdrFieldExpLastSubjSeq},
		{JSExpectedLastSubjSeqSubj, jsHdrFieldExpLastSubjSeqSubj},
		{JSExpectedLastMsgId, jsHdrFieldExpLastMsgId},
		{JSMsgRollup, jsHdrFieldRollup},
		{JSMessageTTL, jsHdrFieldTTL},
		{JSMessageIncr, jsHdrFieldIncr},
		{JSBatchId, jsHdrFieldBatchId},
		{JSBatchSeq, jsHdrFieldBatchSeq},
		{JSBatchCommit, jsHdrFieldBatchCommit},
		{JSSchedulePattern, jsHdrFieldSchedPattern},
		{JSScheduleTTL, jsHdrFieldSchedTtl},
		{JSScheduleTarget, jsHdrFieldSchedTarget},
		{JSScheduler, jsHdrFieldScheduler},
		{JSScheduleNext, jsHdrFieldSchedNext},
		{JSRequiredApiLevel, jsHdrFieldReqApiLevel},
	}

	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			result := matchHdrKey([]byte(tt.key))
			if result != tt.field {
				t.Errorf("matchHdrKey(%q) = %v, want %v", tt.key, result, tt.field)
			}
		})
	}

	// Test unknown keys
	unknownKeys := []string{
		"Unknown-Header",
		"Nats-Unknown",
		"Nats-Msg-Id-Extra",
		"",
		"X",
	}

	for _, key := range unknownKeys {
		t.Run("unknown_"+key, func(t *testing.T) {
			result := matchHdrKey([]byte(key))
			if result != jsHdrFieldNone {
				t.Errorf("matchHdrKey(%q) = %v, want jsHdrFieldNone", key, result)
			}
		})
	}
}

func TestIndexJsHdrWithTrie(t *testing.T) {
	// Test that indexJsHdr correctly uses the trie to index headers
	hdr := []byte("NATS/1.0\r\n" +
		"Nats-Msg-Id: test123\r\n" +
		"Nats-Expected-Stream: mystream\r\n" +
		"Nats-Rollup: subject\r\n" +
		"\r\n")

	_, idx := indexJsHdr(hdr)
	if idx == nil {
		t.Fatal("Expected non-nil index")
	}

	if string(idx.msgId) != "test123" {
		t.Errorf("msgId = %q, want %q", idx.msgId, "test123")
	}

	if string(idx.expStream) != "mystream" {
		t.Errorf("expStream = %q, want %q", idx.expStream, "mystream")
	}

	if string(idx.rollup) != "subject" {
		t.Errorf("rollup = %q, want %q", idx.rollup, "subject")
	}
}
