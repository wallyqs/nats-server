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

import "time"

// Position represents a source location in a configuration file.
type Position struct {
	// Line is the 1-based line number.
	Line int
	// Column is the 0-based character offset within the line.
	Column int
	// File is the source file path, empty for in-memory parsing.
	File string
}

// Node is the interface implemented by all AST node types. Every node
// carries source position information and a type identifier.
type Node interface {
	// Position returns the source location of this node.
	Position() Position
	// Type returns the node type name (e.g., "Document", "KeyValue").
	Type() string
}

// NodeBase provides common fields embedded by all AST node types.
// It stores the source position for the node.
type NodeBase struct {
	// Pos is the source position of this node.
	Pos Position
}

// Position returns the source location of this node.
func (n *NodeBase) Position() Position {
	return n.Pos
}

// Document is the root AST node representing an entire configuration.
// It contains an ordered list of top-level items (key-value pairs,
// comments, includes).
type Document struct {
	NodeBase
	// Items holds the ordered top-level nodes in the document.
	Items []Node
	// usedVarKeys tracks keys that were used as variable sources during
	// parsing. Each entry maps a KeyValueNode pointer to true.
	// This is used by the backwards-compatible API for pedantic mode.
	usedVarKeys map[*KeyValueNode]bool
	// Source is the original source text of the document.
	// Set during raw-mode parsing for round-trip emission.
	Source string
}

// Type returns "Document".
func (d *Document) Type() string { return "Document" }

// KeyValueNode represents a key-value pair in the configuration.
// It stores the key (with separator style), the value node, and
// an optional trailing comment.
type KeyValueNode struct {
	NodeBase
	// Key is the key node for this pair.
	Key *KeyNode
	// Value is the value node for this pair.
	Value Node
	// Comment is an optional trailing comment on the same line.
	Comment *CommentNode
}

// Type returns "KeyValue".
func (kv *KeyValueNode) Type() string { return "KeyValue" }

// MapNode represents an ordered collection of key-value pairs
// enclosed in braces ({}). Items may include key-value pairs,
// comments, and include directives.
type MapNode struct {
	NodeBase
	// Items holds the ordered nodes within the map block.
	Items []Node
}

// Type returns "Map".
func (m *MapNode) Type() string { return "Map" }

// ArrayNode represents an ordered collection of values
// enclosed in brackets ([]).
type ArrayNode struct {
	NodeBase
	// Elements holds the ordered value nodes in the array.
	Elements []Node
}

// Type returns "Array".
func (a *ArrayNode) Type() string { return "Array" }

// StringNode represents a string value in the configuration.
type StringNode struct {
	NodeBase
	// Value is the string content after processing escape sequences.
	Value string
	// Raw is the original text representation including quotes and escape
	// sequences. Set during raw-mode parsing for round-trip emission.
	Raw string
}

// Type returns "String".
func (s *StringNode) Type() string { return "String" }

// IntegerNode represents an integer value, possibly parsed from
// a value with a size suffix (e.g., 1k, 2MB).
type IntegerNode struct {
	NodeBase
	// Value is the integer value after suffix expansion.
	Value int64
	// Raw is the original text representation including any suffix.
	Raw string
}

// Type returns "Integer".
func (n *IntegerNode) Type() string { return "Integer" }

// FloatNode represents a floating-point value.
type FloatNode struct {
	NodeBase
	// Value is the parsed float64 value.
	Value float64
	// Raw is the original text representation.
	// Set during raw-mode parsing for round-trip emission.
	Raw string
}

// Type returns "Float".
func (f *FloatNode) Type() string { return "Float" }

// BoolNode represents a boolean value. Recognized literals include
// true/false, yes/no, and on/off (case-insensitive).
type BoolNode struct {
	NodeBase
	// Value is the parsed boolean value.
	Value bool
	// Raw is the original text representation (e.g., "true", "yes", "on").
	// Set during raw-mode parsing for round-trip emission.
	Raw string
}

// Type returns "Bool".
func (b *BoolNode) Type() string { return "Bool" }

// DatetimeNode represents an ISO8601 Zulu datetime value.
type DatetimeNode struct {
	NodeBase
	// Value is the parsed time.Time value.
	Value time.Time
	// Raw is the original text representation.
	// Set during raw-mode parsing for round-trip emission.
	Raw string
}

// Type returns "Datetime".
func (d *DatetimeNode) Type() string { return "Datetime" }

// CommentNode represents a comment in the configuration. Both
// hash (#) and slash (//) comment styles are supported.
type CommentNode struct {
	NodeBase
	// Style indicates whether this is a hash or slash comment.
	Style CommentStyle
	// Text is the comment body (excluding the comment delimiter).
	Text string
}

// Type returns "Comment".
func (c *CommentNode) Type() string { return "Comment" }

// VariableNode represents a variable reference ($name) in the
// configuration. It stores the variable name and optionally
// the resolved value after variable resolution.
type VariableNode struct {
	NodeBase
	// Name is the variable name (without the $ prefix).
	Name string
	// ResolvedValue is the resolved value node, set during parsing.
	ResolvedValue Node
}

// Type returns "Variable".
func (v *VariableNode) Type() string { return "Variable" }

// IncludeNode represents an include directive that references
// another configuration file to be merged into the current scope.
type IncludeNode struct {
	NodeBase
	// Path is the include file path (may be relative).
	Path string
	// Raw is the original include directive text (e.g., "include './file.conf'").
	// Set during raw-mode parsing for round-trip emission.
	Raw string
}

// Type returns "Include".
func (i *IncludeNode) Type() string { return "Include" }

// BlockStringNode represents a block string delimited by
// parentheses ((...)), which can contain multi-line content.
type BlockStringNode struct {
	NodeBase
	// Value is the block string content (excluding delimiters).
	Value string
}

// Type returns "BlockString".
func (bs *BlockStringNode) Type() string { return "BlockString" }

// KeyNode represents a configuration key with its associated
// separator style. Keys can be unquoted, single-quoted, or
// double-quoted in the source.
type KeyNode struct {
	NodeBase
	// Name is the key name.
	Name string
	// Separator is the style of separator used between this key and its value.
	Separator KeySeparator
	// sepKnown indicates whether Separator has been explicitly set.
	sepKnown bool
	// rawSep is the raw separator text between key and value
	// (e.g., " = ", ": ", " "). Set during raw-mode parsing.
	rawSep string
}

// Type returns "Key".
func (k *KeyNode) Type() string { return "Key" }
