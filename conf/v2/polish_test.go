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

// TestIncludeSepKnownPreservation verifies that after including a file,
// the key separator metadata from the included file is not overwritten
// by the parent lexer state. The included key should have sepKnown=true
// so that setValue does not overwrite its separator.
func TestIncludeSepKnownPreservation(t *testing.T) {
	// Create temporary included file using colon separator.
	dir := t.TempDir()
	includedFile := filepath.Join(dir, "included.conf")
	if err := os.WriteFile(includedFile, []byte("inner_key: inner_value\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Parent file uses equals separator.
	parentConfig := fmt.Sprintf("parent_key = parent_value\ninclude %q\n", includedFile)

	doc, err := ParseAST(parentConfig)
	if err != nil {
		t.Fatalf("ParseAST error: %v", err)
	}

	// Find the included key-value node and verify its separator.
	for _, item := range doc.Items {
		kv, ok := item.(*KeyValueNode)
		if !ok {
			continue
		}
		if kv.Key.Name == "inner_key" {
			if kv.Key.Separator != SepColon {
				t.Errorf("included key 'inner_key' separator = %v, want %v (SepColon)", kv.Key.Separator, SepColon)
			}
			if !kv.Key.sepKnown {
				t.Errorf("included key 'inner_key' sepKnown = false, want true")
			}
			return
		}
	}
	t.Error("included key 'inner_key' not found in AST")
}

// TestDatetimeCasingConsistency verifies that both ItemDatetime.String()
// and DatetimeNode.Type() use consistent "Datetime" casing.
func TestDatetimeCasingConsistency(t *testing.T) {
	itemStr := ItemDatetime.String()
	node := &DatetimeNode{}
	nodeType := node.Type()

	if itemStr != "Datetime" {
		t.Errorf("ItemDatetime.String() = %q, want %q", itemStr, "Datetime")
	}
	if nodeType != "Datetime" {
		t.Errorf("DatetimeNode.Type() = %q, want %q", nodeType, "Datetime")
	}
	if itemStr != nodeType {
		t.Errorf("casing mismatch: ItemDatetime.String() = %q, DatetimeNode.Type() = %q", itemStr, nodeType)
	}
}

// TestEnumStringFallbackDoesNotPanic verifies that calling String()
// on unknown enum values returns a formatted fallback instead of panicking.
func TestEnumStringFallbackDoesNotPanic(t *testing.T) {
	t.Run("ItemType unknown", func(t *testing.T) {
		unknownType := ItemType(999)
		got := unknownType.String()
		expected := "ItemType(999)"
		if got != expected {
			t.Errorf("ItemType(999).String() = %q, want %q", got, expected)
		}
	})

	t.Run("KeySeparator unknown", func(t *testing.T) {
		unknownSep := KeySeparator(42)
		got := unknownSep.String()
		expected := "KeySeparator(42)"
		if got != expected {
			t.Errorf("KeySeparator(42).String() = %q, want %q", got, expected)
		}
	})

	t.Run("CommentStyle unknown", func(t *testing.T) {
		unknownStyle := CommentStyle(99)
		got := unknownStyle.String()
		expected := "CommentStyle(99)"
		if got != expected {
			t.Errorf("CommentStyle(99).String() = %q, want %q", got, expected)
		}
	})
}

// TestStrictModePropagationInMap verifies that strict mode is properly
// propagated to nested struct values within map[string]StructType fields.
func TestStrictModePropagationInMap(t *testing.T) {
	type Inner struct {
		Name string `conf:"name"`
	}

	type Config struct {
		Items map[string]Inner `conf:"items"`
	}

	// Config with an unknown key inside a map value struct.
	data := []byte(`
items {
  first {
    name: "hello"
    unknown_key: "should fail in strict mode"
  }
}
`)

	// In strict mode, the unknown key inside the map value should cause an error.
	var cfg Config
	err := UnmarshalWith(data, &cfg, &UnmarshalOptions{Strict: true})
	if err == nil {
		t.Fatal("expected error for unknown key in strict mode, got nil")
	}
	if !strings.Contains(err.Error(), "unknown_key") {
		t.Errorf("error should mention 'unknown_key', got: %v", err)
	}

	// In permissive mode, the unknown key should be silently ignored.
	var cfg2 Config
	err = UnmarshalWith(data, &cfg2, nil)
	if err != nil {
		t.Fatalf("unexpected error in permissive mode: %v", err)
	}
	if cfg2.Items["first"].Name != "hello" {
		t.Errorf("Items['first'].Name = %q, want %q", cfg2.Items["first"].Name, "hello")
	}
}

// customSliceElem is a value type whose pointer implements Unmarshaler.
type customSliceElem struct {
	Transformed string
}

func (c *customSliceElem) UnmarshalConfig(v any) error {
	s, ok := v.(string)
	if !ok {
		return fmt.Errorf("expected string, got %T", v)
	}
	c.Transformed = "custom:" + s
	return nil
}

// TestUnmarshalerInterfaceForValueTypeSliceElements verifies that
// when *customType implements Unmarshaler, []customType elements
// are correctly unmarshaled using the custom interface.
func TestUnmarshalerInterfaceForValueTypeSliceElements(t *testing.T) {
	type Config struct {
		Items []customSliceElem `conf:"items"`
	}

	data := []byte(`items = ["alpha", "beta"]`)

	var cfg Config
	if err := Unmarshal(data, &cfg); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}

	if len(cfg.Items) != 2 {
		t.Fatalf("len(Items) = %d, want 2", len(cfg.Items))
	}
	if cfg.Items[0].Transformed != "custom:alpha" {
		t.Errorf("Items[0].Transformed = %q, want %q", cfg.Items[0].Transformed, "custom:alpha")
	}
	if cfg.Items[1].Transformed != "custom:beta" {
		t.Errorf("Items[1].Transformed = %q, want %q", cfg.Items[1].Transformed, "custom:beta")
	}
}

// TestUnmarshalerSliceElementError verifies that errors from
// Unmarshaler on slice elements are properly propagated.
type failingSliceElem struct{}

func (f *failingSliceElem) UnmarshalConfig(v any) error {
	return fmt.Errorf("custom unmarshal error")
}

func TestUnmarshalerSliceElementError(t *testing.T) {
	type Config struct {
		Items []failingSliceElem `conf:"items"`
	}

	data := []byte(`items = ["value"]`)

	var cfg Config
	err := Unmarshal(data, &cfg)
	if err == nil {
		t.Fatal("expected error from failing Unmarshaler, got nil")
	}
	if !strings.Contains(err.Error(), "custom unmarshal error") {
		t.Errorf("error = %q, should contain 'custom unmarshal error'", err)
	}
}
