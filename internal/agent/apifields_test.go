package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

// A key inside "api" that no endpoint has is named, as one beside it is: an
// address written as "url" was otherwise ignored without a word.
func TestUnknownKeysInsideAPIAreNamed(t *testing.T) {
	c := Merge(&File{Agents: []json.RawMessage{json.RawMessage(
		`{"id": "gw", "api": {"url": "http://gw/v1", "BaseURL": "http://gw/v1", "wire": "openai", "models": []}}`)}})
	for _, want := range []string{`"url" in "api" is not something an endpoint has`, `"models" belongs beside "api"`} {
		if !strings.Contains(c.Notice, want) {
			t.Errorf("notice %q should say %s", c.Notice, want)
		}
	}
	for _, used := range []string{`"BaseURL"`, `"wire"`} {
		if strings.Contains(c.Notice, used) {
			t.Errorf("notice %q names %s, which is used", c.Notice, used)
		}
	}
}
