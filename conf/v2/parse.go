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

// Parser for the NATS configuration format. Consumes lexer tokens and
// builds an AST with full position information, comment preservation,
// variable resolution, and include handling.

package v2

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const bcryptPrefix = "2a$"

// parser holds the state for parsing a NATS configuration into an AST.
type parser struct {
	lx *lexer

	// fp is the directory of the file being parsed, used for include resolution.
	fp string

	// contexts is the stack of container nodes (Document, MapNode, ArrayNode).
	contexts []Node

	// keys is the stack of pending key nodes waiting for their values.
	keys []*KeyNode

	// keyItems is the stack of pending key items for position tracking.
	keyItems []item

	// envVarReferences tracks environment variable references for cycle detection.
	envVarReferences map[string]bool

	// comments collects pending comment nodes to attach to the next key-value.
	pendingComments []*CommentNode

	// usedVarKeys tracks KeyValueNodes whose values were referenced as
	// variables during parsing. Used by the backwards-compatible pedantic API.
	usedVarKeys map[*KeyValueNode]bool

	// raw enables raw-mode parsing that preserves variable references and
	// include directives as AST nodes instead of resolving/expanding them.
	// Used for round-trip emission.
	raw bool
}

// ParseAST parses the given NATS configuration data and returns the AST root.
// The returned Document contains all parsed key-value pairs, comments,
// and nested structures. Variables are resolved with block scoping,
// environment variable fallback, and cycle detection.
func ParseAST(data string) (*Document, error) {
	return parseAST(data, "")
}

// ParseASTFile parses a NATS configuration file and returns the AST root.
// Include paths are resolved relative to the file's directory.
func ParseASTFile(fp string) (*Document, error) {
	data, err := os.ReadFile(fp)
	if err != nil {
		return nil, fmt.Errorf("error opening config file: %v", err)
	}
	return parseAST(string(data), fp)
}

// ParseASTRaw parses the given NATS configuration data in raw mode and
// returns the AST root. In raw mode, variable references are preserved
// as VariableNode instead of being resolved, and include directives are
// preserved as IncludeNode instead of being expanded. Raw text
// representations are stored on AST nodes for round-trip emission.
func ParseASTRaw(data string) (*Document, error) {
	return parseASTRaw(data, "")
}

// ParseASTRawFile parses a NATS configuration file in raw mode.
// In raw mode, include directives are preserved as IncludeNode
// instead of being expanded, and variables are preserved as VariableNode.
func ParseASTRawFile(fp string) (*Document, error) {
	data, err := os.ReadFile(fp)
	if err != nil {
		return nil, fmt.Errorf("error opening config file: %v", err)
	}
	return parseASTRaw(string(data), fp)
}

func parseAST(data, fp string) (*Document, error) {
	p := newASTParser(data, fp)
	return p.parse(fp)
}

func parseASTRaw(data, fp string) (*Document, error) {
	p := newASTParser(data, fp)
	p.raw = true
	doc, err := p.parse(fp)
	if err != nil {
		return nil, err
	}
	doc.Source = data
	return doc, nil
}

func parseASTEnv(data string, parent *parser) (*Document, error) {
	p := newASTParser(data, "")
	p.envVarReferences = parent.envVarReferences
	return p.parse("")
}

func newASTParser(data, fp string) *parser {
	return &parser{
		lx:               lex(data),
		fp:               filepath.Dir(fp),
		contexts:         make([]Node, 0, 4),
		keys:             make([]*KeyNode, 0, 4),
		keyItems:         make([]item, 0, 4),
		envVarReferences: make(map[string]bool),
		pendingComments:  make([]*CommentNode, 0),
		usedVarKeys:      make(map[*KeyValueNode]bool),
	}
}

func (p *parser) parse(fp string) (*Document, error) {
	doc := &Document{
		NodeBase: NodeBase{Pos: Position{Line: 1, Column: 0, File: fp}},
		Items:    make([]Node, 0),
	}
	p.pushContext(doc)

	var prevItem item
	for {
		it := p.next()
		if it.typ == ItemEOF {
			// Check for invalid config: key with no value.
			if prevItem.typ == ItemKey && prevItem.val != "}" {
				return nil, fmt.Errorf("config is invalid (%s:%d:%d)", fp, it.line, it.pos)
			}
			break
		}
		prevItem = it
		if err := p.processItem(it, fp); err != nil {
			return nil, err
		}
	}

	// Flush any remaining pending comments to the document.
	p.flushPendingComments()

	// Transfer used variable keys tracking to the document.
	doc.usedVarKeys = p.usedVarKeys

	return doc, nil
}

func (p *parser) next() item {
	return p.lx.nextItem()
}

func (p *parser) pushContext(ctx Node) {
	p.contexts = append(p.contexts, ctx)
}

func (p *parser) popContext() Node {
	if len(p.contexts) == 0 {
		panic("BUG in parser, context stack empty")
	}
	li := len(p.contexts) - 1
	last := p.contexts[li]
	p.contexts = p.contexts[:li]
	return last
}

func (p *parser) currentContext() Node {
	if len(p.contexts) == 0 {
		panic("BUG in parser, no current context")
	}
	return p.contexts[len(p.contexts)-1]
}

func (p *parser) pushKey(key *KeyNode) {
	p.keys = append(p.keys, key)
}

func (p *parser) popKey() *KeyNode {
	if len(p.keys) == 0 {
		panic("BUG in parser, keys stack empty")
	}
	li := len(p.keys) - 1
	last := p.keys[li]
	p.keys = p.keys[:li]
	return last
}

func (p *parser) pushKeyItem(it item) {
	p.keyItems = append(p.keyItems, it)
}

func (p *parser) popKeyItem() item {
	if len(p.keyItems) == 0 {
		panic("BUG in parser, key items stack empty")
	}
	li := len(p.keyItems) - 1
	last := p.keyItems[li]
	p.keyItems = p.keyItems[:li]
	return last
}

// flushPendingComments adds any accumulated pending comments to the current context.
func (p *parser) flushPendingComments() {
	for _, c := range p.pendingComments {
		p.addToContext(c)
	}
	p.pendingComments = p.pendingComments[:0]
}

// addToContext adds a node to the current context (Document, MapNode, or ArrayNode).
func (p *parser) addToContext(node Node) {
	ctx := p.currentContext()
	switch c := ctx.(type) {
	case *Document:
		c.Items = append(c.Items, node)
	case *MapNode:
		c.Items = append(c.Items, node)
	case *ArrayNode:
		c.Elements = append(c.Elements, node)
	}
}

// setValue sets a value for the current pending key in the current context.
func (p *parser) setValue(valueNode Node) {
	ctx := p.currentContext()

	switch c := ctx.(type) {
	case *ArrayNode:
		c.Elements = append(c.Elements, valueNode)
	case *Document, *MapNode:
		key := p.popKey()
		_ = p.popKeyItem()

		// Set the key separator from the lexer now that the separator
		// has been consumed. For included keys, the separator is
		// already set (sepKnown flag is true).
		if !key.sepKnown {
			key.Separator = keySeparatorFromLexer(p.lx)
		}

		kv := &KeyValueNode{
			NodeBase: NodeBase{Pos: key.Pos},
			Key:      key,
			Value:    valueNode,
		}

		// Attach any pending leading comments to the document before this KV.
		p.flushPendingComments()

		switch cc := c.(type) {
		case *Document:
			cc.Items = append(cc.Items, kv)
		case *MapNode:
			cc.Items = append(cc.Items, kv)
		}
	}
}

// keySeparatorFromLexer retrieves the last key separator recorded by the lexer.
func keySeparatorFromLexer(lx *lexer) KeySeparator {
	return lx.lastKeySep
}

func (p *parser) processItem(it item, fp string) error {
	switch it.typ {
	case ItemError:
		return fmt.Errorf("Parse error on line %d: '%s'", it.line, it.val)

	case ItemKey:
		pos := Position{Line: it.line, Column: it.pos, File: fp}
		key := &KeyNode{
			NodeBase: NodeBase{Pos: pos},
			Name:     it.val,
			// Separator will be set when the value is processed, since
			// the lexer records the separator after the key is emitted.
		}
		p.pushKey(key)
		p.pushKeyItem(it)

	case ItemMapStart:
		mapNode := &MapNode{
			NodeBase: NodeBase{Pos: Position{Line: it.line, Column: it.pos, File: fp}},
			Items:    make([]Node, 0),
		}
		p.pushContext(mapNode)

	case ItemMapEnd:
		mapNode := p.popContext().(*MapNode)
		p.setValue(mapNode)

	case ItemString:
		pos := Position{Line: it.line, Column: it.pos, File: fp}
		node := &StringNode{
			NodeBase: NodeBase{Pos: pos},
			Value:    it.val,
		}
		p.setValue(node)

	case ItemInteger:
		pos := Position{Line: it.line, Column: it.pos, File: fp}
		value, err := parseIntegerValue(it.val)
		if err != nil {
			return err
		}
		node := &IntegerNode{
			NodeBase: NodeBase{Pos: pos},
			Value:    value,
			Raw:      it.val,
		}
		p.setValue(node)

	case ItemFloat:
		pos := Position{Line: it.line, Column: it.pos, File: fp}
		num, err := strconv.ParseFloat(it.val, 64)
		if err != nil {
			if e, ok := err.(*strconv.NumError); ok && e.Err == strconv.ErrRange {
				return fmt.Errorf("float '%s' is out of the range", it.val)
			}
			return fmt.Errorf("expected float, but got '%s'", it.val)
		}
		if math.IsInf(num, 0) {
			return fmt.Errorf("float '%s' is out of the range", it.val)
		}
		node := &FloatNode{
			NodeBase: NodeBase{Pos: pos},
			Value:    num,
		}
		if p.raw {
			node.Raw = it.val
		}
		p.setValue(node)

	case ItemBool:
		pos := Position{Line: it.line, Column: it.pos, File: fp}
		var val bool
		switch strings.ToLower(it.val) {
		case "true", "yes", "on":
			val = true
		case "false", "no", "off":
			val = false
		default:
			return fmt.Errorf("expected boolean value, but got '%s'", it.val)
		}
		node := &BoolNode{
			NodeBase: NodeBase{Pos: pos},
			Value:    val,
		}
		if p.raw {
			node.Raw = it.val
		}
		p.setValue(node)

	case ItemDatetime:
		pos := Position{Line: it.line, Column: it.pos, File: fp}
		dt, err := time.Parse("2006-01-02T15:04:05Z", it.val)
		if err != nil {
			return fmt.Errorf("expected Zulu formatted DateTime, but got '%s'", it.val)
		}
		node := &DatetimeNode{
			NodeBase: NodeBase{Pos: pos},
			Value:    dt,
		}
		if p.raw {
			node.Raw = it.val
		}
		p.setValue(node)

	case ItemArrayStart:
		arr := &ArrayNode{
			NodeBase: NodeBase{Pos: Position{Line: it.line, Column: it.pos, File: fp}},
			Elements: make([]Node, 0),
		}
		p.pushContext(arr)

	case ItemArrayEnd:
		arr := p.popContext().(*ArrayNode)
		p.setValue(arr)

	case ItemVariable:
		pos := Position{Line: it.line, Column: it.pos, File: fp}
		if p.raw {
			// In raw mode, preserve variable reference as VariableNode.
			node := &VariableNode{
				NodeBase: NodeBase{Pos: pos},
				Name:     it.val,
			}
			p.setValue(node)
		} else {
			value, found, err := p.lookupVariable(it.val)
			if err != nil {
				return fmt.Errorf("variable reference for '%s' on line %d could not be parsed: %s",
					it.val, it.line, err)
			}
			if !found {
				return fmt.Errorf("variable reference for '%s' on line %d can not be found",
					it.val, it.line)
			}
			// Clone the resolved value node with the variable's position.
			resolved := cloneNodeWithPosition(value, pos)
			p.setValue(resolved)
		}

	case ItemInclude:
		if p.raw {
			// In raw mode, preserve include directive as IncludeNode.
			pos := Position{Line: it.line, Column: it.pos, File: fp}
			node := &IncludeNode{
				NodeBase: NodeBase{Pos: pos},
				Path:     it.val,
			}
			p.addToContext(node)
		} else {
			if err := p.processInclude(it, fp); err != nil {
				return err
			}
		}

	case ItemCommentStart:
		// v1-style comment: next token is the text.
		textIt := p.next()
		text := ""
		if textIt.typ == ItemText {
			text = textIt.val
		}
		style := p.lx.lastCommentStyle
		pos := Position{Line: it.line, Column: it.pos, File: fp}
		comment := &CommentNode{
			NodeBase: NodeBase{Pos: pos},
			Style:    style,
			Text:     text,
		}
		p.handleComment(comment, it)

	case ItemComment:
		// v2-style complete comment token.
		pos := Position{Line: it.line, Column: it.pos, File: fp}
		comment := &CommentNode{
			NodeBase: NodeBase{Pos: pos},
			Style:    CommentHash,
			Text:     it.val,
		}
		p.handleComment(comment, it)
	}

	return nil
}

// isTrailingComment checks if a comment item appears on the same line as
// the most recent value in the current context. This is determined by
// checking if the last added node is on the same line.
func (p *parser) isTrailingComment(commentItem item) bool {
	ctx := p.currentContext()
	switch c := ctx.(type) {
	case *Document:
		if len(c.Items) > 0 {
			last := c.Items[len(c.Items)-1]
			if kv, ok := last.(*KeyValueNode); ok {
				return kv.Pos.Line == commentItem.line
			}
		}
	case *MapNode:
		if len(c.Items) > 0 {
			last := c.Items[len(c.Items)-1]
			if kv, ok := last.(*KeyValueNode); ok {
				return kv.Pos.Line == commentItem.line
			}
		}
	case *ArrayNode:
		// In arrays, comments are standalone elements.
		return false
	}
	return false
}

// handleComment processes a comment node, determining whether it's a trailing
// comment, a comment inside an array, or a standalone/leading comment.
func (p *parser) handleComment(comment *CommentNode, commentItem item) {
	ctx := p.currentContext()
	// If we're in an array context, add the comment directly as an element.
	if _, ok := ctx.(*ArrayNode); ok {
		p.addToContext(comment)
		return
	}
	// Determine if this is a trailing comment (same line as previous value).
	if p.isTrailingComment(commentItem) {
		p.attachTrailingComment(comment)
	} else {
		// It's a leading or standalone comment.
		p.pendingComments = append(p.pendingComments, comment)
	}
}

// attachTrailingComment attaches a comment to the last key-value node in the current context.
func (p *parser) attachTrailingComment(comment *CommentNode) {
	ctx := p.currentContext()
	var items []Node
	switch c := ctx.(type) {
	case *Document:
		items = c.Items
	case *MapNode:
		items = c.Items
	default:
		// For arrays, just add as element.
		p.addToContext(comment)
		return
	}

	if len(items) > 0 {
		if kv, ok := items[len(items)-1].(*KeyValueNode); ok {
			kv.Comment = comment
			return
		}
	}
	// Fallback: add as standalone comment.
	p.addToContext(comment)
}

// processInclude handles an include directive by reading and parsing the included file.
func (p *parser) processInclude(it item, fp string) error {
	// If the include path is absolute, use it directly.
	// Otherwise, resolve relative to the including file's directory.
	includePath := it.val
	if !filepath.IsAbs(includePath) {
		includePath = filepath.Join(p.fp, includePath)
	}
	data, err := os.ReadFile(includePath)
	if err != nil {
		return fmt.Errorf("error parsing include file '%s', %v", it.val, err)
	}

	includeDoc, err := parseAST(string(data), includePath)
	if err != nil {
		return fmt.Errorf("error parsing include file '%s', %v", it.val, err)
	}

	// Merge the included document's items into the current context.
	for _, item := range includeDoc.Items {
		if kv, ok := item.(*KeyValueNode); ok {
			// For included key-values, we need to push the key and set the value
			// to maintain the same variable resolution context.
			kv.Key.sepKnown = true
			p.pushKey(kv.Key)
			p.pushKeyItem(itemFromKeyNode(kv.Key))
			p.setValue(kv.Value)
		} else {
			// Comments and other nodes get added directly.
			p.addToContext(item)
		}
	}

	return nil
}

// itemFromKeyNode creates a lexer item from a KeyNode for the key items stack.
func itemFromKeyNode(key *KeyNode) item {
	return item{
		typ:  ItemKey,
		val:  key.Name,
		line: key.Pos.Line,
		pos:  key.Pos.Column,
	}
}

// lookupVariable resolves a variable reference using block scoping.
// It searches through the context stack from innermost to outermost,
// then falls back to environment variables with sub-parsing.
func (p *parser) lookupVariable(varReference string) (Node, bool, error) {
	// Special case for bcrypt password strings.
	if strings.HasPrefix(varReference, bcryptPrefix) {
		return &StringNode{Value: "$" + varReference}, true, nil
	}

	// Search through context stack (innermost to outermost).
	for i := len(p.contexts) - 1; i >= 0; i-- {
		ctx := p.contexts[i]
		if found, node := p.lookupInContext(ctx, varReference); found {
			return node, true, nil
		}
	}

	// Fall back to environment variables with cycle detection.
	if p.envVarReferences[varReference] {
		return nil, false, fmt.Errorf("variable reference cycle for '%s'", varReference)
	}
	p.envVarReferences[varReference] = true
	defer delete(p.envVarReferences, varReference)

	if vStr, ok := os.LookupEnv(varReference); ok {
		// Sub-parse the environment variable value.
		envData := fmt.Sprintf("__envkey__=%s", vStr)
		envDoc, err := parseASTEnv(envData, p)
		if err != nil {
			return nil, false, err
		}
		// Extract the value from the parsed env key-value.
		for _, item := range envDoc.Items {
			if kv, ok := item.(*KeyValueNode); ok && kv.Key.Name == "__envkey__" {
				return kv.Value, true, nil
			}
		}
	}

	return nil, false, nil
}

// lookupInContext searches for a variable name in a specific context node.
func (p *parser) lookupInContext(ctx Node, name string) (bool, Node) {
	var items []Node
	switch c := ctx.(type) {
	case *Document:
		items = c.Items
	case *MapNode:
		items = c.Items
	default:
		return false, nil
	}

	// Search through key-value pairs (last-wins for duplicates).
	var found Node
	var foundKV *KeyValueNode
	for _, item := range items {
		if kv, ok := item.(*KeyValueNode); ok {
			if kv.Key.Name == name {
				found = kv.Value
				foundKV = kv
			}
		}
	}

	if found != nil {
		// Track that this KV was used as a variable source.
		if foundKV != nil {
			p.usedVarKeys[foundKV] = true
		}
		return true, found
	}
	return false, nil
}

// cloneNodeWithPosition creates a copy of a node with a new position.
// This is used when a variable reference resolves to a value that needs
// to carry the reference's position, not the original definition's position.
func cloneNodeWithPosition(node Node, pos Position) Node {
	switch n := node.(type) {
	case *StringNode:
		return &StringNode{NodeBase: NodeBase{Pos: pos}, Value: n.Value}
	case *IntegerNode:
		return &IntegerNode{NodeBase: NodeBase{Pos: pos}, Value: n.Value, Raw: n.Raw}
	case *FloatNode:
		return &FloatNode{NodeBase: NodeBase{Pos: pos}, Value: n.Value}
	case *BoolNode:
		return &BoolNode{NodeBase: NodeBase{Pos: pos}, Value: n.Value}
	case *DatetimeNode:
		return &DatetimeNode{NodeBase: NodeBase{Pos: pos}, Value: n.Value}
	case *MapNode:
		return &MapNode{NodeBase: NodeBase{Pos: pos}, Items: n.Items}
	case *ArrayNode:
		return &ArrayNode{NodeBase: NodeBase{Pos: pos}, Elements: n.Elements}
	default:
		return node
	}
}

// parseIntegerValue parses an integer value string with optional size suffix.
// Matches v1 behavior for all supported suffixes.
func parseIntegerValue(val string) (int64, error) {
	lastDigit := 0
	for _, r := range val {
		if !unicode.IsDigit(r) && r != '-' {
			break
		}
		lastDigit++
	}
	numStr := val[:lastDigit]
	num, err := strconv.ParseInt(numStr, 10, 64)
	if err != nil {
		if e, ok := err.(*strconv.NumError); ok && e.Err == strconv.ErrRange {
			return 0, fmt.Errorf("integer '%s' is out of the range", val)
		}
		return 0, fmt.Errorf("expected integer, but got '%s'", val)
	}

	suffix := strings.ToLower(strings.TrimSpace(val[lastDigit:]))

	switch suffix {
	case "":
		return num, nil
	case "k":
		return num * 1000, nil
	case "kb", "ki", "kib":
		return num * 1024, nil
	case "m":
		return num * 1000 * 1000, nil
	case "mb", "mi", "mib":
		return num * 1024 * 1024, nil
	case "g":
		return num * 1000 * 1000 * 1000, nil
	case "gb", "gi", "gib":
		return num * 1024 * 1024 * 1024, nil
	case "t":
		return num * 1000 * 1000 * 1000 * 1000, nil
	case "tb", "ti", "tib":
		return num * 1024 * 1024 * 1024 * 1024, nil
	case "p":
		return num * 1000 * 1000 * 1000 * 1000 * 1000, nil
	case "pb", "pi", "pib":
		return num * 1024 * 1024 * 1024 * 1024 * 1024, nil
	case "e":
		return num * 1000 * 1000 * 1000 * 1000 * 1000 * 1000, nil
	case "eb", "ei", "eib":
		return num * 1024 * 1024 * 1024 * 1024 * 1024 * 1024, nil
	}

	return num, nil
}
