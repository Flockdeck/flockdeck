package chat

import (
	"strings"
	"testing"
)

// What a conversation cost is priced from a table, never read off the bill,
// and the status line marks it "~" as the pane header marks the same figure.
// The token counts are exact and are not marked.
func TestTheStatusLineMarksItsCostAsAnEstimate(t *testing.T) {
	u := Usage{In: 12_345, Out: 678}
	line := statusLine("claude-opus-5", u, spendOf("claude-opus-5", u))
	if !strings.Contains(line, " · ~$") {
		t.Errorf("status line %q, want its cost marked as an estimate", line)
	}
	if strings.Contains(line, "~12k") || strings.Contains(line, "~678") {
		t.Errorf("status line %q marks exact token counts as estimates", line)
	}
	var s spend
	s.add("claude-opus-5", u)
	s.add("qwen3-coder", u)
	if line := statusLine("qwen3-coder", u, s); !strings.Contains(line, "~$") || !strings.HasSuffix(line, "+") {
		t.Errorf("status line %q, want the floor marked as an estimate too", line)
	}
}
