package schemacheck

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func schemaOf(t *testing.T, src string) Schema {
	t.Helper()
	var s Schema
	if err := json.Unmarshal([]byte(src), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAnyOfNeedsOneAlternative(t *testing.T) {
	s := schemaOf(t, `{"type":"object","anyOf":[{"required":["a"]},{"required":["b"]}]}`)
	for doc, ok := range map[string]bool{`{"a":1}`: true, `{"b":1}`: true, `{"a":1,"b":2}`: true, `{"c":1}`: false, `{}`: false} {
		if got := len(s.Validate([]byte(doc))) == 0; got != ok {
			t.Errorf("%s: accepted = %v, want %v", doc, got, ok)
		}
	}
}

func TestNotRefusesWhatMatches(t *testing.T) {
	s := schemaOf(t, `{"type":"object","not":{"anyOf":[{"required":["id"]},{"required":["text"]}]}}`)
	for doc, ok := range map[string]bool{`{}`: true, `{"time":"x"}`: true, `{"id":1}`: false, `{"text":"t"}`: false, `{"id":1,"text":"t"}`: false} {
		if got := len(s.Validate([]byte(doc))) == 0; got != ok {
			t.Errorf("%s: accepted = %v, want %v", doc, got, ok)
		}
	}
}

// A schema that uses a keyword wrongly is reported as an error in the result,
// and never panics or is silently ignored.
func TestMalformedKeywordsAreErrors(t *testing.T) {
	cases := map[string]string{
		"anyOf entry is not an object": `{"anyOf":[true]}`,
		"anyOf is not an array":        `{"anyOf":"x"}`,
		"anyOf is an object":           `{"anyOf":{"required":["a"]}}`,
		"not is true":                  `{"not":true}`,
		"not is false":                 `{"not":false}`,
		"not is an array":              `{"not":[{"required":["a"]}]}`,
		"allOf entry is a number":      `{"allOf":[1]}`,
		"allOf is not an array":        `{"allOf":{"required":["a"]}}`,
		"required entry is a number":   `{"required":[1]}`,
		"property is not an object":    `{"properties":{"a":true}}`,
		"if is not an object":          `{"if":true,"then":{}}`,
		"then is not an object":        `{"if":{},"then":[]}`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			s := schemaOf(t, src)
			errs := s.Validate([]byte(`{"a":1}`))
			if len(errs) == 0 {
				t.Fatalf("a malformed schema %s was accepted", src)
			}
			found := false
			for _, e := range errs {
				if strings.Contains(e, "schema:") {
					found = true
				}
			}
			if !found {
				t.Fatalf("errors %v do not say the schema is at fault", errs)
			}
		})
	}
}

// An error nested inside a valid keyword is found too, and a panic anywhere is
// turned into an error by the backstop in Validate.
func TestMalformedKeywordNestedInAProperty(t *testing.T) {
	s := schemaOf(t, `{"properties":{"a":{"anyOf":[7]}}}`)
	if errs := s.Validate([]byte(`{"a":1}`)); len(errs) == 0 {
		t.Fatal("a nested malformed anyOf was accepted")
	}
}

func TestValidatePanicIsAnError(t *testing.T) {
	// A pattern that is not a valid regular expression makes regexp panic, which
	// no check above anticipates, so this reaches the backstop in Validate.
	s := Schema{"properties": map[string]any{"a": map[string]any{"pattern": "["}}}
	errs := s.Validate([]byte(`{"a":"x"}`))
	if len(errs) == 0 || !strings.Contains(errs[0], "schema:") {
		t.Fatalf("errors = %v, want a schema error for the bad pattern", errs)
	}
}

// A mistake anywhere in the schema is reported, whatever the document is and
// however the keyword is reached: under anyOf, not or an if condition, in a then
// that does not apply, in a property the document does not have, or in $defs.
func TestMalformedKeywordsAreFoundWhereverTheyAre(t *testing.T) {
	cases := map[string]string{
		"anyOf inside not":                   `{"not":{"anyOf":[true]}}`,
		"required inside not":                `{"not":{"required":[5]}}`,
		"not inside an if condition":         `{"if":{"not":3},"then":{"required":["x"]}}`,
		"not inside an anyOf alternative":    `{"anyOf":[{"not":3},{"type":"object"}]}`,
		"then that does not apply":           `{"if":{"required":["zzz"]},"then":{"anyOf":[true]}}`,
		"property the document lacks":        `{"properties":{"absent":{"not":false}}}`,
		"in $defs nothing refers to":         `{"$defs":{"unused":{"allOf":7}}}`,
		"in items":                           `{"items":{"anyOf":"x"}}`,
		"required is a string":               `{"required":"a"}`,
		"properties is an array":             `{"properties":[1]}`,
		"pattern is a number":                `{"pattern":5}`,
		"pattern is not an expression":       `{"pattern":"["}`,
		"type is a number":                   `{"type":3}`,
		"minimum is a string":                `{"minimum":"1"}`,
		"additionalProperties is a string":   `{"additionalProperties":"no"}`,
		"enum is an object":                  `{"enum":{"a":1}}`,
		"$ref that does not resolve":         `{"$ref":"#/$defs/nowhere"}`,
		"$ref that is not a string":          `{"$ref":4}`,
		"unresolved $ref under an anyOf":     `{"anyOf":[{"$ref":"#/$defs/nowhere"},{}]}`,
		"property that is not a schema":      `{"properties":{"a":"x"}}`,
		"$defs member that is not an object": `{"$defs":{"a":[]}}`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			s := schemaOf(t, src)
			for _, doc := range []string{`{}`, `{"a":1}`, `"text"`} {
				if errs := s.Validate([]byte(doc)); !hasSchemaError(errs) {
					t.Errorf("%s on %s: errors %v do not name a schema fault", src, doc, errs)
				}
			}
		})
	}
}

func hasSchemaError(errs []string) bool {
	for _, e := range errs {
		if strings.Contains(e, "schema:") {
			return true
		}
	}
	return false
}

// A schema that refers to itself without consuming the document ends in an
// error, not a stack overflow, including where the result of the check is only
// used to see whether it matched.
func TestSelfReferencingRefEndsInAnError(t *testing.T) {
	for name, src := range map[string]string{
		"direct":             `{"$defs":{"a":{"$ref":"#/$defs/a"}},"$ref":"#/$defs/a"}`,
		"two steps":          `{"$defs":{"a":{"$ref":"#/$defs/b"},"b":{"$ref":"#/$defs/a"}},"$ref":"#/$defs/a"}`,
		"through allOf":      `{"$defs":{"a":{"allOf":[{"$ref":"#/$defs/a"}]}},"$ref":"#/$defs/a"}`,
		"under anyOf":        `{"$defs":{"a":{"$ref":"#/$defs/a"}},"anyOf":[{"$ref":"#/$defs/a"},{}]}`,
		"under not":          `{"$defs":{"a":{"$ref":"#/$defs/a"}},"not":{"$ref":"#/$defs/a"}}`,
		"unevaluated fields": `{"$defs":{"a":{"$ref":"#/$defs/a"}},"unevaluatedProperties":false,"$ref":"#/$defs/a"}`,
	} {
		t.Run(name, func(t *testing.T) {
			s := schemaOf(t, src)
			errs := s.Validate([]byte(`{"a":1}`))
			if !hasSchemaError(errs) {
				t.Fatalf("errors %v do not name a schema fault", errs)
			}
		})
	}
}

// A $ref back to an ancestor through a property is ordinary recursion and ends
// with the document.
func TestRecursiveSchemaThroughAPropertyIsFine(t *testing.T) {
	s := schemaOf(t, `{"$defs":{"node":{"type":"object","properties":{"next":{"$ref":"#/$defs/node"}}}},"$ref":"#/$defs/node"}`)
	if errs := s.Validate([]byte(`{"next":{"next":{"next":{}}}}`)); len(errs) != 0 {
		t.Fatalf("errors = %v", errs)
	}
}

// A $ref cycle that branches doubles its work at every level. It must end in a
// handful of errors, quickly, not after 2^100 calls.
func TestBranchingRefCycleEndsQuickly(t *testing.T) {
	for name, src := range map[string]string{
		"two branches":   `{"$defs":{"a":{"allOf":[{"$ref":"#/$defs/a"},{"$ref":"#/$defs/a"}]}},"$ref":"#/$defs/a"}`,
		"three branches": `{"$defs":{"a":{"allOf":[{"$ref":"#/$defs/a"},{"$ref":"#/$defs/a"},{"$ref":"#/$defs/a"}]}},"$ref":"#/$defs/a"}`,
		"two names":      `{"$defs":{"a":{"allOf":[{"$ref":"#/$defs/b"},{"$ref":"#/$defs/b"}]},"b":{"allOf":[{"$ref":"#/$defs/a"},{"$ref":"#/$defs/a"}]}},"$ref":"#/$defs/a"}`,
	} {
		t.Run(name, func(t *testing.T) {
			s := schemaOf(t, src)
			start := time.Now()
			errs := s.Validate([]byte(`{"a":1}`))
			if d := time.Since(start); d > 5*time.Second {
				t.Fatalf("took %v", d)
			}
			if !hasSchemaError(errs) {
				t.Fatalf("errors %v do not name a schema fault", errs)
			}
			if len(errs) > maxSink+5 {
				t.Fatalf("%d errors", len(errs))
			}
		})
	}
}

// The work budget alone ends a schema that never repeats a $ref at the same
// place: a long fan-out of distinct definitions.
func TestWorkBudgetEndsWideSchemas(t *testing.T) {
	var defs []string
	var refs []string
	for i := 0; i < 30; i++ {
		refs = append(refs, fmt.Sprintf(`{"$ref":"#/$defs/d%d"}`, i+1))
	}
	for i := 0; i < 30; i++ {
		defs = append(defs, fmt.Sprintf(`"d%d":{"allOf":[%s,%s]}`, i, refs[i], refs[i]))
	}
	defs = append(defs, `"d30":{}`)
	s := schemaOf(t, `{"$defs":{`+strings.Join(defs, ",")+`},"$ref":"#/$defs/d0"}`)
	start := time.Now()
	errs := s.Validate([]byte(`{}`))
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("took %v", d)
	}
	if !hasSchemaError(errs) {
		t.Fatalf("errors %v do not name a schema fault", errs)
	}
}

// Only $ref hops count toward the depth limit, so a recursive schema over a
// document well past 100 levels deep is fine.
func TestDeepDocumentUnderRecursiveRef(t *testing.T) {
	s := schemaOf(t, `{"$defs":{"node":{"type":"object","properties":{"next":{"$ref":"#/$defs/node"}}}},"$ref":"#/$defs/node"}`)
	doc := strings.Repeat(`{"next":`, 150) + `{}` + strings.Repeat(`}`, 150)
	if errs := s.Validate([]byte(doc)); len(errs) != 0 {
		t.Fatalf("errors = %v", errs)
	}
}

// evaluated follows a $ref the way check does and needs its own guard: here
// check stops at the cycle but evaluated is reached by unevaluatedProperties
// and has to stop on its own, at the depth limit and not only by running out of budget.
func TestUnevaluatedPropertiesOverASelfRef(t *testing.T) {
	s := schemaOf(t, `{"$defs":{"a":{"$ref":"#/$defs/a"}},"unevaluatedProperties":false,"allOf":[{"$ref":"#/$defs/a"}]}`)
	errs := s.Validate([]byte(`{"a":1}`))
	if !strings.Contains(strings.Join(errs, "\n"), "hops deep") {
		t.Fatalf("errors %v do not say the chain was cut at the depth limit", errs)
	}
}

func TestLintRefMessagesAreDistinct(t *testing.T) {
	s := schemaOf(t, `{"$ref":5}`)
	if errs := s.Validate([]byte(`{}`)); !strings.Contains(strings.Join(errs, "\n"), "must be a string") {
		t.Fatalf("errors = %v", errs)
	}
	s = schemaOf(t, `{"$ref":"#/$defs/missing"}`)
	if errs := s.Validate([]byte(`{}`)); !strings.Contains(strings.Join(errs, "\n"), "does not resolve") {
		t.Fatalf("errors = %v", errs)
	}
}

func TestUnknownTypeNameIsRefused(t *testing.T) {
	s := schemaOf(t, `{"type":"nul"}`)
	if errs := s.Validate([]byte(`null`)); !strings.Contains(strings.Join(errs, "\n"), "not one of") {
		t.Fatalf("errors = %v", errs)
	}
}
