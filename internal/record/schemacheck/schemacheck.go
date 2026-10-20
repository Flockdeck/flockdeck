// Package schemacheck validates a JSON value against the small subset of JSON
// Schema that docs/recording-line.schema.json uses, so that tests can check
// real recording lines against the published schema without a dependency.
//
// The subset: type, const, enum, pattern, minimum, minLength, minItems, minProperties, required,
// properties, additionalProperties and unevaluatedProperties (false only), $ref to #/$defs, items, allOf, anyOf, not and if/then. Anything else in a schema is ignored,
// which is why the schema sticks to these.
//
// A schema that uses one of these keywords wrongly (an allOf that is not an array, a
// not that is not an object, an entry that is not a schema, a pattern that is not an
// expression, a $ref that does not resolve) is reported as an error in the result,
// prefixed "schema:", wherever in the schema it is and whether or not the document
// reaches it. Validate does not panic.
package schemacheck

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
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

// Validate checks one JSON document and returns what is wrong with it, and what
// is wrong with the schema.
func (s Schema) Validate(doc []byte) []string {
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		return []string{"not JSON: " + err.Error()}
	}
	root := map[string]any(s)
	// The schema is read through once for its own mistakes. check does not
	// report them: a sub-check under anyOf, not or if only says whether the
	// document matched, so an error raised there would be lost.
	errs := lint(root, root, "#")
	val := validator{root: root, depth: new(int), sink: new([]string)}
	errs = append(errs, val.checked(root, v, "$")...)
	// Schema errors raised where their result is thrown away, such as a $ref
	// chain that never ends under an anyOf.
	seen := map[string]bool{}
	for _, e := range errs {
		seen[e] = true
	}
	for _, e := range *val.sink {
		if !seen[e] {
			seen[e] = true
			errs = append(errs, e)
		}
	}
	return errs
}

// checked is check with a backstop: a schema shape nothing above anticipated is
// reported as an error in place of a panic. When it fires, the errors found
// earlier in the same run are lost, because they were in a slice the panic
// unwound; the schema error is what matters then.
func (val validator) checked(schema map[string]any, v any, path string) (errs []string) {
	defer func() {
		if r := recover(); r != nil {
			errs = []string{fmt.Sprintf("%s: schema: not understood (%v)", path, r)}
		}
	}()
	return val.check(schema, v, path)
}

// maxDepth is how many schemas deep one check may go, through $ref and through
// nesting. A schema that refers to itself without consuming the document would
// otherwise overflow the stack, which no recover catches.
const maxDepth = 200

type validator struct {
	root map[string]any
	// depth is how deep the current check is, and sink collects the schema
	// errors raised where a check's own result is thrown away.
	depth *int
	sink  *[]string
}

func (val validator) check(schema map[string]any, v any, path string) []string {
	*val.depth++
	defer func() { *val.depth-- }()
	if *val.depth > maxDepth {
		msg := fmt.Sprintf("%s: schema: more than %d schemas deep (does a $ref refer to itself?)", path, maxDepth)
		*val.sink = append(*val.sink, msg)
		return []string{msg}
	}
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
		if p, ok := schema["pattern"].(string); ok {
			// A pattern that does not compile is lint's to report.
			if re, err := regexp.Compile(p); err == nil && !re.MatchString(x) {
				add("%q does not match %s", x, p)
			}
		}
		if n, ok := schema["minLength"].(float64); ok && float64(len([]rune(x))) < n {
			add("shorter than %v", n)
		}
	case float64:
		if n, ok := schema["minimum"].(float64); ok && x < n {
			add("%v is below %v", x, n)
		}
	case []any:
		if n, ok := schema["minItems"].(float64); ok && float64(len(x)) < n {
			add("has %d items, want at least %v", len(x), n)
		}
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
				// An entry that is not a string is lint's to report.
				if name, ok := r.(string); ok {
					if _, there := x[name]; !there {
						add("missing %q", name)
					}
				}
			}
		}
		if props, ok := schema["properties"].(map[string]any); ok {
			for name, sub := range props {
				member, there := x[name]
				subSchema, ok := sub.(map[string]any)
				if there && ok {
					errs = append(errs, val.check(subSchema, member, path+"."+name)...)
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
		for _, e := range all {
			if sub, ok := e.(map[string]any); ok {
				errs = append(errs, val.check(sub, v, path)...)
			}
		}
	}
	if alts, ok := schema["anyOf"].([]any); ok {
		matched := false
		for _, e := range alts {
			if sub, ok := e.(map[string]any); ok && len(val.check(sub, v, path)) == 0 {
				matched = true
				break
			}
		}
		if !matched {
			add("matches none of the anyOf alternatives")
		}
	}
	if not, ok := schema["not"].(map[string]any); ok && len(val.check(not, v, path)) == 0 {
		add("matches a schema it must not match")
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
	*val.depth++
	defer func() { *val.depth-- }()
	if *val.depth > maxDepth {
		*val.sink = append(*val.sink, fmt.Sprintf("$: schema: more than %d schemas deep (does a $ref refer to itself?)", maxDepth))
		return out
	}
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
		for _, e := range all {
			// A malformed entry is lint's to report; here it names nothing.
			if sub, ok := e.(map[string]any); ok {
				for k := range val.evaluated(sub, obj) {
					out[k] = true
				}
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

// lint reads a schema through for the mistakes that make it mean something other
// than its author wrote, and returns one error for each, prefixed "schema:". It
// follows the structure (properties, items, allOf, anyOf, not, if, then and
// $defs) and not $ref, so it ends. path names where in the schema an error is.
func lint(root, schema map[string]any, path string) []string {
	var errs []string
	bad := func(format string, a ...any) {
		errs = append(errs, path+": schema: "+fmt.Sprintf(format, a...))
	}
	kind := func(key, want string, ok func(any) bool) {
		if raw, there := schema[key]; there && !ok(raw) {
			bad("%s must be %s, got %T", key, want, raw)
		}
	}
	isString := func(x any) bool { _, ok := x.(string); return ok }
	isNumber := func(x any) bool { _, ok := x.(float64); return ok }
	isBool := func(x any) bool { _, ok := x.(bool); return ok }
	isArray := func(x any) bool { _, ok := x.([]any); return ok }

	if raw, there := schema["$ref"]; there {
		ref, ok := raw.(string)
		name, found := strings.CutPrefix(ref, "#/$defs/")
		defs, _ := root["$defs"].(map[string]any)
		switch {
		case !ok:
			bad("$ref must be a string, got %T", raw)
		case !found || defs[name] == nil:
			bad("$ref %s does not resolve", ref)
		}
	}
	kind("type", "a string", isString)
	for _, key := range []string{"minimum", "minLength", "minItems", "minProperties"} {
		kind(key, "a number", isNumber)
	}
	for _, key := range []string{"additionalProperties", "unevaluatedProperties"} {
		kind(key, "true or false", isBool)
	}
	kind("enum", "an array", isArray)
	if raw, there := schema["pattern"]; there {
		if p, ok := raw.(string); !ok {
			bad("pattern must be a string, got %T", raw)
		} else if _, err := regexp.Compile(p); err != nil {
			bad("pattern %q is not a regular expression: %v", p, err)
		}
	}
	if raw, there := schema["required"]; there {
		arr, ok := raw.([]any)
		if !ok {
			bad("required must be an array, got %T", raw)
		}
		for i, r := range arr {
			if _, ok := r.(string); !ok {
				bad("required[%d] must be a string, got %T", i, r)
			}
		}
	}
	for _, key := range []string{"properties", "$defs"} {
		raw, there := schema[key]
		if !there {
			continue
		}
		m, ok := raw.(map[string]any)
		if !ok {
			bad("%s must be an object, got %T", key, raw)
			continue
		}
		names := make([]string, 0, len(m))
		for name := range m {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			sub, ok := m[name].(map[string]any)
			if !ok {
				bad("%s.%s must be an object, got %T", key, name, m[name])
				continue
			}
			errs = append(errs, lint(root, sub, path+"/"+key+"/"+name)...)
		}
	}
	for _, key := range []string{"items", "not", "if", "then"} {
		raw, there := schema[key]
		if !there {
			continue
		}
		sub, ok := raw.(map[string]any)
		if !ok {
			bad("%s must be an object, got %T", key, raw)
			continue
		}
		errs = append(errs, lint(root, sub, path+"/"+key)...)
	}
	for _, key := range []string{"allOf", "anyOf"} {
		raw, there := schema[key]
		if !there {
			continue
		}
		arr, ok := raw.([]any)
		if !ok {
			bad("%s must be an array, got %T", key, raw)
			continue
		}
		for i, e := range arr {
			sub, ok := e.(map[string]any)
			if !ok {
				bad("%s[%d] must be an object, got %T", key, i, e)
				continue
			}
			errs = append(errs, lint(root, sub, fmt.Sprintf("%s/%s/%d", path, key, i))...)
		}
	}
	return errs
}
