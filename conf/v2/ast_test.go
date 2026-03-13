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
	"testing"
	"time"
)

// TestItemTypeConstants verifies that all v1 item types have corresponding
// v2 item type constants, plus the additional itemComment type.
func TestItemTypeConstants(t *testing.T) {
	// All v1 item types must exist in v2.
	expectedTypes := []struct {
		typ  ItemType
		name string
	}{
		{ItemError, "Error"},
		{ItemNIL, "NIL"},
		{ItemEOF, "EOF"},
		{ItemKey, "Key"},
		{ItemText, "Text"},
		{ItemString, "String"},
		{ItemBool, "Bool"},
		{ItemInteger, "Integer"},
		{ItemFloat, "Float"},
		{ItemDatetime, "DateTime"},
		{ItemArrayStart, "ArrayStart"},
		{ItemArrayEnd, "ArrayEnd"},
		{ItemMapStart, "MapStart"},
		{ItemMapEnd, "MapEnd"},
		{ItemCommentStart, "CommentStart"},
		{ItemVariable, "Variable"},
		{ItemInclude, "Include"},
		// v2 addition: comment token
		{ItemComment, "Comment"},
	}

	for _, et := range expectedTypes {
		if et.typ.String() != et.name {
			t.Errorf("ItemType %d: got String() = %q, want %q", et.typ, et.typ.String(), et.name)
		}
	}
}

// TestItemTypeValues verifies the numeric values of item type constants
// match v1 ordering (itemError=0 through itemInclude=16).
func TestItemTypeValues(t *testing.T) {
	if ItemError != 0 {
		t.Errorf("ItemError = %d, want 0", ItemError)
	}
	if ItemNIL != 1 {
		t.Errorf("ItemNIL = %d, want 1", ItemNIL)
	}
	if ItemEOF != 2 {
		t.Errorf("ItemEOF = %d, want 2", ItemEOF)
	}
	if ItemKey != 3 {
		t.Errorf("ItemKey = %d, want 3", ItemKey)
	}
	if ItemText != 4 {
		t.Errorf("ItemText = %d, want 4", ItemText)
	}
	if ItemString != 5 {
		t.Errorf("ItemString = %d, want 5", ItemString)
	}
	if ItemBool != 6 {
		t.Errorf("ItemBool = %d, want 6", ItemBool)
	}
	if ItemInteger != 7 {
		t.Errorf("ItemInteger = %d, want 7", ItemInteger)
	}
	if ItemFloat != 8 {
		t.Errorf("ItemFloat = %d, want 8", ItemFloat)
	}
	if ItemDatetime != 9 {
		t.Errorf("ItemDatetime = %d, want 9", ItemDatetime)
	}
	if ItemArrayStart != 10 {
		t.Errorf("ItemArrayStart = %d, want 10", ItemArrayStart)
	}
	if ItemArrayEnd != 11 {
		t.Errorf("ItemArrayEnd = %d, want 11", ItemArrayEnd)
	}
	if ItemMapStart != 12 {
		t.Errorf("ItemMapStart = %d, want 12", ItemMapStart)
	}
	if ItemMapEnd != 13 {
		t.Errorf("ItemMapEnd = %d, want 13", ItemMapEnd)
	}
	if ItemCommentStart != 14 {
		t.Errorf("ItemCommentStart = %d, want 14", ItemCommentStart)
	}
	if ItemVariable != 15 {
		t.Errorf("ItemVariable = %d, want 15", ItemVariable)
	}
	if ItemInclude != 16 {
		t.Errorf("ItemInclude = %d, want 16", ItemInclude)
	}
	// ItemComment is a v2 addition, comes after ItemInclude.
	if ItemComment != 17 {
		t.Errorf("ItemComment = %d, want 17", ItemComment)
	}
}

// TestKeySeparatorEnum verifies the key separator style enum values.
func TestKeySeparatorEnum(t *testing.T) {
	tests := []struct {
		sep  KeySeparator
		name string
	}{
		{SepEquals, "equals"},
		{SepColon, "colon"},
		{SepSpace, "space"},
	}
	for _, tt := range tests {
		if tt.sep.String() != tt.name {
			t.Errorf("KeySeparator %d: got String() = %q, want %q", tt.sep, tt.sep.String(), tt.name)
		}
	}
}

// TestCommentStyleEnum verifies the comment style enum values.
func TestCommentStyleEnum(t *testing.T) {
	tests := []struct {
		style CommentStyle
		name  string
	}{
		{CommentHash, "hash"},
		{CommentSlash, "slash"},
	}
	for _, tt := range tests {
		if tt.style.String() != tt.name {
			t.Errorf("CommentStyle %d: got String() = %q, want %q", tt.style, tt.style.String(), tt.name)
		}
	}
}

// TestPositionStruct verifies the Position struct fields.
func TestPositionStruct(t *testing.T) {
	pos := Position{Line: 10, Column: 5, File: "test.conf"}
	if pos.Line != 10 {
		t.Errorf("Position.Line = %d, want 10", pos.Line)
	}
	if pos.Column != 5 {
		t.Errorf("Position.Column = %d, want 5", pos.Column)
	}
	if pos.File != "test.conf" {
		t.Errorf("Position.File = %q, want %q", pos.File, "test.conf")
	}
}

// TestNodeBasePosition verifies that NodeBase embeds position info accessible via Pos().
func TestNodeBasePosition(t *testing.T) {
	nb := NodeBase{Pos: Position{Line: 3, Column: 7, File: "server.conf"}}
	pos := nb.Position()
	if pos.Line != 3 || pos.Column != 7 || pos.File != "server.conf" {
		t.Errorf("NodeBase.Position() = %+v, want {Line:3 Column:7 File:server.conf}", pos)
	}
}

// TestNodeInterface verifies that all AST node types implement the Node interface.
func TestNodeInterface(t *testing.T) {
	pos := Position{Line: 1, Column: 0, File: "test.conf"}

	// Every AST node type must implement Node with Position() and Type().
	nodes := []Node{
		&Document{NodeBase: NodeBase{Pos: pos}},
		&KeyValueNode{NodeBase: NodeBase{Pos: pos}},
		&MapNode{NodeBase: NodeBase{Pos: pos}},
		&ArrayNode{NodeBase: NodeBase{Pos: pos}},
		&StringNode{NodeBase: NodeBase{Pos: pos}},
		&IntegerNode{NodeBase: NodeBase{Pos: pos}},
		&FloatNode{NodeBase: NodeBase{Pos: pos}},
		&BoolNode{NodeBase: NodeBase{Pos: pos}},
		&DatetimeNode{NodeBase: NodeBase{Pos: pos}},
		&CommentNode{NodeBase: NodeBase{Pos: pos}},
		&VariableNode{NodeBase: NodeBase{Pos: pos}},
		&IncludeNode{NodeBase: NodeBase{Pos: pos}},
		&BlockStringNode{NodeBase: NodeBase{Pos: pos}},
		&KeyNode{NodeBase: NodeBase{Pos: pos}},
	}

	for _, n := range nodes {
		p := n.Position()
		if p.Line != 1 || p.Column != 0 || p.File != "test.conf" {
			t.Errorf("%T.Position() = %+v, want {Line:1 Column:0 File:test.conf}", n, p)
		}
		// Type() should return a non-empty string.
		if n.Type() == "" {
			t.Errorf("%T.Type() returned empty string", n)
		}
	}
}

// TestDocumentNode verifies the Document (root) node type.
func TestDocumentNode(t *testing.T) {
	doc := &Document{
		NodeBase: NodeBase{Pos: Position{Line: 1, Column: 0, File: "root.conf"}},
	}
	if doc.Type() != "Document" {
		t.Errorf("Document.Type() = %q, want %q", doc.Type(), "Document")
	}
	if doc.Items == nil {
		// Items should be usable even if nil (zero-value slice).
		doc.Items = []Node{}
	}
	if len(doc.Items) != 0 {
		t.Errorf("Document.Items has %d items, want 0", len(doc.Items))
	}
}

// TestKeyValueNode verifies the KeyValue node type carries key, value, and separator.
func TestKeyValueNode(t *testing.T) {
	key := &KeyNode{
		NodeBase:  NodeBase{Pos: Position{Line: 1, Column: 0}},
		Name:      "port",
		Separator: SepEquals,
	}
	val := &IntegerNode{
		NodeBase: NodeBase{Pos: Position{Line: 1, Column: 7}},
		Value:    4222,
	}
	kv := &KeyValueNode{
		NodeBase: NodeBase{Pos: Position{Line: 1, Column: 0}},
		Key:      key,
		Value:    val,
	}
	if kv.Type() != "KeyValue" {
		t.Errorf("KeyValueNode.Type() = %q, want %q", kv.Type(), "KeyValue")
	}
	if kv.Key.Name != "port" {
		t.Errorf("KeyValueNode.Key.Name = %q, want %q", kv.Key.Name, "port")
	}
	if kv.Key.Separator != SepEquals {
		t.Errorf("KeyValueNode.Key.Separator = %v, want SepEquals", kv.Key.Separator)
	}
}

// TestMapNode verifies the MapNode type holds ordered key-value pairs.
func TestMapNode(t *testing.T) {
	m := &MapNode{
		NodeBase: NodeBase{Pos: Position{Line: 2, Column: 4}},
		Items:    []Node{},
	}
	if m.Type() != "Map" {
		t.Errorf("MapNode.Type() = %q, want %q", m.Type(), "Map")
	}
}

// TestArrayNode verifies the ArrayNode type holds ordered elements.
func TestArrayNode(t *testing.T) {
	a := &ArrayNode{
		NodeBase: NodeBase{Pos: Position{Line: 3, Column: 0}},
		Elements: []Node{},
	}
	if a.Type() != "Array" {
		t.Errorf("ArrayNode.Type() = %q, want %q", a.Type(), "Array")
	}
}

// TestStringNode verifies the StringNode type.
func TestStringNode(t *testing.T) {
	s := &StringNode{
		NodeBase: NodeBase{Pos: Position{Line: 1, Column: 10}},
		Value:    "hello world",
	}
	if s.Type() != "String" {
		t.Errorf("StringNode.Type() = %q, want %q", s.Type(), "String")
	}
	if s.Value != "hello world" {
		t.Errorf("StringNode.Value = %q, want %q", s.Value, "hello world")
	}
}

// TestIntegerNode verifies the IntegerNode type.
func TestIntegerNode(t *testing.T) {
	n := &IntegerNode{
		NodeBase: NodeBase{Pos: Position{Line: 1, Column: 10}},
		Value:    4222,
	}
	if n.Type() != "Integer" {
		t.Errorf("IntegerNode.Type() = %q, want %q", n.Type(), "Integer")
	}
	if n.Value != 4222 {
		t.Errorf("IntegerNode.Value = %d, want 4222", n.Value)
	}
}

// TestFloatNode verifies the FloatNode type.
func TestFloatNode(t *testing.T) {
	f := &FloatNode{
		NodeBase: NodeBase{Pos: Position{Line: 1, Column: 10}},
		Value:    3.14,
	}
	if f.Type() != "Float" {
		t.Errorf("FloatNode.Type() = %q, want %q", f.Type(), "Float")
	}
	if f.Value != 3.14 {
		t.Errorf("FloatNode.Value = %f, want 3.14", f.Value)
	}
}

// TestBoolNode verifies the BoolNode type.
func TestBoolNode(t *testing.T) {
	b := &BoolNode{
		NodeBase: NodeBase{Pos: Position{Line: 1, Column: 10}},
		Value:    true,
	}
	if b.Type() != "Bool" {
		t.Errorf("BoolNode.Type() = %q, want %q", b.Type(), "Bool")
	}
	if !b.Value {
		t.Error("BoolNode.Value = false, want true")
	}
}

// TestDatetimeNode verifies the DatetimeNode type.
func TestDatetimeNode(t *testing.T) {
	dt := time.Date(2023, 11, 1, 0, 0, 0, 0, time.UTC)
	d := &DatetimeNode{
		NodeBase: NodeBase{Pos: Position{Line: 1, Column: 10}},
		Value:    dt,
	}
	if d.Type() != "Datetime" {
		t.Errorf("DatetimeNode.Type() = %q, want %q", d.Type(), "Datetime")
	}
	if !d.Value.Equal(dt) {
		t.Errorf("DatetimeNode.Value = %v, want %v", d.Value, dt)
	}
}

// TestCommentNode verifies the CommentNode type stores comment style and text.
func TestCommentNode(t *testing.T) {
	tests := []struct {
		style CommentStyle
		text  string
	}{
		{CommentHash, " This is a hash comment"},
		{CommentSlash, " This is a slash comment"},
	}
	for _, tt := range tests {
		c := &CommentNode{
			NodeBase: NodeBase{Pos: Position{Line: 1, Column: 0}},
			Style:    tt.style,
			Text:     tt.text,
		}
		if c.Type() != "Comment" {
			t.Errorf("CommentNode.Type() = %q, want %q", c.Type(), "Comment")
		}
		if c.Style != tt.style {
			t.Errorf("CommentNode.Style = %v, want %v", c.Style, tt.style)
		}
		if c.Text != tt.text {
			t.Errorf("CommentNode.Text = %q, want %q", c.Text, tt.text)
		}
	}
}

// TestVariableNode verifies the VariableNode type stores name and resolved value.
func TestVariableNode(t *testing.T) {
	v := &VariableNode{
		NodeBase: NodeBase{Pos: Position{Line: 5, Column: 8}},
		Name:     "MY_VAR",
	}
	if v.Type() != "Variable" {
		t.Errorf("VariableNode.Type() = %q, want %q", v.Type(), "Variable")
	}
	if v.Name != "MY_VAR" {
		t.Errorf("VariableNode.Name = %q, want %q", v.Name, "MY_VAR")
	}
}

// TestIncludeNode verifies the IncludeNode type stores the include path.
func TestIncludeNode(t *testing.T) {
	inc := &IncludeNode{
		NodeBase: NodeBase{Pos: Position{Line: 10, Column: 0}},
		Path:     "includes/auth.conf",
	}
	if inc.Type() != "Include" {
		t.Errorf("IncludeNode.Type() = %q, want %q", inc.Type(), "Include")
	}
	if inc.Path != "includes/auth.conf" {
		t.Errorf("IncludeNode.Path = %q, want %q", inc.Path, "includes/auth.conf")
	}
}

// TestBlockStringNode verifies the BlockStringNode type.
func TestBlockStringNode(t *testing.T) {
	bs := &BlockStringNode{
		NodeBase: NodeBase{Pos: Position{Line: 3, Column: 4}},
		Value:    "multi\nline\nblock",
	}
	if bs.Type() != "BlockString" {
		t.Errorf("BlockStringNode.Type() = %q, want %q", bs.Type(), "BlockString")
	}
	if bs.Value != "multi\nline\nblock" {
		t.Errorf("BlockStringNode.Value = %q, want %q", bs.Value, "multi\nline\nblock")
	}
}

// TestKeyNode verifies the KeyNode type carries separator style.
func TestKeyNode(t *testing.T) {
	tests := []struct {
		name string
		sep  KeySeparator
	}{
		{"port", SepEquals},
		{"host", SepColon},
		{"debug", SepSpace},
	}
	for _, tt := range tests {
		k := &KeyNode{
			NodeBase:  NodeBase{Pos: Position{Line: 1, Column: 0}},
			Name:      tt.name,
			Separator: tt.sep,
		}
		if k.Type() != "Key" {
			t.Errorf("KeyNode.Type() = %q, want %q", k.Type(), "Key")
		}
		if k.Name != tt.name {
			t.Errorf("KeyNode.Name = %q, want %q", k.Name, tt.name)
		}
		if k.Separator != tt.sep {
			t.Errorf("KeyNode.Separator = %v, want %v", k.Separator, tt.sep)
		}
	}
}

// TestNodeTypeStrings verifies all node types return distinct, non-empty type strings.
func TestNodeTypeStrings(t *testing.T) {
	pos := Position{Line: 1, Column: 0}
	nb := NodeBase{Pos: pos}

	types := map[string]Node{
		"Document":    &Document{NodeBase: nb},
		"KeyValue":    &KeyValueNode{NodeBase: nb},
		"Map":         &MapNode{NodeBase: nb},
		"Array":       &ArrayNode{NodeBase: nb},
		"String":      &StringNode{NodeBase: nb},
		"Integer":     &IntegerNode{NodeBase: nb},
		"Float":       &FloatNode{NodeBase: nb},
		"Bool":        &BoolNode{NodeBase: nb},
		"Datetime":    &DatetimeNode{NodeBase: nb},
		"Comment":     &CommentNode{NodeBase: nb},
		"Variable":    &VariableNode{NodeBase: nb},
		"Include":     &IncludeNode{NodeBase: nb},
		"BlockString": &BlockStringNode{NodeBase: nb},
		"Key":         &KeyNode{NodeBase: nb},
	}

	seen := make(map[string]bool)
	for expected, node := range types {
		actual := node.Type()
		if actual != expected {
			t.Errorf("Node %T.Type() = %q, want %q", node, actual, expected)
		}
		if seen[actual] {
			t.Errorf("Duplicate node type string %q", actual)
		}
		seen[actual] = true
	}
}

// TestDocumentWithChildren verifies a Document can hold various child nodes.
func TestDocumentWithChildren(t *testing.T) {
	pos := Position{Line: 1, Column: 0, File: "test.conf"}
	doc := &Document{
		NodeBase: NodeBase{Pos: pos},
		Items: []Node{
			&CommentNode{
				NodeBase: NodeBase{Pos: Position{Line: 1, Column: 0}},
				Style:    CommentHash,
				Text:     " Server config",
			},
			&KeyValueNode{
				NodeBase: NodeBase{Pos: Position{Line: 2, Column: 0}},
				Key: &KeyNode{
					NodeBase:  NodeBase{Pos: Position{Line: 2, Column: 0}},
					Name:      "port",
					Separator: SepEquals,
				},
				Value: &IntegerNode{
					NodeBase: NodeBase{Pos: Position{Line: 2, Column: 7}},
					Value:    4222,
				},
			},
		},
	}

	if len(doc.Items) != 2 {
		t.Fatalf("Document has %d items, want 2", len(doc.Items))
	}

	// First item should be a comment.
	comment, ok := doc.Items[0].(*CommentNode)
	if !ok {
		t.Fatalf("First item is %T, want *CommentNode", doc.Items[0])
	}
	if comment.Style != CommentHash {
		t.Errorf("Comment style = %v, want CommentHash", comment.Style)
	}

	// Second item should be a key-value.
	kv, ok := doc.Items[1].(*KeyValueNode)
	if !ok {
		t.Fatalf("Second item is %T, want *KeyValueNode", doc.Items[1])
	}
	if kv.Key.Name != "port" {
		t.Errorf("Key name = %q, want %q", kv.Key.Name, "port")
	}
}

// TestKeyValueWithComments verifies KeyValueNode can carry associated comments.
func TestKeyValueWithComments(t *testing.T) {
	kv := &KeyValueNode{
		NodeBase: NodeBase{Pos: Position{Line: 3, Column: 0}},
		Key: &KeyNode{
			NodeBase:  NodeBase{Pos: Position{Line: 3, Column: 0}},
			Name:      "debug",
			Separator: SepSpace,
		},
		Value: &BoolNode{
			NodeBase: NodeBase{Pos: Position{Line: 3, Column: 6}},
			Value:    true,
		},
		Comment: &CommentNode{
			NodeBase: NodeBase{Pos: Position{Line: 3, Column: 12}},
			Style:    CommentHash,
			Text:     " enable debug",
		},
	}

	if kv.Comment == nil {
		t.Fatal("KeyValueNode.Comment is nil")
	}
	if kv.Comment.Text != " enable debug" {
		t.Errorf("Comment text = %q, want %q", kv.Comment.Text, " enable debug")
	}
}

// TestMapNodeWithItems verifies MapNode can hold key-value items.
func TestMapNodeWithItems(t *testing.T) {
	m := &MapNode{
		NodeBase: NodeBase{Pos: Position{Line: 5, Column: 10}},
		Items: []Node{
			&KeyValueNode{
				NodeBase: NodeBase{Pos: Position{Line: 6, Column: 2}},
				Key: &KeyNode{
					NodeBase:  NodeBase{Pos: Position{Line: 6, Column: 2}},
					Name:      "name",
					Separator: SepColon,
				},
				Value: &StringNode{
					NodeBase: NodeBase{Pos: Position{Line: 6, Column: 9}},
					Value:    "my-cluster",
				},
			},
		},
	}

	if len(m.Items) != 1 {
		t.Fatalf("MapNode has %d items, want 1", len(m.Items))
	}
}

// TestArrayNodeWithElements verifies ArrayNode can hold multiple element types.
func TestArrayNodeWithElements(t *testing.T) {
	a := &ArrayNode{
		NodeBase: NodeBase{Pos: Position{Line: 1, Column: 8}},
		Elements: []Node{
			&StringNode{
				NodeBase: NodeBase{Pos: Position{Line: 1, Column: 9}},
				Value:    "nats://host1:4222",
			},
			&StringNode{
				NodeBase: NodeBase{Pos: Position{Line: 1, Column: 29}},
				Value:    "nats://host2:4222",
			},
		},
	}

	if len(a.Elements) != 2 {
		t.Fatalf("ArrayNode has %d elements, want 2", len(a.Elements))
	}
}

// TestVariableNodeResolvedValue verifies VariableNode can store a resolved value.
func TestVariableNodeResolvedValue(t *testing.T) {
	v := &VariableNode{
		NodeBase:      NodeBase{Pos: Position{Line: 1, Column: 10}},
		Name:          "PORT",
		ResolvedValue: &IntegerNode{
			NodeBase: NodeBase{Pos: Position{Line: 1, Column: 10}},
			Value:    4222,
		},
	}
	if v.ResolvedValue == nil {
		t.Fatal("VariableNode.ResolvedValue is nil")
	}
	intNode, ok := v.ResolvedValue.(*IntegerNode)
	if !ok {
		t.Fatalf("ResolvedValue is %T, want *IntegerNode", v.ResolvedValue)
	}
	if intNode.Value != 4222 {
		t.Errorf("ResolvedValue.Value = %d, want 4222", intNode.Value)
	}
}
