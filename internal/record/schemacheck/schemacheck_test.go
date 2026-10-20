package schemacheck

import (
	"encoding/json"
	"strings"
	"testing"
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
