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
// into map[string]any via the v2 parser, then populates a target struct
// using reflection and struct tags following encoding/json patterns.

package v2

import (
	"fmt"
	"math"
	"reflect"
	"strings"
)

// Unmarshal parses the NATS configuration data and populates the target
// struct v using reflection. The target must be a non-nil pointer to a
// struct. Struct fields are matched to config keys using the conf struct
// tag for custom naming (e.g., conf:"name,omitempty") or by lowercased
// field name for untagged fields. Key matching is case-insensitive.
//
// Supported types: string, bool, all integer types (with overflow checking),
// float64, float32, nested structs, and pointer variants of these types.
// Fields tagged conf:"-" are skipped. Unexported fields are silently ignored.
// Embedded (anonymous) structs have their fields promoted to the outer struct,
// matching encoding/json behavior.
func Unmarshal(data []byte, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("unmarshal requires a non-nil pointer to a struct")
	}
	rv = rv.Elem()
	if rv.Kind() != reflect.Struct {
		return fmt.Errorf("unmarshal requires a non-nil pointer to a struct, got pointer to %s", rv.Kind())
	}

	m, err := Parse(string(data))
	if err != nil {
		return fmt.Errorf("parse error: %w", err)
	}

	return unmarshalMap(m, rv)
}

// unmarshalMap populates the struct value rv from the parsed config map.
func unmarshalMap(m map[string]any, rv reflect.Value) error {
	fields := buildFieldIndex(rv.Type())

	for key, val := range m {
		fi, ok := fields[strings.ToLower(key)]
		if !ok {
			// Permissive mode: unknown keys are silently ignored.
			continue
		}

		fv := fieldByIndex(rv, fi.index)
		if !fv.IsValid() || !fv.CanSet() {
			continue
		}

		if err := assignValue(fv, val, key); err != nil {
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

		// Determine the config key name for this field.
		name := configFieldName(sf)
		if name == "-" {
			continue
		}

		fields[strings.ToLower(name)] = &fieldInfo{index: index}
	}
}

// configFieldName returns the config key name for a struct field.
// If the field has a conf tag, the tag name is used. Otherwise,
// the lowercased Go field name is used.
func configFieldName(sf reflect.StructField) string {
	tag := sf.Tag.Get("conf")
	if tag == "" {
		return sf.Name
	}

	// Parse the tag: "name,omitempty" -> name
	name := tag
	if idx := strings.Index(tag, ","); idx != -1 {
		name = tag[:idx]
	}

	if name == "" {
		return sf.Name
	}

	return name
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
// type conversions, pointer allocation, and overflow checking.
func assignValue(fv reflect.Value, val any, key string) error {
	// Handle pointer fields: allocate and assign through the pointer.
	if fv.Kind() == reflect.Pointer {
		return assignPointerValue(fv, val, key)
	}

	// Handle nested struct fields from map values.
	if fv.Kind() == reflect.Struct {
		m, ok := val.(map[string]any)
		if !ok {
			return fmt.Errorf("cannot unmarshal %T into struct field %q (type %s)", val, key, fv.Type())
		}
		return unmarshalMap(m, fv)
	}

	return assignScalar(fv, val, key)
}

// assignPointerValue allocates and assigns through a pointer field.
func assignPointerValue(fv reflect.Value, val any, key string) error {
	elemType := fv.Type().Elem()

	// If the value is a map and the pointer target is a struct, handle nested struct.
	if elemType.Kind() == reflect.Struct {
		m, ok := val.(map[string]any)
		if !ok {
			return fmt.Errorf("cannot unmarshal %T into field %q (type %s)", val, key, fv.Type())
		}
		ptr := reflect.New(elemType)
		if err := unmarshalMap(m, ptr.Elem()); err != nil {
			return err
		}
		fv.Set(ptr)
		return nil
	}

	// For scalar pointer types, allocate and assign the scalar.
	ptr := reflect.New(elemType)
	if err := assignScalar(ptr.Elem(), val, key); err != nil {
		return err
	}
	fv.Set(ptr)
	return nil
}

// assignScalar assigns a scalar config value to a non-pointer, non-struct field.
func assignScalar(fv reflect.Value, val any, key string) error {
	switch fv.Kind() {
	case reflect.String:
		return assignString(fv, val, key)
	case reflect.Bool:
		return assignBool(fv, val, key)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return assignInt(fv, val, key)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return assignUint(fv, val, key)
	case reflect.Float32, reflect.Float64:
		return assignFloat(fv, val, key)
	default:
		return fmt.Errorf("unsupported field type %s for key %q", fv.Type(), key)
	}
}

// assignString assigns a string config value to a string field.
func assignString(fv reflect.Value, val any, key string) error {
	s, ok := val.(string)
	if !ok {
		return fmt.Errorf("cannot unmarshal %T into field %q of type string", val, key)
	}
	fv.SetString(s)
	return nil
}

// assignBool assigns a boolean config value to a bool field.
func assignBool(fv reflect.Value, val any, key string) error {
	b, ok := val.(bool)
	if !ok {
		return fmt.Errorf("cannot unmarshal %T into field %q of type bool", val, key)
	}
	fv.SetBool(b)
	return nil
}

// assignInt assigns an integer config value to an int field with overflow checking.
func assignInt(fv reflect.Value, val any, key string) error {
	var i64 int64
	switch v := val.(type) {
	case int64:
		i64 = v
	default:
		return fmt.Errorf("cannot unmarshal %T into field %q of type %s", val, key, fv.Type())
	}

	// Check overflow for narrower types.
	switch fv.Kind() {
	case reflect.Int8:
		if i64 < math.MinInt8 || i64 > math.MaxInt8 {
			return fmt.Errorf("value %d overflow for field %q of type int8", i64, key)
		}
	case reflect.Int16:
		if i64 < math.MinInt16 || i64 > math.MaxInt16 {
			return fmt.Errorf("value %d overflow for field %q of type int16", i64, key)
		}
	case reflect.Int32:
		if i64 < math.MinInt32 || i64 > math.MaxInt32 {
			return fmt.Errorf("value %d overflow for field %q of type int32", i64, key)
		}
	}

	fv.SetInt(i64)
	return nil
}

// assignUint assigns an integer config value to a uint field with overflow checking.
func assignUint(fv reflect.Value, val any, key string) error {
	var i64 int64
	switch v := val.(type) {
	case int64:
		i64 = v
	default:
		return fmt.Errorf("cannot unmarshal %T into field %q of type %s", val, key, fv.Type())
	}

	if i64 < 0 {
		return fmt.Errorf("value %d overflow for field %q of type %s (negative value)", i64, key, fv.Type())
	}

	u64 := uint64(i64)

	// Check overflow for narrower types.
	switch fv.Kind() {
	case reflect.Uint8:
		if u64 > math.MaxUint8 {
			return fmt.Errorf("value %d overflow for field %q of type uint8", i64, key)
		}
	case reflect.Uint16:
		if u64 > math.MaxUint16 {
			return fmt.Errorf("value %d overflow for field %q of type uint16", i64, key)
		}
	case reflect.Uint32:
		if u64 > math.MaxUint32 {
			return fmt.Errorf("value %d overflow for field %q of type uint32", i64, key)
		}
	}

	fv.SetUint(u64)
	return nil
}

// assignFloat assigns a float or integer config value to a float field.
// int64 values are coerced to float64/float32 without error.
func assignFloat(fv reflect.Value, val any, key string) error {
	var f64 float64
	switch v := val.(type) {
	case float64:
		f64 = v
	case int64:
		// int64-to-float coercion: needed for timeout/rate fields.
		f64 = float64(v)
	default:
		return fmt.Errorf("cannot unmarshal %T into field %q of type %s", val, key, fv.Type())
	}

	// For float32, check for overflow.
	if fv.Kind() == reflect.Float32 {
		if f64 > math.MaxFloat32 || f64 < -math.MaxFloat32 {
			if !math.IsInf(f64, 0) && f64 != 0 {
				return fmt.Errorf("value %g overflow for field %q of type float32", f64, key)
			}
		}
	}

	fv.SetFloat(f64)
	return nil
}
