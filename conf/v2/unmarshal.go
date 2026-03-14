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

// Unmarshal engine for the NATS configuration format. Parses config data
// into map[string]any via the v2 parser in pedantic mode, then populates
// a target struct using reflection and struct tags following encoding/json
// patterns. All errors include source position info (file:line:column).

package v2

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

// ConfigError is a configuration error that includes source position
// information. It matches the error reporting pattern used by the
// NATS server's configErr type in server/errors.go.
type ConfigError struct {
	// Line is the 1-based line number where the error occurred.
	Line int
	// Column is the 0-based column offset within the line.
	Column int
	// File is the source file path. Empty when parsing from a string.
	File string
	// Reason is the human-readable error description.
	Reason string
}

// Source reports the location of a configuration error. When a file is
// known, the format is "file:line:col". When parsing from a string
// (no file), the format is "line:col".
func (e *ConfigError) Source() string {
	if e.File != "" {
		return fmt.Sprintf("%s:%d:%d", e.File, e.Line, e.Column)
	}
	return fmt.Sprintf("%d:%d", e.Line, e.Column)
}

// Error reports the location and reason from a configuration error.
// Format: "file:line:col: reason" or "line:col: reason".
func (e *ConfigError) Error() string {
	return fmt.Sprintf("%s: %s", e.Source(), e.Reason)
}

// newConfigError creates a ConfigError from position info.
func newConfigError(line, col int, file, reason string) *ConfigError {
	return &ConfigError{
		Line:   line,
		Column: col,
		File:   file,
		Reason: reason,
	}
}

// newUnknownFieldError creates a ConfigError for unknown fields in strict mode,
// matching the server's unknownConfigFieldErr pattern.
func newUnknownFieldError(line, col int, file, field string) *ConfigError {
	return &ConfigError{
		Line:   line,
		Column: col,
		File:   file,
		Reason: fmt.Sprintf("unknown field %q", field),
	}
}

// unwrapTokenValue extracts the underlying value and position info from a
// pedantic token wrapper. If the value is not a token, returns the value
// with zero position. This follows the pattern of server/opts.go unwrapValue.
func unwrapTokenValue(v any) (val any, line, col int, file string) {
	if tk, ok := v.(*token); ok {
		return tk.value, tk.Line(), tk.Position(), tk.SourceFile()
	}
	return v, 0, 0, ""
}

// wrapUnmarshalerErr wraps an error from a custom Unmarshaler with source
// position info if the error does not already include it. Errors that are
// already *ConfigError are returned as-is.
func wrapUnmarshalerErr(err error, line, col int, file string) error {
	if err == nil {
		return nil
	}
	// If already a ConfigError, return as-is.
	if _, ok := err.(*ConfigError); ok {
		return err
	}
	// Only wrap if we have meaningful position info.
	if line > 0 {
		return newConfigError(line, col, file, err.Error())
	}
	return err
}

// Unmarshaler is the interface implemented by types that can unmarshal
// a NATS configuration value. UnmarshalConfig receives the raw parsed
// value (string, int64, float64, bool, map[string]any, []any, or time.Time)
// and should populate the receiver accordingly. Errors are propagated
// to the caller.
type Unmarshaler interface {
	UnmarshalConfig(v any) error
}

// unmarshalerType is cached for interface checks.
var unmarshalerType = reflect.TypeOf((*Unmarshaler)(nil)).Elem()

// UnmarshalOptions controls optional behavior for the Unmarshal engine.
type UnmarshalOptions struct {
	// Strict enables strict mode where unknown config keys that do not
	// match any struct field produce an error. Default is false (permissive).
	Strict bool
}

// Unmarshal parses the NATS configuration data and populates the target
// struct v using reflection. The target must be a non-nil pointer to a
// struct. Struct fields are matched to config keys using the conf struct
// tag for custom naming (e.g., conf:"name,omitempty") or by lowercased
// field name for untagged fields. Key matching is case-insensitive.
//
// Supported types: string, bool, all integer types (with overflow checking),
// float64, float32, time.Duration, time.Time, slices, maps, nested structs,
// and pointer variants of these types. Types implementing the Unmarshaler
// interface receive the raw parsed value for custom handling.
// Fields tagged conf:"-" are skipped. Unexported fields are silently ignored.
// Embedded (anonymous) structs have their fields promoted to the outer struct,
// matching encoding/json behavior.
//
// Unknown config keys are silently ignored in the default permissive mode.
// Use UnmarshalWith to enable strict mode.
func Unmarshal(data []byte, v any) error {
	return UnmarshalWith(data, v, nil)
}

// UnmarshalWith is like Unmarshal but accepts options to control behavior
// such as strict mode for unknown fields. If opts is nil, defaults are used.
func UnmarshalWith(data []byte, v any, opts *UnmarshalOptions) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("unmarshal requires a non-nil pointer to a struct")
	}
	rv = rv.Elem()
	if rv.Kind() != reflect.Struct {
		return fmt.Errorf("unmarshal requires a non-nil pointer to a struct, got pointer to %s", rv.Kind())
	}

	// Use pedantic mode (ParseWithChecks) to get token position info.
	m, err := ParseWithChecks(string(data))
	if err != nil {
		return fmt.Errorf("parse error: %w", err)
	}

	strict := opts != nil && opts.Strict
	return unmarshalMap(m, rv, strict)
}

// UnmarshalFile reads a NATS configuration file, parses it, and
// unmarshals its contents into the target struct v. The target must
// be a non-nil pointer to a struct. Include paths in the config file
// are resolved relative to the file's directory.
// Missing files return an *os.PathError-compatible error.
func UnmarshalFile(path string, v any) error {
	return UnmarshalFileWith(path, v, nil)
}

// UnmarshalFileWith is like UnmarshalFile but accepts options to control
// behavior such as strict mode for unknown fields.
func UnmarshalFileWith(path string, v any, opts *UnmarshalOptions) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("unmarshal requires a non-nil pointer to a struct")
	}
	rv = rv.Elem()
	if rv.Kind() != reflect.Struct {
		return fmt.Errorf("unmarshal requires a non-nil pointer to a struct, got pointer to %s", rv.Kind())
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		return err
	}

	// Use pedantic mode to get token position info including filename.
	m, err := parseCompat(string(data), absPath, true)
	if err != nil {
		return err
	}

	strict := opts != nil && opts.Strict
	return unmarshalMap(m, rv, strict)
}

// unmarshalMap populates the struct value rv from the parsed config map.
// Values may be wrapped in pedantic token structs; they are unwrapped to
// extract position info for error reporting.
func unmarshalMap(m map[string]any, rv reflect.Value, strict bool) error {
	fields := buildFieldIndex(rv.Type())

	for key, rawVal := range m {
		// Skip variable definitions that were referenced by $var syntax.
		// These are config-level variable assignments (e.g., monitoring_port = 8222)
		// consumed via $monitoring_port elsewhere. They are not real config fields
		// and must be silently skipped regardless of strict/permissive mode,
		// matching v1 behavior in server/opts.go (tk.IsUsedVariable() checks).
		if tk, ok := rawVal.(*token); ok && tk.IsUsedVariable() {
			continue
		}

		// Unwrap pedantic token to get the raw value and position info.
		val, line, col, file := unwrapTokenValue(rawVal)

		fi, ok := fields[strings.ToLower(key)]
		if !ok {
			if strict {
				return newUnknownFieldError(line, col, file, key)
			}
			// Permissive mode: unknown keys are silently ignored.
			continue
		}

		fv := fieldByIndex(rv, fi.index)
		if !fv.IsValid() || !fv.CanSet() {
			continue
		}

		if err := assignValue(fv, val, key, strict, line, col, file); err != nil {
			return err
		}
	}

	return nil
}

// fieldInfo holds metadata about a struct field for unmarshal key matching.
type fieldInfo struct {
	// index is the chain of field indices for nested embedded structs.
	index []int
}

// buildFieldIndex creates a case-insensitive map from config key names to
// struct field info. It handles struct tags, unexported fields, embedded
// structs (promoted fields), and skip tags.
func buildFieldIndex(t reflect.Type) map[string]*fieldInfo {
	fields := make(map[string]*fieldInfo)
	buildFieldIndexRecursive(t, nil, fields)
	return fields
}

// buildFieldIndexRecursive recursively indexes struct fields including
// promoted fields from embedded structs.
func buildFieldIndexRecursive(t reflect.Type, parentIndex []int, fields map[string]*fieldInfo) {
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		index := append(append([]int(nil), parentIndex...), i)

		// Skip unexported fields (unless they are embedded structs).
		if !sf.IsExported() && !sf.Anonymous {
			continue
		}

		// Handle embedded (anonymous) structs: promote their fields.
		if sf.Anonymous {
			ft := sf.Type
			if ft.Kind() == reflect.Pointer {
				ft = ft.Elem()
			}
			if ft.Kind() == reflect.Struct {
				buildFieldIndexRecursive(ft, index, fields)
				continue
			}
		}

		// Skip unexported non-anonymous fields.
		if !sf.IsExported() {
			continue
		}

		// Determine the config key name(s) for this field.
		// Aliases allow multiple config keys to map to the same struct field
		// (e.g., conf:"host|net" maps both "host" and "net").
		aliases := configFieldAliases(sf)
		if len(aliases) == 1 && aliases[0] == "-" {
			continue
		}

		fi := &fieldInfo{index: index}
		for _, alias := range aliases {
			fields[strings.ToLower(alias)] = fi
		}
	}
}

// configFieldAliases returns the list of config key aliases for a struct
// field. If the field has a conf tag with pipe-separated aliases (e.g.,
// conf:"host|net"), all aliases are returned. The first alias is the
// primary name used by Marshal. If there is no tag or the tag name is
// empty, the Go field name is returned as the single alias.
// A tag of "-" returns ["-"] to signal the field should be skipped.
func configFieldAliases(sf reflect.StructField) []string {
	tag := sf.Tag.Get("conf")
	if tag == "" {
		return []string{sf.Name}
	}

	// Parse the tag: "name1|name2,omitempty" -> "name1|name2"
	name := tag
	if idx := strings.Index(tag, ","); idx != -1 {
		name = tag[:idx]
	}

	if name == "" {
		return []string{sf.Name}
	}

	// Split on pipe to get aliases.
	return strings.Split(name, "|")
}

// fieldByIndex traverses the struct value to reach a field at the given
// multi-level index, allocating pointer embedded structs as needed.
func fieldByIndex(rv reflect.Value, index []int) reflect.Value {
	for _, i := range index {
		if rv.Kind() == reflect.Pointer {
			if rv.IsNil() {
				rv.Set(reflect.New(rv.Type().Elem()))
			}
			rv = rv.Elem()
		}
		rv = rv.Field(i)
	}
	return rv
}

// assignValue assigns a parsed config value to a struct field, handling
// type conversions, pointer allocation, overflow checking, custom
// unmarshalers, and complex types (slices, maps, duration, time).
// Position parameters (line, col, file) are used for error reporting.
func assignValue(fv reflect.Value, val any, key string, strict bool, line, col int, file string) error {
	// Check for custom Unmarshaler interface on the field's type.
	// For pointer types, defer Unmarshaler check to assignPointerValue
	// which handles allocation.
	if fv.Kind() != reflect.Pointer {
		if fv.CanAddr() {
			ptrVal := fv.Addr()
			if ptrVal.Type().Implements(unmarshalerType) {
				return wrapUnmarshalerErr(ptrVal.Interface().(Unmarshaler).UnmarshalConfig(val), line, col, file)
			}
		}
		if fv.Type().Implements(unmarshalerType) {
			if fv.CanInterface() {
				return wrapUnmarshalerErr(fv.Interface().(Unmarshaler).UnmarshalConfig(val), line, col, file)
			}
		}
	}

	// Handle pointer fields: allocate and assign through the pointer.
	if fv.Kind() == reflect.Pointer {
		return assignPointerValue(fv, val, key, strict, line, col, file)
	}

	// Handle time.Duration fields.
	if fv.Type() == reflect.TypeOf(time.Duration(0)) {
		return assignDuration(fv, val, key, line, col, file)
	}

	// Handle time.Time fields.
	if fv.Type() == reflect.TypeOf(time.Time{}) {
		return assignTime(fv, val, key, line, col, file)
	}

	// Handle slice fields.
	if fv.Kind() == reflect.Slice {
		return assignSlice(fv, val, key, strict, line, col, file)
	}

	// Handle map fields.
	if fv.Kind() == reflect.Map {
		return assignMap(fv, val, key, strict, line, col, file)
	}

	// Handle nested struct fields from map values.
	if fv.Kind() == reflect.Struct {
		m, ok := val.(map[string]any)
		if !ok {
			return newConfigError(line, col, file,
				fmt.Sprintf("cannot unmarshal %T into struct field %q (type %s)", val, key, fv.Type()))
		}
		return unmarshalMap(m, fv, strict)
	}

	// Handle interface{} / any fields.
	if fv.Kind() == reflect.Interface {
		fv.Set(reflect.ValueOf(val))
		return nil
	}

	return assignScalar(fv, val, key, line, col, file)
}

// assignPointerValue allocates and assigns through a pointer field.
func assignPointerValue(fv reflect.Value, val any, key string, strict bool, line, col int, file string) error {
	elemType := fv.Type().Elem()

	// Check if the pointer type implements Unmarshaler.
	if fv.Type().Implements(unmarshalerType) {
		if fv.IsNil() {
			fv.Set(reflect.New(elemType))
		}
		return wrapUnmarshalerErr(fv.Interface().(Unmarshaler).UnmarshalConfig(val), line, col, file)
	}
	if reflect.PointerTo(elemType).Implements(unmarshalerType) {
		ptr := reflect.New(elemType)
		if err := ptr.Interface().(Unmarshaler).UnmarshalConfig(val); err != nil {
			return wrapUnmarshalerErr(err, line, col, file)
		}
		fv.Set(ptr)
		return nil
	}

	// Handle time.Duration pointer.
	if elemType == reflect.TypeOf(time.Duration(0)) {
		ptr := reflect.New(elemType)
		if err := assignDuration(ptr.Elem(), val, key, line, col, file); err != nil {
			return err
		}
		fv.Set(ptr)
		return nil
	}

	// Handle time.Time pointer.
	if elemType == reflect.TypeOf(time.Time{}) {
		ptr := reflect.New(elemType)
		if err := assignTime(ptr.Elem(), val, key, line, col, file); err != nil {
			return err
		}
		fv.Set(ptr)
		return nil
	}

	// If the value is a map and the pointer target is a struct, handle nested struct.
	if elemType.Kind() == reflect.Struct {
		m, ok := val.(map[string]any)
		if !ok {
			return newConfigError(line, col, file,
				fmt.Sprintf("cannot unmarshal %T into field %q (type %s)", val, key, fv.Type()))
		}
		ptr := reflect.New(elemType)
		if err := unmarshalMap(m, ptr.Elem(), strict); err != nil {
			return err
		}
		fv.Set(ptr)
		return nil
	}

	// Handle pointer to slice.
	if elemType.Kind() == reflect.Slice {
		ptr := reflect.New(elemType)
		if err := assignSlice(ptr.Elem(), val, key, strict, line, col, file); err != nil {
			return err
		}
		fv.Set(ptr)
		return nil
	}

	// Handle pointer to map.
	if elemType.Kind() == reflect.Map {
		ptr := reflect.New(elemType)
		if err := assignMap(ptr.Elem(), val, key, strict, line, col, file); err != nil {
			return err
		}
		fv.Set(ptr)
		return nil
	}

	// For scalar pointer types, allocate and assign the scalar.
	ptr := reflect.New(elemType)
	if err := assignScalar(ptr.Elem(), val, key, line, col, file); err != nil {
		return err
	}
	fv.Set(ptr)
	return nil
}

// assignDuration assigns a value to a time.Duration field.
// String values are parsed via time.ParseDuration. Integer values
// are treated as seconds for backwards compatibility.
func assignDuration(fv reflect.Value, val any, key string, line, col int, file string) error {
	switch v := val.(type) {
	case string:
		d, err := time.ParseDuration(v)
		if err != nil {
			return newConfigError(line, col, file,
				fmt.Sprintf("invalid duration for field %q: %v", key, err))
		}
		fv.SetInt(int64(d))
		return nil
	case int64:
		// Treat integer values as seconds.
		fv.SetInt(int64(time.Duration(v) * time.Second))
		return nil
	default:
		return newConfigError(line, col, file,
			fmt.Sprintf("cannot unmarshal %T into field %q of type time.Duration", val, key))
	}
}

// assignTime assigns a value to a time.Time field.
// Values must be ISO8601 Zulu datetime strings or time.Time values
// already parsed by the config parser.
func assignTime(fv reflect.Value, val any, key string, line, col int, file string) error {
	switch v := val.(type) {
	case time.Time:
		fv.Set(reflect.ValueOf(v))
		return nil
	case string:
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return newConfigError(line, col, file,
				fmt.Sprintf("invalid datetime for field %q: %v", key, err))
		}
		fv.Set(reflect.ValueOf(t))
		return nil
	default:
		return newConfigError(line, col, file,
			fmt.Sprintf("cannot unmarshal %T into field %q of type time.Time", val, key))
	}
}

// assignSlice assigns a value to a slice field. Config arrays ([]any)
// are converted to typed slices. Scalar values are coerced to single-element
// slices to support shorthand config patterns (e.g., routes = "host:4222"
// as shorthand for routes = ["host:4222"]).
func assignSlice(fv reflect.Value, val any, key string, strict bool, line, col int, file string) error {
	elemType := fv.Type().Elem()

	switch arr := val.(type) {
	case []any:
		return assignSliceFromArray(fv, arr, elemType, key, strict, line, col, file)
	default:
		// Scalar-to-single-element-slice coercion.
		return assignSliceFromArray(fv, []any{val}, elemType, key, strict, line, col, file)
	}
}

// assignSliceFromArray creates and populates a typed slice from a []any array.
func assignSliceFromArray(fv reflect.Value, arr []any, elemType reflect.Type, key string, strict bool, line, col int, file string) error {
	slice := reflect.MakeSlice(fv.Type(), 0, len(arr))

	for i, elem := range arr {
		elemVal := reflect.New(elemType).Elem()
		elemKey := fmt.Sprintf("%s[%d]", key, i)

		// Check if the element type (or its pointer) implements Unmarshaler
		// before kind-based branching, so []customType works when
		// *customType implements Unmarshaler.
		if reflect.PointerTo(elemType).Implements(unmarshalerType) {
			ptrVal := reflect.New(elemType)
			if err := ptrVal.Interface().(Unmarshaler).UnmarshalConfig(elem); err != nil {
				return wrapUnmarshalerErr(err, line, col, file)
			}
			elemVal.Set(ptrVal.Elem())
		} else if elemType.Implements(unmarshalerType) {
			if err := elemVal.Interface().(Unmarshaler).UnmarshalConfig(elem); err != nil {
				return wrapUnmarshalerErr(err, line, col, file)
			}
		} else if elemType.Kind() == reflect.Struct {
			// Handle slice of structs: each element should be a map.
			// Check for special types first.
			if elemType == reflect.TypeOf(time.Time{}) {
				if err := assignTime(elemVal, elem, elemKey, line, col, file); err != nil {
					return err
				}
			} else {
				m, ok := elem.(map[string]any)
				if !ok {
					return newConfigError(line, col, file,
						fmt.Sprintf("cannot unmarshal %T into element of %s for key %q", elem, fv.Type(), key))
				}
				if err := unmarshalMap(m, elemVal, strict); err != nil {
					return err
				}
			}
		} else if elemType.Kind() == reflect.Pointer {
			// Handle slice of pointers.
			if err := assignPointerValue(elemVal, elem, elemKey, strict, line, col, file); err != nil {
				return err
			}
		} else if elemType.Kind() == reflect.Interface {
			// For []any / []interface{}, assign directly.
			elemVal.Set(reflect.ValueOf(elem))
		} else if elemType.Kind() == reflect.Map {
			if err := assignMap(elemVal, elem, elemKey, strict, line, col, file); err != nil {
				return err
			}
		} else if elemType.Kind() == reflect.Slice {
			if err := assignSlice(elemVal, elem, elemKey, strict, line, col, file); err != nil {
				return err
			}
		} else if elemType == reflect.TypeOf(time.Duration(0)) {
			// Handle []time.Duration: elements may be duration strings.
			if err := assignDuration(elemVal, elem, elemKey, line, col, file); err != nil {
				return err
			}
		} else {
			if err := assignScalar(elemVal, elem, elemKey, line, col, file); err != nil {
				return err
			}
		}

		slice = reflect.Append(slice, elemVal)
	}

	fv.Set(slice)
	return nil
}

// assignMap assigns a value to a map field. The config map (map[string]any)
// is converted to the target map type. Empty maps result in non-nil empty maps.
func assignMap(fv reflect.Value, val any, key string, strict bool, line, col int, file string) error {
	m, ok := val.(map[string]any)
	if !ok {
		return newConfigError(line, col, file,
			fmt.Sprintf("cannot unmarshal %T into field %q of type %s", val, key, fv.Type()))
	}

	mapType := fv.Type()
	keyType := mapType.Key()
	valType := mapType.Elem()

	// Only string keys are supported.
	if keyType.Kind() != reflect.String {
		return newConfigError(line, col, file,
			fmt.Sprintf("unsupported map key type %s for field %q; only string keys are supported", keyType, key))
	}

	newMap := reflect.MakeMapWithSize(mapType, len(m))

	for mk, rawMv := range m {
		// Unwrap pedantic token for map values.
		mv, mvLine, mvCol, mvFile := unwrapTokenValue(rawMv)

		mapKey := reflect.ValueOf(mk)
		mapVal := reflect.New(valType).Elem()

		elemKey := fmt.Sprintf("%s.%s", key, mk)

		if valType.Kind() == reflect.Interface {
			// map[string]any: assign directly.
			mapVal.Set(reflect.ValueOf(mv))
		} else if valType.Kind() == reflect.Struct {
			inner, ok := mv.(map[string]any)
			if !ok {
				return newConfigError(mvLine, mvCol, mvFile,
					fmt.Sprintf("cannot unmarshal %T into map value for key %q (type %s)", mv, elemKey, valType))
			}
			if err := unmarshalMap(inner, mapVal, strict); err != nil {
				return err
			}
		} else if valType.Kind() == reflect.Map {
			if err := assignMap(mapVal, mv, elemKey, strict, mvLine, mvCol, mvFile); err != nil {
				return err
			}
		} else {
			if err := assignScalar(mapVal, mv, elemKey, mvLine, mvCol, mvFile); err != nil {
				return err
			}
		}

		newMap.SetMapIndex(mapKey, mapVal)
	}

	fv.Set(newMap)
	return nil
}

// assignScalar assigns a scalar config value to a non-pointer, non-struct field.
func assignScalar(fv reflect.Value, val any, key string, line, col int, file string) error {
	switch fv.Kind() {
	case reflect.String:
		return assignString(fv, val, key, line, col, file)
	case reflect.Bool:
		return assignBool(fv, val, key, line, col, file)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return assignInt(fv, val, key, line, col, file)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return assignUint(fv, val, key, line, col, file)
	case reflect.Float32, reflect.Float64:
		return assignFloat(fv, val, key, line, col, file)
	default:
		return newConfigError(line, col, file,
			fmt.Sprintf("unsupported field type %s for key %q", fv.Type(), key))
	}
}

// assignString assigns a string config value to a string field.
func assignString(fv reflect.Value, val any, key string, line, col int, file string) error {
	s, ok := val.(string)
	if !ok {
		return newConfigError(line, col, file,
			fmt.Sprintf("cannot unmarshal %T into field %q of type string", val, key))
	}
	fv.SetString(s)
	return nil
}

// assignBool assigns a boolean config value to a bool field.
func assignBool(fv reflect.Value, val any, key string, line, col int, file string) error {
	b, ok := val.(bool)
	if !ok {
		return newConfigError(line, col, file,
			fmt.Sprintf("cannot unmarshal %T into field %q of type bool", val, key))
	}
	fv.SetBool(b)
	return nil
}

// assignInt assigns an integer config value to an int field with overflow checking.
func assignInt(fv reflect.Value, val any, key string, line, col int, file string) error {
	var i64 int64
	switch v := val.(type) {
	case int64:
		i64 = v
	default:
		return newConfigError(line, col, file,
			fmt.Sprintf("cannot unmarshal %T into field %q of type %s", val, key, fv.Type()))
	}

	// Check overflow for narrower types.
	switch fv.Kind() {
	case reflect.Int8:
		if i64 < math.MinInt8 || i64 > math.MaxInt8 {
			return newConfigError(line, col, file,
				fmt.Sprintf("value %d overflow for field %q of type int8", i64, key))
		}
	case reflect.Int16:
		if i64 < math.MinInt16 || i64 > math.MaxInt16 {
			return newConfigError(line, col, file,
				fmt.Sprintf("value %d overflow for field %q of type int16", i64, key))
		}
	case reflect.Int32:
		if i64 < math.MinInt32 || i64 > math.MaxInt32 {
			return newConfigError(line, col, file,
				fmt.Sprintf("value %d overflow for field %q of type int32", i64, key))
		}
	}

	fv.SetInt(i64)
	return nil
}

// assignUint assigns an integer config value to a uint field with overflow checking.
func assignUint(fv reflect.Value, val any, key string, line, col int, file string) error {
	var i64 int64
	switch v := val.(type) {
	case int64:
		i64 = v
	default:
		return newConfigError(line, col, file,
			fmt.Sprintf("cannot unmarshal %T into field %q of type %s", val, key, fv.Type()))
	}

	if i64 < 0 {
		return newConfigError(line, col, file,
			fmt.Sprintf("value %d overflow for field %q of type %s (negative value)", i64, key, fv.Type()))
	}

	u64 := uint64(i64)

	// Check overflow for narrower types.
	switch fv.Kind() {
	case reflect.Uint8:
		if u64 > math.MaxUint8 {
			return newConfigError(line, col, file,
				fmt.Sprintf("value %d overflow for field %q of type uint8", i64, key))
		}
	case reflect.Uint16:
		if u64 > math.MaxUint16 {
			return newConfigError(line, col, file,
				fmt.Sprintf("value %d overflow for field %q of type uint16", i64, key))
		}
	case reflect.Uint32:
		if u64 > math.MaxUint32 {
			return newConfigError(line, col, file,
				fmt.Sprintf("value %d overflow for field %q of type uint32", i64, key))
		}
	}

	fv.SetUint(u64)
	return nil
}

// assignFloat assigns a float or integer config value to a float field.
// int64 values are coerced to float64/float32 without error.
func assignFloat(fv reflect.Value, val any, key string, line, col int, file string) error {
	var f64 float64
	switch v := val.(type) {
	case float64:
		f64 = v
	case int64:
		// int64-to-float coercion: needed for timeout/rate fields.
		f64 = float64(v)
	default:
		return newConfigError(line, col, file,
			fmt.Sprintf("cannot unmarshal %T into field %q of type %s", val, key, fv.Type()))
	}

	// For float32, check for overflow.
	if fv.Kind() == reflect.Float32 {
		if f64 > math.MaxFloat32 || f64 < -math.MaxFloat32 {
			if !math.IsInf(f64, 0) && f64 != 0 {
				return newConfigError(line, col, file,
					fmt.Sprintf("value %g overflow for field %q of type float32", f64, key))
			}
		}
	}

	fv.SetFloat(f64)
	return nil
}
