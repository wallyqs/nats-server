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
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// computeBehavioralDigest computes the behavioral SHA256 digest of a config
// file by parsing it, JSON-encoding the resulting map, and computing the
// SHA256 of that. This matches the digest semantics used by processInclude
// and lockIncludeNode, where comments and formatting don't affect digests.
func computeBehavioralDigest(t *testing.T, filePath string) string {
	t.Helper()
	_, digest, err := ParseFileWithChecksDigest(filePath)
	if err != nil {
		t.Fatalf("computeBehavioralDigest: ParseFileWithChecksDigest(%q) error: %v", filePath, err)
	}
	return digest
}

// --- Lexer Tests ---

// TestLexIncludeDigestDoubleQuoted verifies the lexer emits ItemInclude
// followed by ItemIncludeDigest for double-quoted path with digest.
func TestLexIncludeDigestDoubleQuoted(t *testing.T) {
	input := `include "foo.conf" "sha256:abc123"`
	lx := lex(input)

	expected := []item{
		{ItemInclude, "foo.conf", 1, 9},
		{ItemIncludeDigest, "sha256:abc123", 1, 20},
		{ItemEOF, "", 1, 0},
	}
	for i, exp := range expected {
		got := lx.nextItem()
		if got.typ != exp.typ {
			t.Fatalf("token %d: type = %v, want %v (val=%q)", i, got.typ, exp.typ, got.val)
		}
		if got.val != exp.val {
			t.Fatalf("token %d: val = %q, want %q", i, got.val, exp.val)
		}
	}
}

// TestLexIncludeDigestSingleQuoted verifies single-quoted path with digest.
func TestLexIncludeDigestSingleQuoted(t *testing.T) {
	input := `include 'foo.conf' 'sha256:abc123'`
	lx := lex(input)

	expected := []item{
		{ItemInclude, "foo.conf", 1, 0},
		{ItemIncludeDigest, "sha256:abc123", 1, 0},
		{ItemEOF, "", 1, 0},
	}
	for i, exp := range expected {
		got := lx.nextItem()
		if got.typ != exp.typ {
			t.Fatalf("token %d: type = %v, want %v (val=%q)", i, got.typ, exp.typ, got.val)
		}
		if got.val != exp.val {
			t.Fatalf("token %d: val = %q, want %q", i, got.val, exp.val)
		}
	}
}

// TestLexIncludeDigestUnquotedPath verifies unquoted path with quoted digest.
func TestLexIncludeDigestUnquotedPath(t *testing.T) {
	input := `include foo.conf "sha256:abc123"`
	lx := lex(input)

	expected := []item{
		{ItemInclude, "foo.conf", 1, 0},
		{ItemIncludeDigest, "sha256:abc123", 1, 0},
		{ItemEOF, "", 1, 0},
	}
	for i, exp := range expected {
		got := lx.nextItem()
		if got.typ != exp.typ {
			t.Fatalf("token %d: type = %v, want %v (val=%q)", i, got.typ, exp.typ, got.val)
		}
		if got.val != exp.val {
			t.Fatalf("token %d: val = %q, want %q", i, got.val, exp.val)
		}
	}
}

// TestLexIncludeNoDigest verifies backwards compat: include without digest
// still works and only emits ItemInclude.
func TestLexIncludeNoDigest(t *testing.T) {
	input := `include "foo.conf"`
	lx := lex(input)

	got := lx.nextItem()
	if got.typ != ItemInclude {
		t.Fatalf("expected ItemInclude, got %v", got.typ)
	}
	if got.val != "foo.conf" {
		t.Fatalf("expected val 'foo.conf', got %q", got.val)
	}
	got = lx.nextItem()
	if got.typ != ItemEOF {
		t.Fatalf("expected EOF after include without digest, got %v (val=%q)", got.typ, got.val)
	}
}

// TestLexIncludeNoDigestNewline verifies backwards compat: include followed
// by newline doesn't try to lex digest.
func TestLexIncludeNoDigestNewline(t *testing.T) {
	input := "include \"foo.conf\"\nport = 4222"
	lx := lex(input)

	got := lx.nextItem()
	if got.typ != ItemInclude || got.val != "foo.conf" {
		t.Fatalf("expected ItemInclude 'foo.conf', got %v %q", got.typ, got.val)
	}
	// Next should be ItemKey "port", not ItemIncludeDigest
	got = lx.nextItem()
	if got.typ != ItemKey || got.val != "port" {
		t.Fatalf("expected ItemKey 'port', got %v %q", got.typ, got.val)
	}
}

// TestLexIncludeDigestInMap verifies digest works inside a map block.
func TestLexIncludeDigestInMap(t *testing.T) {
	input := `foo { include "bar.conf" "sha256:def456" }`
	lx := lex(input)

	expected := []item{
		{typ: ItemKey, val: "foo"},
		{typ: ItemMapStart},
		{typ: ItemInclude, val: "bar.conf"},
		{typ: ItemIncludeDigest, val: "sha256:def456"},
		{typ: ItemMapEnd},
		{typ: ItemEOF},
	}
	for i, exp := range expected {
		got := lx.nextItem()
		if got.typ != exp.typ {
			t.Fatalf("token %d: type = %v, want %v (val=%q)", i, got.typ, exp.typ, got.val)
		}
		if exp.val != "" && got.val != exp.val {
			t.Fatalf("token %d: val = %q, want %q", i, got.val, exp.val)
		}
	}
}

// --- ItemIncludeDigest Token String Test ---

func TestItemIncludeDigestString(t *testing.T) {
	if ItemIncludeDigest.String() != "IncludeDigest" {
		t.Fatalf("ItemIncludeDigest.String() = %q, want %q", ItemIncludeDigest.String(), "IncludeDigest")
	}
}

// --- Parser/Integration Tests ---

// TestIncludeWithMatchingDigest verifies that include with correct digest succeeds.
func TestIncludeWithMatchingDigest(t *testing.T) {
	dir := t.TempDir()
	content := []byte("port = 4222\n")

	includeFile := filepath.Join(dir, "included.conf")
	if err := os.WriteFile(includeFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	digest := computeBehavioralDigest(t, includeFile)
	mainContent := fmt.Sprintf("include '%s' '%s'\n", includeFile, digest)
	m, err := Parse(mainContent)
	if err != nil {
		t.Fatalf("expected no error with matching digest, got: %v", err)
	}
	if m["port"] != int64(4222) {
		t.Fatalf("expected port=4222, got %v", m["port"])
	}
}

// TestIncludeWithMismatchingDigest verifies that include with wrong digest fails.
func TestIncludeWithMismatchingDigest(t *testing.T) {
	dir := t.TempDir()
	content := []byte("port = 4222\n")

	includeFile := filepath.Join(dir, "included.conf")
	if err := os.WriteFile(includeFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	mainContent := fmt.Sprintf("include '%s' 'sha256:0000000000000000000000000000000000000000000000000000000000000000'\n", includeFile)
	_, err := Parse(mainContent)
	if err == nil {
		t.Fatal("expected error with mismatching digest")
	}
	if !strings.Contains(err.Error(), "integrity check failed") {
		t.Fatalf("expected error to contain 'integrity check failed', got: %v", err)
	}
}

// TestIncludeWithoutDigestBackwardsCompat verifies that include without
// digest still works unchanged.
func TestIncludeWithoutDigestBackwardsCompat(t *testing.T) {
	dir := t.TempDir()
	content := []byte("port = 4222\n")

	includeFile := filepath.Join(dir, "included.conf")
	if err := os.WriteFile(includeFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	mainContent := fmt.Sprintf("include '%s'\n", includeFile)
	m, err := Parse(mainContent)
	if err != nil {
		t.Fatalf("expected no error without digest, got: %v", err)
	}
	if m["port"] != int64(4222) {
		t.Fatalf("expected port=4222, got %v", m["port"])
	}
}

// TestIncludeDigestAllPathStyles verifies digest works with all three
// path styles: single-quoted, double-quoted, and unquoted.
func TestIncludeDigestAllPathStyles(t *testing.T) {
	dir := t.TempDir()
	content := []byte("port = 4222\n")

	includeFile := filepath.Join(dir, "included.conf")
	if err := os.WriteFile(includeFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	digest := computeBehavioralDigest(t, includeFile)

	tests := []struct {
		name  string
		input string
	}{
		{"single-quoted", fmt.Sprintf("include '%s' '%s'\n", includeFile, digest)},
		{"double-quoted", fmt.Sprintf("include \"%s\" \"%s\"\n", includeFile, digest)},
		{"unquoted-path", fmt.Sprintf("include %s \"%s\"\n", includeFile, digest)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := Parse(tt.input)
			if err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
			if m["port"] != int64(4222) {
				t.Fatalf("expected port=4222, got %v", m["port"])
			}
		})
	}
}

// TestIncludeDigestInsideMapBlock verifies digest works when include
// is inside a map block.
func TestIncludeDigestInsideMapBlock(t *testing.T) {
	dir := t.TempDir()
	content := []byte("user = \"admin\"\npassword = \"secret\"\n")

	includeFile := filepath.Join(dir, "auth.conf")
	if err := os.WriteFile(includeFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	digest := computeBehavioralDigest(t, includeFile)

	mainContent := fmt.Sprintf("authorization {\n  include '%s' '%s'\n}\n", includeFile, digest)
	m, err := Parse(mainContent)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	auth, ok := m["authorization"].(map[string]any)
	if !ok {
		t.Fatalf("expected authorization to be map, got %T", m["authorization"])
	}
	if auth["user"] != "admin" {
		t.Fatalf("expected user=admin, got %v", auth["user"])
	}
	if auth["password"] != "secret" {
		t.Fatalf("expected password=secret, got %v", auth["password"])
	}
}

// TestIncludeDigestErrorIncludesPosition verifies that the integrity
// check failed error includes position information.
func TestIncludeDigestErrorIncludesPosition(t *testing.T) {
	dir := t.TempDir()
	content := []byte("port = 4222\n")

	includeFile := filepath.Join(dir, "included.conf")
	if err := os.WriteFile(includeFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	mainContent := fmt.Sprintf("include '%s' 'sha256:baddigest'\n", includeFile)
	_, err := Parse(mainContent)
	if err == nil {
		t.Fatal("expected error with bad digest")
	}
	errStr := err.Error()
	// Error should contain the include file path.
	if !strings.Contains(errStr, includeFile) {
		t.Fatalf("error should contain include file path, got: %v", errStr)
	}
	// Error should contain "integrity check failed".
	if !strings.Contains(errStr, "integrity check failed") {
		t.Fatalf("error should contain 'integrity check failed', got: %v", errStr)
	}
	// Error should contain expected and actual digests.
	if !strings.Contains(errStr, "sha256:baddigest") {
		t.Fatalf("error should contain expected digest, got: %v", errStr)
	}
	if !strings.Contains(errStr, "sha256:") {
		t.Fatalf("error should contain actual digest, got: %v", errStr)
	}
}

// TestIncludeDigestASTRoundTrip verifies that ParseASTRaw preserves the
// digest in IncludeNode.Digest and Emit outputs it correctly.
func TestIncludeDigestASTRoundTrip(t *testing.T) {
	input := "include './foo.conf' 'sha256:abc123def'\n"
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw error: %v", err)
	}

	// Find the IncludeNode and verify Digest field.
	var found *IncludeNode
	for _, item := range doc.Items {
		if inc, ok := item.(*IncludeNode); ok {
			found = inc
			break
		}
	}
	if found == nil {
		t.Fatal("expected IncludeNode in AST")
	}
	if found.Path != "./foo.conf" {
		t.Fatalf("expected Path='./foo.conf', got %q", found.Path)
	}
	if found.Digest != "sha256:abc123def" {
		t.Fatalf("expected Digest='sha256:abc123def', got %q", found.Digest)
	}

	// Emit and verify the digest appears in output.
	out, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit error: %v", err)
	}
	result := string(out)
	if !strings.Contains(result, "sha256:abc123def") {
		t.Fatalf("Emitted output should contain digest, got:\n%s", result)
	}
	if !strings.Contains(result, "include") {
		t.Fatalf("Emitted output should contain 'include', got:\n%s", result)
	}
}

// TestIncludeDigestASTRoundTripNoDigest verifies that ParseASTRaw and Emit
// work correctly when no digest is present (backwards compat).
func TestIncludeDigestASTRoundTripNoDigest(t *testing.T) {
	input := "include './foo.conf'\n"
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw error: %v", err)
	}

	var found *IncludeNode
	for _, item := range doc.Items {
		if inc, ok := item.(*IncludeNode); ok {
			found = inc
			break
		}
	}
	if found == nil {
		t.Fatal("expected IncludeNode in AST")
	}
	if found.Digest != "" {
		t.Fatalf("expected empty Digest, got %q", found.Digest)
	}

	out, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit error: %v", err)
	}
	result := string(out)
	if strings.Contains(result, "sha256") {
		t.Fatalf("Emitted output should NOT contain digest when not present, got:\n%s", result)
	}
}

// TestIncludeDigestParseFile verifies ParseFile supports include with digest.
func TestIncludeDigestParseFile(t *testing.T) {
	dir := t.TempDir()
	content := []byte("port = 4222\n")

	includeFile := filepath.Join(dir, "included.conf")
	if err := os.WriteFile(includeFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	digest := computeBehavioralDigest(t, includeFile)

	mainContent := fmt.Sprintf("include '%s' '%s'\n", includeFile, digest)
	mainFile := filepath.Join(dir, "main.conf")
	if err := os.WriteFile(mainFile, []byte(mainContent), 0644); err != nil {
		t.Fatal(err)
	}

	m, err := ParseFile(mainFile)
	if err != nil {
		t.Fatalf("ParseFile error: %v", err)
	}
	if m["port"] != int64(4222) {
		t.Fatalf("expected port=4222, got %v", m["port"])
	}
}

// TestIncludeDigestDoubleQuotedPathDoubleQuotedDigest verifies
// double-quoted path with double-quoted digest.
func TestIncludeDigestDoubleQuotedPathDoubleQuotedDigest(t *testing.T) {
	dir := t.TempDir()
	content := []byte("port = 4222\n")

	includeFile := filepath.Join(dir, "included.conf")
	if err := os.WriteFile(includeFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	digest := computeBehavioralDigest(t, includeFile)

	mainContent := fmt.Sprintf("include \"%s\" \"%s\"\n", includeFile, digest)
	m, err := Parse(mainContent)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if m["port"] != int64(4222) {
		t.Fatalf("expected port=4222, got %v", m["port"])
	}
}

// TestIncludeDigestASTRoundTripMapContext verifies AST round-trip for
// include with digest inside a map block.
func TestIncludeDigestASTRoundTripMapContext(t *testing.T) {
	input := "authorization {\n  include './auth.conf' 'sha256:xyz789'\n}\n"
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw error: %v", err)
	}

	// Find the IncludeNode inside the map.
	var found *IncludeNode
	for _, item := range doc.Items {
		if kv, ok := item.(*KeyValueNode); ok {
			if m, ok := kv.Value.(*MapNode); ok {
				for _, mi := range m.Items {
					if inc, ok := mi.(*IncludeNode); ok {
						found = inc
					}
				}
			}
		}
	}
	if found == nil {
		t.Fatal("expected IncludeNode in authorization map")
	}
	if found.Digest != "sha256:xyz789" {
		t.Fatalf("expected Digest='sha256:xyz789', got %q", found.Digest)
	}

	// Emit and verify digest appears.
	out, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit error: %v", err)
	}
	result := string(out)
	if !strings.Contains(result, "sha256:xyz789") {
		t.Fatalf("Emitted output should contain digest, got:\n%s", result)
	}
}

// TestExistingIncludeTestsStillPass is a marker test to ensure
// backwards compatibility — the existing include tests in parse_test.go
// and compat_test.go still pass alongside these new tests.
func TestExistingIncludeTestsStillPass(t *testing.T) {
	// This test just verifies that basic include parsing works.
	dir := t.TempDir()
	content := []byte("host = \"localhost\"\n")
	includeFile := filepath.Join(dir, "basic.conf")
	if err := os.WriteFile(includeFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	// Single-quoted include (no digest) still works.
	m, err := Parse(fmt.Sprintf("include '%s'\n", includeFile))
	if err != nil {
		t.Fatalf("basic include failed: %v", err)
	}
	if m["host"] != "localhost" {
		t.Fatalf("expected host=localhost, got %v", m["host"])
	}

	// Double-quoted include (no digest) still works.
	m, err = Parse(fmt.Sprintf("include \"%s\"\n", includeFile))
	if err != nil {
		t.Fatalf("double-quoted include failed: %v", err)
	}
	if m["host"] != "localhost" {
		t.Fatalf("expected host=localhost, got %v", m["host"])
	}

	// Unquoted include (no digest) still works.
	m, err = Parse(fmt.Sprintf("include %s\n", includeFile))
	if err != nil {
		t.Fatalf("unquoted include failed: %v", err)
	}
	if m["host"] != "localhost" {
		t.Fatalf("expected host=localhost, got %v", m["host"])
	}
}

// TestIncludeDigestIgnoresComments verifies that two files with the same
// key-value pairs but different comments produce the same behavioral digest.
// This is the key property of behavioral digests: only the parsed configuration
// values matter, not comments or formatting.
func TestIncludeDigestIgnoresComments(t *testing.T) {
	dir := t.TempDir()

	// File with comments.
	contentWithComments := []byte("# This is a comment\nport = 4222\n# Another comment\nhost = \"localhost\"\n")
	fileWithComments := filepath.Join(dir, "with_comments.conf")
	if err := os.WriteFile(fileWithComments, contentWithComments, 0644); err != nil {
		t.Fatal(err)
	}

	// File without comments, same keys and values.
	contentWithoutComments := []byte("port = 4222\nhost = \"localhost\"\n")
	fileWithoutComments := filepath.Join(dir, "without_comments.conf")
	if err := os.WriteFile(fileWithoutComments, contentWithoutComments, 0644); err != nil {
		t.Fatal(err)
	}

	digestWithComments := computeBehavioralDigest(t, fileWithComments)
	digestWithoutComments := computeBehavioralDigest(t, fileWithoutComments)

	if digestWithComments != digestWithoutComments {
		t.Fatalf("behavioral digests should be the same regardless of comments:\n  with comments:    %s\n  without comments: %s",
			digestWithComments, digestWithoutComments)
	}

	// Now verify that an include using this digest works for both files.
	mainContent := fmt.Sprintf("include '%s' '%s'\n", fileWithComments, digestWithComments)
	m, err := Parse(mainContent)
	if err != nil {
		t.Fatalf("expected no error with behavioral digest for commented file, got: %v", err)
	}
	if m["port"] != int64(4222) {
		t.Fatalf("expected port=4222, got %v", m["port"])
	}

	// The same digest should also work for the uncommented file.
	mainContent2 := fmt.Sprintf("include '%s' '%s'\n", fileWithoutComments, digestWithoutComments)
	m2, err := Parse(mainContent2)
	if err != nil {
		t.Fatalf("expected no error with behavioral digest for uncommented file, got: %v", err)
	}
	if m2["port"] != int64(4222) {
		t.Fatalf("expected port=4222, got %v", m2["port"])
	}

	// But a file with different values should produce a different digest.
	contentDifferent := []byte("port = 5222\nhost = \"localhost\"\n")
	fileDifferent := filepath.Join(dir, "different.conf")
	if err := os.WriteFile(fileDifferent, contentDifferent, 0644); err != nil {
		t.Fatal(err)
	}

	digestDifferent := computeBehavioralDigest(t, fileDifferent)
	if digestDifferent == digestWithComments {
		t.Fatalf("different config values should produce different behavioral digests, both got: %s", digestDifferent)
	}

	// Verify the wrong digest is rejected.
	mainContent3 := fmt.Sprintf("include '%s' '%s'\n", fileDifferent, digestWithComments)
	_, err = Parse(mainContent3)
	if err == nil {
		t.Fatal("expected error when using wrong behavioral digest")
	}
	if !strings.Contains(err.Error(), "integrity check failed") {
		t.Fatalf("expected 'integrity check failed' error, got: %v", err)
	}
}
