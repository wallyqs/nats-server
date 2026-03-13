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

// Marshal engine for the NATS configuration format. Converts Go structs
// to valid NATS config text using reflection and struct tags.

package v2

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Marshaler is the interface implemented by types that can marshal
// themselves into valid NATS configuration text. MarshalConfig returns
// the raw bytes to embed in the output for this field's value.
type Marshaler interface {
	MarshalConfig() ([]byte, error)
}

// marshalerType is cached for interface checks.
var marshalerType = reflect.TypeOf((*Marshaler)(nil)).Elem()

// Marshal converts a Go struct to NATS configuration format text.
// The input must be a struct or a non-nil pointer to a struct.
// Fields are named using the conf struct tag (e.g., conf:"name,omitempty")
// or lowercased Go field name if untagged. Fields tagged conf:"-" are
// skipped. Nil pointer fields are skipped. omitempty skips zero-value
// fields. All output uses ": " as the key-value separator.
//
// Supported types: string, bool, all integer types, float64, float32,
// time.Duration (as Go duration string), time.Time (as ISO8601 Zulu),
// slices, maps, nested structs, pointers, and types implementing the
// Marshaler interface. Unsupported types (chan, func, complex) return
// a descriptive error.
func Marshal(v any) ([]byte, error) {
	return marshalWithOptions(v, "", "")
}

// MarshalIndent is like Marshal but applies indentation to the output.
// Each nested level is indented by the indent string, and each line
// is prefixed with the prefix string.
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	return marshalWithOptions(v, prefix, indent)
}

// marshalWithOptions is the internal marshal implementation supporting
// both Marshal and MarshalIndent.
func marshalWithOptions(v any, prefix, indent string) ([]byte, error) {
	if v == nil {
		return nil, fmt.Errorf("marshal: cannot marshal nil value")
	}

	rv := reflect.ValueOf(v)

	// Dereference pointer.
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, fmt.Errorf("marshal: cannot marshal nil pointer")
		}
		rv = rv.Elem()
	}

	if rv.Kind() != reflect.Struct {
		return nil, fmt.Errorf("marshal: expected struct, got %s", rv.Kind())
	}

	var buf bytes.Buffer
	enc := &encoder{
		buf:    &buf,
		prefix: prefix,
		indent: indent,
		depth:  0,
	}

	if err := enc.marshalStruct(rv); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

// encoder holds state for marshaling a struct to NATS config text.
type encoder struct {
	buf    *bytes.Buffer
	prefix string
	indent string
	depth  int
}

// writePrefix writes the current line prefix (prefix + indent*depth).
func (e *encoder) writePrefix() {
	if e.prefix != "" {
		e.buf.WriteString(e.prefix)
	}
	for i := 0; i < e.depth; i++ {
		e.buf.WriteString(e.indent)
	}
}

// marshalStruct marshals all exported fields of a struct value.
func (e *encoder) marshalStruct(rv reflect.Value) error {
	rt := rv.Type()
	return e.marshalStructFields(rv, rt, nil)
}

// marshalStructFields recursively marshals struct fields, handling
// embedded structs by promoting their fields.
func (e *encoder) marshalStructFields(rv reflect.Value, rt reflect.Type, parentIndex []int) error {
	for i := 0; i < rt.NumField(); i++ {
		sf := rt.Field(i)

		// Handle embedded (anonymous) structs: promote fields.
		if sf.Anonymous {
			ft := sf.Type
			fv := rv.Field(i)
			if ft.Kind() == reflect.Pointer {
				if fv.IsNil() {
					continue
				}
				ft = ft.Elem()
				fv = fv.Elem()
			}
			if ft.Kind() == reflect.Struct {
				if err := e.marshalStructFields(fv, ft, append(parentIndex, i)); err != nil {
					return err
				}
				continue
			}
		}

		// Skip unexported fields.
		if !sf.IsExported() {
			continue
		}

		// Get config key name and options from tag.
		name, opts := parseTag(sf)
		if name == "-" {
			continue
		}

		fv := rv.Field(i)

		// Handle nil pointers: always skip.
		if fv.Kind() == reflect.Pointer && fv.IsNil() {
			continue
		}

		// Handle omitempty: skip zero-value fields.
		if opts.omitempty && isZeroValue(fv) {
			continue
		}

		// Dereference pointer for the actual value.
		actualVal := fv
		if actualVal.Kind() == reflect.Pointer {
			actualVal = actualVal.Elem()
		}

		// Check for unsupported types before attempting to marshal.
		if err := checkUnsupported(actualVal); err != nil {
			return err
		}

		// Check for Marshaler interface.
		if customBytes, ok, err := tryMarshalCustom(fv); err != nil {
			return err
		} else if ok {
			e.writePrefix()
			e.buf.WriteString(name)
			e.buf.WriteString(": ")
			e.buf.Write(customBytes)
			e.buf.WriteByte('\n')
			continue
		}

		// Marshal based on type.
		if err := e.marshalField(name, actualVal); err != nil {
			return err
		}
	}
	return nil
}

// tagOptions holds parsed struct tag options.
type tagOptions struct {
	omitempty bool
}

// parseTag parses the conf struct tag for a field, returning the config
// key name and options.
func parseTag(sf reflect.StructField) (string, tagOptions) {
	tag := sf.Tag.Get("conf")
	var opts tagOptions

	if tag == "" {
		return strings.ToLower(sf.Name), opts
	}

	parts := strings.Split(tag, ",")
	name := parts[0]

	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			opts.omitempty = true
		}
	}

	if name == "" {
		return strings.ToLower(sf.Name), opts
	}

	return name, opts
}

// isZeroValue returns true if the reflect.Value is the zero value for its type.
func isZeroValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Slice, reflect.Map:
		return v.IsNil()
	case reflect.Pointer, reflect.Interface:
		return v.IsNil()
	}
	return false
}

// checkUnsupported returns an error for unsupported Go types.
func checkUnsupported(v reflect.Value) error {
	switch v.Kind() {
	case reflect.Chan:
		return fmt.Errorf("marshal: unsupported type chan (field type %s)", v.Type())
	case reflect.Func:
		return fmt.Errorf("marshal: unsupported type func (field type %s)", v.Type())
	case reflect.Complex64, reflect.Complex128:
		return fmt.Errorf("marshal: unsupported type complex (field type %s)", v.Type())
	}
	return nil
}

// tryMarshalCustom checks if a value implements the Marshaler interface
// and calls MarshalConfig if so. Returns (bytes, true, nil) if custom
// marshaling was used, (nil, false, nil) if not, or (nil, false, err)
// on error.
func tryMarshalCustom(v reflect.Value) ([]byte, bool, error) {
	// Check the value itself.
	if v.Type().Implements(marshalerType) {
		if v.CanInterface() {
			b, err := v.Interface().(Marshaler).MarshalConfig()
			return b, true, err
		}
	}

	// Check the pointer to the value.
	if v.CanAddr() {
		pv := v.Addr()
		if pv.Type().Implements(marshalerType) {
			b, err := pv.Interface().(Marshaler).MarshalConfig()
			return b, true, err
		}
	}

	// For pointer values, check the element.
	if v.Kind() == reflect.Pointer && !v.IsNil() {
		elem := v.Elem()
		// Check pointer-to-element.
		if v.Type().Implements(marshalerType) {
			b, err := v.Interface().(Marshaler).MarshalConfig()
			return b, true, err
		}
		if elem.CanAddr() {
			pv := elem.Addr()
			if pv.Type().Implements(marshalerType) {
				b, err := pv.Interface().(Marshaler).MarshalConfig()
				return b, true, err
			}
		}
	}

	return nil, false, nil
}

// marshalField writes a key-value pair to the output buffer.
func (e *encoder) marshalField(name string, v reflect.Value) error {
	// Handle time.Duration.
	if v.Type() == reflect.TypeOf(time.Duration(0)) {
		e.writePrefix()
		e.buf.WriteString(name)
		e.buf.WriteString(": ")
		d := time.Duration(v.Int())
		// Duration strings like "5s", "2m30s" must always be quoted
		// because the NATS lexer would otherwise interpret the numeric
		// prefix with letter suffix as a size-suffixed integer.
		e.buf.WriteString(escapeString(d.String()))
		e.buf.WriteByte('\n')
		return nil
	}

	// Handle time.Time.
	if v.Type() == reflect.TypeOf(time.Time{}) {
		e.writePrefix()
		e.buf.WriteString(name)
		e.buf.WriteString(": ")
		t := v.Interface().(time.Time)
		e.buf.WriteString(t.UTC().Format("2006-01-02T15:04:05Z"))
		e.buf.WriteByte('\n')
		return nil
	}

	switch v.Kind() {
	case reflect.String:
		e.writePrefix()
		e.buf.WriteString(name)
		e.buf.WriteString(": ")
		e.buf.WriteString(quoteString(v.String()))
		e.buf.WriteByte('\n')

	case reflect.Bool:
		e.writePrefix()
		e.buf.WriteString(name)
		e.buf.WriteString(": ")
		if v.Bool() {
			e.buf.WriteString("true")
		} else {
			e.buf.WriteString("false")
		}
		e.buf.WriteByte('\n')

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		e.writePrefix()
		e.buf.WriteString(name)
		e.buf.WriteString(": ")
		e.buf.WriteString(strconv.FormatInt(v.Int(), 10))
		e.buf.WriteByte('\n')

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		e.writePrefix()
		e.buf.WriteString(name)
		e.buf.WriteString(": ")
		e.buf.WriteString(strconv.FormatUint(v.Uint(), 10))
		e.buf.WriteByte('\n')

	case reflect.Float32, reflect.Float64:
		e.writePrefix()
		e.buf.WriteString(name)
		e.buf.WriteString(": ")
		e.buf.WriteString(formatFloat(v.Float()))
		e.buf.WriteByte('\n')

	case reflect.Slice:
		return e.marshalSlice(name, v)

	case reflect.Map:
		return e.marshalMap(name, v)

	case reflect.Struct:
		return e.marshalNestedStruct(name, v)

	case reflect.Interface:
		if v.IsNil() {
			// Skip nil interface values.
			return nil
		}
		return e.marshalField(name, v.Elem())

	default:
		return fmt.Errorf("marshal: unsupported type %s for field %q", v.Type(), name)
	}

	return nil
}

// marshalSlice writes a slice as a NATS array [...].
func (e *encoder) marshalSlice(name string, v reflect.Value) error {
	e.writePrefix()
	e.buf.WriteString(name)
	e.buf.WriteString(": [")

	if v.Len() == 0 {
		e.buf.WriteString("]\n")
		return nil
	}

	e.buf.WriteByte('\n')
	e.depth++

	for i := 0; i < v.Len(); i++ {
		elem := v.Index(i)
		// Dereference pointer elements.
		if elem.Kind() == reflect.Pointer {
			if elem.IsNil() {
				continue
			}
			elem = elem.Elem()
		}

		// Check for unsupported element types.
		if err := checkUnsupported(elem); err != nil {
			return err
		}

		// Check for custom Marshaler on element.
		if customBytes, ok, err := tryMarshalCustom(v.Index(i)); err != nil {
			return err
		} else if ok {
			e.writePrefix()
			e.buf.Write(customBytes)
			e.buf.WriteByte('\n')
			continue
		}

		switch elem.Kind() {
		case reflect.Struct:
			// Handle time.Time.
			if elem.Type() == reflect.TypeOf(time.Time{}) {
				e.writePrefix()
				t := elem.Interface().(time.Time)
				e.buf.WriteString(t.UTC().Format("2006-01-02T15:04:05Z"))
				e.buf.WriteByte('\n')
			} else {
				// Struct element: emit as inline map block.
				e.writePrefix()
				e.buf.WriteString("{\n")
				e.depth++
				if err := e.marshalStruct(elem); err != nil {
					return err
				}
				e.depth--
				e.writePrefix()
				e.buf.WriteString("}\n")
			}
		case reflect.Map:
			e.writePrefix()
			e.buf.WriteString("{\n")
			e.depth++
			if err := e.marshalMapEntries(elem); err != nil {
				return err
			}
			e.depth--
			e.writePrefix()
			e.buf.WriteString("}\n")
		case reflect.String:
			e.writePrefix()
			e.buf.WriteString(quoteString(elem.String()))
			e.buf.WriteByte('\n')
		case reflect.Bool:
			e.writePrefix()
			if elem.Bool() {
				e.buf.WriteString("true")
			} else {
				e.buf.WriteString("false")
			}
			e.buf.WriteByte('\n')
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			// Check for time.Duration (underlying kind is int64).
			if elem.Type() == reflect.TypeOf(time.Duration(0)) {
				e.writePrefix()
				d := time.Duration(elem.Int())
				e.buf.WriteString(escapeString(d.String()))
				e.buf.WriteByte('\n')
				continue
			}
			e.writePrefix()
			e.buf.WriteString(strconv.FormatInt(elem.Int(), 10))
			e.buf.WriteByte('\n')
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			e.writePrefix()
			e.buf.WriteString(strconv.FormatUint(elem.Uint(), 10))
			e.buf.WriteByte('\n')
		case reflect.Float32, reflect.Float64:
			e.writePrefix()
			e.buf.WriteString(formatFloat(elem.Float()))
			e.buf.WriteByte('\n')
		case reflect.Interface:
			if elem.IsNil() {
				continue
			}
			return e.marshalSliceElement(elem.Elem())
		default:
			return fmt.Errorf("marshal: unsupported slice element type %s", elem.Type())
		}
	}

	e.depth--
	e.writePrefix()
	e.buf.WriteString("]\n")
	return nil
}

// marshalSliceElement marshals a single element in a slice (used for
// interface{} elements where the actual type is only known at runtime).
func (e *encoder) marshalSliceElement(v reflect.Value) error {
	switch v.Kind() {
	case reflect.String:
		e.writePrefix()
		e.buf.WriteString(quoteString(v.String()))
		e.buf.WriteByte('\n')
	case reflect.Bool:
		e.writePrefix()
		if v.Bool() {
			e.buf.WriteString("true")
		} else {
			e.buf.WriteString("false")
		}
		e.buf.WriteByte('\n')
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		e.writePrefix()
		e.buf.WriteString(strconv.FormatInt(v.Int(), 10))
		e.buf.WriteByte('\n')
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		e.writePrefix()
		e.buf.WriteString(strconv.FormatUint(v.Uint(), 10))
		e.buf.WriteByte('\n')
	case reflect.Float32, reflect.Float64:
		e.writePrefix()
		e.buf.WriteString(formatFloat(v.Float()))
		e.buf.WriteByte('\n')
	case reflect.Map:
		e.writePrefix()
		e.buf.WriteString("{\n")
		e.depth++
		if err := e.marshalMapEntries(v); err != nil {
			return err
		}
		e.depth--
		e.writePrefix()
		e.buf.WriteString("}\n")
	default:
		return fmt.Errorf("marshal: unsupported slice element type %s", v.Type())
	}
	return nil
}

// marshalMap writes a map as a NATS map block { key: value }.
func (e *encoder) marshalMap(name string, v reflect.Value) error {
	e.writePrefix()
	e.buf.WriteString(name)
	e.buf.WriteString(": {\n")
	e.depth++

	if err := e.marshalMapEntries(v); err != nil {
		return err
	}

	e.depth--
	e.writePrefix()
	e.buf.WriteString("}\n")
	return nil
}

// marshalMapEntries writes the entries of a map value.
func (e *encoder) marshalMapEntries(v reflect.Value) error {
	// Sort keys for deterministic output.
	keys := v.MapKeys()
	sort.Slice(keys, func(i, j int) bool {
		return keys[i].String() < keys[j].String()
	})

	for _, key := range keys {
		val := v.MapIndex(key)

		// Dereference interface values.
		if val.Kind() == reflect.Interface {
			val = val.Elem()
		}

		if err := e.marshalField(key.String(), val); err != nil {
			return err
		}
	}
	return nil
}

// marshalNestedStruct writes a nested struct as a NATS map block.
func (e *encoder) marshalNestedStruct(name string, v reflect.Value) error {
	e.writePrefix()
	e.buf.WriteString(name)
	e.buf.WriteString(": {\n")
	e.depth++

	if err := e.marshalStruct(v); err != nil {
		return err
	}

	e.depth--
	e.writePrefix()
	e.buf.WriteString("}\n")
	return nil
}

// quoteString returns a string value suitable for NATS config output.
// Strings that are safe (no spaces, no special characters, and won't
// be re-parsed as a different type) are returned unquoted. Otherwise,
// the string is double-quoted with proper escaping.
func quoteString(s string) string {
	if s == "" {
		return `""`
	}

	if needsQuoting(s) {
		return escapeString(s)
	}

	return s
}

// needsQuoting returns true if a string value requires quoting to
// be safely represented in NATS config format.
func needsQuoting(s string) bool {
	// Check for ambiguous bool strings (case-insensitive).
	lower := strings.ToLower(s)
	switch lower {
	case "true", "false", "yes", "no", "on", "off":
		return true
	}

	// Check for variable reference.
	if strings.HasPrefix(s, "$") {
		return true
	}

	// Check for numeric strings (would re-parse as int or float).
	if looksNumeric(s) {
		return true
	}

	// Check for special characters that need quoting.
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r', '#', '{', '}', '[', ']', '=', ':', '"', '\'', ',', ';':
			return true
		}
		// Non-printable characters need quoting.
		if !unicode.IsPrint(r) {
			return true
		}
	}

	// Check for // comment prefix.
	if strings.Contains(s, "//") {
		return true
	}

	return false
}

// looksNumeric returns true if the string would be parsed as a number.
func looksNumeric(s string) bool {
	if len(s) == 0 {
		return false
	}

	start := 0
	if s[0] == '-' || s[0] == '+' {
		start = 1
		if start >= len(s) {
			return false
		}
	}

	// Check if all remaining chars are digits (integer).
	allDigits := true
	hasDot := false
	hasE := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if c >= '0' && c <= '9' {
			continue
		}
		if c == '.' && !hasDot {
			hasDot = true
			allDigits = false
			continue
		}
		if (c == 'e' || c == 'E') && !hasE {
			hasE = true
			allDigits = false
			continue
		}
		allDigits = false
		break
	}

	if allDigits && start < len(s) {
		// Pure integer string.
		return true
	}

	// Try parsing as float.
	if hasDot || hasE {
		_, err := strconv.ParseFloat(s, 64)
		return err == nil
	}

	// Also check if it starts with digits (might get parsed as integer
	// with suffix or as an integer).
	if start < len(s) && s[start] >= '0' && s[start] <= '9' {
		_, err := strconv.ParseInt(s, 10, 64)
		return err == nil
	}

	return false
}

// escapeString returns a double-quoted string with proper escaping
// for NATS config format.
func escapeString(s string) string {
	var buf strings.Builder
	buf.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			buf.WriteString(`\"`)
		case '\\':
			buf.WriteString(`\\`)
		case '\n':
			buf.WriteString(`\n`)
		case '\r':
			buf.WriteString(`\r`)
		case '\t':
			buf.WriteString(`\t`)
		default:
			buf.WriteRune(r)
		}
	}
	buf.WriteByte('"')
	return buf.String()
}

// formatFloat formats a float64 value for NATS config output.
// Ensures the output always has a decimal point to distinguish
// from integers.
func formatFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	// Ensure there's a decimal point so it parses as float.
	if !strings.Contains(s, ".") {
		s += ".0"
	}
	return s
}
