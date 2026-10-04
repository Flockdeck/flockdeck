// Package schemacheck validates a JSON value against the small subset of JSON
// Schema that docs/recording-line.schema.json uses, so that tests can check
// real recording lines against the published schema without a dependency.
//
// The subset: type, const, enum, pattern, minimum, minLength, minProperties, required,
// properties, additionalProperties and unevaluatedProperties (false only), $ref to #/$defs, items, allOf and if/then. Anything else in a schema is ignored,
// which is why the schema sticks to these.
package schemacheck

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
)

// Schema is a parsed schema.
type Schema map[string]any

// Load reads a schema file.
func Load(path string) (Schema, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Schema
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// Validate checks one JSON document and returns what is wrong with it.
func (s Schema) Validate(doc []byte) []string {
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return []string{"not JSON: " + err.Error()}
	}
	val := validator{root: map[string]any(s)}
	return val.check(map[string]any(s), v, "$")
}

type validator struct{ root map[string]any }

func (val validator) check(schema map[string]any, v any, path string) []string {
	var errs []string
	if ref, ok := schema["$ref"].(string); ok {
		name, found := strings.CutPrefix(ref, "#/$defs/")
		defs, _ := val.root["$defs"].(map[string]any)
		target, _ := defs[name].(map[string]any)
		if !found || target == nil {
			return []string{path + ": unresolved $ref " + ref}
		}
		return val.check(target, v, path)
	}
	add := func(format string, a ...any) { errs = append(errs, path+": "+fmt.Sprintf(format, a...)) }

	if t, ok := schema["type"].(string); ok && !isType(v, t) {
		add("want %s, got %T", t, v)
		return errs
	}
	if c, ok := schema["const"]; ok && !equal(c, v) {
		add("want %v, got %v", c, v)
	}
	if enum, ok := schema["enum"].([]any); ok {
		found := false
		for _, e := range enum {
			found = found || equal(e, v)
		}
		if !found {
			add("%v is not one of %v", v, enum)
		}
	}
	switch x := v.(type) {
	case string:
		if p, ok := schema["pattern"].(string); ok && !regexp.MustCompile(p).MatchString(x) {
			add("%q does not match %s", x, p)
		}
		if n, ok := schema["minLength"].(float64); ok && float64(len([]rune(x))) < n {
			add("shorter than %v", n)
		}
	case float64:
		if n, ok := schema["minimum"].(float64); ok && x < n {
			add("%v is below %v", x, n)
		}
	case []any:
		if items, ok := schema["items"].(map[string]any); ok {
			for i, e := range x {
				errs = append(errs, val.check(items, e, fmt.Sprintf("%s[%d]", path, i))...)
			}
		}
	case map[string]any:
		if n, ok := schema["minProperties"].(float64); ok && float64(len(x)) < n {
			add("has %d members, want at least %v", len(x), n)
		}
		if req, ok := schema["required"].([]any); ok {
			for _, r := range req {
				if _, there := x[r.(string)]; !there {
					add("missing %q", r)
				}
			}
		}
		if props, ok := schema["properties"].(map[string]any); ok {
			for name, sub := range props {
				if member, there := x[name]; there {
					errs = append(errs, val.check(sub.(map[string]any), member, path+"."+name)...)
				}
			}
		}
		if un, ok := schema["unevaluatedProperties"].(bool); ok && !un {
			seen := val.evaluated(schema, x)
			for name := range x {
				if !seen[name] {
					add("field %q is not allowed on this line", name)
				}
			}
		}
		if extra, ok := schema["additionalProperties"].(bool); ok && !extra {
			props, _ := schema["properties"].(map[string]any)
			for name := range x {
				if _, known := props[name]; !known {
					add("unknown field %q", name)
				}
			}
		}
	}
	if all, ok := schema["allOf"].([]any); ok {
		for _, sub := range all {
			errs = append(errs, val.check(sub.(map[string]any), v, path)...)
		}
	}
	if cond, ok := schema["if"].(map[string]any); ok && len(val.check(cond, v, path)) == 0 {
		if then, ok := schema["then"].(map[string]any); ok {
			errs = append(errs, val.check(then, v, path)...)
		}
	}
	return errs
}

func isType(v any, t string) bool {
	switch t {
	case "object":
		_, ok := v.(map[string]any)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "number":
		_, ok := v.(float64)
		return ok
	case "integer":
		f, ok := v.(float64)
		return ok && f == math.Trunc(f)
	}
	return true
}

func equal(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return strings.TrimSpace(string(x)) == strings.TrimSpace(string(y))
}

// evaluated is the names of the members of an object that the schema, and the
// subschemas that apply to it (allOf, and the then of an if that holds), give a
// definition: what unevaluatedProperties false measures against.
func (val validator) evaluated(schema map[string]any, obj map[string]any) map[string]bool {
	out := map[string]bool{}
	if ref, ok := schema["$ref"].(string); ok {
		name, _ := strings.CutPrefix(ref, "#/$defs/")
		defs, _ := val.root["$defs"].(map[string]any)
		if target, ok := defs[name].(map[string]any); ok {
			for k := range val.evaluated(target, obj) {
				out[k] = true
			}
		}
	}
	if props, ok := schema["properties"].(map[string]any); ok {
		for k := range props {
			out[k] = true
		}
	}
	if all, ok := schema["allOf"].([]any); ok {
		for _, sub := range all {
			for k := range val.evaluated(sub.(map[string]any), obj) {
				out[k] = true
			}
		}
	}
	if cond, ok := schema["if"].(map[string]any); ok && len(val.check(cond, obj, "$")) == 0 {
		if then, ok := schema["then"].(map[string]any); ok {
			for k := range val.evaluated(then, obj) {
				out[k] = true
			}
		}
	}
	return out
}
