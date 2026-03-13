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

package v2

import (
	"strings"
	"testing"
)

// expect is a test helper that compares lexer output against expected items.
func expect(t *testing.T, lx *lexer, items []item) {
	t.Helper()
	for i := 0; i < len(items); i++ {
		it := lx.nextItem()
		_ = it.String()
		if it.typ == ItemEOF {
			break
		}
		if it != items[i] {
			t.Fatalf("Testing: '%s'\nExpected %q, received %q\n",
				lx.input, items[i], it)
		}
		if it.typ == ItemError {
			break
		}
	}
}

// =============================================================================
// VAL-PARSER-001: All Scalar Token Types Lexed Correctly
// =============================================================================

func TestLexPlainValue(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("foo")
	expect(t, lx, expectedItems)
}

func TestLexSimpleKeyStringValues(t *testing.T) {
	// Double quotes
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "bar", 1, 7},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("foo = \"bar\"")
	expect(t, lx, expectedItems)

	// Single quotes
	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "bar", 1, 7},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo = 'bar'")
	expect(t, lx, expectedItems)

	// No spaces
	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "bar", 1, 5},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo='bar'")
	expect(t, lx, expectedItems)

	// NL (CRLF)
	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "bar", 1, 5},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo='bar'\r\n")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "bar", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo=\t'bar'\t")
	expect(t, lx, expectedItems)
}

func TestLexComplexStringValues(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "bar\\r\\n  \\t", 1, 7},
		{ItemEOF, "", 2, 0},
	}

	lx := lex("foo = 'bar\\r\\n  \\t'")
	expect(t, lx, expectedItems)
}

func TestLexSimpleKeyIntegerValues(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemInteger, "123", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("foo = 123")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemInteger, "123", 1, 4},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo=123")
	expect(t, lx, expectedItems)
	lx = lex("foo=123\r\n")
	expect(t, lx, expectedItems)
}

func TestLexSimpleKeyNegativeIntegerValues(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemInteger, "-123", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("foo = -123")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemInteger, "-123", 1, 4},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo=-123")
	expect(t, lx, expectedItems)
	lx = lex("foo=-123\r\n")
	expect(t, lx, expectedItems)
}

func TestLexSimpleKeyFloatValues(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemFloat, "22.2", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("foo = 22.2")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemFloat, "22.2", 1, 4},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo=22.2")
	expect(t, lx, expectedItems)
	lx = lex("foo=22.2\r\n")
	expect(t, lx, expectedItems)
}

// VAL-PARSER-030: Negative Float Values
func TestLexNegativeFloatValues(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemFloat, "-22.2", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("foo = -22.2")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemFloat, "-0.5", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo = -0.5")
	expect(t, lx, expectedItems)
}

func TestLexSimpleKeyBoolValues(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemBool, "true", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("foo = true")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemBool, "true", 1, 4},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo=true")
	expect(t, lx, expectedItems)
	lx = lex("foo=true\r\n")
	expect(t, lx, expectedItems)
}

func TestLexAllBoolLiterals(t *testing.T) {
	bools := []string{"true", "false", "yes", "no", "on", "off",
		"True", "False", "YES", "NO", "ON", "OFF"}
	for _, b := range bools {
		lx := lex("foo = " + b)
		it := lx.nextItem() // key
		if it.typ != ItemKey {
			t.Fatalf("Expected key, got %v for input %q", it, b)
		}
		it = lx.nextItem() // value
		if it.typ != ItemBool {
			t.Fatalf("Expected bool for %q, got %v", b, it)
		}
		if !strings.EqualFold(it.val, b) {
			t.Fatalf("Expected bool value %q, got %q", b, it.val)
		}
	}
}

func TestLexDateValues(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemDatetime, "2016-05-04T18:53:41Z", 1, 6},
		{ItemEOF, "", 1, 0},
	}

	lx := lex("foo = 2016-05-04T18:53:41Z")
	expect(t, lx, expectedItems)
}

func TestLexVariableValues(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemVariable, "bar", 1, 7},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("foo = $bar")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemVariable, "bar", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo =$bar")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemVariable, "bar", 1, 5},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo $bar")
	expect(t, lx, expectedItems)
}

func TestLexRawString(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "bar", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("foo = bar")
	expect(t, lx, expectedItems)
	lx = lex(`foo = bar' `)
	expect(t, lx, expectedItems)
}

func TestLexEmptyStringDQ(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "", 1, 7},
		{ItemEOF, "", 2, 0},
	}
	lx := lex(`foo = ""`)
	expect(t, lx, expectedItems)
}

func TestLexEmptyStringSQ(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "", 1, 7},
		{ItemEOF, "", 2, 0},
	}
	lx := lex(`foo = ''`)
	expect(t, lx, expectedItems)
}

// =============================================================================
// VAL-PARSER-001: Convenient Integer Values (Size Suffixes)
// =============================================================================

func TestLexConvenientIntegerValues(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemInteger, "1k", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("foo = 1k")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemInteger, "1K", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo = 1K")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemInteger, "1m", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo = 1m")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemInteger, "1M", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo = 1M")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemInteger, "1g", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo = 1g")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemInteger, "1G", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo = 1G")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemInteger, "1MB", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo = 1MB")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemInteger, "1Gb", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo = 1Gb")
	expect(t, lx, expectedItems)

	// Negative versions
	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemInteger, "-1m", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo = -1m")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemInteger, "-1GB", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo = -1GB ")
	expect(t, lx, expectedItems)

	// Invalid suffixes => string
	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "1Ghz", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo = 1Ghz")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "2Pie", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo = 2Pie")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "3Mbs", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo = 3Mbs,")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemInteger, "4Gb", 1, 6},
		{ItemKey, "bar", 1, 11},
		{ItemString, "5Gø", 1, 17},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo = 4Gb, bar = 5Gø")
	expect(t, lx, expectedItems)
}

// Additional size suffixes: KB/GB/TB/PB/EB, Ki/Mi/Gi/Ti/Pi/Ei, KiB/MiB/GiB/TiB/PiB/EiB
func TestLexAllSizeSuffixes(t *testing.T) {
	suffixes := []string{
		"k", "K", "m", "M", "g", "G", "t", "T", "p", "P", "e", "E",
		"KB", "MB", "GB", "TB", "PB", "EB",
		"Ki", "Mi", "Gi", "Ti", "Pi", "Ei",
		"KiB", "MiB", "GiB", "TiB", "PiB", "EiB",
		"Kb", "Mb", "Gb", "Tb", "Pb", "Eb",
	}
	for _, s := range suffixes {
		input := "foo = 1" + s
		lx := lex(input)
		it := lx.nextItem() // key
		if it.typ != ItemKey || it.val != "foo" {
			t.Fatalf("Suffix %q: expected key 'foo', got %v", s, it)
		}
		it = lx.nextItem() // value
		if it.typ != ItemInteger {
			t.Fatalf("Suffix %q: expected integer for '1%s', got %v", s, s, it)
		}
		if it.val != "1"+s {
			t.Fatalf("Suffix %q: expected value '1%s', got %q", s, s, it.val)
		}
	}
}

// =============================================================================
// VAL-PARSER-002: Key Separators and Key Lexing
// =============================================================================

func TestLexColonKeySep(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemInteger, "123", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("foo : 123")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemInteger, "123", 1, 4},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo:123")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemInteger, "123", 1, 5},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo: 123")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemInteger, "123", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo:  123\r\n")
	expect(t, lx, expectedItems)
}

func TestLexWhitespaceKeySep(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemInteger, "123", 1, 4},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("foo 123")
	expect(t, lx, expectedItems)
	lx = lex("foo 123")
	expect(t, lx, expectedItems)
	lx = lex("foo\t123")
	expect(t, lx, expectedItems)
	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemInteger, "123", 1, 5},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo\t\t123\r\n")
	expect(t, lx, expectedItems)
}

func TestLexQuotedKeys(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemInteger, "123", 1, 6},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("foo : 123")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 1},
		{ItemInteger, "123", 1, 8},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("'foo' : 123")
	expect(t, lx, expectedItems)
	lx = lex("\"foo\" : 123")
	expect(t, lx, expectedItems)
}

func TestLexQuotedKeysWithSpace(t *testing.T) {
	expectedItems := []item{
		{ItemKey, " foo", 1, 1},
		{ItemInteger, "123", 1, 9},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("' foo' : 123")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, " foo", 1, 1},
		{ItemInteger, "123", 1, 9},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("\" foo\" : 123")
	expect(t, lx, expectedItems)
}

// =============================================================================
// VAL-PARSER-003: Escape Sequences in Strings
// =============================================================================

var escString = `
foo  = \t
bar  = \r
baz  = \n
q    = \"
bs   = \\
`

func TestLexEscapedString(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 2, 1},
		{ItemString, "\t", 2, 9},
		{ItemKey, "bar", 3, 1},
		{ItemString, "\r", 3, 9},
		{ItemKey, "baz", 4, 1},
		{ItemString, "\n", 4, 9},
		{ItemKey, "q", 5, 1},
		{ItemString, "\"", 5, 9},
		{ItemKey, "bs", 6, 1},
		{ItemString, "\\", 6, 9},
		{ItemEOF, "", 6, 0},
	}
	lx := lex(escString)
	expect(t, lx, expectedItems)
}

func TestLexBinaryString(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "e", 1, 9},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("foo = \\x65")
	expect(t, lx, expectedItems)
}

func TestLexBinaryStringLatin1(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "\xe9", 1, 9},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("foo = \\xe9")
	expect(t, lx, expectedItems)
}

func TestLexCompoundStringES(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "\\end", 1, 8},
		{ItemEOF, "", 2, 0},
	}
	lx := lex(`foo = "\\end"`)
	expect(t, lx, expectedItems)
}

func TestLexCompoundStringSE(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "start\\", 1, 8},
		{ItemEOF, "", 2, 0},
	}
	lx := lex(`foo = "start\\"`)
	expect(t, lx, expectedItems)
}

func TestLexCompoundStringEE(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "Eq", 1, 12},
		{ItemEOF, "", 2, 0},
	}
	lx := lex(`foo = \x45\x71`)
	expect(t, lx, expectedItems)
}

func TestLexCompoundStringSEE(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "startEq", 1, 12},
		{ItemEOF, "", 2, 0},
	}
	lx := lex(`foo = start\x45\x71`)
	expect(t, lx, expectedItems)
}

func TestLexCompoundStringSES(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "start|end", 1, 9},
		{ItemEOF, "", 2, 0},
	}
	lx := lex(`foo = start\x7Cend`)
	expect(t, lx, expectedItems)
}

func TestLexCompoundStringEES(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "<>end", 1, 12},
		{ItemEOF, "", 2, 0},
	}
	lx := lex(`foo = \x3c\x3eend`)
	expect(t, lx, expectedItems)
}

func TestLexCompoundStringESE(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "<middle>", 1, 12},
		{ItemEOF, "", 2, 0},
	}
	lx := lex(`foo = \x3cmiddle\x3E`)
	expect(t, lx, expectedItems)
}

// Escaped backslash prefix suppresses bool detection
func TestLexNonBool(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "\\true", 1, 7},
		{ItemEOF, "", 2, 0},
	}
	lx := lex(`foo = \\true`)
	expect(t, lx, expectedItems)
}

// Escaped backslash prefix suppresses variable detection
func TestLexNonVariable(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "\\$var", 1, 7},
		{ItemEOF, "", 2, 0},
	}
	lx := lex(`foo = \\$var`)
	expect(t, lx, expectedItems)
}

// Single-quoted strings do NOT interpret escapes
func TestLexSingleQuotedNoEscape(t *testing.T) {
	// Single-quoted string preserves backslashes literally
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "bar\\r\\n  \\t", 1, 7},
		{ItemEOF, "", 2, 0},
	}
	lx := lex("foo = 'bar\\r\\n  \\t'")
	expect(t, lx, expectedItems)
}

// =============================================================================
// VAL-PARSER-004: Escape Sequence Error Reporting
// =============================================================================

func TestLexBadStringEscape(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemError, "Invalid escape character 'y'. Only the following escape characters are allowed: \\xXX, \\t, \\n, \\r, \\\", \\\\.", 1, 8},
		{ItemEOF, "", 2, 0},
	}
	lx := lex(`foo = \y`)
	expect(t, lx, expectedItems)
}

func TestLexBadBinaryStringEndingAfterZeroHexChars(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemError, "Expected two hexadecimal digits after '\\x', but hit end of line", 2, 1},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("foo = xyz\\x\n")
	expect(t, lx, expectedItems)
}

func TestLexBadBinaryStringEndingAfterOneHexChar(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemError, "Expected two hexadecimal digits after '\\x', but hit end of line", 2, 1},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("foo = xyz\\xF\n")
	expect(t, lx, expectedItems)
}

func TestLexBadBinaryStringWithZeroHexChars(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemError, "Expected two hexadecimal digits after '\\x', but got ']\"'", 1, 12},
		{ItemEOF, "", 1, 0},
	}
	lx := lex(`foo = "[\x]"`)
	expect(t, lx, expectedItems)
}

func TestLexBadBinaryStringWithOneHexChar(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemError, "Expected two hexadecimal digits after '\\x', but got 'e]'", 1, 12},
		{ItemEOF, "", 1, 0},
	}
	lx := lex(`foo = "[\xe]"`)
	expect(t, lx, expectedItems)
}

// =============================================================================
// VAL-PARSER-005: Comment Lexing
// =============================================================================

func TestLexComments(t *testing.T) {
	expectedItems := []item{
		{ItemCommentStart, "", 1, 1},
		{ItemText, " This is a comment", 1, 1},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("# This is a comment")
	expect(t, lx, expectedItems)
	lx = lex("# This is a comment\r\n")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemCommentStart, "", 1, 2},
		{ItemText, " This is a comment", 1, 2},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("// This is a comment\r\n")
	expect(t, lx, expectedItems)
}

func TestLexTopValuesWithComments(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemInteger, "123", 1, 6},
		{ItemCommentStart, "", 1, 12},
		{ItemText, " This is a comment", 1, 12},
		{ItemEOF, "", 1, 0},
	}

	lx := lex("foo = 123 // This is a comment")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemInteger, "123", 1, 4},
		{ItemCommentStart, "", 1, 12},
		{ItemText, " This is a comment", 1, 12},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo=123    # This is a comment")
	expect(t, lx, expectedItems)
}

// =============================================================================
// VAL-PARSER-006: Array and Map Structure Tokens
// =============================================================================

func TestLexArrays(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemArrayStart, "", 1, 7},
		{ItemInteger, "1", 1, 7},
		{ItemInteger, "2", 1, 10},
		{ItemInteger, "3", 1, 13},
		{ItemString, "bar", 1, 17},
		{ItemArrayEnd, "", 1, 22},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("foo = [1, 2, 3, 'bar']")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemArrayStart, "", 1, 7},
		{ItemInteger, "1", 1, 7},
		{ItemInteger, "2", 1, 9},
		{ItemInteger, "3", 1, 11},
		{ItemString, "bar", 1, 14},
		{ItemArrayEnd, "", 1, 19},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo = [1,2,3,'bar']")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemArrayStart, "", 1, 7},
		{ItemInteger, "1", 1, 7},
		{ItemInteger, "2", 1, 10},
		{ItemInteger, "3", 1, 12},
		{ItemString, "bar", 1, 15},
		{ItemArrayEnd, "", 1, 20},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo = [1, 2,3,'bar']")
	expect(t, lx, expectedItems)
}

var mlArray = `
# top level comment
foo = [
 1, # One
 2, // Two
 3 # Three
 'bar'     ,
 "bar"
]
`

func TestLexMultilineArrays(t *testing.T) {
	expectedItems := []item{
		{ItemCommentStart, "", 2, 2},
		{ItemText, " top level comment", 2, 2},
		{ItemKey, "foo", 3, 1},
		{ItemArrayStart, "", 3, 8},
		{ItemInteger, "1", 4, 2},
		{ItemCommentStart, "", 4, 6},
		{ItemText, " One", 4, 6},
		{ItemInteger, "2", 5, 2},
		{ItemCommentStart, "", 5, 7},
		{ItemText, " Two", 5, 7},
		{ItemInteger, "3", 6, 2},
		{ItemCommentStart, "", 6, 5},
		{ItemText, " Three", 6, 5},
		{ItemString, "bar", 7, 3},
		{ItemString, "bar", 8, 3},
		{ItemArrayEnd, "", 9, 2},
		{ItemEOF, "", 9, 0},
	}
	lx := lex(mlArray)
	expect(t, lx, expectedItems)
}

var mlArrayNoSep = `
# top level comment
foo = [
 1 // foo
 2
 3
 'bar'
 "bar"
]
`

func TestLexMultilineArraysNoSep(t *testing.T) {
	expectedItems := []item{
		{ItemCommentStart, "", 2, 2},
		{ItemText, " top level comment", 2, 2},
		{ItemKey, "foo", 3, 1},
		{ItemArrayStart, "", 3, 8},
		{ItemInteger, "1", 4, 2},
		{ItemCommentStart, "", 4, 6},
		{ItemText, " foo", 4, 6},
		{ItemInteger, "2", 5, 2},
		{ItemInteger, "3", 6, 2},
		{ItemString, "bar", 7, 3},
		{ItemString, "bar", 8, 3},
		{ItemArrayEnd, "", 9, 2},
		{ItemEOF, "", 9, 0},
	}
	lx := lex(mlArrayNoSep)
	expect(t, lx, expectedItems)
}

func TestLexSimpleMap(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemMapStart, "", 1, 7},
		{ItemKey, "ip", 1, 7},
		{ItemString, "127.0.0.1", 1, 11},
		{ItemKey, "port", 1, 23},
		{ItemInteger, "4242", 1, 30},
		{ItemMapEnd, "", 1, 35},
		{ItemEOF, "", 1, 0},
	}

	lx := lex("foo = {ip='127.0.0.1', port = 4242}")
	expect(t, lx, expectedItems)
}

var mlMap = `
foo = {
  ip = '127.0.0.1' # the IP
  port= 4242 // the port
}
`

func TestLexMultilineMap(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 2, 1},
		{ItemMapStart, "", 2, 8},
		{ItemKey, "ip", 3, 3},
		{ItemString, "127.0.0.1", 3, 9},
		{ItemCommentStart, "", 3, 21},
		{ItemText, " the IP", 3, 21},
		{ItemKey, "port", 4, 3},
		{ItemInteger, "4242", 4, 9},
		{ItemCommentStart, "", 4, 16},
		{ItemText, " the port", 4, 16},
		{ItemMapEnd, "", 5, 2},
		{ItemEOF, "", 5, 0},
	}

	lx := lex(mlMap)
	expect(t, lx, expectedItems)
}

var nestedMap = `
foo = {
  host = {
    ip = '127.0.0.1'
    port= 4242
  }
}
`

func TestLexNestedMaps(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 2, 1},
		{ItemMapStart, "", 2, 8},
		{ItemKey, "host", 3, 3},
		{ItemMapStart, "", 3, 11},
		{ItemKey, "ip", 4, 5},
		{ItemString, "127.0.0.1", 4, 11},
		{ItemKey, "port", 5, 5},
		{ItemInteger, "4242", 5, 11},
		{ItemMapEnd, "", 6, 4},
		{ItemMapEnd, "", 7, 2},
		{ItemEOF, "", 7, 0},
	}

	lx := lex(nestedMap)
	expect(t, lx, expectedItems)
}

var nestedWhitespaceMap = `
foo  {
  host  {
    ip = '127.0.0.1'
    port= 4242
  }
}
`

func TestLexNestedWhitespaceMaps(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 2, 1},
		{ItemMapStart, "", 2, 7},
		{ItemKey, "host", 3, 3},
		{ItemMapStart, "", 3, 10},
		{ItemKey, "ip", 4, 5},
		{ItemString, "127.0.0.1", 4, 11},
		{ItemKey, "port", 5, 5},
		{ItemInteger, "4242", 5, 11},
		{ItemMapEnd, "", 6, 4},
		{ItemMapEnd, "", 7, 2},
		{ItemEOF, "", 7, 0},
	}

	lx := lex(nestedWhitespaceMap)
	expect(t, lx, expectedItems)
}

func TestLexMapQuotedKeys(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemMapStart, "", 1, 7},
		{ItemKey, "bar", 1, 8},
		{ItemInteger, "4242", 1, 15},
		{ItemMapEnd, "", 1, 20},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("foo = {'bar' = 4242}")
	expect(t, lx, expectedItems)
	lx = lex("foo = {\"bar\" = 4242}")
	expect(t, lx, expectedItems)
}

func TestLexSpecialCharsMapQuotedKeys(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemMapStart, "", 1, 7},
		{ItemKey, "bar-1.2.3", 1, 8},
		{ItemMapStart, "", 1, 22},
		{ItemKey, "port", 1, 23},
		{ItemInteger, "4242", 1, 28},
		{ItemMapEnd, "", 1, 34},
		{ItemMapEnd, "", 1, 35},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("foo = {'bar-1.2.3' = { port:4242 }}")
	expect(t, lx, expectedItems)
	lx = lex("foo = {\"bar-1.2.3\" = { port:4242 }}")
	expect(t, lx, expectedItems)
}

var mlnestedmap = `
systems {
  allinone {
    description: "This is a description."
  }
}
`

func TestLexDoubleNestedMapsNewLines(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "systems", 2, 1},
		{ItemMapStart, "", 2, 10},
		{ItemKey, "allinone", 3, 3},
		{ItemMapStart, "", 3, 13},
		{ItemKey, "description", 4, 5},
		{ItemString, "This is a description.", 4, 19},
		{ItemMapEnd, "", 5, 4},
		{ItemMapEnd, "", 6, 2},
		{ItemEOF, "", 7, 0},
	}
	lx := lex(mlnestedmap)
	expect(t, lx, expectedItems)
}

var arrayOfMaps = `
authorization {
    users = [
      {user: alice, password: foo}
      {user: bob,   password: bar}
    ]
    timeout: 0.5
}
`

func TestLexArrayOfMaps(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "authorization", 2, 1},
		{ItemMapStart, "", 2, 16},
		{ItemKey, "users", 3, 5},
		{ItemArrayStart, "", 3, 14},
		{ItemMapStart, "", 4, 8},
		{ItemKey, "user", 4, 8},
		{ItemString, "alice", 4, 14},
		{ItemKey, "password", 4, 21},
		{ItemString, "foo", 4, 31},
		{ItemMapEnd, "", 4, 35},
		{ItemMapStart, "", 5, 8},
		{ItemKey, "user", 5, 8},
		{ItemString, "bob", 5, 14},
		{ItemKey, "password", 5, 21},
		{ItemString, "bar", 5, 31},
		{ItemMapEnd, "", 5, 35},
		{ItemArrayEnd, "", 6, 6},
		{ItemKey, "timeout", 7, 5},
		{ItemFloat, "0.5", 7, 14},
		{ItemMapEnd, "", 8, 2},
		{ItemEOF, "", 9, 0},
	}
	lx := lex(arrayOfMaps)
	expect(t, lx, expectedItems)
}

// =============================================================================
// VAL-PARSER-007: Block String Lexing
// =============================================================================

var blockexample = `
numbers (
1234567890
)
`

func TestLexBlockString(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "numbers", 2, 1},
		{ItemString, "\n1234567890\n", 4, 10},
	}
	lx := lex(blockexample)
	expect(t, lx, expectedItems)
}

func TestLexBlockStringEOF(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "numbers", 2, 1},
		{ItemString, "\n1234567890\n", 4, 10},
	}
	blockbytes := []byte(blockexample[0 : len(blockexample)-1])
	blockbytes = append(blockbytes, 0)
	lx := lex(string(blockbytes))
	expect(t, lx, expectedItems)
}

var mlblockexample = `
numbers (
  12(34)56
  (
    7890
  )
)
`

func TestLexBlockStringMultiLine(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "numbers", 2, 1},
		{ItemString, "\n  12(34)56\n  (\n    7890\n  )\n", 7, 10},
	}
	lx := lex(mlblockexample)
	expect(t, lx, expectedItems)
}

// =============================================================================
// VAL-PARSER-008: IP Address and Hostname Lexing
// =============================================================================

func TestLexUnquotedIPAddr(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "listen", 1, 0},
		{ItemString, "127.0.0.1:4222", 1, 8},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("listen: 127.0.0.1:4222")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "listen", 1, 0},
		{ItemString, "127.0.0.1", 1, 8},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("listen: 127.0.0.1")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "listen", 1, 0},
		{ItemString, "apcera.me:80", 1, 8},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("listen: apcera.me:80")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "listen", 1, 0},
		{ItemString, "nats.io:-1", 1, 8},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("listen: nats.io:-1")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "listen", 1, 0},
		{ItemInteger, "-1", 1, 8},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("listen: -1")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "listen", 1, 0},
		{ItemString, ":-1", 1, 8},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("listen: :-1")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "listen", 1, 0},
		{ItemString, ":80", 1, 9},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("listen = :80")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "listen", 1, 0},
		{ItemArrayStart, "", 1, 10},
		{ItemString, "localhost:4222", 1, 10},
		{ItemString, "localhost:4333", 1, 26},
		{ItemArrayEnd, "", 1, 41},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("listen = [localhost:4222, localhost:4333]")
	expect(t, lx, expectedItems)
}

// =============================================================================
// VAL-PARSER-009: Strings Starting with Numbers
// =============================================================================

func TestLexStringStartingWithNumber(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "3xyz", 1, 6},
		{ItemEOF, "", 2, 0},
	}

	lx := lex(`foo = 3xyz`)
	expect(t, lx, expectedItems)

	lx = lex(`foo = 3xyz,`)
	expect(t, lx, expectedItems)

	lx = lex(`foo = 3xyz;`)
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 2, 9},
		{ItemString, "3xyz", 2, 15},
		{ItemEOF, "", 2, 0},
	}
	content := `
        foo = 3xyz
        `
	lx = lex(content)
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "map", 2, 9},
		{ItemMapStart, "", 2, 14},
		{ItemKey, "foo", 3, 11},
		{ItemString, "3xyz", 3, 17},
		{ItemMapEnd, "", 3, 22},
		{ItemEOF, "", 2, 0},
	}
	content = `
        map {
          foo = 3xyz}
        `
	lx = lex(content)
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "map", 2, 9},
		{ItemMapStart, "", 2, 14},
		{ItemKey, "foo", 3, 11},
		{ItemString, "3xyz", 3, 17},
		{ItemMapEnd, "", 4, 10},
		{ItemEOF, "", 2, 0},
	}
	content = `
        map {
          foo = 3xyz;
        }
        `
	lx = lex(content)
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "map", 2, 9},
		{ItemMapStart, "", 2, 14},
		{ItemKey, "foo", 3, 11},
		{ItemString, "3xyz", 3, 17},
		{ItemKey, "bar", 4, 11},
		{ItemString, "4wqs", 4, 17},
		{ItemMapEnd, "", 5, 10},
		{ItemEOF, "", 2, 0},
	}
	content = `
        map {
          foo = 3xyz,
          bar = 4wqs
        }
        `
	lx = lex(content)
	expect(t, lx, expectedItems)
}

// =============================================================================
// VAL-PARSER-010: Bcrypt Password Special Case
// This is validated at the parser level (variable resolution).
// At the lexer level, $2a$ would start as a variable or string
// depending on context. This test confirms the lexer behavior.
// =============================================================================

func TestLexBcryptLexerLevel(t *testing.T) {
	// At the lexer level, $2a$11$... starts with $ so it's treated
	// as a variable reference. The parser layer handles bcrypt detection.
	// This just verifies the lexer doesn't crash.
	lx := lex("password = $2a$11$ooo")
	it := lx.nextItem() // key
	if it.typ != ItemKey || it.val != "password" {
		t.Fatalf("Expected key 'password', got %v", it)
	}
	it = lx.nextItem() // value - will be variable with name "2a$11$ooo"
	if it.typ != ItemVariable {
		t.Fatalf("Expected variable token for $2a$11$ooo, got %v", it)
	}
}

// =============================================================================
// VAL-PARSER-011: JSON Compatibility
// =============================================================================

func TestLexJSONCompat(t *testing.T) {
	for _, test := range []struct {
		name     string
		input    string
		expected []item
	}{
		{
			name: "should omit initial and final brackets at top level with a single item",
			input: `
                        {
                          "http_port": 8223
                        }
                        `,
			expected: []item{
				{ItemKey, "http_port", 3, 28},
				{ItemInteger, "8223", 3, 40},
				{ItemKey, "}", 4, 25},
				{ItemEOF, "", 0, 0},
			},
		},
		{
			name: "should omit trailing commas at top level with two items",
			input: `
                        {
                          "http_port": 8223,
                          "port": 4223
                        }
                        `,
			expected: []item{
				{ItemKey, "http_port", 3, 28},
				{ItemInteger, "8223", 3, 40},
				{ItemKey, "port", 4, 28},
				{ItemInteger, "4223", 4, 35},
				{ItemKey, "}", 5, 25},
				{ItemEOF, "", 0, 0},
			},
		},
		{
			name: "should omit trailing commas at top level with multiple items",
			input: `
                        {
                          "http_port": 8223,
                          "port": 4223,
                          "max_payload": "5MB",
                          "debug": true,
                          "max_control_line": 1024
                        }
                        `,
			expected: []item{
				{ItemKey, "http_port", 3, 28},
				{ItemInteger, "8223", 3, 40},
				{ItemKey, "port", 4, 28},
				{ItemInteger, "4223", 4, 35},
				{ItemKey, "max_payload", 5, 28},
				{ItemString, "5MB", 5, 43},
				{ItemKey, "debug", 6, 28},
				{ItemBool, "true", 6, 36},
				{ItemKey, "max_control_line", 7, 28},
				{ItemInteger, "1024", 7, 47},
				{ItemKey, "}", 8, 25},
				{ItemEOF, "", 0, 0},
			},
		},
		{
			name:  "should support JSON not prettified",
			input: "{\"http_port\": 8224,\"port\": 4224}\n                        ",
			expected: []item{
				{ItemKey, "http_port", 1, 2},
				{ItemInteger, "8224", 1, 14},
				{ItemKey, "port", 1, 20},
				{ItemInteger, "4224", 1, 27},
				{ItemEOF, "", 0, 0},
			},
		},
		{
			name:  "should support JSON not prettified with final bracket after newline",
			input: "{\"http_port\": 8225,\"port\": 4225\n                        }\n                        ",
			expected: []item{
				{ItemKey, "http_port", 1, 2},
				{ItemInteger, "8225", 1, 14},
				{ItemKey, "port", 1, 20},
				{ItemInteger, "4225", 1, 27},
				{ItemKey, "}", 2, 25},
				{ItemEOF, "", 0, 0},
			},
		},
		{
			name:  "should support uglified JSON with inner blocks",
			input: "{\"http_port\": 8227,\"port\": 4227,\"write_deadline\": \"1h\",\"cluster\": {\"port\": 6222,\"routes\": [\"nats://127.0.0.1:4222\",\"nats://127.0.0.1:4223\",\"nats://127.0.0.1:4224\"]}}\n                        ",
			expected: []item{
				{ItemKey, "http_port", 1, 2},
				{ItemInteger, "8227", 1, 14},
				{ItemKey, "port", 1, 20},
				{ItemInteger, "4227", 1, 27},
				{ItemKey, "write_deadline", 1, 33},
				{ItemString, "1h", 1, 51},
				{ItemKey, "cluster", 1, 56},
				{ItemMapStart, "", 1, 67},
				{ItemKey, "port", 1, 68},
				{ItemInteger, "6222", 1, 75},
				{ItemKey, "routes", 1, 81},
				{ItemArrayStart, "", 1, 91},
				{ItemString, "nats://127.0.0.1:4222", 1, 92},
				{ItemString, "nats://127.0.0.1:4223", 1, 116},
				{ItemString, "nats://127.0.0.1:4224", 1, 140},
				{ItemArrayEnd, "", 1, 163},
				{ItemMapEnd, "", 1, 164},
				{ItemKey, "}", 14, 25},
				{ItemEOF, "", 0, 0},
			},
		},
		{
			name: "should support prettified JSON with inner blocks",
			input: `
                        {
                          "http_port": 8227,
                          "port": 4227,
                          "write_deadline": "1h",
                          "cluster": {
                            "port": 6222,
                            "routes": [
                              "nats://127.0.0.1:4222",
                              "nats://127.0.0.1:4223",
                              "nats://127.0.0.1:4224"
                            ]
                          }
                        }
                        `,
			expected: []item{
				{ItemKey, "http_port", 3, 28},
				{ItemInteger, "8227", 3, 40},
				{ItemKey, "port", 4, 28},
				{ItemInteger, "4227", 4, 35},
				{ItemKey, "write_deadline", 5, 28},
				{ItemString, "1h", 5, 46},
				{ItemKey, "cluster", 6, 28},
				{ItemMapStart, "", 6, 39},
				{ItemKey, "port", 7, 30},
				{ItemInteger, "6222", 7, 37},
				{ItemKey, "routes", 8, 30},
				{ItemArrayStart, "", 8, 40},
				{ItemString, "nats://127.0.0.1:4222", 9, 32},
				{ItemString, "nats://127.0.0.1:4223", 10, 32},
				{ItemString, "nats://127.0.0.1:4224", 11, 32},
				{ItemArrayEnd, "", 12, 30},
				{ItemMapEnd, "", 13, 28},
				{ItemKey, "}", 14, 25},
				{ItemEOF, "", 0, 0},
			},
		},
		{
			name: "should support JSON with blocks",
			input: `{
                          "jetstream": {
                            "store_dir": "/tmp/nats"
                            "max_mem": 1000000,
                          },
                          "port": 4222,
                          "server_name": "nats1"
                        }
                        `,
			expected: []item{
				{ItemKey, "jetstream", 2, 28},
				{ItemMapStart, "", 2, 41},
				{ItemKey, "store_dir", 3, 30},
				{ItemString, "/tmp/nats", 3, 43},
				{ItemKey, "max_mem", 4, 30},
				{ItemInteger, "1000000", 4, 40},
				{ItemMapEnd, "", 5, 28},
				{ItemKey, "port", 6, 28},
				{ItemInteger, "4222", 6, 35},
				{ItemKey, "server_name", 7, 28},
				{ItemString, "nats1", 7, 43},
				{ItemKey, "}", 8, 25},
				{ItemEOF, "", 0, 0},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			lx := lex(test.input)
			expect(t, lx, test.expected)
		})
	}
}

// =============================================================================
// VAL-PARSER-012: Semicolons and Value Terminators
// =============================================================================

var semicolons = `
foo = 123;
bar = 'baz';
baz = 'boo'
map {
 id = 1;
}
`

func TestLexOptionalSemicolons(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 2, 1},
		{ItemInteger, "123", 2, 7},
		{ItemKey, "bar", 3, 1},
		{ItemString, "baz", 3, 8},
		{ItemKey, "baz", 4, 1},
		{ItemString, "boo", 4, 8},
		{ItemKey, "map", 5, 1},
		{ItemMapStart, "", 5, 6},
		{ItemKey, "id", 6, 2},
		{ItemInteger, "1", 6, 7},
		{ItemMapEnd, "", 7, 2},
		{ItemEOF, "", 8, 0},
	}

	lx := lex(semicolons)
	expect(t, lx, expectedItems)
}

func TestLexSemicolonChaining(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemString, "1", 1, 5},
		{ItemKey, "bar", 1, 9},
		{ItemFloat, "2.2", 1, 13},
		{ItemKey, "baz", 1, 18},
		{ItemBool, "true", 1, 22},
		{ItemEOF, "", 1, 0},
	}

	lx := lex("foo='1'; bar=2.2; baz=true;")
	expect(t, lx, expectedItems)
}

// =============================================================================
// VAL-PARSER-013: Error Reporting with Line and Position
// =============================================================================

func TestLexBadFloatValues(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemError, "Floats must start with a digit", 1, 7},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("foo = .2")
	expect(t, lx, expectedItems)
}

func TestLexBadKey(t *testing.T) {
	expectedItems := []item{
		{ItemError, "Unexpected key separator ':'", 1, 1},
		{ItemEOF, "", 1, 0},
	}
	lx := lex(" :foo = 22")
	expect(t, lx, expectedItems)
}

var danglingquote = `
listen: "localhost:4242

http: localhost:8222
`

func TestLexDanglingQuotedString(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "listen", 2, 1},
		{ItemError, "Unexpected EOF.", 5, 1},
	}
	lx := lex(danglingquote)
	expect(t, lx, expectedItems)
}

var keydanglingquote = `
foo = "
listen: "

http: localhost:8222

"
`

func TestLexKeyDanglingQuotedString(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 2, 1},
		{ItemString, "\nlisten: ", 3, 8},
		{ItemKey, "http", 5, 1},
		{ItemString, "localhost:8222", 5, 7},
		{ItemError, "Unexpected EOF.", 8, 1},
	}
	lx := lex(keydanglingquote)
	expect(t, lx, expectedItems)
}

var danglingsquote = `
listen: 'localhost:4242

http: localhost:8222
`

func TestLexDanglingSingleQuotedString(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "listen", 2, 1},
		{ItemError, "Unexpected EOF.", 5, 1},
	}
	lx := lex(danglingsquote)
	expect(t, lx, expectedItems)
}

var keydanglingsquote = `
foo = '
listen: '

http: localhost:8222

'
`

func TestLexKeyDanglingSingleQuotedString(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 2, 1},
		{ItemString, "\nlisten: ", 3, 8},
		{ItemKey, "http", 5, 1},
		{ItemString, "localhost:8222", 5, 7},
		{ItemError, "Unexpected EOF.", 8, 1},
	}
	lx := lex(keydanglingsquote)
	expect(t, lx, expectedItems)
}

var mapdanglingbracket = `
listen = 4222

cluster = {

  foo = bar

`

func TestLexMapDanglingBracket(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "listen", 2, 1},
		{ItemInteger, "4222", 2, 10},
		{ItemKey, "cluster", 4, 1},
		{ItemMapStart, "", 4, 12},
		{ItemKey, "foo", 6, 3},
		{ItemString, "bar", 6, 9},
		{ItemError, "Unexpected EOF processing map.", 8, 1},
	}
	lx := lex(mapdanglingbracket)
	expect(t, lx, expectedItems)
}

var blockdanglingparens = `
listen = 4222

quote = (

  foo = bar

`

func TestLexBlockDanglingParens(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "listen", 2, 1},
		{ItemInteger, "4222", 2, 10},
		{ItemKey, "quote", 4, 1},
		{ItemError, "Unexpected EOF processing block.", 8, 1},
	}
	lx := lex(blockdanglingparens)
	expect(t, lx, expectedItems)
}

// =============================================================================
// VAL-PARSER-027: CRLF Line Ending Support
// =============================================================================

func TestLexCRLFSupport(t *testing.T) {
	// Same input with LF and CRLF should produce identical tokens
	lfInput := "foo = 123\nbar = true\n"
	crlfInput := "foo = 123\r\nbar = true\r\n"

	lxLF := lex(lfInput)
	lxCRLF := lex(crlfInput)

	for {
		itemLF := lxLF.nextItem()
		itemCRLF := lxCRLF.nextItem()
		if itemLF.typ != itemCRLF.typ || itemLF.val != itemCRLF.val {
			t.Fatalf("LF/CRLF mismatch:\n  LF:   %v\n  CRLF: %v", itemLF, itemCRLF)
		}
		if itemLF.typ == ItemEOF {
			break
		}
	}
}

func TestLexCRLFMultiline(t *testing.T) {
	input := "foo = 123\r\nbar = 'hello'\r\nbaz = true\r\n"
	lx := lex(input)

	it := lx.nextItem()
	if it.typ != ItemKey || it.val != "foo" {
		t.Fatalf("Expected key 'foo', got %v", it)
	}
	it = lx.nextItem()
	if it.typ != ItemInteger || it.val != "123" {
		t.Fatalf("Expected integer '123', got %v", it)
	}
	it = lx.nextItem()
	if it.typ != ItemKey || it.val != "bar" {
		t.Fatalf("Expected key 'bar', got %v", it)
	}
	it = lx.nextItem()
	if it.typ != ItemString || it.val != "hello" {
		t.Fatalf("Expected string 'hello', got %v", it)
	}
	it = lx.nextItem()
	if it.typ != ItemKey || it.val != "baz" {
		t.Fatalf("Expected key 'baz', got %v", it)
	}
	it = lx.nextItem()
	if it.typ != ItemBool || it.val != "true" {
		t.Fatalf("Expected bool 'true', got %v", it)
	}
}

// =============================================================================
// Include Lexing
// =============================================================================

func TestLexInclude(t *testing.T) {
	expectedItems := []item{
		{ItemInclude, "users.conf", 1, 9},
		{ItemEOF, "", 1, 0},
	}
	lx := lex("include \"users.conf\"")
	expect(t, lx, expectedItems)

	lx = lex("include 'users.conf'")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemInclude, "users.conf", 1, 8},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("include users.conf")
	expect(t, lx, expectedItems)
}

func TestLexMapInclude(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 1, 0},
		{ItemMapStart, "", 1, 5},
		{ItemInclude, "users.conf", 1, 14},
		{ItemMapEnd, "", 1, 26},
		{ItemEOF, "", 1, 0},
	}

	lx := lex("foo { include users.conf }")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemMapStart, "", 1, 5},
		{ItemInclude, "users.conf", 1, 13},
		{ItemMapEnd, "", 1, 24},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo {include users.conf}")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemMapStart, "", 1, 5},
		{ItemInclude, "users.conf", 1, 15},
		{ItemMapEnd, "", 1, 28},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo { include 'users.conf' }")
	expect(t, lx, expectedItems)

	expectedItems = []item{
		{ItemKey, "foo", 1, 0},
		{ItemMapStart, "", 1, 5},
		{ItemInclude, "users.conf", 1, 15},
		{ItemMapEnd, "", 1, 27},
		{ItemEOF, "", 1, 0},
	}
	lx = lex("foo { include \"users.conf\"}")
	expect(t, lx, expectedItems)
}

// =============================================================================
// Non-Quoted Strings (comprehensive test)
// =============================================================================

var noquotes = `
foo = 123
bar = baz
baz=boo
map {
 id:one
 id2 : onetwo
}
t true
f false
tstr "true"
tkey = two
fkey = five # This should be a string
`

func TestLexNonQuotedStrings(t *testing.T) {
	expectedItems := []item{
		{ItemKey, "foo", 2, 1},
		{ItemInteger, "123", 2, 7},
		{ItemKey, "bar", 3, 1},
		{ItemString, "baz", 3, 7},
		{ItemKey, "baz", 4, 1},
		{ItemString, "boo", 4, 5},
		{ItemKey, "map", 5, 1},
		{ItemMapStart, "", 5, 6},
		{ItemKey, "id", 6, 2},
		{ItemString, "one", 6, 5},
		{ItemKey, "id2", 7, 2},
		{ItemString, "onetwo", 7, 8},
		{ItemMapEnd, "", 8, 2},
		{ItemKey, "t", 9, 1},
		{ItemBool, "true", 9, 3},
		{ItemKey, "f", 10, 1},
		{ItemBool, "false", 10, 3},
		{ItemKey, "tstr", 11, 1},
		{ItemString, "true", 11, 7},
		{ItemKey, "tkey", 12, 1},
		{ItemString, "two", 12, 8},
		{ItemKey, "fkey", 13, 1},
		{ItemString, "five", 13, 8},
		{ItemCommentStart, "", 13, 14},
		{ItemText, " This should be a string", 13, 14},
		{ItemEOF, "", 14, 0},
	}
	lx := lex(noquotes)
	expect(t, lx, expectedItems)
}

// =============================================================================
// Cross-validation: Compare v2 lexer output with known v1 expected output
// These tests use the exact same inputs and expected tokens as v1 lex_test.go
// =============================================================================

func TestLexCrossValidatePlainValue(t *testing.T) {
	// v1: TestPlainValue
	lx := lex("foo")
	it := lx.nextItem()
	if it.typ != ItemKey || it.val != "foo" || it.line != 1 || it.pos != 0 {
		t.Fatalf("Cross-validate plain value: expected (Key, 'foo', 1, 0), got %v", it)
	}
}

func TestLexCrossValidateAllV1TestInputs(t *testing.T) {
	// This test verifies that for representative inputs from v1,
	// the v2 lexer produces tokens with matching types, values, lines, and positions.
	tests := []struct {
		name     string
		input    string
		expected []item
	}{
		{
			name:  "simple key=string",
			input: "foo = \"bar\"",
			expected: []item{
				{ItemKey, "foo", 1, 0},
				{ItemString, "bar", 1, 7},
			},
		},
		{
			name:  "simple key=integer",
			input: "foo = 123",
			expected: []item{
				{ItemKey, "foo", 1, 0},
				{ItemInteger, "123", 1, 6},
			},
		},
		{
			name:  "simple key=float",
			input: "foo = 22.2",
			expected: []item{
				{ItemKey, "foo", 1, 0},
				{ItemFloat, "22.2", 1, 6},
			},
		},
		{
			name:  "simple key=bool",
			input: "foo = true",
			expected: []item{
				{ItemKey, "foo", 1, 0},
				{ItemBool, "true", 1, 6},
			},
		},
		{
			name:  "simple key=datetime",
			input: "foo = 2016-05-04T18:53:41Z",
			expected: []item{
				{ItemKey, "foo", 1, 0},
				{ItemDatetime, "2016-05-04T18:53:41Z", 1, 6},
			},
		},
		{
			name:  "simple key=variable",
			input: "foo = $bar",
			expected: []item{
				{ItemKey, "foo", 1, 0},
				{ItemVariable, "bar", 1, 7},
			},
		},
		{
			name:  "negative integer",
			input: "foo = -123",
			expected: []item{
				{ItemKey, "foo", 1, 0},
				{ItemInteger, "-123", 1, 6},
			},
		},
		{
			name:  "negative float",
			input: "foo = -22.2",
			expected: []item{
				{ItemKey, "foo", 1, 0},
				{ItemFloat, "-22.2", 1, 6},
			},
		},
		{
			name:  "escape in raw string",
			input: `foo = \t`,
			expected: []item{
				{ItemKey, "foo", 1, 0},
				{ItemString, "\t", 1, 7},
			},
		},
		{
			name:  "hash comment",
			input: "# hello",
			expected: []item{
				{ItemCommentStart, "", 1, 1},
				{ItemText, " hello", 1, 1},
			},
		},
		{
			name:  "slash comment",
			input: "// hello",
			expected: []item{
				{ItemCommentStart, "", 1, 2},
				{ItemText, " hello", 1, 2},
			},
		},
		{
			name:  "include quoted",
			input: `include "users.conf"`,
			expected: []item{
				{ItemInclude, "users.conf", 1, 9},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lx := lex(test.input)
			for _, exp := range test.expected {
				it := lx.nextItem()
				if it != exp {
					t.Fatalf("Expected %v, got %v", exp, it)
				}
			}
		})
	}
}

// =============================================================================
// Item String representation
// =============================================================================

func TestLexItemString(t *testing.T) {
	it := item{ItemKey, "foo", 1, 0}
	s := it.String()
	if s != "(Key, 'foo', 1, 0)" {
		t.Fatalf("Expected '(Key, 'foo', 1, 0)', got %q", s)
	}
}

// =============================================================================
// VAL-PARSER-028: Numeric Overflow Error Reporting
// =============================================================================

func TestLexIntegerOverflow(t *testing.T) {
	// A number exceeding int64 max (9223372036854775807) must produce an error.
	lx := lex("foo = 99999999999999999999")
	it := lx.nextItem() // key
	if it.typ != ItemKey || it.val != "foo" {
		t.Fatalf("Expected key 'foo', got %v", it)
	}
	it = lx.nextItem() // value should be error
	if it.typ != ItemError {
		t.Fatalf("Expected error for int64 overflow, got %v", it)
	}
	if !strings.Contains(it.val, "integer range") {
		t.Fatalf("Expected error message containing 'integer range', got %q", it.val)
	}
}

func TestLexNegativeIntegerOverflow(t *testing.T) {
	// A negative number exceeding int64 min must produce an error.
	lx := lex("foo = -99999999999999999999")
	it := lx.nextItem() // key
	if it.typ != ItemKey || it.val != "foo" {
		t.Fatalf("Expected key 'foo', got %v", it)
	}
	it = lx.nextItem() // value should be error
	if it.typ != ItemError {
		t.Fatalf("Expected error for negative int64 overflow, got %v", it)
	}
	if !strings.Contains(it.val, "integer range") {
		t.Fatalf("Expected error message containing 'integer range', got %q", it.val)
	}
}

func TestLexFloatOverflow(t *testing.T) {
	// Float overflow is checked at parse time (strconv.ParseFloat),
	// not at lex time, since the lexer doesn't support scientific notation.
	// This test verifies that extremely long decimal floats still lex correctly.
	// Actual float range checking will be done by the parser.
	lx := lex("foo = 999999999999999999999999999999.999999999999999")
	it := lx.nextItem() // key
	if it.typ != ItemKey || it.val != "foo" {
		t.Fatalf("Expected key 'foo', got %v", it)
	}
	it = lx.nextItem() // value - should be float (Go handles this fine)
	if it.typ != ItemFloat {
		t.Fatalf("Expected float for large decimal, got %v", it)
	}
}

func TestLexIntegerOverflowWithSuffix(t *testing.T) {
	// Very large number with suffix should also overflow.
	lx := lex("foo = 99999999999999999999k")
	it := lx.nextItem() // key
	if it.typ != ItemKey || it.val != "foo" {
		t.Fatalf("Expected key 'foo', got %v", it)
	}
	it = lx.nextItem() // value should be error
	if it.typ != ItemError {
		t.Fatalf("Expected error for int64 overflow with suffix, got %v", it)
	}
	if !strings.Contains(it.val, "integer range") {
		t.Fatalf("Expected error message containing 'integer range', got %q", it.val)
	}
}

func TestLexValidLargeInteger(t *testing.T) {
	// Max int64 value should be valid.
	lx := lex("foo = 9223372036854775807")
	it := lx.nextItem() // key
	if it.typ != ItemKey || it.val != "foo" {
		t.Fatalf("Expected key 'foo', got %v", it)
	}
	it = lx.nextItem() // value
	if it.typ != ItemInteger {
		t.Fatalf("Expected integer for max int64, got %v", it)
	}
	if it.val != "9223372036854775807" {
		t.Fatalf("Expected value '9223372036854775807', got %q", it.val)
	}
}

// =============================================================================
// VAL-PARSER-027: Comprehensive CRLF Line Ending Support
// =============================================================================

func TestLexCRLFAllValueTypes(t *testing.T) {
	// Test that each value type produces identical token types and values
	// with LF vs CRLF line endings.
	tests := []struct {
		name  string
		input string
	}{
		{"string_dq", "foo = \"hello\"\nbar = 'world'\n"},
		{"string_sq", "foo = 'hello'\n"},
		{"integer", "foo = 123\nbar = -456\n"},
		{"integer_suffix", "foo = 1k\nbar = 2MB\n"},
		{"float", "foo = 3.14\nbar = -0.5\n"},
		{"bool", "foo = true\nbar = false\n"},
		{"datetime", "foo = 2016-05-04T18:53:41Z\n"},
		{"variable", "foo = $bar\n"},
		{"comment_hash", "# hello\nfoo = 1\n"},
		{"comment_slash", "// hello\nfoo = 1\n"},
		{"array", "foo = [1, 2, 3, 'bar']\n"},
		{"map", "foo {\n  bar = 1\n}\n"},
		{"include", "include users.conf\n"},
		{"semicolons", "foo = 'a'; bar = 2\n"},
		{"ip_addr", "listen = 127.0.0.1:4222\n"},
		{"raw_string", "foo = bar\n"},
		{"empty_string_dq", "foo = \"\"\n"},
		{"empty_string_sq", "foo = ''\n"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			lfInput := test.input
			crlfInput := strings.ReplaceAll(lfInput, "\n", "\r\n")

			// Collect all tokens from LF version.
			lxLF := lex(lfInput)
			var lfTokens []item
			for {
				it := lxLF.nextItem()
				lfTokens = append(lfTokens, it)
				if it.typ == ItemEOF || it.typ == ItemError {
					break
				}
			}

			// Collect all tokens from CRLF version.
			lxCRLF := lex(crlfInput)
			var crlfTokens []item
			for {
				it := lxCRLF.nextItem()
				crlfTokens = append(crlfTokens, it)
				if it.typ == ItemEOF || it.typ == ItemError {
					break
				}
			}

			// Compare token counts.
			if len(lfTokens) != len(crlfTokens) {
				t.Fatalf("Token count mismatch: LF=%d, CRLF=%d", len(lfTokens), len(crlfTokens))
			}

			// Compare types and values (not positions, as CRLF adds extra characters).
			for i := range lfTokens {
				if lfTokens[i].typ != crlfTokens[i].typ {
					t.Fatalf("Token %d type mismatch:\n  LF:   %v\n  CRLF: %v", i, lfTokens[i], crlfTokens[i])
				}
				if lfTokens[i].val != crlfTokens[i].val {
					t.Fatalf("Token %d value mismatch:\n  LF:   %v\n  CRLF: %v", i, lfTokens[i], crlfTokens[i])
				}
			}
		})
	}
}

// =============================================================================
// Comprehensive cross-validation: complex multi-line configs
// =============================================================================

func TestLexCrossValidateComplexConfig(t *testing.T) {
	// Full server-like config exercising many token types.
	input := `
# Server configuration
port = 4222
listen: 127.0.0.1:4222

debug = true
trace = off

max_payload = 1MB
max_control_line: 512

cluster {
  port: 6222
  name = "my-cluster"
  routes = [
    "nats://127.0.0.1:4222"
    "nats://127.0.0.1:4223"
  ]
  timeout: 0.5
}

created = 2016-05-04T18:53:41Z
`
	lx := lex(input)
	var items []item
	for {
		it := lx.nextItem()
		items = append(items, it)
		if it.typ == ItemEOF || it.typ == ItemError {
			break
		}
	}

	// Verify we got the expected tokens (spot-check key ones).
	found := make(map[string]bool)
	for _, it := range items {
		switch {
		case it.typ == ItemCommentStart:
			found["comment"] = true
		case it.typ == ItemKey && it.val == "port":
			found["key_port"] = true
		case it.typ == ItemInteger && it.val == "4222":
			found["int_4222"] = true
		case it.typ == ItemString && it.val == "127.0.0.1:4222":
			found["ip_addr"] = true
		case it.typ == ItemBool && it.val == "true":
			found["bool_true"] = true
		case it.typ == ItemBool && it.val == "off":
			found["bool_off"] = true
		case it.typ == ItemInteger && it.val == "1MB":
			found["int_suffix"] = true
		case it.typ == ItemMapStart:
			found["map_start"] = true
		case it.typ == ItemMapEnd:
			found["map_end"] = true
		case it.typ == ItemArrayStart:
			found["array_start"] = true
		case it.typ == ItemArrayEnd:
			found["array_end"] = true
		case it.typ == ItemFloat && it.val == "0.5":
			found["float"] = true
		case it.typ == ItemDatetime:
			found["datetime"] = true
		}
	}

	expected := []string{
		"comment", "key_port", "int_4222", "ip_addr",
		"bool_true", "bool_off", "int_suffix",
		"map_start", "map_end", "array_start", "array_end",
		"float", "datetime",
	}
	for _, key := range expected {
		if !found[key] {
			t.Errorf("Missing expected token type: %s", key)
		}
	}

	// Last item should be EOF, not error.
	last := items[len(items)-1]
	if last.typ != ItemEOF {
		t.Fatalf("Expected EOF, got %v", last)
	}
}
