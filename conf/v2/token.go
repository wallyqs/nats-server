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

// Package v2 provides a configuration parser for NATS that builds an
// AST with full position information, comment preservation, and
// encoding/json-style Marshal/Unmarshal support while maintaining
// backwards compatibility with the v1 conf package.
package v2

import "fmt"

// ItemType represents the type of a lexer token. The constants match
// the v1 conf package's itemType values for compatibility, with the
// addition of ItemComment for comment tokens.
type ItemType int

const (
	// ItemError indicates a lexer error token.
	ItemError ItemType = iota
	// ItemNIL is used in the parser to indicate no type.
	ItemNIL
	// ItemEOF indicates the end of input.
	ItemEOF
	// ItemKey represents a configuration key.
	ItemKey
	// ItemText represents raw text content (e.g., comment body).
	ItemText
	// ItemString represents a string value.
	ItemString
	// ItemBool represents a boolean value.
	ItemBool
	// ItemInteger represents an integer value, possibly with size suffix.
	ItemInteger
	// ItemFloat represents a floating-point value.
	ItemFloat
	// ItemDatetime represents an ISO8601 Zulu datetime value.
	ItemDatetime
	// ItemArrayStart represents the opening '[' of an array.
	ItemArrayStart
	// ItemArrayEnd represents the closing ']' of an array.
	ItemArrayEnd
	// ItemMapStart represents the opening '{' of a map.
	ItemMapStart
	// ItemMapEnd represents the closing '}' of a map.
	ItemMapEnd
	// ItemCommentStart represents the beginning of a comment (v1 compat).
	ItemCommentStart
	// ItemVariable represents a variable reference ($name).
	ItemVariable
	// ItemInclude represents an include directive.
	ItemInclude
	// ItemComment represents a complete comment token (v2 addition).
	ItemComment
)

// String returns the human-readable name of an ItemType.
func (t ItemType) String() string {
	switch t {
	case ItemError:
		return "Error"
	case ItemNIL:
		return "NIL"
	case ItemEOF:
		return "EOF"
	case ItemKey:
		return "Key"
	case ItemText:
		return "Text"
	case ItemString:
		return "String"
	case ItemBool:
		return "Bool"
	case ItemInteger:
		return "Integer"
	case ItemFloat:
		return "Float"
	case ItemDatetime:
		return "DateTime"
	case ItemArrayStart:
		return "ArrayStart"
	case ItemArrayEnd:
		return "ArrayEnd"
	case ItemMapStart:
		return "MapStart"
	case ItemMapEnd:
		return "MapEnd"
	case ItemCommentStart:
		return "CommentStart"
	case ItemVariable:
		return "Variable"
	case ItemInclude:
		return "Include"
	case ItemComment:
		return "Comment"
	}
	panic(fmt.Sprintf("BUG: Unknown ItemType %d", t))
}

// KeySeparator represents the style of separator between a key and its value.
type KeySeparator int

const (
	// SepEquals represents the '=' key separator (e.g., foo = 2).
	SepEquals KeySeparator = iota
	// SepColon represents the ':' key separator (e.g., foo: 2).
	SepColon
	// SepSpace represents whitespace as the key separator (e.g., foo 2).
	SepSpace
)

// String returns the human-readable name of a KeySeparator.
func (s KeySeparator) String() string {
	switch s {
	case SepEquals:
		return "equals"
	case SepColon:
		return "colon"
	case SepSpace:
		return "space"
	}
	panic(fmt.Sprintf("BUG: Unknown KeySeparator %d", s))
}

// CommentStyle represents the style of a comment.
type CommentStyle int

const (
	// CommentHash represents a '#' style comment.
	CommentHash CommentStyle = iota
	// CommentSlash represents a '//' style comment.
	CommentSlash
)

// String returns the human-readable name of a CommentStyle.
func (s CommentStyle) String() string {
	switch s {
	case CommentHash:
		return "hash"
	case CommentSlash:
		return "slash"
	}
	panic(fmt.Sprintf("BUG: Unknown CommentStyle %d", s))
}
