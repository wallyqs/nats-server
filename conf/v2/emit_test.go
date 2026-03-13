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
	"reflect"
	"strings"
	"testing"
)

// --- ParseASTRaw Tests ---

func TestParseASTRawPreservesVariableNode(t *testing.T) {
	input := `
index = 22
foo = $index
`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw error: %v", err)
	}
	// Find the 'foo' key-value pair.
	var fooKV *KeyValueNode
	for _, item := range doc.Items {
		if kv, ok := item.(*KeyValueNode); ok && kv.Key.Name == "foo" {
			fooKV = kv
			break
		}
	}
	if fooKV == nil {
		t.Fatal("Expected 'foo' key-value in document")
	}
	// In raw mode, 'foo' should have a VariableNode, not a resolved value.
	varNode, ok := fooKV.Value.(*VariableNode)
	if !ok {
		t.Fatalf("Expected VariableNode for 'foo', got %T", fooKV.Value)
	}
	if varNode.Name != "index" {
		t.Fatalf("Expected variable name 'index', got %q", varNode.Name)
	}
}

func TestParseASTRawPreservesIncludeNode(t *testing.T) {
	input := `
listen: 127.0.0.1:4222

authorization {
  include './includes/users.conf'
  timeout: 0.5
}
`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw error: %v", err)
	}
	// Find the authorization map.
	var authKV *KeyValueNode
	for _, item := range doc.Items {
		if kv, ok := item.(*KeyValueNode); ok && kv.Key.Name == "authorization" {
			authKV = kv
			break
		}
	}
	if authKV == nil {
		t.Fatal("Expected 'authorization' key-value in document")
	}
	mv, ok := authKV.Value.(*MapNode)
	if !ok {
		t.Fatalf("Expected MapNode, got %T", authKV.Value)
	}
	// Should have an IncludeNode in the map items.
	var includeFound bool
	for _, item := range mv.Items {
		if inc, ok := item.(*IncludeNode); ok {
			includeFound = true
			if !strings.Contains(inc.Path, "users.conf") {
				t.Fatalf("Expected include path containing 'users.conf', got %q", inc.Path)
			}
			break
		}
	}
	if !includeFound {
		t.Fatal("Expected IncludeNode in authorization map")
	}
}

func TestParseASTRawStoresSource(t *testing.T) {
	input := `port = 4222`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw error: %v", err)
	}
	if doc.Source != input {
		t.Fatalf("Expected Source to be %q, got %q", input, doc.Source)
	}
}

func TestParseASTRawPreservesBoolRaw(t *testing.T) {
	input := `debug = yes`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw error: %v", err)
	}
	kv := doc.Items[0].(*KeyValueNode)
	bv := kv.Value.(*BoolNode)
	if bv.Raw != "yes" {
		t.Fatalf("Expected Raw 'yes', got %q", bv.Raw)
	}
}

func TestParseASTRawPreservesFloatRaw(t *testing.T) {
	input := `rate = 3.14`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw error: %v", err)
	}
	kv := doc.Items[0].(*KeyValueNode)
	fv := kv.Value.(*FloatNode)
	if fv.Raw != "3.14" {
		t.Fatalf("Expected Raw '3.14', got %q", fv.Raw)
	}
}

func TestParseASTRawPreservesDatetimeRaw(t *testing.T) {
	input := `created = 2016-05-04T18:53:41Z`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw error: %v", err)
	}
	kv := doc.Items[0].(*KeyValueNode)
	dv := kv.Value.(*DatetimeNode)
	if dv.Raw != "2016-05-04T18:53:41Z" {
		t.Fatalf("Expected Raw '2016-05-04T18:53:41Z', got %q", dv.Raw)
	}
}

// --- Emit Tests ---

func TestEmitSimpleKeyValue(t *testing.T) {
	input := `port = 4222`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw error: %v", err)
	}
	out, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit error: %v", err)
	}
	result := string(out)
	if !strings.Contains(result, "port") {
		t.Fatalf("Expected 'port' in output, got %q", result)
	}
	if !strings.Contains(result, "4222") {
		t.Fatalf("Expected '4222' in output, got %q", result)
	}
}

func TestEmitPreservesComments(t *testing.T) {
	input := `# Server configuration
port = 4222 # default port
// debug mode
debug = true
`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw error: %v", err)
	}
	out, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit error: %v", err)
	}
	result := string(out)
	if !strings.Contains(result, "# Server configuration") {
		t.Fatalf("Expected leading comment in output, got:\n%s", result)
	}
	if !strings.Contains(result, "# default port") {
		t.Fatalf("Expected trailing comment in output, got:\n%s", result)
	}
	if !strings.Contains(result, "// debug mode") {
		t.Fatalf("Expected slash comment in output, got:\n%s", result)
	}
}

func TestEmitPreservesCommentStyles(t *testing.T) {
	input := `# hash comment
// slash comment
port = 4222`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw error: %v", err)
	}
	out, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit error: %v", err)
	}
	result := string(out)
	// Both comment styles must be retained.
	if !strings.Contains(result, "#") {
		t.Fatalf("Expected '#' comment style in output, got:\n%s", result)
	}
	if !strings.Contains(result, "//") {
		t.Fatalf("Expected '//' comment style in output, got:\n%s", result)
	}
}

func TestEmitPreservesVariableDefinitions(t *testing.T) {
	input := `index = 22
foo = $index
`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw error: %v", err)
	}
	out, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit error: %v", err)
	}
	result := string(out)
	// Variable definition should be present.
	if !strings.Contains(result, "index") {
		t.Fatalf("Expected 'index' definition in output, got:\n%s", result)
	}
	// Variable reference should be preserved as $index, NOT expanded to 22.
	if !strings.Contains(result, "$index") {
		t.Fatalf("Expected '$index' reference in output, got:\n%s", result)
	}
}

func TestEmitPreservesIncludeDirective(t *testing.T) {
	input := `listen: 127.0.0.1:4222

authorization {
  include './includes/users.conf'
  timeout: 0.5
}
`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw error: %v", err)
	}
	out, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit error: %v", err)
	}
	result := string(out)
	if !strings.Contains(result, "include") {
		t.Fatalf("Expected 'include' directive in output, got:\n%s", result)
	}
	if !strings.Contains(result, "users.conf") {
		t.Fatalf("Expected 'users.conf' path in output, got:\n%s", result)
	}
}

func TestEmitPreservesFormatting(t *testing.T) {
	input := `port = 4222
host = "0.0.0.0"

cluster {
  port: 6222
  name: "my-cluster"
}
`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw error: %v", err)
	}
	out, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit error: %v", err)
	}
	result := string(out)
	// Check indentation is preserved for nested map.
	if !strings.Contains(result, "  port") {
		t.Fatalf("Expected indented 'port' inside cluster block, got:\n%s", result)
	}
	if !strings.Contains(result, "  name") {
		t.Fatalf("Expected indented 'name' inside cluster block, got:\n%s", result)
	}
}

func TestEmitPreservesKeySeparatorStyles(t *testing.T) {
	input := `port = 4222
host: "0.0.0.0"
debug true
`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw error: %v", err)
	}
	out, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit error: %v", err)
	}
	result := string(out)
	// Check that separator styles are preserved.
	lines := strings.Split(result, "\n")
	foundEquals := false
	foundColon := false
	foundSpace := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "port") && strings.Contains(line, "=") {
			foundEquals = true
		}
		if strings.HasPrefix(line, "host") && strings.Contains(line, ":") {
			foundColon = true
		}
		if strings.HasPrefix(line, "debug") && !strings.Contains(line, "=") && !strings.Contains(line, ":") && strings.Contains(line, "true") {
			foundSpace = true
		}
	}
	if !foundEquals {
		t.Fatalf("Expected '=' separator for port, got:\n%s", result)
	}
	if !foundColon {
		t.Fatalf("Expected ':' separator for host, got:\n%s", result)
	}
	if !foundSpace {
		t.Fatalf("Expected space separator for debug, got:\n%s", result)
	}
}

// TestEmitRoundTripStructuralEquivalence verifies that
// Parse(Emit(ParseASTRaw(input))) == Parse(input) for various inputs.
func TestEmitRoundTripStructuralEquivalence(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "simple key-value",
			input: "port = 4222\nhost = \"0.0.0.0\"\n",
		},
		{
			name: "nested map",
			input: `cluster {
  port: 6222
  name: "my-cluster"
}
`,
		},
		{
			name: "array",
			input: `routes = [
  "nats://host1:4222"
  "nats://host2:4222"
]
`,
		},
		{
			name: "array of maps",
			input: `users = [
  {user: alice, password: foo}
  {user: bob, password: bar}
]
`,
		},
		{
			name: "boolean values",
			input: `debug = true
trace = false
`,
		},
		{
			name: "integer with suffix",
			input: `max_payload = 1MB
`,
		},
		{
			name:  "float value",
			input: "rate = 3.14\n",
		},
		{
			name: "mixed types",
			input: `port = 4222
host = "0.0.0.0"
debug = true
rate = 3.14
max_payload = 1MB
`,
		},
		{
			name: "JSON-style config",
			input: `{
  "port": 4222,
  "host": "0.0.0.0"
}
`,
		},
		{
			name: "deeply nested",
			input: `server {
  cluster {
    routes = [
      "nats://host1:4222"
    ]
  }
}
`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// First parse: get the expected map.
			expected, err := Parse(tc.input)
			if err != nil {
				t.Fatalf("First Parse failed: %v", err)
			}

			// Raw parse -> Emit -> re-parse.
			doc, err := ParseASTRaw(tc.input)
			if err != nil {
				t.Fatalf("ParseASTRaw failed: %v", err)
			}
			emitted, err := Emit(doc)
			if err != nil {
				t.Fatalf("Emit failed: %v", err)
			}
			actual, err := Parse(string(emitted))
			if err != nil {
				t.Fatalf("Re-parse of emitted output failed: %v\nEmitted:\n%s", err, string(emitted))
			}

			if !reflect.DeepEqual(expected, actual) {
				t.Fatalf("Round-trip structural equivalence failed.\nInput:\n%s\nEmitted:\n%s\nExpected map: %v\nActual map: %v",
					tc.input, string(emitted), expected, actual)
			}
		})
	}
}

// TestEmitRoundTripWithComments verifies round-trip preserves comments.
func TestEmitRoundTripWithComments(t *testing.T) {
	input := `# Top comment
port = 4222 # inline comment
# Between items
host = "0.0.0.0"
`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw failed: %v", err)
	}
	emitted, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit failed: %v", err)
	}
	result := string(emitted)

	// All comments should be present.
	if !strings.Contains(result, "# Top comment") {
		t.Fatalf("Missing '# Top comment' in emitted output:\n%s", result)
	}
	if !strings.Contains(result, "# inline comment") {
		t.Fatalf("Missing '# inline comment' in emitted output:\n%s", result)
	}
	if !strings.Contains(result, "# Between items") {
		t.Fatalf("Missing '# Between items' in emitted output:\n%s", result)
	}

	// Structural equivalence.
	expected, _ := Parse(input)
	actual, err := Parse(result)
	if err != nil {
		t.Fatalf("Re-parse failed: %v\nEmitted:\n%s", err, result)
	}
	if !reflect.DeepEqual(expected, actual) {
		t.Fatalf("Round-trip equivalence failed with comments")
	}
}

// TestEmitRoundTripWithVariables verifies that variable definitions and
// references are preserved, not expanded.
func TestEmitRoundTripWithVariables(t *testing.T) {
	input := `index = 22
foo = $index
bar = $index
`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw failed: %v", err)
	}
	emitted, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit failed: %v", err)
	}
	result := string(emitted)

	// Variable definition and references should be in output.
	if !strings.Contains(result, "index = 22") && !strings.Contains(result, "index=22") && !strings.Contains(result, "index: 22") {
		t.Fatalf("Missing variable definition in emitted output:\n%s", result)
	}
	// Count $index references - should be 2.
	count := strings.Count(result, "$index")
	if count < 2 {
		t.Fatalf("Expected at least 2 '$index' references, got %d in:\n%s", count, result)
	}

	// Structural equivalence: Parse resolves variables, so both should yield same map.
	expected, _ := Parse(input)
	actual, err := Parse(result)
	if err != nil {
		t.Fatalf("Re-parse failed: %v\nEmitted:\n%s", err, result)
	}
	if !reflect.DeepEqual(expected, actual) {
		t.Fatalf("Round-trip equivalence failed with variables.\nExpected: %v\nActual: %v", expected, actual)
	}
}

// TestEmitRoundTripWithIncludes tests include directive preservation.
func TestEmitRoundTripWithIncludes(t *testing.T) {
	input := `listen: 127.0.0.1:4222

authorization {
  include './includes/users.conf'
  timeout: 0.5
}
`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw failed: %v", err)
	}
	emitted, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit failed: %v", err)
	}
	result := string(emitted)

	// Include directive should be present.
	if !strings.Contains(result, "include") {
		t.Fatalf("Missing include directive in emitted output:\n%s", result)
	}
	if !strings.Contains(result, "users.conf") {
		t.Fatalf("Missing include path in emitted output:\n%s", result)
	}
}

// TestEmitRoundTripSimpleConf tests round-trip with conf/simple.conf.
func TestEmitRoundTripSimpleConf(t *testing.T) {
	confPath := filepath.Join("..", "simple.conf")
	data, err := os.ReadFile(confPath)
	if err != nil {
		t.Skipf("Skipping: cannot read %s: %v", confPath, err)
	}

	doc, err := ParseASTRaw(string(data))
	if err != nil {
		t.Fatalf("ParseASTRaw failed: %v", err)
	}
	emitted, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit failed: %v", err)
	}
	result := string(emitted)

	// Include directive should be preserved.
	if !strings.Contains(result, "include") {
		t.Fatalf("Missing include directive in emitted simple.conf:\n%s", result)
	}

	// Comment should be preserved.
	if !strings.Contains(result, "Pull in from file") {
		t.Fatalf("Missing comment in emitted simple.conf:\n%s", result)
	}
}

// TestEmitRoundTripV1TestCorpus runs round-trip on configs from v1 test corpus.
func TestEmitRoundTripV1TestCorpus(t *testing.T) {
	corpus := []struct {
		name  string
		input string
	}{
		{
			name:  "simple config",
			input: "listen: localhost:4242\n",
		},
		{
			name: "cluster config",
			input: `listen: localhost:4242
cluster {
  host: 127.0.0.1
  port: 4244
  authorization {
    user: route_user
    password: top_secret
    timeout: 0.5
  }
  routes = [
    nats-route://foo:bar@localhost:4245
    nats-route://foo:bar@localhost:4246
  ]
}
`,
		},
		{
			name: "nested maps",
			input: `a {
  b {
    c = 1
    d = "hello"
  }
  e = true
}
`,
		},
		{
			name: "mixed separators",
			input: `port = 4222
host: "0.0.0.0"
debug true
`,
		},
		{
			name: "multiline array",
			input: `routes = [
  "nats://host1:4222"
  "nats://host2:4222"
  "nats://host3:4222"
]
`,
		},
	}

	for _, tc := range corpus {
		t.Run(tc.name, func(t *testing.T) {
			expected, err := Parse(tc.input)
			if err != nil {
				t.Fatalf("First Parse failed: %v", err)
			}

			doc, err := ParseASTRaw(tc.input)
			if err != nil {
				t.Fatalf("ParseASTRaw failed: %v", err)
			}
			emitted, err := Emit(doc)
			if err != nil {
				t.Fatalf("Emit failed: %v", err)
			}
			actual, err := Parse(string(emitted))
			if err != nil {
				t.Fatalf("Re-parse failed: %v\nEmitted:\n%s", err, string(emitted))
			}

			if !reflect.DeepEqual(expected, actual) {
				t.Fatalf("Round-trip failed for %q.\nExpected: %v\nActual: %v\nEmitted:\n%s",
					tc.name, expected, actual, string(emitted))
			}
		})
	}
}

// TestEmitNilDocument returns error for nil document.
func TestEmitNilDocument(t *testing.T) {
	_, err := Emit(nil)
	if err == nil {
		t.Fatal("Expected error for nil document")
	}
}

// TestEmitEmptyDocument produces empty or minimal output.
func TestEmitEmptyDocument(t *testing.T) {
	doc := &Document{
		NodeBase: NodeBase{Pos: Position{Line: 1, Column: 0}},
		Items:    []Node{},
	}
	out, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit error: %v", err)
	}
	// Empty document should produce empty (or whitespace-only) output.
	result := strings.TrimSpace(string(out))
	if result != "" {
		t.Fatalf("Expected empty output for empty document, got %q", result)
	}
}

// TestEmitBlockString verifies block strings are emitted correctly.
func TestEmitBlockString(t *testing.T) {
	input := `cert = (
multi
line
content
)
`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw failed: %v", err)
	}
	emitted, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit failed: %v", err)
	}

	// Round-trip: parse both and compare.
	expected, _ := Parse(input)
	actual, err := Parse(string(emitted))
	if err != nil {
		t.Fatalf("Re-parse failed: %v\nEmitted:\n%s", err, string(emitted))
	}
	if !reflect.DeepEqual(expected, actual) {
		t.Fatalf("Block string round-trip failed.\nExpected: %v\nActual: %v", expected, actual)
	}
}

// TestEmitPreservesBlankLines checks that blank lines between items are
// preserved to maintain original formatting.
func TestEmitPreservesBlankLines(t *testing.T) {
	input := `port = 4222

host = "0.0.0.0"
`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw failed: %v", err)
	}
	emitted, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit failed: %v", err)
	}
	result := string(emitted)

	// There should be a blank line between port and host.
	if !strings.Contains(result, "4222\n\n") {
		t.Fatalf("Expected blank line between port and host in:\n%s", result)
	}
}

// TestEmitPreservesIndentation verifies nested block indentation.
func TestEmitPreservesIndentation(t *testing.T) {
	input := `server {
  port: 4222
  cluster {
    port: 6222
  }
}
`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw failed: %v", err)
	}
	emitted, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit failed: %v", err)
	}
	result := string(emitted)

	// Check indentation levels.
	lines := strings.Split(result, "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "{" || trimmed == "}" {
			continue
		}
		if strings.HasPrefix(trimmed, "server") {
			// Top level, should have no indentation.
			if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				t.Fatalf("Expected 'server' at column 0, got: %q", line)
			}
		}
		if strings.HasPrefix(trimmed, "port: 4222") {
			// Should be indented.
			if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
				t.Fatalf("Expected 'port: 4222' to be indented, got: %q", line)
			}
		}
	}
}

// TestEmitArrayWithComments verifies comments inside arrays are preserved.
func TestEmitArrayWithComments(t *testing.T) {
	input := `routes = [
  "nats://host1:4222" # primary
  "nats://host2:4222" # secondary
]
`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw failed: %v", err)
	}
	emitted, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit failed: %v", err)
	}
	result := string(emitted)

	// Comments inside arrays should be present.
	if !strings.Contains(result, "# primary") {
		t.Fatalf("Expected '# primary' comment in array, got:\n%s", result)
	}
	if !strings.Contains(result, "# secondary") {
		t.Fatalf("Expected '# secondary' comment in array, got:\n%s", result)
	}
}

// TestEmitBcryptPassword verifies bcrypt password strings are preserved.
func TestEmitBcryptPasswordRoundTrip(t *testing.T) {
	input := `password = "$2a$11$ooo"
`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw failed: %v", err)
	}
	emitted, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit failed: %v", err)
	}

	expected, _ := Parse(input)
	actual, err := Parse(string(emitted))
	if err != nil {
		t.Fatalf("Re-parse failed: %v\nEmitted:\n%s", err, string(emitted))
	}
	if !reflect.DeepEqual(expected, actual) {
		t.Fatalf("Bcrypt round-trip failed.\nExpected: %v\nActual: %v", expected, actual)
	}
}

// TestEmitVariableNestedScope verifies variable definitions and references
// in nested scopes are preserved.
func TestEmitVariableNestedScope(t *testing.T) {
	input := `index = 22
nest {
  inner_index = 11
  foo = $inner_index
}
bar = $index
`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw failed: %v", err)
	}
	emitted, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit failed: %v", err)
	}
	result := string(emitted)

	// Both variable definitions and references should be preserved.
	if !strings.Contains(result, "$inner_index") {
		t.Fatalf("Expected '$inner_index' in output, got:\n%s", result)
	}
	if !strings.Contains(result, "$index") {
		t.Fatalf("Expected '$index' in output, got:\n%s", result)
	}

	// Structural equivalence.
	expected, _ := Parse(input)
	actual, err := Parse(result)
	if err != nil {
		t.Fatalf("Re-parse failed: %v\nEmitted:\n%s", err, result)
	}
	if !reflect.DeepEqual(expected, actual) {
		t.Fatalf("Nested variable round-trip failed.\nExpected: %v\nActual: %v", expected, actual)
	}
}

// TestEmitMultipleCommentBlocks verifies multiple consecutive comment blocks.
func TestEmitMultipleCommentBlocks(t *testing.T) {
	input := `# Block 1
# Block 1 continued

# Block 2
port = 4222
`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw failed: %v", err)
	}
	emitted, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit failed: %v", err)
	}
	result := string(emitted)

	if !strings.Contains(result, "# Block 1") {
		t.Fatalf("Expected '# Block 1' in output, got:\n%s", result)
	}
	if !strings.Contains(result, "# Block 1 continued") {
		t.Fatalf("Expected '# Block 1 continued' in output, got:\n%s", result)
	}
	if !strings.Contains(result, "# Block 2") {
		t.Fatalf("Expected '# Block 2' in output, got:\n%s", result)
	}
}

// TestEmitInlineArray verifies inline arrays are emitted correctly.
func TestEmitInlineArray(t *testing.T) {
	input := `routes = ["a.com", "b.com", "c.com"]
`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw failed: %v", err)
	}
	emitted, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit failed: %v", err)
	}

	expected, _ := Parse(input)
	actual, err := Parse(string(emitted))
	if err != nil {
		t.Fatalf("Re-parse failed: %v\nEmitted:\n%s", err, string(emitted))
	}
	if !reflect.DeepEqual(expected, actual) {
		t.Fatalf("Inline array round-trip failed.\nExpected: %v\nActual: %v", expected, actual)
	}
}

// TestEmitIPAddresses verifies IP address values round-trip correctly.
func TestEmitIPAddressRoundTrip(t *testing.T) {
	input := `listen = 127.0.0.1:4222
`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw failed: %v", err)
	}
	emitted, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit failed: %v", err)
	}

	expected, _ := Parse(input)
	actual, err := Parse(string(emitted))
	if err != nil {
		t.Fatalf("Re-parse failed: %v\nEmitted:\n%s", err, string(emitted))
	}
	if !reflect.DeepEqual(expected, actual) {
		t.Fatalf("IP address round-trip failed.\nExpected: %v\nActual: %v", expected, actual)
	}
}

// TestEmitSemicolonSeparatedValues verifies semicolons in the original are handled.
func TestEmitSemicolonSeparatedRoundTrip(t *testing.T) {
	input := `port = 4222; host = "0.0.0.0"
`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw failed: %v", err)
	}
	emitted, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit failed: %v", err)
	}

	expected, _ := Parse(input)
	actual, err := Parse(string(emitted))
	if err != nil {
		t.Fatalf("Re-parse failed: %v\nEmitted:\n%s", err, string(emitted))
	}
	if !reflect.DeepEqual(expected, actual) {
		t.Fatalf("Semicolon round-trip failed.\nExpected: %v\nActual: %v", expected, actual)
	}
}

// TestEmitDocumentMethod verifies the Document.Emit() convenience method.
func TestEmitDocumentMethod(t *testing.T) {
	input := `port = 4222
`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw failed: %v", err)
	}
	out, err := doc.Emit()
	if err != nil {
		t.Fatalf("Document.Emit error: %v", err)
	}
	if !strings.Contains(string(out), "port") {
		t.Fatalf("Expected 'port' in output, got %q", string(out))
	}
}

// TestEmitPreservesBoolLiterals verifies that boolean literals like "yes",
// "on", etc. are preserved in raw mode.
func TestEmitPreservesBoolLiterals(t *testing.T) {
	input := `debug = yes
trace = on
verbose = off
`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw failed: %v", err)
	}
	emitted, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit failed: %v", err)
	}
	result := string(emitted)

	if !strings.Contains(result, "yes") {
		t.Fatalf("Expected 'yes' in output, got:\n%s", result)
	}
	if !strings.Contains(result, "on") {
		t.Fatalf("Expected 'on' in output, got:\n%s", result)
	}
	if !strings.Contains(result, "off") {
		t.Fatalf("Expected 'off' in output, got:\n%s", result)
	}
}

// TestEmitPreservesIntegerRaw verifies that integer raw representation
// including size suffixes is preserved.
func TestEmitPreservesIntegerRaw(t *testing.T) {
	input := `max_payload = 1MB
`
	doc, err := ParseASTRaw(input)
	if err != nil {
		t.Fatalf("ParseASTRaw failed: %v", err)
	}
	emitted, err := Emit(doc)
	if err != nil {
		t.Fatalf("Emit failed: %v", err)
	}
	result := string(emitted)

	if !strings.Contains(result, "1MB") {
		t.Fatalf("Expected '1MB' suffix preserved, got:\n%s", result)
	}
}
