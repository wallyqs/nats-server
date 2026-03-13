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

// Backwards-compatible public API matching the v1 conf package signatures.
// Provides Parse, ParseFile, ParseWithChecks, ParseFileWithChecks, and
// ParseFileWithChecksDigest with identical behavior to the v1 conf package.

package v2

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
)

// token wraps a parsed value with source position information for
// pedantic mode. It provides the same interface methods as v1's token.
type token struct {
	// keyItem stores the key's lexer item for position reporting.
	keyItem item
	// value is the underlying Go value (string, bool, int64, float64, etc.).
	value any
	// usedVariable indicates if this token is a variable definition
	// that was referenced by another key.
	usedVariable bool
	// sourceFile is the path of the source file this token came from.
	sourceFile string
}

// Value returns the underlying Go value.
func (t *token) Value() any {
	return t.value
}

// Line returns the 1-based line number of the key for this token.
func (t *token) Line() int {
	return t.keyItem.line
}

// Position returns the 0-based character offset of the key within its line.
// This returns the key's position (not the value's), matching v1 behavior.
func (t *token) Position() int {
	return t.keyItem.pos
}

// IsUsedVariable returns true if this token is a variable definition
// that was referenced by another key via $name syntax.
func (t *token) IsUsedVariable() bool {
	return t.usedVariable
}

// SourceFile returns the path of the configuration file this token was
// parsed from. Returns empty string for in-memory parsing.
func (t *token) SourceFile() string {
	return t.sourceFile
}

// MarshalJSON implements json.Marshaler by marshaling the underlying value.
func (t *token) MarshalJSON() ([]byte, error) {
	return json.Marshal(t.value)
}

// Parse parses the given NATS configuration data and returns a map of
// keys to values. The values are raw Go types: string, bool, int64,
// float64, time.Time, []any, map[string]any. This is the non-pedantic
// mode matching v1's conf.Parse behavior.
func Parse(data string) (map[string]any, error) {
	return parseCompat(data, "", false)
}

// ParseWithChecks is equivalent to Parse but runs in pedantic mode.
// Values are wrapped in token structs providing position information
// via Value(), Line(), Position(), IsUsedVariable(), SourceFile(),
// and MarshalJSON() methods.
func ParseWithChecks(data string) (map[string]any, error) {
	return parseCompat(data, "", true)
}

// ParseFile parses a NATS configuration file and returns a map of
// keys to raw Go values. Include paths are resolved relative to the
// file's directory.
func ParseFile(fp string) (map[string]any, error) {
	data, err := os.ReadFile(fp)
	if err != nil {
		return nil, fmt.Errorf("error opening config file: %v", err)
	}
	return parseCompat(string(data), fp, false)
}

// ParseFileWithChecks is equivalent to ParseFile but runs in pedantic mode.
func ParseFileWithChecks(fp string) (map[string]any, error) {
	data, err := os.ReadFile(fp)
	if err != nil {
		return nil, err
	}
	return parseCompat(string(data), fp, true)
}

// ParseFileWithChecksDigest parses a file in pedantic mode and returns
// the parsed map along with a SHA-256 digest. The digest is computed
// from the JSON-encoded pedantic map after removing variable reference
// entries (usedVariable). The digest is returned as "sha256:<hex>".
func ParseFileWithChecksDigest(fp string) (map[string]any, string, error) {
	data, err := os.ReadFile(fp)
	if err != nil {
		return nil, "", err
	}
	m, err := parseCompat(string(data), fp, true)
	if err != nil {
		return nil, "", err
	}
	// Remove variable references before computing digest.
	cleanupUsedEnvVars(m)
	digest := sha256.New()
	e := json.NewEncoder(digest)
	err = e.Encode(m)
	if err != nil {
		return nil, "", err
	}
	return m, fmt.Sprintf("sha256:%x", digest.Sum(nil)), nil
}

// cleanupUsedEnvVars recursively removes entries where the token is
// marked as a used variable definition, matching v1 behavior.
func cleanupUsedEnvVars(m map[string]any) {
	for k, v := range m {
		t, ok := v.(*token)
		if !ok {
			continue
		}
		if t.usedVariable {
			delete(m, k)
			continue
		}
		// Recurse into nested maps.
		if tm, ok := t.value.(map[string]any); ok {
			cleanupUsedEnvVars(tm)
		}
	}
}

// converter holds state for converting an AST Document to map[string]any.
type converter struct {
	pedantic    bool
	usedVarKeys map[*KeyValueNode]bool
}

// parseCompat parses NATS config data and converts the AST to map[string]any,
// matching v1 behavior exactly. In pedantic mode, values are wrapped in tokens.
func parseCompat(data, fp string, pedantic bool) (map[string]any, error) {
	doc, err := parseAST(data, fp)
	if err != nil {
		return nil, err
	}
	c := &converter{
		pedantic:    pedantic,
		usedVarKeys: doc.usedVarKeys,
	}
	return c.astToMap(doc)
}

// astToMap converts a Document AST into a map[string]any, matching v1 output.
// In pedantic mode, values are wrapped in token structs.
func (c *converter) astToMap(doc *Document) (map[string]any, error) {
	m := make(map[string]any)
	if err := c.convertItems(doc.Items, m); err != nil {
		return nil, err
	}
	return m, nil
}

// convertItems processes a list of AST nodes and populates the target map.
func (c *converter) convertItems(items []Node, m map[string]any) error {
	for _, nd := range items {
		kv, ok := nd.(*KeyValueNode)
		if !ok {
			continue // Skip comments and other non-KV nodes
		}
		val, err := c.convertValue(kv.Value)
		if err != nil {
			return err
		}
		if c.pedantic {
			// Wrap in token with key position.
			keyItem := item2FromKeyNode(kv.Key)
			tk := &token{
				keyItem:      keyItem,
				value:        val,
				usedVariable: c.usedVarKeys[kv],
				sourceFile:   kv.Key.Pos.File,
			}
			m[kv.Key.Name] = tk
		} else {
			m[kv.Key.Name] = val
		}
	}
	return nil
}

// convertValue converts an AST value node to the corresponding Go type.
func (c *converter) convertValue(node Node) (any, error) {
	switch n := node.(type) {
	case *StringNode:
		return n.Value, nil
	case *IntegerNode:
		return n.Value, nil
	case *FloatNode:
		return n.Value, nil
	case *BoolNode:
		return n.Value, nil
	case *DatetimeNode:
		return n.Value, nil
	case *MapNode:
		inner := make(map[string]any)
		if err := c.convertItems(n.Items, inner); err != nil {
			return nil, err
		}
		return inner, nil
	case *ArrayNode:
		arr := make([]any, 0, len(n.Elements))
		for _, elem := range n.Elements {
			if _, ok := elem.(*CommentNode); ok {
				continue // Skip comments in arrays
			}
			val, err := c.convertValue(elem)
			if err != nil {
				return nil, err
			}
			arr = append(arr, val)
		}
		return arr, nil
	case *BlockStringNode:
		return n.Value, nil
	default:
		return nil, fmt.Errorf("unexpected node type: %T", node)
	}
}

// item2FromKeyNode creates a lexer item from a KeyNode for position tracking.
func item2FromKeyNode(key *KeyNode) item {
	return item{
		typ:  ItemKey,
		val:  key.Name,
		line: key.Pos.Line,
		pos:  key.Pos.Column,
	}
}
