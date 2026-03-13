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

// AST-to-NATS-config-text emitter. Walks the AST and produces text that
// preserves comments, formatting, variable definitions, include directives,
// and key separator styles for round-trip fidelity.

package v2

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// Emit is a convenience method on Document that calls the package-level
// Emit function.
func (d *Document) Emit() ([]byte, error) {
	return Emit(d)
}

// Emit produces NATS configuration text from the given AST Document.
// The emitter walks the AST and reconstructs the original text, preserving:
//   - All comments in their original positions (both # and // styles)
//   - Formatting (indentation, blank lines)
//   - Variable definitions ($name = value) and variable references ($name)
//   - Include directives (include 'path') verbatim
//   - Key separator styles (=, :, space)
//
// For round-trip fidelity, use ParseASTRaw to parse the input, which
// preserves variable references and include directives as AST nodes.
// Then Emit produces text that, when re-parsed with Parse, yields
// structurally equivalent results.
func Emit(doc *Document) ([]byte, error) {
	if doc == nil {
		return nil, fmt.Errorf("emit: cannot emit nil document")
	}
	e := &emitter{
		buf:         &bytes.Buffer{},
		currentLine: 1,
	}
	e.emitItems(doc.Items, 0)
	return e.buf.Bytes(), nil
}

// emitter holds state for walking the AST and producing text output.
type emitter struct {
	buf         *bytes.Buffer
	currentLine int
}

// emitItems emits a list of AST nodes at the given indentation depth.
// depth is measured in number of nesting levels (used for reconstructing
// indentation from position info).
func (e *emitter) emitItems(items []Node, depth int) {
	for _, item := range items {
		switch n := item.(type) {
		case *KeyValueNode:
			e.emitKeyValue(n, depth)
		case *CommentNode:
			e.emitStandaloneComment(n, depth)
		case *IncludeNode:
			e.emitInclude(n, depth)
		}
	}
}

// emitBlankLines emits blank lines to advance from the current line
// to the target line number. This preserves blank lines in the original.
func (e *emitter) emitBlankLines(targetLine int) {
	for e.currentLine < targetLine {
		e.buf.WriteByte('\n')
		e.currentLine++
	}
}

// emitIndent writes indentation whitespace based on the node's column position.
func (e *emitter) emitIndent(col int) {
	for i := 0; i < col; i++ {
		e.buf.WriteByte(' ')
	}
}

// emitKeyValue writes a key-value pair to the output.
func (e *emitter) emitKeyValue(kv *KeyValueNode, depth int) {
	// Emit blank lines to reach the correct line.
	e.emitBlankLines(kv.Pos.Line)

	// Emit indentation.
	e.emitIndent(kv.Key.Pos.Column)

	// Emit key.
	e.buf.WriteString(kv.Key.Name)

	// Emit separator.
	switch kv.Key.Separator {
	case SepEquals:
		e.buf.WriteString(" = ")
	case SepColon:
		e.buf.WriteString(": ")
	case SepSpace:
		e.buf.WriteByte(' ')
	}

	// Emit value.
	e.emitValue(kv.Value, depth)

	// Emit trailing comment if present.
	if kv.Comment != nil {
		e.buf.WriteByte(' ')
		e.emitCommentInline(kv.Comment)
	}

	e.buf.WriteByte('\n')
	e.currentLine++
}

// emitValue writes a value node to the output without a trailing newline.
func (e *emitter) emitValue(node Node, depth int) {
	switch n := node.(type) {
	case *StringNode:
		e.emitStringValue(n)
	case *IntegerNode:
		e.emitIntegerValue(n)
	case *FloatNode:
		e.emitFloatValue(n)
	case *BoolNode:
		e.emitBoolValue(n)
	case *DatetimeNode:
		e.emitDatetimeValue(n)
	case *MapNode:
		e.emitMapValue(n, depth)
	case *ArrayNode:
		e.emitArrayValue(n, depth)
	case *VariableNode:
		e.buf.WriteByte('$')
		e.buf.WriteString(n.Name)
	case *BlockStringNode:
		e.emitBlockStringValue(n)
	case *IncludeNode:
		e.buf.WriteString("include '")
		e.buf.WriteString(n.Path)
		e.buf.WriteByte('\'')
		if n.Digest != "" {
			e.buf.WriteString(" '")
			e.buf.WriteString(n.Digest)
			e.buf.WriteByte('\'')
		}
	}
}

// emitStringValue writes a string value, using quoting when necessary.
func (e *emitter) emitStringValue(n *StringNode) {
	if n.Raw != "" {
		// Use the raw representation if available (preserves original quoting).
		e.buf.WriteString(n.Raw)
		return
	}
	// Generate an appropriate representation.
	e.buf.WriteString(quoteString(n.Value))
}

// emitIntegerValue writes an integer value, using the raw representation
// if available to preserve size suffixes.
func (e *emitter) emitIntegerValue(n *IntegerNode) {
	if n.Raw != "" {
		e.buf.WriteString(n.Raw)
		return
	}
	e.buf.WriteString(strconv.FormatInt(n.Value, 10))
}

// emitFloatValue writes a float value.
func (e *emitter) emitFloatValue(n *FloatNode) {
	if n.Raw != "" {
		e.buf.WriteString(n.Raw)
		return
	}
	e.buf.WriteString(formatFloat(n.Value))
}

// emitBoolValue writes a boolean value, using the raw representation
// if available to preserve the original literal (true/yes/on etc.).
func (e *emitter) emitBoolValue(n *BoolNode) {
	if n.Raw != "" {
		e.buf.WriteString(n.Raw)
		return
	}
	if n.Value {
		e.buf.WriteString("true")
	} else {
		e.buf.WriteString("false")
	}
}

// emitDatetimeValue writes a datetime value.
func (e *emitter) emitDatetimeValue(n *DatetimeNode) {
	if n.Raw != "" {
		e.buf.WriteString(n.Raw)
		return
	}
	e.buf.WriteString(n.Value.UTC().Format("2006-01-02T15:04:05Z"))
}

// emitMapValue writes a map block { ... }.
func (e *emitter) emitMapValue(m *MapNode, depth int) {
	e.buf.WriteByte('{')
	e.buf.WriteByte('\n')
	e.currentLine++

	e.emitMapItems(m.Items, depth+1)

	// Emit closing brace with indentation.
	// Find the closing brace position: it should be at the same indent
	// as the opening key. Estimate from the map's items.
	closingIndent := depth * 2
	// Try to infer from the items: if we have items, the parent key's
	// column gives us a hint for the closing brace.
	if m.Pos.Column > 0 {
		closingIndent = m.Pos.Column - 2
		if closingIndent < 0 {
			closingIndent = 0
		}
	}
	e.emitIndent(closingIndent)
	e.buf.WriteByte('}')
}

// emitMapItems emits the items inside a map block.
func (e *emitter) emitMapItems(items []Node, depth int) {
	for _, item := range items {
		switch n := item.(type) {
		case *KeyValueNode:
			e.emitKeyValue(n, depth)
		case *CommentNode:
			e.emitStandaloneComment(n, depth)
		case *IncludeNode:
			e.emitInclude(n, depth)
		}
	}
}

// emitArrayValue writes an array [...].
func (e *emitter) emitArrayValue(a *ArrayNode, depth int) {
	e.buf.WriteByte('[')

	if len(a.Elements) == 0 {
		e.buf.WriteByte(']')
		return
	}

	// Determine if the array is inline (all elements on same line)
	// or multiline.
	firstLine := 0
	lastLine := 0
	for i, elem := range a.Elements {
		pos := elem.Position()
		if i == 0 {
			firstLine = pos.Line
		}
		lastLine = pos.Line
	}
	multiline := firstLine != lastLine || firstLine != a.Pos.Line

	if multiline {
		e.buf.WriteByte('\n')
		e.currentLine++
		for _, elem := range a.Elements {
			pos := elem.Position()
			e.emitBlankLines(pos.Line)
			// Check if this is a comment node (inside array).
			if cn, ok := elem.(*CommentNode); ok {
				e.emitIndent(cn.Pos.Column)
				e.emitCommentInline(cn)
				e.buf.WriteByte('\n')
				e.currentLine++
				continue
			}
			e.emitIndent(pos.Column)
			e.emitValue(elem, depth+1)
			e.buf.WriteByte('\n')
			e.currentLine++
		}
		// Closing bracket on its own line.
		e.emitIndent(a.Pos.Column)
		e.buf.WriteByte(']')
	} else {
		// Inline array.
		for i, elem := range a.Elements {
			if i > 0 {
				e.buf.WriteString(", ")
			}
			e.emitValue(elem, depth)
		}
		e.buf.WriteByte(']')
	}
}

// emitStandaloneComment writes a standalone comment (leading or between items).
func (e *emitter) emitStandaloneComment(c *CommentNode, depth int) {
	e.emitBlankLines(c.Pos.Line)
	e.emitIndent(c.Pos.Column)
	e.emitCommentInline(c)
	e.buf.WriteByte('\n')
	e.currentLine++
}

// emitCommentInline writes a comment without a trailing newline.
func (e *emitter) emitCommentInline(c *CommentNode) {
	switch c.Style {
	case CommentHash:
		e.buf.WriteByte('#')
		e.buf.WriteString(c.Text)
	case CommentSlash:
		e.buf.WriteString("//")
		e.buf.WriteString(c.Text)
	}
}

// emitInclude writes an include directive.
func (e *emitter) emitInclude(inc *IncludeNode, depth int) {
	e.emitBlankLines(inc.Pos.Line)
	e.emitIndent(inc.Pos.Column)
	e.buf.WriteString("include '")
	e.buf.WriteString(inc.Path)
	e.buf.WriteByte('\'')
	if inc.Digest != "" {
		e.buf.WriteString(" '")
		e.buf.WriteString(inc.Digest)
		e.buf.WriteByte('\'')
	}
	e.buf.WriteByte('\n')
	e.currentLine++
}

// emitBlockStringValue writes a block string value (...).
func (e *emitter) emitBlockStringValue(bs *BlockStringNode) {
	e.buf.WriteByte('(')
	e.buf.WriteByte('\n')
	e.buf.WriteString(bs.Value)
	e.buf.WriteByte('\n')
	e.buf.WriteByte(')')
	// Count newlines in the block string content for line tracking.
	e.currentLine += 2 + strings.Count(bs.Value, "\n")
}
