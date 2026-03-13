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
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	conf "github.com/nats-io/nats-server/v2/conf"
)

// =============================================================================
// Cross-validation helper: Compare v1 and v2 Parse output for identical input.
// =============================================================================

func crossValidate(t *testing.T, data string) {
	t.Helper()

	v1m, err := conf.Parse(data)
	if err != nil {
		t.Fatalf("v1 Parse failed: %v", err)
	}
	v2m, err := Parse(data)
	if err != nil {
		t.Fatalf("v2 Parse failed: %v", err)
	}
	if !reflect.DeepEqual(v1m, v2m) {
		t.Fatalf("v1/v2 Parse mismatch:\n  v1: %+v\n  v2: %+v", v1m, v2m)
	}

	// Also cross-validate JSON output of pedantic mode.
	v1p, err := conf.ParseWithChecks(data)
	if err != nil {
		t.Fatalf("v1 ParseWithChecks failed: %v", err)
	}
	v2p, err := ParseWithChecks(data)
	if err != nil {
		t.Fatalf("v2 ParseWithChecks failed: %v", err)
	}
	v1j, err := json.Marshal(v1p)
	if err != nil {
		t.Fatalf("v1 JSON marshal failed: %v", err)
	}
	v2j, err := json.Marshal(v2p)
	if err != nil {
		t.Fatalf("v2 JSON marshal failed: %v", err)
	}
	if !bytes.Equal(v1j, v2j) {
		t.Fatalf("v1/v2 pedantic JSON mismatch:\n  v1: %s\n  v2: %s", string(v1j), string(v2j))
	}
}

// crossValidateFile compares v1 and v2 ParseFile output.
func crossValidateFile(t *testing.T, fp string) {
	t.Helper()

	v1m, err := conf.ParseFile(fp)
	if err != nil {
		t.Fatalf("v1 ParseFile failed: %v", err)
	}
	v2m, err := ParseFile(fp)
	if err != nil {
		t.Fatalf("v2 ParseFile failed: %v", err)
	}
	if !reflect.DeepEqual(v1m, v2m) {
		t.Fatalf("v1/v2 ParseFile mismatch:\n  v1: %+v\n  v2: %+v", v1m, v2m)
	}
}

// =============================================================================
// VAL-PARSER-023: Parse/ParseFile Produce Identical map[string]any Output
// Cross-validation tests using all inputs from v1's parse_test.go.
// =============================================================================

func TestCompatSimpleTopLevel(t *testing.T) {
	crossValidate(t, "foo='1'; bar=2.2; baz=true; boo=22")
}

func TestCompatBools(t *testing.T) {
	for _, input := range []string{
		"foo=true", "foo=TRUE", "foo=yes", "foo=on",
		"foo=false", "foo=FALSE", "foo=no", "foo=off",
	} {
		t.Run(input, func(t *testing.T) {
			crossValidate(t, input)
		})
	}
}

func TestCompatSimpleVariable(t *testing.T) {
	crossValidate(t, `
  index = 22
  foo = $index
`)
}

func TestCompatNestedVariable(t *testing.T) {
	crossValidate(t, `
  index = 22
  nest {
    index = 11
    foo = $index
  }
  bar = $index
`)
}

func TestCompatMissingVariable(t *testing.T) {
	_, err := Parse("foo=$index")
	if err == nil {
		t.Fatal("Expected error for missing variable")
	}
	if !strings.HasPrefix(err.Error(), "variable reference") {
		t.Fatalf("Expected variable reference error, got: %v", err)
	}
}

func TestCompatEnvVariable(t *testing.T) {
	evar := "__V2_COMPAT_INT__"
	os.Setenv(evar, "22")
	defer os.Unsetenv(evar)
	crossValidate(t, fmt.Sprintf("foo = $%s", evar))
}

func TestCompatEnvVariableString(t *testing.T) {
	evar := "__V2_COMPAT_STR__"
	os.Setenv(evar, "xyz")
	defer os.Unsetenv(evar)
	crossValidate(t, fmt.Sprintf("foo = $%s", evar))
}

func TestCompatEnvVariableStringStartingWithNumberAndSizeUnit(t *testing.T) {
	evar := "__V2_COMPAT_NUM3G__"
	os.Setenv(evar, "3Gyz")
	defer os.Unsetenv(evar)
	crossValidate(t, fmt.Sprintf("foo = $%s", evar))
}

func TestCompatEnvVariableStringStartingWithNumberUsingQuotes(t *testing.T) {
	evar := "__V2_COMPAT_NUMQ__"
	os.Setenv(evar, "'3xyz'")
	defer os.Unsetenv(evar)
	crossValidate(t, fmt.Sprintf("foo = $%s", evar))
}

func TestCompatBcryptVariable(t *testing.T) {
	crossValidate(t, "password: $2a$11$ooo")
}

func TestCompatConvenientNumbers(t *testing.T) {
	crossValidate(t, `
k = 8k
kb = 4kb
ki = 3ki
kib = 4ki
m = 1m
mb = 2MB
mi = 2Mi
mib = 64MiB
g = 2g
gb = 22GB
gi = 22Gi
gib = 22GiB
tb = 22TB
ti = 22Ti
tib = 22TiB
pb = 22PB
pi = 22Pi
pib = 22PiB
`)
}

func TestCompatSample1(t *testing.T) {
	crossValidate(t, `
foo  {
  host {
    ip   = '127.0.0.1'
    port = 4242
  }
  servers = [ "a.com", "b.com", "c.com"]
}
`)
}

func TestCompatCluster(t *testing.T) {
	crossValidate(t, `
cluster {
  port: 4244

  authorization {
    user: route_user
    password: top_secret
    timeout: 1
  }

  # Routes are actively solicited and connected to from this server.
  # Other servers can connect to us if they supply the correct credentials
  # in their routes definitions from above.

  // Test both styles of comments

  routes = [
    nats-route://foo:bar@apcera.me:4245
    nats-route://foo:bar@apcera.me:4246
  ]
}
`)
}

func TestCompatSample3(t *testing.T) {
	crossValidate(t, `
foo  {
  expr = '(true == "false")'
  text = 'This is a multi-line
text block.'
}
`)
}

func TestCompatSample4(t *testing.T) {
	crossValidate(t, `
  array [
    { abc: 123 }
    { xyz: "word" }
  ]
`)
}

func TestCompatSample5(t *testing.T) {
	crossValidate(t, `
  now = 2016-05-04T18:53:41Z
  gmt = false
`)
}

func TestCompatEmptyConfig(t *testing.T) {
	crossValidate(t, "")
}

func TestCompatCommentOnlyConfig(t *testing.T) {
	crossValidate(t, `
# just comments with no values
# is still valid.
`)
}

func TestCompatTopLevelJSON(t *testing.T) {
	crossValidate(t, `
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
`)
}

func TestCompatJSONEmpty(t *testing.T) {
	crossValidate(t, `{}`)
}

func TestCompatNegativeFloat(t *testing.T) {
	crossValidate(t, `foo = -22.2`)
}

// =============================================================================
// VAL-PARSER-023: Concrete type matching
// =============================================================================

func TestCompatConcreteTypes(t *testing.T) {
	m, err := Parse("s = 'hello'; b = true; i = 42; f = 3.14")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m["s"].(string); !ok {
		t.Fatalf("Expected string type, got %T", m["s"])
	}
	if _, ok := m["b"].(bool); !ok {
		t.Fatalf("Expected bool type, got %T", m["b"])
	}
	if _, ok := m["i"].(int64); !ok {
		t.Fatalf("Expected int64 type, got %T", m["i"])
	}
	if _, ok := m["f"].(float64); !ok {
		t.Fatalf("Expected float64 type, got %T", m["f"])
	}
}

func TestCompatConcreteTypeDatetime(t *testing.T) {
	m, err := Parse("d = 2016-05-04T18:53:41Z")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m["d"].(time.Time); !ok {
		t.Fatalf("Expected time.Time type, got %T", m["d"])
	}
}

func TestCompatConcreteTypeArray(t *testing.T) {
	m, err := Parse("a = [\"x\", \"y\", \"z\"]")
	if err != nil {
		t.Fatal(err)
	}
	arr, ok := m["a"].([]any)
	if !ok {
		t.Fatalf("Expected []any type, got %T", m["a"])
	}
	if len(arr) != 3 {
		t.Fatalf("Expected 3 elements, got %d", len(arr))
	}
}

func TestCompatConcreteTypeMap(t *testing.T) {
	m, err := Parse("block { key = 1 }")
	if err != nil {
		t.Fatal(err)
	}
	inner, ok := m["block"].(map[string]any)
	if !ok {
		t.Fatalf("Expected map[string]any type, got %T", m["block"])
	}
	if _, ok := inner["key"].(int64); !ok {
		t.Fatalf("Expected int64 inside map, got %T", inner["key"])
	}
}

// =============================================================================
// VAL-PARSER-024: Pedantic Mode with Token Wrappers
// =============================================================================

func TestCompatPedanticTokenMethods(t *testing.T) {
	m, err := ParseWithChecks("foo = 42")
	if err != nil {
		t.Fatal(err)
	}
	v, ok := m["foo"]
	if !ok {
		t.Fatal("Expected 'foo' key")
	}
	tk, ok := v.(*token)
	if !ok {
		t.Fatalf("Expected *token, got %T", v)
	}
	if tk.Value() != int64(42) {
		t.Fatalf("Expected Value()=42, got %v", tk.Value())
	}
	if tk.Line() != 1 {
		t.Fatalf("Expected Line()=1, got %d", tk.Line())
	}
	if tk.Position() != 0 {
		t.Fatalf("Expected Position()=0, got %d", tk.Position())
	}
	if tk.IsUsedVariable() {
		t.Fatal("Expected IsUsedVariable()=false")
	}
	if tk.SourceFile() != "" {
		t.Fatalf("Expected SourceFile()='', got %q", tk.SourceFile())
	}
	j, err := tk.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(j) != "42" {
		t.Fatalf("Expected MarshalJSON()='42', got %q", string(j))
	}
}

func TestCompatPedanticKeyPosition(t *testing.T) {
	// Position() must return the key's position, not the value's.
	m, err := ParseWithChecks("    foo = 42")
	if err != nil {
		t.Fatal(err)
	}
	tk := m["foo"].(*token)
	// Key "foo" starts at position 4 (0-based) on line 1.
	if tk.Position() != 4 {
		t.Fatalf("Expected key Position()=4, got %d", tk.Position())
	}
}

func TestCompatPedanticJSONMatchesNonPedantic(t *testing.T) {
	data := `
foo = "hello"
bar = 42
baz = true
arr = ["a", "b", "c"]
map { key = "val" }
`
	np, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	p, err := ParseWithChecks(data)
	if err != nil {
		t.Fatal(err)
	}
	npJSON, err := json.Marshal(np)
	if err != nil {
		t.Fatal(err)
	}
	pJSON, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(npJSON, pJSON) {
		t.Fatalf("Pedantic JSON != non-pedantic JSON:\n  NP: %s\n  P:  %s", string(npJSON), string(pJSON))
	}
}

func TestCompatPedanticUsedVariable(t *testing.T) {
	m, err := ParseWithChecks(`
index = 22
foo = $index
`)
	if err != nil {
		t.Fatal(err)
	}
	// "index" should be marked as usedVariable since $index references it.
	indexTk := m["index"].(*token)
	if !indexTk.IsUsedVariable() {
		t.Fatal("Expected 'index' to be marked as usedVariable")
	}
	// "foo" should NOT be marked as usedVariable.
	fooTk := m["foo"].(*token)
	if fooTk.IsUsedVariable() {
		t.Fatal("Expected 'foo' NOT to be marked as usedVariable")
	}
	// "foo" should have the same value as "index".
	if fooTk.Value() != int64(22) {
		t.Fatalf("Expected foo Value()=22, got %v", fooTk.Value())
	}
}

// =============================================================================
// VAL-PARSER-024: Cross-validate pedantic mode positions with v1
// =============================================================================

func TestCompatPedanticIncludeVariablesWithChecks(t *testing.T) {
	// This mirrors v1's TestIncludeVariablesWithChecks.
	data := `
authorization {

  include "./includes/passwords.conf"

  CAROL_PASS: foo

  users = [
   {user: alice, password: $ALICE_PASS}
   {user: bob,   password: $BOB_PASS}
   {user: carol, password: $CAROL_PASS}
  ]
}
`
	// Write to temp dir with include files.
	dir := t.TempDir()
	includesDir := filepath.Join(dir, "includes")
	if err := os.MkdirAll(includesDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(includesDir, "passwords.conf"), []byte(`
ALICE_PASS: $2a$10$UHR6GhotWhpLsKtVP0/i6.Nh9.fuY73cWjLoJjb2sKT8KISBcUW5q
BOB_PASS: $2a$11$dZM98SpGeI7dCFFGSpt.JObQcix8YHml4TBUZoge9R1uxnMIln5ly
`), 0644); err != nil {
		t.Fatal(err)
	}
	mainFile := filepath.Join(dir, "main.conf")
	if err := os.WriteFile(mainFile, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}

	// Parse with v1 and v2.
	v1m, err := conf.ParseFileWithChecks(mainFile)
	if err != nil {
		t.Fatalf("v1 ParseFileWithChecks failed: %v", err)
	}
	v2m, err := ParseFileWithChecks(mainFile)
	if err != nil {
		t.Fatalf("v2 ParseFileWithChecks failed: %v", err)
	}

	// Compare JSON output.
	v1j, _ := json.Marshal(v1m)
	v2j, _ := json.Marshal(v2m)
	if !bytes.Equal(v1j, v2j) {
		t.Fatalf("Pedantic JSON mismatch:\n  v1: %s\n  v2: %s", string(v1j), string(v2j))
	}
}

// =============================================================================
// VAL-PARSER-025: Digest Computation Matches v1
// =============================================================================

func TestCompatDigest(t *testing.T) {
	for i, test := range []struct {
		input    string
		includes map[string]string
		digest   string
	}{
		{
			`foo = bar`,
			nil,
			"sha256:226e49e13d16e5e8aa0d62e58cd63361bf097d3e2b2444aa3044334628a2e8de",
		},
		{
			`# Comments and whitespace have no effect
                        foo = bar
                        `,
			nil,
			"sha256:226e49e13d16e5e8aa0d62e58cd63361bf097d3e2b2444aa3044334628a2e8de",
		},
		{
			`# Syntax changes have no effect
                        'foo': 'bar'
                        `,
			nil,
			"sha256:226e49e13d16e5e8aa0d62e58cd63361bf097d3e2b2444aa3044334628a2e8de",
		},
		{
			`# Syntax changes have no effect
                        { 'foo': 'bar' }
                        `,
			nil,
			"sha256:226e49e13d16e5e8aa0d62e58cd63361bf097d3e2b2444aa3044334628a2e8de",
		},
		{
			`# substitutions
                        BAR_USERS = { users = [ {user = "bar"} ]}
                        hello = 'world'
                        accounts {
                          QUUX_USERS = [ { user: quux }]
                          bar = $BAR_USERS
                          quux = { users = $QUUX_USERS }
                        }
                        very { nested { env { VAR = 'NESTED', quux = $VAR }}}
                        `,
			nil,
			"sha256:34f8faf3f269fe7509edc4742f20c8c4a7ad51fe21f8b361764314b533ac3ab5",
		},
		{
			`# substitutions, same as previous one without env vars.
                        hello = 'world'
                        accounts {
                          bar  = { users = [ { user = "bar" } ]}
                          quux = { users = [ { user: quux   } ]}
                        }
                        very { nested { env { quux = 'NESTED' }}}
                        `,
			nil,
			"sha256:34f8faf3f269fe7509edc4742f20c8c4a7ad51fe21f8b361764314b533ac3ab5",
		},
		{
			`# substitutions
                        BAR_USERS = { users = [ {user = "foo"} ]}
                        bar = $BAR_USERS
                        accounts {
                          users = $BAR_USERS
                        }
                        `,
			nil,
			"sha256:f5d943b4ed22b80c6199203f8a7eaa8eb68ef7b2d46ef6b1b26f05e21f8beb13",
		},
		{
			`# substitutions
                        bar = { users = [ {user = "foo"} ]}
                        accounts {
                          users = { users = [ {user = "foo"} ]}
                        }
                        `,
			nil,
			"sha256:f5d943b4ed22b80c6199203f8a7eaa8eb68ef7b2d46ef6b1b26f05e21f8beb13",
		},
		{
			`# includes
			accounts {
                          foo { include 'foo.conf'}
                          bar { users = [{user = "bar"}] }
                          quux { include 'quux.conf'}
                        }
                        `,
			map[string]string{
				"foo.conf":  ` users = [{user = "foo"}]`,
				"quux.conf": ` users = [{user = "quux"}]`,
			},
			"sha256:e72d70c91b64b0f880f86decb95ec2600cbdcf8bdcd2355fce5ebc54a84a77e9",
		},
		{
			`# includes
			accounts {
                          foo { include 'foo.conf'}
                          bar { include 'bar.conf'}
                          quux { include 'quux.conf'}
                        }
                        `,
			map[string]string{
				"foo.conf":  ` users = [{user = "foo"}]`,
				"bar.conf":  ` users = [{user = "bar"}]`,
				"quux.conf": ` users = [{user = "quux"}]`,
			},
			"sha256:e72d70c91b64b0f880f86decb95ec2600cbdcf8bdcd2355fce5ebc54a84a77e9",
		},
	} {
		t.Run(fmt.Sprintf("digest_%d", i), func(t *testing.T) {
			sdir := t.TempDir()
			f, err := os.CreateTemp(sdir, "nats.conf-")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.Name(), []byte(test.input), 0644); err != nil {
				t.Fatal(err)
			}
			if test.includes != nil {
				for includeFile, contents := range test.includes {
					inf, err := os.Create(filepath.Join(sdir, includeFile))
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(inf.Name(), []byte(contents), 0644); err != nil {
						t.Fatal(err)
					}
				}
			}

			// v2 digest.
			_, v2digest, err := ParseFileWithChecksDigest(f.Name())
			if err != nil {
				t.Fatalf("v2 digest failed: %v", err)
			}
			if v2digest != test.digest {
				t.Errorf("v2 digest mismatch:\n  got:      %s\n  expected: %s", v2digest, test.digest)
			}

			// Also compare with v1 digest.
			_, v1digest, err := conf.ParseFileWithChecksDigest(f.Name())
			if err != nil {
				t.Fatalf("v1 digest failed: %v", err)
			}
			if v2digest != v1digest {
				t.Errorf("v2 digest != v1 digest:\n  v1: %s\n  v2: %s", v1digest, v2digest)
			}
		})
	}
}

// =============================================================================
// VAL-PARSER-025: Cross-validate digest with v1 for all digest test cases
// =============================================================================

func TestCompatDigestCrossValidation(t *testing.T) {
	// Simple case: no substitutions.
	sdir := t.TempDir()
	f, err := os.CreateTemp(sdir, "nats.conf-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.Name(), []byte(`foo = bar`), 0644); err != nil {
		t.Fatal(err)
	}
	_, v1d, err := conf.ParseFileWithChecksDigest(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	_, v2d, err := ParseFileWithChecksDigest(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if v1d != v2d {
		t.Fatalf("Digest mismatch: v1=%s v2=%s", v1d, v2d)
	}
}

// =============================================================================
// VAL-PARSER-029: Duplicate Key Last-Wins Semantics
// =============================================================================

func TestCompatDuplicateKeyLastWins(t *testing.T) {
	crossValidate(t, "foo = 1\nfoo = 2")
}

// =============================================================================
// ParseFile cross-validation with v1 simple.conf
// =============================================================================

func TestCompatParseFileSimpleConf(t *testing.T) {
	// Use the actual simple.conf from the conf/ directory.
	confDir := filepath.Join("..", "simple.conf")
	if _, err := os.Stat(confDir); os.IsNotExist(err) {
		t.Skip("simple.conf not found, skipping")
	}
	crossValidateFile(t, confDir)
}

// =============================================================================
// VAL-PARSER-023: More complex cross-validation tests
// =============================================================================

func TestCompatParseFileWithIncludes(t *testing.T) {
	dir := t.TempDir()
	includesDir := filepath.Join(dir, "includes")
	if err := os.MkdirAll(includesDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create include files matching conf/includes structure.
	if err := os.WriteFile(filepath.Join(includesDir, "passwords.conf"), []byte(`
ALICE_PASS: $2a$10$UHR6GhotWhpLsKtVP0/i6.Nh9.fuY73cWjLoJjb2sKT8KISBcUW5q
BOB_PASS: $2a$11$dZM98SpGeI7dCFFGSpt.JObQcix8YHml4TBUZoge9R1uxnMIln5ly
`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(includesDir, "users.conf"), []byte(`
include ./passwords.conf;

users = [
  {user: alice, password: $ALICE_PASS}
  {user: bob,   password: $BOB_PASS}
]
`), 0644); err != nil {
		t.Fatal(err)
	}

	mainConf := `
listen: 127.0.0.1:4222

authorization {
  include 'includes/users.conf'
  timeout: 0.5
}
`
	mainFile := filepath.Join(dir, "simple.conf")
	if err := os.WriteFile(mainFile, []byte(mainConf), 0644); err != nil {
		t.Fatal(err)
	}

	// Cross-validate.
	v1m, err := conf.ParseFile(mainFile)
	if err != nil {
		t.Fatalf("v1 ParseFile failed: %v", err)
	}
	v2m, err := ParseFile(mainFile)
	if err != nil {
		t.Fatalf("v2 ParseFile failed: %v", err)
	}
	if !reflect.DeepEqual(v1m, v2m) {
		t.Fatalf("v1/v2 ParseFile mismatch:\n  v1: %+v\n  v2: %+v", v1m, v2m)
	}
}

// =============================================================================
// JSON-style configs cross-validation
// =============================================================================

func TestCompatJSONWithNestedBlocks(t *testing.T) {
	inputs := []string{
		`{
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
}`,
		`{
  "jetstream": {
    "store_dir": "/tmp/nats"
    "max_mem": 1000000,
  },
  "port": 4222,
  "server_name": "nats1"
}`,
		`{}`,
	}
	for i, input := range inputs {
		t.Run(fmt.Sprintf("json_%d", i), func(t *testing.T) {
			crossValidate(t, input)
		})
	}
}

// =============================================================================
// Block configs cross-validation
// =============================================================================

func TestCompatBlocks(t *testing.T) {
	inputs := []string{
		`{ listen: 0.0.0.0:4222 }`,
		`{
  listen: 0.0.0.0:4222
}`,
		`{
  "debug":              False
  "prof_port":          8221
  "server_name":        "aws-useast2-natscj1-1"
}`,
	}
	for i, input := range inputs {
		t.Run(fmt.Sprintf("block_%d", i), func(t *testing.T) {
			crossValidate(t, input)
		})
	}
}

// =============================================================================
// VAL-PARSER-026: Fuzz Resilience — No Panics on Malformed Input
// =============================================================================

func FuzzParse(f *testing.F) {
	// Seed corpus with representative inputs.
	f.Add("foo = bar")
	f.Add("foo = 42")
	f.Add("foo = true")
	f.Add("foo = 3.14")
	f.Add("foo = -22.2")
	f.Add("foo { bar = 1 }")
	f.Add("foo = [1, 2, 3]")
	f.Add("# comment")
	f.Add("// comment")
	f.Add("include 'file.conf'")
	f.Add("foo = $var")
	f.Add("password: $2a$11$ooo")
	f.Add("foo='1'; bar=2.2; baz=true")
	f.Add("{}")
	f.Add("")
	f.Add("  \n  \t  ")
	f.Add(`A@@Føøøø?˛ø:{øøøø˙˙`)
	f.Add(`include "9/�`)
	f.Add("foo = 99999999999999999999")
	f.Add("foo = (\nblock\n)\n")
	f.Add("listen: 127.0.0.1:4222")
	f.Add("foo = 2016-05-04T18:53:41Z")

	f.Fuzz(func(t *testing.T, data string) {
		// Must not panic on any input.
		Parse(data)
		ParseWithChecks(data)
	})
}

// =============================================================================
// Additional edge case tests
// =============================================================================

func TestCompatErrorOnMalformedInput(t *testing.T) {
	// From v1's TestParserNoInfiniteLoop
	for _, input := range []string{
		`A@@Føøøø?˛ø:{øøøø˙˙`,
		`include "9/�`,
	} {
		_, err := Parse(input)
		if err == nil {
			t.Fatalf("Expected error for malformed input %q", input)
		}
	}
}

func TestCompatInvalidKeyNoValue(t *testing.T) {
	for _, test := range []struct {
		name string
		conf string
	}{
		{"key no value", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{"untrimmed key", "              aaaaaaaaaaaaaaaaaaaaaaaaaaa"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse(test.conf)
			if err == nil {
				t.Error("Expected error for invalid config")
			}
			if !strings.Contains(err.Error(), "config is invalid") {
				t.Errorf("Expected 'config is invalid' error, got: %v", err)
			}
		})
	}
}

func TestCompatParseFileError(t *testing.T) {
	_, err := ParseFile("/nonexistent/file.conf")
	if err == nil {
		t.Fatal("Expected error for nonexistent file")
	}
}

func TestCompatParseFileWithChecksDigestError(t *testing.T) {
	_, _, err := ParseFileWithChecksDigest("/nonexistent/file.conf")
	if err == nil {
		t.Fatal("Expected error for nonexistent file")
	}
}
