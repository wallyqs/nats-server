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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Helper to parse and return document or fail.
func mustParse(t *testing.T, input string) *Document {
	t.Helper()
	doc, err := ParseAST(input)
	if err != nil {
		t.Fatalf("Unexpected parse error: %v", err)
	}
	return doc
}

// Helper to parse and expect an error.
func expectParseError(t *testing.T, input string, substr string) {
	t.Helper()
	_, err := ParseAST(input)
	if err == nil {
		t.Fatalf("Expected parse error containing %q, got nil", substr)
	}
	if !strings.Contains(err.Error(), substr) {
		t.Fatalf("Expected error containing %q, got: %v", substr, err)
	}
}

// --- Basic Parsing ---

func TestParseEmptyConfig(t *testing.T) {
	doc := mustParse(t, "")
	if doc == nil {
		t.Fatal("Expected non-nil Document")
	}
	if len(doc.Items) != 0 {
		t.Fatalf("Expected 0 items, got %d", len(doc.Items))
	}
}

func TestParseCommentOnlyConfig(t *testing.T) {
	doc := mustParse(t, "# just a comment\n// another comment\n")
	if doc == nil {
		t.Fatal("Expected non-nil Document")
	}
	// Comment-only configs should be valid, comments should appear as items.
}

func TestParseStringValue(t *testing.T) {
	doc := mustParse(t, `foo = "hello"`)
	if len(doc.Items) < 1 {
		t.Fatal("Expected at least 1 item")
	}
	kv, ok := doc.Items[0].(*KeyValueNode)
	if !ok {
		t.Fatalf("Expected KeyValueNode, got %T", doc.Items[0])
	}
	if kv.Key.Name != "foo" {
		t.Fatalf("Expected key 'foo', got %q", kv.Key.Name)
	}
	sv, ok := kv.Value.(*StringNode)
	if !ok {
		t.Fatalf("Expected StringNode, got %T", kv.Value)
	}
	if sv.Value != "hello" {
		t.Fatalf("Expected value 'hello', got %q", sv.Value)
	}
}

func TestParseSingleQuotedString(t *testing.T) {
	doc := mustParse(t, `foo = 'bar'`)
	kv := doc.Items[0].(*KeyValueNode)
	sv := kv.Value.(*StringNode)
	if sv.Value != "bar" {
		t.Fatalf("Expected 'bar', got %q", sv.Value)
	}
}

func TestParseUnquotedString(t *testing.T) {
	doc := mustParse(t, `foo = some_value`)
	kv := doc.Items[0].(*KeyValueNode)
	sv := kv.Value.(*StringNode)
	if sv.Value != "some_value" {
		t.Fatalf("Expected 'some_value', got %q", sv.Value)
	}
}

func TestParseIntegerValue(t *testing.T) {
	doc := mustParse(t, `port = 4222`)
	kv := doc.Items[0].(*KeyValueNode)
	iv, ok := kv.Value.(*IntegerNode)
	if !ok {
		t.Fatalf("Expected IntegerNode, got %T", kv.Value)
	}
	if iv.Value != 4222 {
		t.Fatalf("Expected 4222, got %d", iv.Value)
	}
}

func TestParseNegativeInteger(t *testing.T) {
	doc := mustParse(t, `offset = -10`)
	kv := doc.Items[0].(*KeyValueNode)
	iv := kv.Value.(*IntegerNode)
	if iv.Value != -10 {
		t.Fatalf("Expected -10, got %d", iv.Value)
	}
}

func TestParseFloatValue(t *testing.T) {
	doc := mustParse(t, `rate = 3.14`)
	kv := doc.Items[0].(*KeyValueNode)
	fv, ok := kv.Value.(*FloatNode)
	if !ok {
		t.Fatalf("Expected FloatNode, got %T", kv.Value)
	}
	if fv.Value != 3.14 {
		t.Fatalf("Expected 3.14, got %f", fv.Value)
	}
}

func TestParseNegativeFloat(t *testing.T) {
	doc := mustParse(t, `temp = -22.5`)
	kv := doc.Items[0].(*KeyValueNode)
	fv := kv.Value.(*FloatNode)
	if fv.Value != -22.5 {
		t.Fatalf("Expected -22.5, got %f", fv.Value)
	}
}

func TestParseBoolValues(t *testing.T) {
	for _, tc := range []struct {
		input    string
		expected bool
	}{
		{`debug = true`, true},
		{`debug = TRUE`, true},
		{`debug = True`, true},
		{`debug = yes`, true},
		{`debug = on`, true},
		{`debug = false`, false},
		{`debug = FALSE`, false},
		{`debug = no`, false},
		{`debug = off`, false},
	} {
		t.Run(tc.input, func(t *testing.T) {
			doc := mustParse(t, tc.input)
			kv := doc.Items[0].(*KeyValueNode)
			bv, ok := kv.Value.(*BoolNode)
			if !ok {
				t.Fatalf("Expected BoolNode, got %T", kv.Value)
			}
			if bv.Value != tc.expected {
				t.Fatalf("Expected %v, got %v", tc.expected, bv.Value)
			}
		})
	}
}

func TestParseDatetimeValue(t *testing.T) {
	doc := mustParse(t, `created = 2016-05-04T18:53:41Z`)
	kv := doc.Items[0].(*KeyValueNode)
	dv, ok := kv.Value.(*DatetimeNode)
	if !ok {
		t.Fatalf("Expected DatetimeNode, got %T", kv.Value)
	}
	expected, _ := time.Parse("2006-01-02T15:04:05Z", "2016-05-04T18:53:41Z")
	if !dv.Value.Equal(expected) {
		t.Fatalf("Expected %v, got %v", expected, dv.Value)
	}
}

func TestParseIntegerWithSizeSuffix(t *testing.T) {
	for _, tc := range []struct {
		input    string
		expected int64
	}{
		{`size = 8k`, int64(8 * 1000)},
		{`size = 4kb`, int64(4 * 1024)},
		{`size = 3ki`, int64(3 * 1024)},
		{`size = 4kib`, int64(4 * 1024)},
		{`size = 1m`, int64(1000 * 1000)},
		{`size = 2MB`, int64(2 * 1024 * 1024)},
		{`size = 2Mi`, int64(2 * 1024 * 1024)},
		{`size = 64MiB`, int64(64 * 1024 * 1024)},
		{`size = 2g`, int64(2 * 1000 * 1000 * 1000)},
		{`size = 22GB`, int64(22 * 1024 * 1024 * 1024)},
		{`size = 22Gi`, int64(22 * 1024 * 1024 * 1024)},
		{`size = 22GiB`, int64(22 * 1024 * 1024 * 1024)},
		{`size = 22TB`, int64(22 * 1024 * 1024 * 1024 * 1024)},
		{`size = 22Ti`, int64(22 * 1024 * 1024 * 1024 * 1024)},
		{`size = 22TiB`, int64(22 * 1024 * 1024 * 1024 * 1024)},
		{`size = 22PB`, int64(22 * 1024 * 1024 * 1024 * 1024 * 1024)},
		{`size = 22Pi`, int64(22 * 1024 * 1024 * 1024 * 1024 * 1024)},
		{`size = 22PiB`, int64(22 * 1024 * 1024 * 1024 * 1024 * 1024)},
	} {
		t.Run(tc.input, func(t *testing.T) {
			doc := mustParse(t, tc.input)
			kv := doc.Items[0].(*KeyValueNode)
			iv, ok := kv.Value.(*IntegerNode)
			if !ok {
				t.Fatalf("Expected IntegerNode, got %T", kv.Value)
			}
			if iv.Value != tc.expected {
				t.Fatalf("Expected %d, got %d", tc.expected, iv.Value)
			}
		})
	}
}

// --- Key Separator Styles ---

func TestParseKeySeparatorEquals(t *testing.T) {
	doc := mustParse(t, `foo = 1`)
	kv := doc.Items[0].(*KeyValueNode)
	if kv.Key.Separator != SepEquals {
		t.Fatalf("Expected SepEquals, got %v", kv.Key.Separator)
	}
}

func TestParseKeySeparatorColon(t *testing.T) {
	doc := mustParse(t, `foo: 1`)
	kv := doc.Items[0].(*KeyValueNode)
	if kv.Key.Separator != SepColon {
		t.Fatalf("Expected SepColon, got %v", kv.Key.Separator)
	}
}

func TestParseKeySeparatorSpace(t *testing.T) {
	doc := mustParse(t, `foo 1`)
	kv := doc.Items[0].(*KeyValueNode)
	if kv.Key.Separator != SepSpace {
		t.Fatalf("Expected SepSpace, got %v", kv.Key.Separator)
	}
}

// --- Map/Block Parsing ---

func TestParseMapBlock(t *testing.T) {
	doc := mustParse(t, `
cluster {
  port: 4244
  name: "test"
}
`)
	if len(doc.Items) < 1 {
		t.Fatal("Expected at least 1 item")
	}
	kv := doc.Items[0].(*KeyValueNode)
	if kv.Key.Name != "cluster" {
		t.Fatalf("Expected key 'cluster', got %q", kv.Key.Name)
	}
	mv, ok := kv.Value.(*MapNode)
	if !ok {
		t.Fatalf("Expected MapNode, got %T", kv.Value)
	}
	if len(mv.Items) < 2 {
		t.Fatalf("Expected at least 2 items in map, got %d", len(mv.Items))
	}
}

func TestParseNestedMap(t *testing.T) {
	doc := mustParse(t, `
foo {
  host {
    ip = '127.0.0.1'
    port = 4242
  }
  servers = ["a.com", "b.com"]
}
`)
	kv := doc.Items[0].(*KeyValueNode)
	mv := kv.Value.(*MapNode)
	// Should have 'host' and 'servers'
	count := 0
	for _, item := range mv.Items {
		if _, ok := item.(*KeyValueNode); ok {
			count++
		}
	}
	if count < 2 {
		t.Fatalf("Expected at least 2 key-value items in map, got %d", count)
	}
}

// --- Array Parsing ---

func TestParseArray(t *testing.T) {
	doc := mustParse(t, `routes = ["a.com", "b.com", "c.com"]`)
	kv := doc.Items[0].(*KeyValueNode)
	arr, ok := kv.Value.(*ArrayNode)
	if !ok {
		t.Fatalf("Expected ArrayNode, got %T", kv.Value)
	}
	if len(arr.Elements) != 3 {
		t.Fatalf("Expected 3 elements, got %d", len(arr.Elements))
	}
}

func TestParseArrayOfMaps(t *testing.T) {
	doc := mustParse(t, `
array [
  { abc: 123 }
  { xyz: "word" }
]
`)
	kv := doc.Items[0].(*KeyValueNode)
	arr := kv.Value.(*ArrayNode)
	if len(arr.Elements) != 2 {
		t.Fatalf("Expected 2 elements, got %d", len(arr.Elements))
	}
	m1, ok := arr.Elements[0].(*MapNode)
	if !ok {
		t.Fatalf("Expected MapNode, got %T", arr.Elements[0])
	}
	if len(m1.Items) < 1 {
		t.Fatal("Expected at least 1 item in first map")
	}
}

// --- Comment Attachment ---

func TestParseTrailingComment(t *testing.T) {
	doc := mustParse(t, `port = 4222 # default port`)
	kv := doc.Items[0].(*KeyValueNode)
	if kv.Comment == nil {
		t.Fatal("Expected trailing comment on key-value")
	}
	if !strings.Contains(kv.Comment.Text, "default port") {
		t.Fatalf("Expected comment containing 'default port', got %q", kv.Comment.Text)
	}
}

func TestParseLeadingComment(t *testing.T) {
	doc := mustParse(t, `# port configuration
port = 4222`)
	// The leading comment should be in the document items before the key-value
	if len(doc.Items) < 2 {
		t.Fatalf("Expected at least 2 items (comment + kv), got %d", len(doc.Items))
	}
	_, ok := doc.Items[0].(*CommentNode)
	if !ok {
		t.Fatalf("Expected first item to be CommentNode, got %T", doc.Items[0])
	}
	_, ok = doc.Items[1].(*KeyValueNode)
	if !ok {
		t.Fatalf("Expected second item to be KeyValueNode, got %T", doc.Items[1])
	}
}

func TestParseCommentsInsideArray(t *testing.T) {
	doc := mustParse(t, `routes = [
  "a.com" # first
  "b.com" # second
]`)
	kv := doc.Items[0].(*KeyValueNode)
	arr := kv.Value.(*ArrayNode)
	// Array should have elements. Comments inside arrays are mixed
	// with value elements, so count only non-comment elements.
	count := 0
	for _, el := range arr.Elements {
		if _, ok := el.(*CommentNode); !ok {
			count++
		}
	}
	if count < 2 {
		t.Fatalf("Expected at least 2 non-comment elements, got %d", count)
	}
}

func TestParseSlashComment(t *testing.T) {
	doc := mustParse(t, `port = 4222 // slash comment`)
	kv := doc.Items[0].(*KeyValueNode)
	if kv.Comment == nil {
		t.Fatal("Expected trailing slash comment on key-value")
	}
	if kv.Comment.Style != CommentSlash {
		t.Fatalf("Expected CommentSlash, got %v", kv.Comment.Style)
	}
}

// --- Variable Resolution ---

func TestParseVariableSimple(t *testing.T) {
	doc := mustParse(t, `
index = 22
foo = $index
`)
	// foo should be resolved to 22
	kvs := extractKeyValues(doc)
	if v, ok := kvs["foo"]; ok {
		iv, ok := v.(*IntegerNode)
		if !ok {
			t.Fatalf("Expected IntegerNode for 'foo', got %T", v)
		}
		if iv.Value != 22 {
			t.Fatalf("Expected 22, got %d", iv.Value)
		}
	} else {
		t.Fatal("Expected 'foo' key in document")
	}
}

func TestParseVariableNestedScope(t *testing.T) {
	doc := mustParse(t, `
index = 22
nest {
  index = 11
  foo = $index
}
bar = $index
`)
	kvs := extractKeyValues(doc)

	// bar should resolve to outer index (22)
	if v, ok := kvs["bar"]; ok {
		iv := v.(*IntegerNode)
		if iv.Value != 22 {
			t.Fatalf("Expected bar=22, got %d", iv.Value)
		}
	} else {
		t.Fatal("Expected 'bar' key")
	}

	// nest.foo should resolve to inner index (11)
	if nestVal, ok := kvs["nest"]; ok {
		mv := nestVal.(*MapNode)
		nestKVs := extractKeyValues(&Document{Items: mv.Items})
		if v, ok := nestKVs["foo"]; ok {
			iv := v.(*IntegerNode)
			if iv.Value != 11 {
				t.Fatalf("Expected nest.foo=11, got %d", iv.Value)
			}
		} else {
			t.Fatal("Expected 'foo' key in nest")
		}
	} else {
		t.Fatal("Expected 'nest' key")
	}
}

func TestParseVariableMissing(t *testing.T) {
	expectParseError(t, `foo = $missing_var`, "variable reference")
}

func TestParseVariableEnvFallback(t *testing.T) {
	evar := "__V2_TEST_ENVVAR__"
	os.Setenv(evar, "42")
	defer os.Unsetenv(evar)

	doc := mustParse(t, `foo = $__V2_TEST_ENVVAR__`)
	kvs := extractKeyValues(doc)
	if v, ok := kvs["foo"]; ok {
		iv, ok := v.(*IntegerNode)
		if !ok {
			t.Fatalf("Expected IntegerNode for env var, got %T", v)
		}
		if iv.Value != 42 {
			t.Fatalf("Expected 42, got %d", iv.Value)
		}
	} else {
		t.Fatal("Expected 'foo' key")
	}
}

func TestParseVariableEnvString(t *testing.T) {
	evar := "__V2_TEST_ENVSTR__"
	os.Setenv(evar, "hello")
	defer os.Unsetenv(evar)

	doc := mustParse(t, `foo = $__V2_TEST_ENVSTR__`)
	kvs := extractKeyValues(doc)
	if v, ok := kvs["foo"]; ok {
		sv, ok := v.(*StringNode)
		if !ok {
			t.Fatalf("Expected StringNode for env var string, got %T", v)
		}
		if sv.Value != "hello" {
			t.Fatalf("Expected 'hello', got %q", sv.Value)
		}
	} else {
		t.Fatal("Expected 'foo' key")
	}
}

func TestParseVariableEnvBool(t *testing.T) {
	evar := "__V2_TEST_ENVBOOL__"
	os.Setenv(evar, "true")
	defer os.Unsetenv(evar)

	doc := mustParse(t, `foo = $__V2_TEST_ENVBOOL__`)
	kvs := extractKeyValues(doc)
	if v, ok := kvs["foo"]; ok {
		bv, ok := v.(*BoolNode)
		if !ok {
			t.Fatalf("Expected BoolNode for env var bool, got %T", v)
		}
		if !bv.Value {
			t.Fatal("Expected true")
		}
	} else {
		t.Fatal("Expected 'foo' key")
	}
}

func TestParseVariableCycleDetection(t *testing.T) {
	// Create env var that references itself
	evar := "__V2_TEST_CYCLE__"
	os.Setenv(evar, "$__V2_TEST_CYCLE__")
	defer os.Unsetenv(evar)

	expectParseError(t, `foo = $__V2_TEST_CYCLE__`, "variable reference cycle")
}

func TestParseBcryptVariable(t *testing.T) {
	doc := mustParse(t, `password: $2a$11$ooo`)
	kvs := extractKeyValues(doc)
	if v, ok := kvs["password"]; ok {
		sv, ok := v.(*StringNode)
		if !ok {
			t.Fatalf("Expected StringNode for bcrypt, got %T", v)
		}
		if sv.Value != "$2a$11$ooo" {
			t.Fatalf("Expected '$2a$11$ooo', got %q", sv.Value)
		}
	} else {
		t.Fatal("Expected 'password' key")
	}
}

// --- Include Handling ---

func TestParseIncludeFile(t *testing.T) {
	dir := t.TempDir()
	includedContent := `port = 4222`
	includeFile := filepath.Join(dir, "included.conf")
	if err := os.WriteFile(includeFile, []byte(includedContent), 0644); err != nil {
		t.Fatal(err)
	}

	mainContent := `include '` + includeFile + `'`
	doc := mustParse(t, mainContent)
	kvs := extractKeyValues(doc)
	if v, ok := kvs["port"]; ok {
		iv := v.(*IntegerNode)
		if iv.Value != 4222 {
			t.Fatalf("Expected 4222, got %d", iv.Value)
		}
	} else {
		t.Fatal("Expected 'port' from include")
	}
}

func TestParseIncludeInsideMap(t *testing.T) {
	dir := t.TempDir()
	includedContent := `user = "admin"
password = "secret"`
	includeFile := filepath.Join(dir, "auth.conf")
	if err := os.WriteFile(includeFile, []byte(includedContent), 0644); err != nil {
		t.Fatal(err)
	}

	mainContent := `auth {
  include '` + includeFile + `'
  timeout = 5
}`
	doc := mustParse(t, mainContent)
	kvs := extractKeyValues(doc)
	if v, ok := kvs["auth"]; ok {
		mv := v.(*MapNode)
		mapKVs := extractKeyValues(&Document{Items: mv.Items})
		if _, ok := mapKVs["user"]; !ok {
			t.Fatal("Expected 'user' from included file inside map")
		}
		if _, ok := mapKVs["password"]; !ok {
			t.Fatal("Expected 'password' from included file inside map")
		}
		if _, ok := mapKVs["timeout"]; !ok {
			t.Fatal("Expected 'timeout' key in map")
		}
	} else {
		t.Fatal("Expected 'auth' key")
	}
}

func TestParseIncludeRecursive(t *testing.T) {
	dir := t.TempDir()

	// inner.conf
	innerContent := `inner_key = "inner_value"`
	innerFile := filepath.Join(dir, "inner.conf")
	if err := os.WriteFile(innerFile, []byte(innerContent), 0644); err != nil {
		t.Fatal(err)
	}

	// outer.conf includes inner.conf
	outerContent := `include 'inner.conf'
outer_key = "outer_value"`
	outerFile := filepath.Join(dir, "outer.conf")
	if err := os.WriteFile(outerFile, []byte(outerContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Main includes outer.conf
	mainContent := `include '` + outerFile + `'`
	doc := mustParse(t, mainContent)
	kvs := extractKeyValues(doc)
	if _, ok := kvs["inner_key"]; !ok {
		t.Fatal("Expected 'inner_key' from recursive include")
	}
	if _, ok := kvs["outer_key"]; !ok {
		t.Fatal("Expected 'outer_key' from recursive include")
	}
}

func TestParseIncludeErrorPropagation(t *testing.T) {
	dir := t.TempDir()
	// Invalid included file
	badContent := `invalid_no_value_key`
	badFile := filepath.Join(dir, "bad.conf")
	if err := os.WriteFile(badFile, []byte(badContent), 0644); err != nil {
		t.Fatal(err)
	}

	mainContent := `include '` + badFile + `'`
	_, err := ParseAST(mainContent)
	if err == nil {
		t.Fatal("Expected error from bad include file")
	}
	if !strings.Contains(err.Error(), "bad.conf") {
		t.Fatalf("Expected error to contain filename 'bad.conf', got: %v", err)
	}
}

func TestParseIncludeEmptyFile(t *testing.T) {
	dir := t.TempDir()
	emptyFile := filepath.Join(dir, "empty.conf")
	if err := os.WriteFile(emptyFile, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}

	mainContent := `foo = 1
include '` + emptyFile + `'
bar = 2`
	doc := mustParse(t, mainContent)
	kvs := extractKeyValues(doc)
	if _, ok := kvs["foo"]; !ok {
		t.Fatal("Expected 'foo' key")
	}
	if _, ok := kvs["bar"]; !ok {
		t.Fatal("Expected 'bar' key")
	}
}

func TestParseIncludeCommentOnly(t *testing.T) {
	dir := t.TempDir()
	commentFile := filepath.Join(dir, "comments.conf")
	if err := os.WriteFile(commentFile, []byte("# just comments\n# no keys\n"), 0644); err != nil {
		t.Fatal(err)
	}

	mainContent := `foo = 1
include '` + commentFile + `'`
	doc := mustParse(t, mainContent)
	kvs := extractKeyValues(doc)
	if _, ok := kvs["foo"]; !ok {
		t.Fatal("Expected 'foo' key")
	}
}

// --- Duplicate Key Last-Wins ---

func TestParseDuplicateKeyLastWins(t *testing.T) {
	doc := mustParse(t, `
foo = 1
foo = 2
`)
	kvs := extractKeyValues(doc)
	if v, ok := kvs["foo"]; ok {
		iv := v.(*IntegerNode)
		if iv.Value != 2 {
			t.Fatalf("Expected last-wins value 2, got %d", iv.Value)
		}
	} else {
		t.Fatal("Expected 'foo' key")
	}
}

// --- Invalid Config Detection ---

func TestParseInvalidKeyNoValue(t *testing.T) {
	expectParseError(t, `aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa`, "config is invalid")
}

func TestParseInvalidKeyNoValueAfterComments(t *testing.T) {
	expectParseError(t, `
# with comments
# is also invalid
aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
`, "config is invalid")
}

// --- Float Overflow Detection ---

func TestParseFloatOverflow(t *testing.T) {
	// The lexer treats 'e' as a size suffix, so 1e999 becomes integer token "1" + string "e999"
	// or similar. But large float values like 99999999999999999999.99999999999 that overflow
	// should be caught. Let's test with values that ParseFloat returns Inf.
	// We need a float that overflows float64.
	bigFloat := "1" + strings.Repeat("0", 309) + ".0"
	expectParseError(t, `val = `+bigFloat, "float")
}

// --- Position Information ---

func TestParsePositionInfo(t *testing.T) {
	doc := mustParse(t, `foo = 1
bar = 2`)
	if len(doc.Items) < 2 {
		t.Fatalf("Expected at least 2 items, got %d", len(doc.Items))
	}
	kv1 := doc.Items[0].(*KeyValueNode)
	kv2 := doc.Items[1].(*KeyValueNode)

	// First key should be on line 1
	if kv1.Key.Pos.Line != 1 {
		t.Fatalf("Expected key 'foo' on line 1, got %d", kv1.Key.Pos.Line)
	}

	// Second key should be on line 2
	if kv2.Key.Pos.Line != 2 {
		t.Fatalf("Expected key 'bar' on line 2, got %d", kv2.Key.Pos.Line)
	}
}

// --- Block Strings ---

func TestParseBlockString(t *testing.T) {
	input := "foo = (\nline one\nline two\n)\n"
	doc := mustParse(t, input)
	kv := doc.Items[0].(*KeyValueNode)
	sv, ok := kv.Value.(*StringNode)
	if !ok {
		t.Fatalf("Expected StringNode for block string, got %T", kv.Value)
	}
	if !strings.Contains(sv.Value, "line one") {
		t.Fatalf("Expected block string to contain 'line one', got %q", sv.Value)
	}
}

// --- Multiple key-value pairs with semicolons ---

func TestParseSemicolonSeparated(t *testing.T) {
	doc := mustParse(t, `foo='1'; bar=2.2; baz=true; boo=22`)
	kvs := extractKeyValues(doc)
	if len(kvs) != 4 {
		t.Fatalf("Expected 4 keys, got %d", len(kvs))
	}
}

// --- IP Address as string ---

func TestParseIPAddress(t *testing.T) {
	doc := mustParse(t, `listen = 127.0.0.1:4222`)
	kv := doc.Items[0].(*KeyValueNode)
	sv, ok := kv.Value.(*StringNode)
	if !ok {
		t.Fatalf("Expected StringNode for IP, got %T", kv.Value)
	}
	if sv.Value != "127.0.0.1:4222" {
		t.Fatalf("Expected '127.0.0.1:4222', got %q", sv.Value)
	}
}

// --- Complex scenarios matching v1 parse tests ---

func TestParseComplexCluster(t *testing.T) {
	input := `
cluster {
  port: 4244

  authorization {
    user: route_user
    password: top_secret
    timeout: 1
  }

  # Routes are actively solicited
  // Test both styles of comments

  routes = [
    nats-route://foo:bar@apcera.me:4245
    nats-route://foo:bar@apcera.me:4246
  ]
}
`
	doc := mustParse(t, input)
	kvs := extractKeyValues(doc)
	if _, ok := kvs["cluster"]; !ok {
		t.Fatal("Expected 'cluster' key")
	}
	mv := kvs["cluster"].(*MapNode)
	mapKVs := extractKeyValues(&Document{Items: mv.Items})
	if _, ok := mapKVs["port"]; !ok {
		t.Fatal("Expected 'port' in cluster")
	}
	if _, ok := mapKVs["authorization"]; !ok {
		t.Fatal("Expected 'authorization' in cluster")
	}
	if _, ok := mapKVs["routes"]; !ok {
		t.Fatal("Expected 'routes' in cluster")
	}
}

func TestParseTopLevelJSON(t *testing.T) {
	input := `{
  "http_port": 8227,
  "port": 4227
}`
	doc := mustParse(t, input)
	kvs := extractKeyValues(doc)
	if _, ok := kvs["http_port"]; !ok {
		t.Fatal("Expected 'http_port' in JSON-style config")
	}
}

func TestParseDocumentType(t *testing.T) {
	doc := mustParse(t, `foo = 1`)
	if doc.Type() != "Document" {
		t.Fatalf("Expected Document type, got %q", doc.Type())
	}
}

// --- Env var value sub-parsing (numeric, bool, quoted) ---

func TestParseEnvVarSubParsingQuotedString(t *testing.T) {
	evar := "__V2_TEST_QUOTED__"
	os.Setenv(evar, "'3xyz'")
	defer os.Unsetenv(evar)

	doc := mustParse(t, `foo = $__V2_TEST_QUOTED__`)
	kvs := extractKeyValues(doc)
	if v, ok := kvs["foo"]; ok {
		sv, ok := v.(*StringNode)
		if !ok {
			t.Fatalf("Expected StringNode for quoted env, got %T", v)
		}
		if sv.Value != "3xyz" {
			t.Fatalf("Expected '3xyz', got %q", sv.Value)
		}
	} else {
		t.Fatal("Expected 'foo' key")
	}
}

// --- Float Inf detection ---

func TestParseFloatInfDetection(t *testing.T) {
	// Use a large float value that overflows float64 to +Inf.
	expectParseError(t, `val = 1`+strings.Repeat("7", 310)+`.0`, "float")
}

// --- ParseAST returns Document type ---

func TestParseASTReturnsDocument(t *testing.T) {
	doc, err := ParseAST("foo = 1")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if doc == nil {
		t.Fatal("Expected non-nil document")
	}
	if doc.Type() != "Document" {
		t.Fatalf("Expected 'Document' type, got %q", doc.Type())
	}
}

// --- Multiple items in document ---

func TestParseMultipleTopLevelItems(t *testing.T) {
	doc := mustParse(t, `
foo = 1
bar = "hello"
baz = true
`)
	kvs := extractKeyValues(doc)
	if len(kvs) != 3 {
		t.Fatalf("Expected 3 keys, got %d", len(kvs))
	}
}

// Helper function to extract key-value pairs from a document.
// Uses last-wins semantics for duplicate keys.
func extractKeyValues(doc *Document) map[string]Node {
	result := make(map[string]Node)
	for _, item := range doc.Items {
		if kv, ok := item.(*KeyValueNode); ok {
			result[kv.Key.Name] = kv.Value
		}
	}
	return result
}
