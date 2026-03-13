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

// Lexer for the NATS configuration format. This is a v2 reimplementation
// that produces the same token stream as v1 (conf/lex.go) while also
// emitting comment tokens. Based on Rob Pike's lexer pattern.

package v2

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	eof               = 0
	mapStart          = '{'
	mapEnd            = '}'
	keySepEqual       = '='
	keySepColon       = ':'
	arrayStart        = '['
	arrayEnd          = ']'
	arrayValTerm      = ','
	mapValTerm        = ','
	commentHashStart  = '#'
	commentSlashStart = '/'
	dqStringStart     = '"'
	dqStringEnd       = '"'
	sqStringStart     = '\''
	sqStringEnd       = '\''
	optValTerm        = ';'
	topOptStart       = '{'
	topOptValTerm     = ','
	topOptTerm        = '}'
	blockStart        = '('
	blockEnd          = ')'
)

// item represents a single lexer token with type, value, and position.
type item struct {
	typ  ItemType
	val  string
	line int
	pos  int
}

// String returns a human-readable representation of a lexer item.
func (it item) String() string {
	return fmt.Sprintf("(%s, '%s', %d, %d)", it.typ.String(), it.val, it.line, it.pos)
}

// stateFn represents a state function in the lexer state machine.
type stateFn func(lx *lexer) stateFn

// lexer tokenizes NATS configuration format input using a channel-based
// state machine. It produces the same token stream as the v1 lexer
// while also emitting comment tokens.
type lexer struct {
	input string
	start int
	pos   int
	width int
	line  int
	state stateFn
	items chan item

	// A stack of state functions used to maintain context.
	stack []stateFn

	// Used for processing escapable substrings in double-quoted and raw strings.
	stringParts   []string
	stringStateFn stateFn

	// lstart is the start position of the current line.
	lstart int

	// ilstart is the start position of the line from the current item.
	ilstart int

	// lastKeySep records the key separator style for the most recently
	// consumed key-value separator.
	lastKeySep KeySeparator

	// lastCommentStyle records the comment style for the most recently
	// emitted comment token.
	lastCommentStyle CommentStyle

}

// lex creates a new lexer for the given input string.
func lex(input string) *lexer {
	lx := &lexer{
		input:       input,
		state:       lexTop,
		line:        1,
		items:       make(chan item, 10),
		stack:       make([]stateFn, 0, 10),
		stringParts: []string{},
	}
	return lx
}

// nextItem returns the next token from the lexer.
func (lx *lexer) nextItem() item {
	for {
		select {
		case it := <-lx.items:
			return it
		default:
			lx.state = lx.state(lx)
		}
	}
}

func (lx *lexer) push(state stateFn) {
	lx.stack = append(lx.stack, state)
}

func (lx *lexer) pop() stateFn {
	if len(lx.stack) == 0 {
		return lx.errorf("BUG in lexer: no states to pop.")
	}
	li := len(lx.stack) - 1
	last := lx.stack[li]
	lx.stack = lx.stack[0:li]
	return last
}

func (lx *lexer) emit(typ ItemType) {
	val := strings.Join(lx.stringParts, "") + lx.input[lx.start:lx.pos]
	// Position of item in line where it started.
	pos := lx.pos - lx.ilstart - len(val)
	lx.items <- item{typ, val, lx.line, pos}
	lx.start = lx.pos
	lx.ilstart = lx.lstart
}

func (lx *lexer) emitString() {
	var finalString string
	if len(lx.stringParts) > 0 {
		finalString = strings.Join(lx.stringParts, "") + lx.input[lx.start:lx.pos]
		lx.stringParts = []string{}
	} else {
		finalString = lx.input[lx.start:lx.pos]
	}
	// Position of string in line where it started.
	pos := lx.pos - lx.ilstart - len(finalString)
	lx.items <- item{ItemString, finalString, lx.line, pos}
	lx.start = lx.pos
	lx.ilstart = lx.lstart
}

// checkIntegerOverflow validates the integer value for int64 range.
// Returns true if the value is within range, false (and emits error) if overflow.
func (lx *lexer) checkIntegerOverflow() bool {
	val := lx.input[lx.start:lx.pos]

	// Extract the numeric part by stripping any size suffix.
	numStr := val
	for len(numStr) > 0 {
		last := numStr[len(numStr)-1]
		if (last >= '0' && last <= '9') || last == '-' {
			break
		}
		numStr = numStr[:len(numStr)-1]
	}

	if numStr != "" {
		_, err := strconv.ParseInt(numStr, 10, 64)
		if err != nil {
			if e, ok := err.(*strconv.NumError); ok && e.Err == strconv.ErrRange {
				lx.errorf("Value '%s' is outside of the integer range", val)
				return false
			}
		}
	}
	return true
}

func (lx *lexer) addCurrentStringPart(offset int) {
	lx.stringParts = append(lx.stringParts, lx.input[lx.start:lx.pos-offset])
	lx.start = lx.pos
}

func (lx *lexer) addStringPart(s string) stateFn {
	lx.stringParts = append(lx.stringParts, s)
	lx.start = lx.pos
	return lx.stringStateFn
}

func (lx *lexer) hasEscapedParts() bool {
	return len(lx.stringParts) > 0
}

func (lx *lexer) next() (r rune) {
	if lx.pos >= len(lx.input) {
		lx.width = 0
		return eof
	}

	if lx.input[lx.pos] == '\n' {
		lx.line++
		// Mark start position of current line.
		lx.lstart = lx.pos
	}

	r, lx.width = utf8.DecodeRuneInString(lx.input[lx.pos:])
	lx.pos += lx.width

	return r
}

// ignore skips over the pending input before this point.
func (lx *lexer) ignore() {
	lx.start = lx.pos
	lx.ilstart = lx.lstart
}

// backup steps back one rune. Can be called only once per call of next.
func (lx *lexer) backup() {
	lx.pos -= lx.width
	if lx.pos < len(lx.input) && lx.input[lx.pos] == '\n' {
		lx.line--
	}
}

// peek returns but does not consume the next rune in the input.
func (lx *lexer) peek() rune {
	r := lx.next()
	lx.backup()
	return r
}

// errorf stops all lexing by emitting an error and returning nil.
func (lx *lexer) errorf(format string, values ...any) stateFn {
	for i, value := range values {
		if v, ok := value.(rune); ok {
			values[i] = escapeSpecial(v)
		}
	}

	// Position of error in current line.
	pos := lx.pos - lx.lstart
	lx.items <- item{
		ItemError,
		fmt.Sprintf(format, values...),
		lx.line,
		pos,
	}
	return nil
}

// lexTop consumes elements at the top level of data structure.
func lexTop(lx *lexer) stateFn {
	r := lx.next()
	if unicode.IsSpace(r) {
		return lexSkip(lx, lexTop)
	}

	switch r {
	case topOptStart:
		lx.push(lexTop)
		return lexSkip(lx, lexBlockStart)
	case commentHashStart:
		lx.push(lexTop)
		return lexCommentStart
	case commentSlashStart:
		rn := lx.next()
		if rn == commentSlashStart {
			lx.push(lexTop)
			return lexCommentStart
		}
		lx.backup()
		fallthrough
	case eof:
		if lx.pos > lx.start {
			return lx.errorf("Unexpected EOF.")
		}
		lx.emit(ItemEOF)
		return nil
	}

	// At this point, the only valid item can be a key, so we back up
	// and let the key lexer do the rest.
	lx.backup()
	lx.push(lexTopValueEnd)
	return lexKeyStart
}

// lexTopValueEnd is entered whenever a top-level value has been consumed.
func lexTopValueEnd(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case r == commentHashStart:
		lx.push(lexTop)
		return lexCommentStart
	case r == commentSlashStart:
		rn := lx.next()
		if rn == commentSlashStart {
			lx.push(lexTop)
			return lexCommentStart
		}
		lx.backup()
		fallthrough
	case isWhitespace(r):
		return lexTopValueEnd
	case isNL(r) || r == eof || r == optValTerm || r == topOptValTerm || r == topOptTerm:
		lx.ignore()
		return lexTop
	}
	return lx.errorf("Expected a top-level value to end with a new line, "+
		"comment or EOF, but got '%v' instead.", r)
}

func lexBlockStart(lx *lexer) stateFn {
	r := lx.next()
	if unicode.IsSpace(r) {
		return lexSkip(lx, lexBlockStart)
	}

	switch r {
	case topOptStart:
		lx.push(lexBlockEnd)
		return lexSkip(lx, lexBlockStart)
	case topOptTerm:
		lx.ignore()
		return lx.pop()
	case commentHashStart:
		lx.push(lexBlockStart)
		return lexCommentStart
	case commentSlashStart:
		rn := lx.next()
		if rn == commentSlashStart {
			lx.push(lexBlockStart)
			return lexCommentStart
		}
		lx.backup()
		fallthrough
	case eof:
		if lx.pos > lx.start {
			return lx.errorf("Unexpected EOF.")
		}
		lx.emit(ItemEOF)
		return nil
	}

	lx.backup()
	lx.push(lexBlockValueEnd)
	return lexKeyStart
}

// lexBlockValueEnd is entered whenever a block-level value has been consumed.
func lexBlockValueEnd(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case r == commentHashStart:
		lx.push(lexBlockValueEnd)
		return lexCommentStart
	case r == commentSlashStart:
		rn := lx.next()
		if rn == commentSlashStart {
			lx.push(lexBlockValueEnd)
			return lexCommentStart
		}
		lx.backup()
		fallthrough
	case isWhitespace(r):
		return lexBlockValueEnd
	case isNL(r) || r == optValTerm || r == topOptValTerm:
		lx.ignore()
		return lexBlockStart
	case r == topOptTerm:
		lx.backup()
		return lexBlockEnd
	}
	return lx.errorf("Expected a block-level value to end with a new line, "+
		"comment or EOF, but got '%v' instead.", r)
}

// lexBlockEnd is entered whenever a block-level value has been consumed.
func lexBlockEnd(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case r == commentHashStart:
		lx.push(lexBlockStart)
		return lexCommentStart
	case r == commentSlashStart:
		rn := lx.next()
		if rn == commentSlashStart {
			lx.push(lexBlockStart)
			return lexCommentStart
		}
		lx.backup()
		fallthrough
	case isNL(r) || isWhitespace(r):
		return lexBlockEnd
	case r == optValTerm || r == topOptValTerm:
		lx.ignore()
		return lexBlockStart
	case r == topOptTerm:
		lx.ignore()
		return lx.pop()
	}
	return lx.errorf("Expected a block-level to end with a '}', but got '%v' instead.", r)
}

// lexKeyStart consumes a key name up until the first non-whitespace character.
func lexKeyStart(lx *lexer) stateFn {
	r := lx.peek()
	switch {
	case isKeySeparator(r):
		return lx.errorf("Unexpected key separator '%v'", r)
	case unicode.IsSpace(r):
		lx.next()
		return lexSkip(lx, lexKeyStart)
	case r == dqStringStart:
		lx.next()
		return lexSkip(lx, lexDubQuotedKey)
	case r == sqStringStart:
		lx.next()
		return lexSkip(lx, lexQuotedKey)
	}
	lx.ignore()
	lx.next()
	return lexKey
}

// lexDubQuotedKey consumes the text of a key between double quotes.
func lexDubQuotedKey(lx *lexer) stateFn {
	r := lx.peek()
	if r == dqStringEnd {
		lx.emit(ItemKey)
		lx.next()
		return lexSkip(lx, lexKeyEnd)
	} else if r == eof {
		if lx.pos > lx.start {
			return lx.errorf("Unexpected EOF.")
		}
		lx.emit(ItemEOF)
		return nil
	}
	lx.next()
	return lexDubQuotedKey
}

// lexQuotedKey consumes the text of a key between single quotes.
func lexQuotedKey(lx *lexer) stateFn {
	r := lx.peek()
	if r == sqStringEnd {
		lx.emit(ItemKey)
		lx.next()
		return lexSkip(lx, lexKeyEnd)
	} else if r == eof {
		if lx.pos > lx.start {
			return lx.errorf("Unexpected EOF.")
		}
		lx.emit(ItemEOF)
		return nil
	}
	lx.next()
	return lexQuotedKey
}

// keyCheckKeyword will check for reserved keywords as the key value when the key is
// separated with a space.
func (lx *lexer) keyCheckKeyword(fallThrough, push stateFn) stateFn {
	key := strings.ToLower(lx.input[lx.start:lx.pos])
	switch key {
	case "include":
		lx.ignore()
		if push != nil {
			lx.push(push)
		}
		return lexIncludeStart
	}
	lx.emit(ItemKey)
	return fallThrough
}

// lexIncludeStart will consume the whitespace til the start of the value.
func lexIncludeStart(lx *lexer) stateFn {
	r := lx.next()
	if isWhitespace(r) {
		return lexSkip(lx, lexIncludeStart)
	}
	lx.backup()
	return lexInclude
}

// lexIncludeQuotedString consumes the inner contents of a quoted include path.
func lexIncludeQuotedString(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case r == sqStringEnd:
		lx.backup()
		lx.emit(ItemInclude)
		lx.next()
		lx.ignore()
		return lexIncludeEnd
	case r == eof:
		return lx.errorf("Unexpected EOF in quoted include")
	}
	return lexIncludeQuotedString
}

// lexIncludeDubQuotedString consumes the inner contents of a double-quoted include path.
func lexIncludeDubQuotedString(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case r == dqStringEnd:
		lx.backup()
		lx.emit(ItemInclude)
		lx.next()
		lx.ignore()
		return lexIncludeEnd
	case r == eof:
		return lx.errorf("Unexpected EOF in double quoted include")
	}
	return lexIncludeDubQuotedString
}

// lexIncludeString consumes the inner contents of a raw include path.
func lexIncludeString(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case isNL(r) || r == eof || r == optValTerm || r == mapEnd:
		lx.backup()
		lx.emit(ItemInclude)
		return lx.pop()
	case isWhitespace(r):
		lx.backup()
		lx.emit(ItemInclude)
		return lexIncludeEnd
	case r == sqStringEnd:
		lx.backup()
		lx.emit(ItemInclude)
		lx.next()
		lx.ignore()
		return lexIncludeEnd
	}
	return lexIncludeString
}

// lexIncludeEnd is entered after the include path has been lexed. It looks
// for an optional quoted digest string. If a quote character is found, it
// lexes the digest and emits ItemIncludeDigest. Otherwise (newline, EOF,
// semicolon, closing brace), it backs up and pops — no digest, backwards
// compatible.
func lexIncludeEnd(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case isWhitespace(r):
		lx.ignore()
		return lexIncludeEnd
	case r == sqStringStart:
		lx.ignore()
		return lexIncludeDigestQuotedString
	case r == dqStringStart:
		lx.ignore()
		return lexIncludeDigestDubQuotedString
	case isNL(r) || r == eof || r == optValTerm || r == mapEnd:
		lx.backup()
		return lx.pop()
	}
	// Any other character: not a digest, treat as end of include.
	lx.backup()
	return lx.pop()
}

// lexIncludeDigestQuotedString consumes a single-quoted digest string
// and emits ItemIncludeDigest.
func lexIncludeDigestQuotedString(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case r == sqStringEnd:
		lx.backup()
		lx.emit(ItemIncludeDigest)
		lx.next()
		lx.ignore()
		return lx.pop()
	case r == eof:
		return lx.errorf("Unexpected EOF in include digest")
	}
	return lexIncludeDigestQuotedString
}

// lexIncludeDigestDubQuotedString consumes a double-quoted digest string
// and emits ItemIncludeDigest.
func lexIncludeDigestDubQuotedString(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case r == dqStringEnd:
		lx.backup()
		lx.emit(ItemIncludeDigest)
		lx.next()
		lx.ignore()
		return lx.pop()
	case r == eof:
		return lx.errorf("Unexpected EOF in include digest")
	}
	return lexIncludeDigestDubQuotedString
}

// lexInclude will consume the include value.
func lexInclude(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case r == sqStringStart:
		lx.ignore()
		return lexIncludeQuotedString
	case r == dqStringStart:
		lx.ignore()
		return lexIncludeDubQuotedString
	case r == arrayStart:
		return lx.errorf("Expected include value but found start of an array")
	case r == mapStart:
		return lx.errorf("Expected include value but found start of a map")
	case r == blockStart:
		return lx.errorf("Expected include value but found start of a block")
	case unicode.IsDigit(r), r == '-':
		return lx.errorf("Expected include value but found start of a number")
	case r == '\\':
		return lx.errorf("Expected include value but found escape sequence")
	case isNL(r):
		return lx.errorf("Expected include value but found new line")
	}
	lx.backup()
	return lexIncludeString
}

// lexKey consumes the text of a key.
func lexKey(lx *lexer) stateFn {
	r := lx.peek()
	if unicode.IsSpace(r) {
		return lx.keyCheckKeyword(lexKeyEnd, nil)
	} else if isKeySeparator(r) || r == eof {
		lx.emit(ItemKey)
		return lexKeyEnd
	}
	lx.next()
	return lexKey
}

// lexKeyEnd consumes the end of a key (up to the key separator).
func lexKeyEnd(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case unicode.IsSpace(r):
		return lexSkip(lx, lexKeyEnd)
	case r == keySepEqual:
		lx.lastKeySep = SepEquals
		return lexSkip(lx, lexValue)
	case r == keySepColon:
		lx.lastKeySep = SepColon
		return lexSkip(lx, lexValue)
	case r == eof:
		lx.emit(ItemEOF)
		return nil
	}
	// We start the value here (space separator)
	lx.lastKeySep = SepSpace
	lx.backup()
	return lexValue
}

// lexValue starts the consumption of a value anywhere a value is expected.
func lexValue(lx *lexer) stateFn {
	r := lx.next()
	if isWhitespace(r) {
		return lexSkip(lx, lexValue)
	}

	switch {
	case r == arrayStart:
		lx.ignore()
		lx.emit(ItemArrayStart)
		return lexArrayValue
	case r == mapStart:
		lx.ignore()
		lx.emit(ItemMapStart)
		return lexMapKeyStart
	case r == sqStringStart:
		lx.ignore()
		return lexQuotedString
	case r == dqStringStart:
		lx.ignore()
		lx.stringStateFn = lexDubQuotedString
		return lexDubQuotedString
	case r == '-':
		return lexNegNumberStart
	case r == blockStart:
		lx.ignore()
		return lexBlock
	case unicode.IsDigit(r):
		lx.backup()
		return lexNumberOrDateOrStringOrIPStart
	case r == '.':
		return lx.errorf("Floats must start with a digit")
	case isNL(r):
		return lx.errorf("Expected value but found new line")
	}
	lx.backup()
	lx.stringStateFn = lexString
	return lexString
}

// lexArrayValue consumes one value in an array.
func lexArrayValue(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case unicode.IsSpace(r):
		return lexSkip(lx, lexArrayValue)
	case r == commentHashStart:
		lx.push(lexArrayValue)
		return lexCommentStart
	case r == commentSlashStart:
		rn := lx.next()
		if rn == commentSlashStart {
			lx.push(lexArrayValue)
			return lexCommentStart
		}
		lx.backup()
		fallthrough
	case r == arrayValTerm:
		return lx.errorf("Unexpected array value terminator '%v'.", arrayValTerm)
	case r == arrayEnd:
		return lexArrayEnd
	}

	lx.backup()
	lx.push(lexArrayValueEnd)
	return lexValue
}

// lexArrayValueEnd consumes the cruft between values of an array.
func lexArrayValueEnd(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case isWhitespace(r):
		return lexSkip(lx, lexArrayValueEnd)
	case r == commentHashStart:
		lx.push(lexArrayValueEnd)
		return lexCommentStart
	case r == commentSlashStart:
		rn := lx.next()
		if rn == commentSlashStart {
			lx.push(lexArrayValueEnd)
			return lexCommentStart
		}
		lx.backup()
		fallthrough
	case r == arrayValTerm || isNL(r):
		return lexSkip(lx, lexArrayValue)
	case r == arrayEnd:
		return lexArrayEnd
	}
	return lx.errorf("Expected an array value terminator %q or an array "+
		"terminator %q, but got '%v' instead.", arrayValTerm, arrayEnd, r)
}

// lexArrayEnd finishes the lexing of an array.
func lexArrayEnd(lx *lexer) stateFn {
	lx.ignore()
	lx.emit(ItemArrayEnd)
	return lx.pop()
}

// lexMapKeyStart consumes a key name up until the first non-whitespace character.
func lexMapKeyStart(lx *lexer) stateFn {
	r := lx.peek()
	switch {
	case isKeySeparator(r):
		return lx.errorf("Unexpected key separator '%v'.", r)
	case r == arrayEnd:
		return lx.errorf("Unexpected array end '%v' processing map.", r)
	case unicode.IsSpace(r):
		lx.next()
		return lexSkip(lx, lexMapKeyStart)
	case r == mapEnd:
		lx.next()
		return lexSkip(lx, lexMapEnd)
	case r == commentHashStart:
		lx.next()
		lx.push(lexMapKeyStart)
		return lexCommentStart
	case r == commentSlashStart:
		lx.next()
		rn := lx.next()
		if rn == commentSlashStart {
			lx.push(lexMapKeyStart)
			return lexCommentStart
		}
		lx.backup()
	case r == sqStringStart:
		lx.next()
		return lexSkip(lx, lexMapQuotedKey)
	case r == dqStringStart:
		lx.next()
		return lexSkip(lx, lexMapDubQuotedKey)
	case r == eof:
		return lx.errorf("Unexpected EOF processing map.")
	}
	lx.ignore()
	lx.next()
	return lexMapKey
}

// lexMapQuotedKey consumes the text of a key between single quotes.
func lexMapQuotedKey(lx *lexer) stateFn {
	if r := lx.peek(); r == eof {
		return lx.errorf("Unexpected EOF processing quoted map key.")
	} else if r == sqStringEnd {
		lx.emit(ItemKey)
		lx.next()
		return lexSkip(lx, lexMapKeyEnd)
	}
	lx.next()
	return lexMapQuotedKey
}

// lexMapDubQuotedKey consumes the text of a key between double quotes.
func lexMapDubQuotedKey(lx *lexer) stateFn {
	if r := lx.peek(); r == eof {
		return lx.errorf("Unexpected EOF processing double quoted map key.")
	} else if r == dqStringEnd {
		lx.emit(ItemKey)
		lx.next()
		return lexSkip(lx, lexMapKeyEnd)
	}
	lx.next()
	return lexMapDubQuotedKey
}

// lexMapKey consumes the text of a key.
func lexMapKey(lx *lexer) stateFn {
	if r := lx.peek(); r == eof {
		return lx.errorf("Unexpected EOF processing map key.")
	} else if unicode.IsSpace(r) {
		return lx.keyCheckKeyword(lexMapKeyEnd, lexMapValueEnd)
	} else if isKeySeparator(r) {
		lx.emit(ItemKey)
		return lexMapKeyEnd
	}
	lx.next()
	return lexMapKey
}

// lexMapKeyEnd consumes the end of a key (up to the key separator).
func lexMapKeyEnd(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case unicode.IsSpace(r):
		return lexSkip(lx, lexMapKeyEnd)
	case r == keySepEqual:
		lx.lastKeySep = SepEquals
		return lexSkip(lx, lexMapValue)
	case r == keySepColon:
		lx.lastKeySep = SepColon
		return lexSkip(lx, lexMapValue)
	}
	// We start the value here (space separator)
	lx.lastKeySep = SepSpace
	lx.backup()
	return lexMapValue
}

// lexMapValue consumes one value in a map.
func lexMapValue(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case unicode.IsSpace(r):
		return lexSkip(lx, lexMapValue)
	case r == mapValTerm:
		return lx.errorf("Unexpected map value terminator %q.", mapValTerm)
	case r == mapEnd:
		return lexSkip(lx, lexMapEnd)
	}
	lx.backup()
	lx.push(lexMapValueEnd)
	return lexValue
}

// lexMapValueEnd consumes the cruft between values of a map.
func lexMapValueEnd(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case isWhitespace(r):
		return lexSkip(lx, lexMapValueEnd)
	case r == commentHashStart:
		lx.push(lexMapValueEnd)
		return lexCommentStart
	case r == commentSlashStart:
		rn := lx.next()
		if rn == commentSlashStart {
			lx.push(lexMapValueEnd)
			return lexCommentStart
		}
		lx.backup()
		fallthrough
	case r == optValTerm || r == mapValTerm || isNL(r):
		return lexSkip(lx, lexMapKeyStart)
	case r == mapEnd:
		return lexSkip(lx, lexMapEnd)
	}
	return lx.errorf("Expected a map value terminator %q or a map "+
		"terminator %q, but got '%v' instead.", mapValTerm, mapEnd, r)
}

// lexMapEnd finishes the lexing of a map.
func lexMapEnd(lx *lexer) stateFn {
	lx.ignore()
	lx.emit(ItemMapEnd)
	return lx.pop()
}

// isBool checks if the unquoted string was actually a boolean.
func (lx *lexer) isBool() bool {
	str := strings.ToLower(lx.input[lx.start:lx.pos])
	return str == "true" || str == "false" ||
		str == "on" || str == "off" ||
		str == "yes" || str == "no"
}

// isVariable checks if the unquoted string is a variable reference, starting with $.
func (lx *lexer) isVariable() bool {
	if lx.start >= len(lx.input) {
		return false
	}
	if lx.input[lx.start] == '$' {
		lx.start += 1
		return true
	}
	return false
}

// lexQuotedString consumes the inner contents of a single-quoted string.
func lexQuotedString(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case r == sqStringEnd:
		lx.backup()
		lx.emit(ItemString)
		lx.next()
		lx.ignore()
		return lx.pop()
	case r == eof:
		if lx.pos > lx.start {
			return lx.errorf("Unexpected EOF.")
		}
		lx.emit(ItemEOF)
		return nil
	}
	return lexQuotedString
}

// lexDubQuotedString consumes the inner contents of a double-quoted string.
func lexDubQuotedString(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case r == '\\':
		lx.addCurrentStringPart(1)
		return lexStringEscape
	case r == dqStringEnd:
		lx.backup()
		lx.emitString()
		lx.next()
		lx.ignore()
		return lx.pop()
	case r == eof:
		if lx.pos > lx.start {
			return lx.errorf("Unexpected EOF.")
		}
		lx.emit(ItemEOF)
		return nil
	}
	return lexDubQuotedString
}

// lexString consumes the inner contents of a raw string.
func lexString(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case r == '\\':
		lx.addCurrentStringPart(1)
		return lexStringEscape
	// Termination of non-quoted strings
	case isNL(r) || r == eof || r == optValTerm ||
		r == arrayValTerm || r == arrayEnd || r == mapEnd ||
		isWhitespace(r):

		lx.backup()
		if lx.hasEscapedParts() {
			lx.emitString()
		} else if lx.isBool() {
			lx.emit(ItemBool)
		} else if lx.isVariable() {
			lx.emit(ItemVariable)
		} else {
			lx.emitString()
		}
		return lx.pop()
	case r == sqStringEnd:
		lx.backup()
		lx.emitString()
		lx.next()
		lx.ignore()
		return lx.pop()
	}
	return lexString
}

// lexBlock consumes the inner contents as a string.
func lexBlock(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case r == blockEnd:
		lx.backup()
		lx.backup()

		// Looking for a ')' character on a line by itself, if the previous
		// character isn't a new line, then break so we keep processing the block.
		if lx.next() != '\n' {
			lx.next()
			break
		}
		lx.next()

		// Make sure the next character is a new line or an eof. We want a ')' on a
		// bare line by itself.
		switch lx.next() {
		case '\n', eof:
			lx.backup()
			lx.backup()
			lx.emit(ItemString)
			lx.next()
			lx.ignore()
			return lx.pop()
		}
		lx.backup()
	case r == eof:
		return lx.errorf("Unexpected EOF processing block.")
	}
	return lexBlock
}

// lexStringEscape consumes an escaped character.
func lexStringEscape(lx *lexer) stateFn {
	r := lx.next()
	switch r {
	case 'x':
		return lexStringBinary
	case 't':
		return lx.addStringPart("\t")
	case 'n':
		return lx.addStringPart("\n")
	case 'r':
		return lx.addStringPart("\r")
	case '"':
		return lx.addStringPart("\"")
	case '\\':
		return lx.addStringPart("\\")
	}
	return lx.errorf("Invalid escape character '%v'. Only the following "+
		"escape characters are allowed: \\xXX, \\t, \\n, \\r, \\\", \\\\.", r)
}

// lexStringBinary consumes two hexadecimal digits following '\x'.
func lexStringBinary(lx *lexer) stateFn {
	r := lx.next()
	if isNL(r) {
		return lx.errorf("Expected two hexadecimal digits after '\\x', but hit end of line")
	}
	r = lx.next()
	if isNL(r) {
		return lx.errorf("Expected two hexadecimal digits after '\\x', but hit end of line")
	}
	offset := lx.pos - 2
	byteString, err := hex.DecodeString(lx.input[offset:lx.pos])
	if err != nil {
		return lx.errorf("Expected two hexadecimal digits after '\\x', but got '%s'", lx.input[offset:lx.pos])
	}
	lx.addStringPart(string(byteString))
	return lx.stringStateFn
}

// lexNumberOrDateOrStringOrIPStart consumes either a (positive)
// integer, a float, a datetime, IP, or string that started with a number.
func lexNumberOrDateOrStringOrIPStart(lx *lexer) stateFn {
	r := lx.next()
	if !unicode.IsDigit(r) {
		if r == '.' {
			return lx.errorf("Floats must start with a digit, not '.'.")
		}
		return lx.errorf("Expected a digit but got '%v'.", r)
	}
	return lexNumberOrDateOrStringOrIP
}

// lexNumberOrDateOrStringOrIP consumes either a (positive) integer,
// float, datetime, IP or string without quotes that starts with a number.
func lexNumberOrDateOrStringOrIP(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case r == '-':
		if lx.pos-lx.start != 5 {
			return lx.errorf("All ISO8601 dates must be in full Zulu form.")
		}
		return lexDateAfterYear
	case unicode.IsDigit(r):
		return lexNumberOrDateOrStringOrIP
	case r == '.':
		// Assume float at first, but could be IP
		return lexFloatStart
	case isNumberSuffix(r):
		return lexConvenientNumber
	case !(isNL(r) || r == eof || r == mapEnd || r == optValTerm || r == mapValTerm || isWhitespace(r) || unicode.IsDigit(r)):
		// Treat it as a string value once we get a rune that is not a number.
		lx.stringStateFn = lexString
		return lexString
	}
	lx.backup()
	if !lx.checkIntegerOverflow() {
		return nil
	}
	lx.emit(ItemInteger)
	return lx.pop()
}

// lexConvenientNumber is when we have a suffix, e.g. 1k or 1Mb.
func lexConvenientNumber(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case r == 'b' || r == 'B' || r == 'i' || r == 'I':
		return lexConvenientNumber
	}
	lx.backup()
	if isNL(r) || r == eof || r == mapEnd || r == optValTerm || r == mapValTerm || isWhitespace(r) || unicode.IsDigit(r) {
		if !lx.checkIntegerOverflow() {
			return nil
		}
		lx.emit(ItemInteger)
		return lx.pop()
	}
	// This is not a number, so treat it as a string.
	lx.stringStateFn = lexString
	return lexString
}

// lexDateAfterYear consumes a full Zulu Datetime in ISO8601 format.
func lexDateAfterYear(lx *lexer) stateFn {
	formats := []rune{
		'0', '0', '-', '0', '0',
		'T',
		'0', '0', ':', '0', '0', ':', '0', '0',
		'Z',
	}
	for _, f := range formats {
		r := lx.next()
		if f == '0' {
			if !unicode.IsDigit(r) {
				return lx.errorf("Expected digit in ISO8601 datetime, "+
					"but found '%v' instead.", r)
			}
		} else if f != r {
			return lx.errorf("Expected '%v' in ISO8601 datetime, "+
				"but found '%v' instead.", f, r)
		}
	}
	lx.emit(ItemDatetime)
	return lx.pop()
}

// lexNegNumberStart consumes either an integer or a float.
func lexNegNumberStart(lx *lexer) stateFn {
	r := lx.next()
	if !unicode.IsDigit(r) {
		if r == '.' {
			return lx.errorf("Floats must start with a digit, not '.'.")
		}
		return lx.errorf("Expected a digit but got '%v'.", r)
	}
	return lexNegNumber
}

// lexNegNumber consumes a negative integer or a float after seeing the first digit.
func lexNegNumber(lx *lexer) stateFn {
	r := lx.next()
	switch {
	case unicode.IsDigit(r):
		return lexNegNumber
	case r == '.':
		return lexFloatStart
	case isNumberSuffix(r):
		return lexConvenientNumber
	}
	lx.backup()
	if !lx.checkIntegerOverflow() {
		return nil
	}
	lx.emit(ItemInteger)
	return lx.pop()
}

// lexFloatStart starts the consumption of digits of a float after a '.'.
func lexFloatStart(lx *lexer) stateFn {
	r := lx.next()
	if !unicode.IsDigit(r) {
		return lx.errorf("Floats must have a digit after the '.', but got "+
			"'%v' instead.", r)
	}
	return lexFloat
}

// lexFloat consumes the digits of a float after a '.'.
func lexFloat(lx *lexer) stateFn {
	r := lx.next()
	if unicode.IsDigit(r) {
		return lexFloat
	}

	// Not a digit, if its another '.', need to see if we falsely assumed a float.
	if r == '.' {
		return lexIPAddr
	}

	lx.backup()
	lx.emit(ItemFloat)
	return lx.pop()
}

// lexIPAddr consumes IP addrs, like 127.0.0.1:4222.
func lexIPAddr(lx *lexer) stateFn {
	r := lx.next()
	if unicode.IsDigit(r) || r == '.' || r == ':' || r == '-' {
		return lexIPAddr
	}
	lx.backup()
	lx.emit(ItemString)
	return lx.pop()
}

// lexCommentStart begins the lexing of a comment.
func lexCommentStart(lx *lexer) stateFn {
	// Detect comment style by looking at what was consumed.
	// For // comments, the consumed text includes "//".
	// For # comments, the consumed text includes "#".
	consumed := lx.input[lx.start:lx.pos]
	if strings.Contains(consumed, "//") {
		lx.lastCommentStyle = CommentSlash
	} else {
		lx.lastCommentStyle = CommentHash
	}
	lx.ignore()
	lx.emit(ItemCommentStart)
	return lexComment
}

// lexComment lexes an entire comment.
func lexComment(lx *lexer) stateFn {
	r := lx.peek()
	if isNL(r) || r == eof {
		lx.emit(ItemText)
		return lx.pop()
	}
	lx.next()
	return lexComment
}

// lexSkip ignores all slurped input and moves on to the next state.
func lexSkip(lx *lexer, nextState stateFn) stateFn {
	return func(lx *lexer) stateFn {
		lx.ignore()
		return nextState
	}
}

// isNumberSuffix tests if a rune is a number suffix character.
func isNumberSuffix(r rune) bool {
	return r == 'k' || r == 'K' || r == 'm' || r == 'M' || r == 'g' || r == 'G' || r == 't' || r == 'T' || r == 'p' || r == 'P' || r == 'e' || r == 'E'
}

// isKeySeparator tests for both key separators.
func isKeySeparator(r rune) bool {
	return r == keySepEqual || r == keySepColon
}

// isWhitespace returns true if r is a whitespace character (not newline).
func isWhitespace(r rune) bool {
	return r == '\t' || r == ' '
}

// isNL returns true if r is a newline character.
func isNL(r rune) bool {
	return r == '\n' || r == '\r'
}

// escapeSpecial converts special characters to their escape representation.
func escapeSpecial(c rune) string {
	switch c {
	case '\n':
		return "\\n"
	}
	return string(c)
}
