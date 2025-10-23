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
	"strings"
	"testing"
)

// TestSliceHeaderEmptyValue tests sliceHeader with empty header values
func TestSliceHeaderEmptyValue(t *testing.T) {
	hdr := []byte("NATS/1.0\r\n\r\n")
	hdr = genHeader(hdr, "Empty-Header", "")

	result := sliceHeader("Empty-Header", hdr)
	require_NotNil(t, result)
	require_Equal(t, len(result), 0)
	require_Equal(t, string(result), "")
}

// TestSliceHeaderVeryLongValue tests sliceHeader with very long values
func TestSliceHeaderVeryLongValue(t *testing.T) {
	longValue := strings.Repeat("a", 10000)
	hdr := []byte("NATS/1.0\r\n\r\n")
	hdr = genHeader(hdr, "Long-Header", longValue)

	result := sliceHeader("Long-Header", hdr)
	require_NotNil(t, result)
	require_Equal(t, string(result), longValue)
}

// TestSliceHeaderSpecialCharacters tests headers with special characters
func TestSliceHeaderSpecialCharacters(t *testing.T) {
	specialValues := []string{
		"value with spaces",
		"value-with-dashes",
		"value_with_underscores",
		"value.with.dots",
		"value/with/slashes",
		`{"json":"value"}`,
		"value\twith\ttabs", // tabs in value
		"value;with;semicolons",
	}

	for _, val := range specialValues {
		hdr := []byte("NATS/1.0\r\n\r\n")
		hdr = genHeader(hdr, "Special-Header", val)

		result := sliceHeader("Special-Header", hdr)
		require_NotNil(t, result)
		require_Equal(t, string(result), val)
	}
}

// TestSliceHeaderCaseSensitivity tests that header keys are case-sensitive
func TestSliceHeaderCaseSensitivity(t *testing.T) {
	hdr := []byte("NATS/1.0\r\n\r\n")
	hdr = genHeader(hdr, "X-Test-Header", "lowercase-key")
	hdr = genHeader(hdr, "x-test-header", "uppercase-key")

	// Should find exact match only
	result1 := sliceHeader("X-Test-Header", hdr)
	require_NotNil(t, result1)
	require_Equal(t, string(result1), "lowercase-key")

	result2 := sliceHeader("x-test-header", hdr)
	require_NotNil(t, result2)
	require_Equal(t, string(result2), "uppercase-key")

	// Should not find mismatched case
	result3 := sliceHeader("X-TEST-HEADER", hdr)
	require_True(t, result3 == nil)
}

// TestSliceHeaderNotFound tests various not-found scenarios
func TestSliceHeaderNotFound(t *testing.T) {
	hdr := []byte("NATS/1.0\r\n\r\n")
	hdr = genHeader(hdr, "Existing-Header", "value")

	testCases := []struct {
		name string
		key  string
	}{
		{"NonExistent", "Non-Existent-Header"},
		{"Empty", ""},
		{"PartialMatch", "Existing"},
		{"SuperString", "Existing-Header-Extra"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := sliceHeader(tc.key, hdr)
			require_True(t, result == nil)
		})
	}
}

// TestSliceHeaderMultipleWhitespace tests headers with multiple spaces after colon
func TestSliceHeaderMultipleWhitespace(t *testing.T) {
	// Manually construct header with multiple spaces
	hdr := []byte("NATS/1.0\r\n\r\nX-Test:    value-with-spaces\r\n\r\n")

	result := sliceHeader("X-Test", hdr)
	require_NotNil(t, result)
	// Should skip all leading spaces
	require_Equal(t, string(result), "value-with-spaces")
}

// TestSliceHeaderNoWhitespace tests headers without space after colon
func TestSliceHeaderNoWhitespace(t *testing.T) {
	// Manually construct header without space after colon
	hdr := []byte("NATS/1.0\r\n\r\nX-Test:value\r\n\r\n")

	result := sliceHeader("X-Test", hdr)
	require_NotNil(t, result)
	require_Equal(t, string(result), "value")
}

// TestGetHeaderKeyIndexEdgeCases tests edge cases in getHeaderKeyIndex
func TestGetHeaderKeyIndexEdgeCases(t *testing.T) {
	testCases := []struct {
		name     string
		hdr      []byte
		key      string
		expected int
	}{
		{
			name:     "EmptyHeader",
			hdr:      []byte{},
			key:      "Test",
			expected: -1,
		},
		{
			name:     "OnlyNatsLine",
			hdr:      []byte("NATS/1.0\r\n\r\n"),
			key:      "Test",
			expected: -1,
		},
		{
			name:     "KeyAfterNatsLine", // Valid - has preceding CRLF from NATS line
			hdr:      []byte("NATS/1.0\r\nTest: value\r\n\r\n"),
			key:      "Test",
			expected: 10, // Position of "Test" in the byte array
		},
		{
			name:     "ValidHeader",
			hdr:      []byte("NATS/1.0\r\n\r\nTest: value\r\n\r\n"),
			key:      "Test",
			expected: 12, // Position of "Test" in the byte array
		},
		{
			name:     "MissingColon",
			hdr:      []byte("NATS/1.0\r\n\r\nTest value\r\n\r\n"),
			key:      "Test",
			expected: -1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := getHeaderKeyIndex(tc.key, tc.hdr)
			require_Equal(t, result, tc.expected)
		})
	}
}

// TestSetHeaderEdgeCases tests edge cases for setHeader
func TestSetHeaderEdgeCases(t *testing.T) {
	t.Run("AddNewHeader", func(t *testing.T) {
		hdr := []byte("NATS/1.0\r\n\r\n")
		hdr = genHeader(hdr, "Existing", "old")

		hdr = setHeader("New-Header", "new-value", hdr)

		// Should have both headers
		require_NotNil(t, sliceHeader("Existing", hdr))
		require_Equal(t, string(sliceHeader("Existing", hdr)), "old")
		require_NotNil(t, sliceHeader("New-Header", hdr))
		require_Equal(t, string(sliceHeader("New-Header", hdr)), "new-value")
	})

	t.Run("UpdateExisting", func(t *testing.T) {
		hdr := []byte("NATS/1.0\r\n\r\n")
		hdr = genHeader(hdr, "Test", "old-value")

		hdr = setHeader("Test", "new-value", hdr)

		result := sliceHeader("Test", hdr)
		require_NotNil(t, result)
		require_Equal(t, string(result), "new-value")
	})

	t.Run("EmptyValue", func(t *testing.T) {
		hdr := []byte("NATS/1.0\r\n\r\n")
		hdr = genHeader(hdr, "Test", "old-value")

		hdr = setHeader("Test", "", hdr)

		result := sliceHeader("Test", hdr)
		require_NotNil(t, result)
		require_Equal(t, string(result), "")
	})

	t.Run("PreserveWhitespace", func(t *testing.T) {
		// Test that setHeader preserves the whitespace style
		hdr := []byte("NATS/1.0\r\n\r\nTest: old\r\n\r\n")
		hdr = setHeader("Test", "new", hdr)
		// Should still have space after colon
		require_True(t, bytes.Contains(hdr, []byte("Test: new")))

		hdr = []byte("NATS/1.0\r\n\r\nTest:old\r\n\r\n")
		hdr = setHeader("Test", "new", hdr)
		// Should preserve no-space style
		require_True(t, bytes.Contains(hdr, []byte("Test:new")))
	})
}

// TestRemoveHeaderIfPresentEdgeCases tests edge cases for removeHeaderIfPresent
func TestRemoveHeaderIfPresentEdgeCases(t *testing.T) {
	t.Run("RemoveOnlyHeader", func(t *testing.T) {
		hdr := []byte("NATS/1.0\r\n\r\n")
		hdr = genHeader(hdr, "Only-Header", "value")

		hdr = removeHeaderIfPresent(hdr, "Only-Header")

		// Should return nil when only the NATS line remains
		require_True(t, hdr == nil)
	})

	t.Run("RemoveFirst", func(t *testing.T) {
		hdr := []byte("NATS/1.0\r\n\r\n")
		hdr = genHeader(hdr, "First", "1")
		hdr = genHeader(hdr, "Second", "2")
		hdr = genHeader(hdr, "Third", "3")

		hdr = removeHeaderIfPresent(hdr, "First")

		require_True(t, sliceHeader("First", hdr) == nil)
		require_NotNil(t, sliceHeader("Second", hdr))
		require_NotNil(t, sliceHeader("Third", hdr))
	})

	t.Run("RemoveMiddle", func(t *testing.T) {
		hdr := []byte("NATS/1.0\r\n\r\n")
		hdr = genHeader(hdr, "First", "1")
		hdr = genHeader(hdr, "Second", "2")
		hdr = genHeader(hdr, "Third", "3")

		hdr = removeHeaderIfPresent(hdr, "Second")

		require_NotNil(t, sliceHeader("First", hdr))
		require_True(t, sliceHeader("Second", hdr) == nil)
		require_NotNil(t, sliceHeader("Third", hdr))
	})

	t.Run("RemoveLast", func(t *testing.T) {
		hdr := []byte("NATS/1.0\r\n\r\n")
		hdr = genHeader(hdr, "First", "1")
		hdr = genHeader(hdr, "Second", "2")
		hdr = genHeader(hdr, "Third", "3")

		hdr = removeHeaderIfPresent(hdr, "Third")

		require_NotNil(t, sliceHeader("First", hdr))
		require_NotNil(t, sliceHeader("Second", hdr))
		require_True(t, sliceHeader("Third", hdr) == nil)
	})

	t.Run("RemoveNonExistent", func(t *testing.T) {
		hdr := []byte("NATS/1.0\r\n\r\n")
		hdr = genHeader(hdr, "Existing", "value")

		originalLen := len(hdr)
		hdr = removeHeaderIfPresent(hdr, "NonExistent")

		// Should not modify header
		require_Equal(t, len(hdr), originalLen)
		require_NotNil(t, sliceHeader("Existing", hdr))
	})
}

// TestHeaderFunctionsWithInvalidInput tests all functions with malformed headers
func TestHeaderFunctionsWithInvalidInput(t *testing.T) {
	t.Run("MissingCRLF", func(t *testing.T) {
		hdr := []byte("NATS/1.0\r\n\r\nTest:value")

		// sliceHeader should handle gracefully
		result := sliceHeader("Test", hdr)
		require_NotNil(t, result)
		require_Equal(t, string(result), "value")
	})

	t.Run("OnlyLF", func(t *testing.T) {
		// Headers should have CRLF, not just LF
		hdr := []byte("NATS/1.0\n\nTest:value\n")

		// Should not find header (requires CRLF)
		result := sliceHeader("Test", hdr)
		require_True(t, result == nil)
	})

	t.Run("ExtraColons", func(t *testing.T) {
		hdr := []byte("NATS/1.0\r\n\r\nTest: value:with:colons\r\n\r\n")

		result := sliceHeader("Test", hdr)
		require_NotNil(t, result)
		require_Equal(t, string(result), "value:with:colons")
	})
}

// TestHeaderInfixMatching tests that we don't match keys in the middle of other keys
func TestHeaderInfixMatching(t *testing.T) {
	hdr := []byte("NATS/1.0\r\n\r\n")
	hdr = genHeader(hdr, "X-My-Test-Header", "value1")
	hdr = genHeader(hdr, "Test", "value2")

	// Should find exact "Test" header, not substring in "X-My-Test-Header"
	result := sliceHeader("Test", hdr)
	require_NotNil(t, result)
	require_Equal(t, string(result), "value2")
}

// TestGetHeaderVsSliceHeader ensures getHeader and sliceHeader return equivalent data
func TestGetHeaderVsSliceHeader(t *testing.T) {
	hdr := []byte("NATS/1.0\r\n\r\n")
	hdr = genHeader(hdr, "a", "1")
	hdr = genHeader(hdr, JSExpectedStream, "my-stream")
	hdr = genHeader(hdr, JSExpectedLastSeq, "22")
	hdr = genHeader(hdr, "b", "2")
	hdr = genHeader(hdr, JSExpectedLastSubjSeq, "24")

	keys := []string{"a", JSExpectedStream, JSExpectedLastSeq, "b", JSExpectedLastSubjSeq, "NotFound"}

	for _, key := range keys {
		sliced := sliceHeader(key, hdr)
		copied := getHeader(key, hdr)

		if sliced == nil {
			require_True(t, copied == nil)
		} else {
			require_NotNil(t, copied)
			require_True(t, bytes.Equal(sliced, copied))
			// Verify getHeader actually copied (different capacity)
			if len(sliced) > 0 {
				require_Equal(t, cap(copied), len(copied))
			}
		}
	}
}
