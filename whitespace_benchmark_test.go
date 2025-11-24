package main

import (
	"strings"
	"testing"
	"unicode"
)

// Test strings with different characteristics
var testStrings = []string{
	"no-whitespace-here",
	"has whitespace here",
	"	tab at start",
	"tab	in middle",
	"multiple   spaces   here",
	"newline\nin middle",
	"long-string-with-no-whitespace-that-goes-on-for-quite-a-while-to-test-performance",
	"long string with whitespace that goes on for quite a while to test performance",
	"",
	" ",
	"a",
}

// hasWhitespaceIndexFunc uses strings.IndexFunc with unicode.IsSpace
func hasWhitespaceIndexFunc(s string) bool {
	return strings.IndexFunc(s, unicode.IsSpace) != -1
}

// hasWhitespaceManualLoop manually iterates over the string
func hasWhitespaceManualLoop(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			return true
		}
	}
	return false
}

// hasWhitespaceManualLoopFull manually iterates with full whitespace check
func hasWhitespaceManualLoopFull(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f' {
			return true
		}
	}
	return false
}

// hasWhitespaceRangeLoop uses range to iterate (handles UTF-8 properly)
func hasWhitespaceRangeLoop(s string) bool {
	for _, r := range s {
		if unicode.IsSpace(r) {
			return true
		}
	}
	return false
}

// hasWhitespaceContains uses strings.Contains for common whitespace
func hasWhitespaceContains(s string) bool {
	return strings.Contains(s, " ") ||
		strings.Contains(s, "\t") ||
		strings.Contains(s, "\n") ||
		strings.Contains(s, "\r")
}

// hasWhitespaceContainsAny uses strings.ContainsAny
func hasWhitespaceContainsAny(s string) bool {
	return strings.ContainsAny(s, " \t\n\r")
}

// hasWhitespaceIndexByte uses strings.IndexByte for each whitespace character
func hasWhitespaceIndexByte(s string) bool {
	return strings.IndexByte(s, ' ') != -1 ||
		strings.IndexByte(s, '\t') != -1 ||
		strings.IndexByte(s, '\n') != -1 ||
		strings.IndexByte(s, '\r') != -1
}

func BenchmarkWhitespaceIndexFunc(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for _, s := range testStrings {
			hasWhitespaceIndexFunc(s)
		}
	}
}

func BenchmarkWhitespaceManualLoop(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for _, s := range testStrings {
			hasWhitespaceManualLoop(s)
		}
	}
}

func BenchmarkWhitespaceManualLoopFull(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for _, s := range testStrings {
			hasWhitespaceManualLoopFull(s)
		}
	}
}

func BenchmarkWhitespaceRangeLoop(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for _, s := range testStrings {
			hasWhitespaceRangeLoop(s)
		}
	}
}

func BenchmarkWhitespaceContains(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for _, s := range testStrings {
			hasWhitespaceContains(s)
		}
	}
}

func BenchmarkWhitespaceContainsAny(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for _, s := range testStrings {
			hasWhitespaceContainsAny(s)
		}
	}
}

func BenchmarkWhitespaceIndexByte(b *testing.B) {
	for i := 0; i < b.N; i++ {
		for _, s := range testStrings {
			hasWhitespaceIndexByte(s)
		}
	}
}

// Individual benchmarks for different string types

func BenchmarkNoWhitespace_IndexFunc(b *testing.B) {
	s := "no-whitespace-here-in-this-long-string"
	for i := 0; i < b.N; i++ {
		hasWhitespaceIndexFunc(s)
	}
}

func BenchmarkNoWhitespace_ManualLoop(b *testing.B) {
	s := "no-whitespace-here-in-this-long-string"
	for i := 0; i < b.N; i++ {
		hasWhitespaceManualLoop(s)
	}
}

func BenchmarkNoWhitespace_ContainsAny(b *testing.B) {
	s := "no-whitespace-here-in-this-long-string"
	for i := 0; i < b.N; i++ {
		hasWhitespaceContainsAny(s)
	}
}

func BenchmarkNoWhitespace_IndexByte(b *testing.B) {
	s := "no-whitespace-here-in-this-long-string"
	for i := 0; i < b.N; i++ {
		hasWhitespaceIndexByte(s)
	}
}

func BenchmarkHasWhitespace_IndexFunc(b *testing.B) {
	s := "has whitespace in the middle"
	for i := 0; i < b.N; i++ {
		hasWhitespaceIndexFunc(s)
	}
}

func BenchmarkHasWhitespace_ManualLoop(b *testing.B) {
	s := "has whitespace in the middle"
	for i := 0; i < b.N; i++ {
		hasWhitespaceManualLoop(s)
	}
}

func BenchmarkHasWhitespace_ContainsAny(b *testing.B) {
	s := "has whitespace in the middle"
	for i := 0; i < b.N; i++ {
		hasWhitespaceContainsAny(s)
	}
}

func BenchmarkHasWhitespace_IndexByte(b *testing.B) {
	s := "has whitespace in the middle"
	for i := 0; i < b.N; i++ {
		hasWhitespaceIndexByte(s)
	}
}

func BenchmarkWhitespaceAtStart_IndexFunc(b *testing.B) {
	s := " whitespace-at-start"
	for i := 0; i < b.N; i++ {
		hasWhitespaceIndexFunc(s)
	}
}

func BenchmarkWhitespaceAtStart_ManualLoop(b *testing.B) {
	s := " whitespace-at-start"
	for i := 0; i < b.N; i++ {
		hasWhitespaceManualLoop(s)
	}
}

func BenchmarkWhitespaceAtStart_ContainsAny(b *testing.B) {
	s := " whitespace-at-start"
	for i := 0; i < b.N; i++ {
		hasWhitespaceContainsAny(s)
	}
}

func BenchmarkWhitespaceAtStart_IndexByte(b *testing.B) {
	s := " whitespace-at-start"
	for i := 0; i < b.N; i++ {
		hasWhitespaceIndexByte(s)
	}
}

// Benchmarks for larger strings (>128 bytes)

func BenchmarkLarge256NoWhitespace_ManualLoop(b *testing.B) {
	s := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	for i := 0; i < b.N; i++ {
		hasWhitespaceManualLoop(s)
	}
}

func BenchmarkLarge256NoWhitespace_IndexByte(b *testing.B) {
	s := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	for i := 0; i < b.N; i++ {
		hasWhitespaceIndexByte(s)
	}
}

func BenchmarkLarge256NoWhitespace_ContainsAny(b *testing.B) {
	s := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	for i := 0; i < b.N; i++ {
		hasWhitespaceContainsAny(s)
	}
}

func BenchmarkLarge512NoWhitespace_ManualLoop(b *testing.B) {
	s := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	for i := 0; i < b.N; i++ {
		hasWhitespaceManualLoop(s)
	}
}

func BenchmarkLarge512NoWhitespace_IndexByte(b *testing.B) {
	s := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	for i := 0; i < b.N; i++ {
		hasWhitespaceIndexByte(s)
	}
}

func BenchmarkLarge512NoWhitespace_ContainsAny(b *testing.B) {
	s := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	for i := 0; i < b.N; i++ {
		hasWhitespaceContainsAny(s)
	}
}

func BenchmarkLarge256WithSpaceAt128_ManualLoop(b *testing.B) {
	s := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		" abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	for i := 0; i < b.N; i++ {
		hasWhitespaceManualLoop(s)
	}
}

func BenchmarkLarge256WithSpaceAt128_IndexByte(b *testing.B) {
	s := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		" abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	for i := 0; i < b.N; i++ {
		hasWhitespaceIndexByte(s)
	}
}

func BenchmarkLarge256WithSpaceAt128_ContainsAny(b *testing.B) {
	s := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		" abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_" +
		"abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_"
	for i := 0; i < b.N; i++ {
		hasWhitespaceContainsAny(s)
	}
}
