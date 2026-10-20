package schemacheck

import (
	"encoding/json"
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
