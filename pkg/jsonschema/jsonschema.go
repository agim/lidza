// Package jsonschema derives a JSON Schema from a Go type at runtime, for
// app-defined MCP tools whose input is a Go struct. It follows the same
// conventions as the build-time generator in pkg/inspect: json tags,
// omitempty as optional, pointers as nullable, time.Time as date-time.
package jsonschema

import (
	"encoding/json"
	"reflect"
	"strings"
	"time"
)

var (
	timeType = reflect.TypeFor[time.Time]()
	rawType  = reflect.TypeFor[json.RawMessage]()
)

// Of returns the schema of v's type.
func Of(v any) map[string]any {
	if v == nil {
		return map[string]any{"type": "object"}
	}
	return of(reflect.TypeOf(v), 0)
}

func of(t reflect.Type, depth int) map[string]any {
	if depth > 16 {
		return map[string]any{}
	}
	switch t {
	case timeType:
		return map[string]any{"type": "string", "format": "date-time"}
	case rawType:
		return map[string]any{}
	}
	switch t.Kind() {
	case reflect.Pointer:
		return nullable(of(t.Elem(), depth+1))
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return map[string]any{"type": "string", "contentEncoding": "base64"}
		}
		return map[string]any{"type": "array", "items": of(t.Elem(), depth+1)}
	case reflect.Array:
		return map[string]any{"type": "array", "items": of(t.Elem(), depth+1)}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": of(t.Elem(), depth+1)}
	case reflect.Struct:
		props := map[string]any{}
		required := []string{}
		addFields(t, props, &required, depth)
		return map[string]any{"type": "object", "properties": props, "required": required}
	case reflect.Interface:
		return map[string]any{}
	}
	return map[string]any{}
}

func addFields(t reflect.Type, props map[string]any, required *[]string, depth int) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		name, opts, _ := strings.Cut(tag, ",")
		if name == "-" || (!f.IsExported() && !f.Anonymous) {
			continue
		}
		if f.Anonymous && name == "" {
			et := f.Type
			if et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct {
				addFields(et, props, required, depth+1)
				continue
			}
		}
		if name == "" {
			name = f.Name
		}
		props[name] = of(f.Type, depth+1)
		if !strings.Contains(opts, "omitempty") {
			*required = append(*required, name)
		}
	}
}

func nullable(s map[string]any) map[string]any {
	if t, ok := s["type"].(string); ok {
		out := map[string]any{}
		for k, v := range s {
			out[k] = v
		}
		out["type"] = []any{t, "null"}
		return out
	}
	return s
}
