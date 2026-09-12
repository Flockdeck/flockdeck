package chat

import (
	"fmt"
	"strings"
	"testing"
)

// A turn stopped at its limit of tool calls can be let go on with /retry.
func TestATurnStoppedAtItsToolLimitCanBeLetGoOn(t *testing.T) {
	tool := &fakeTool{name: "list_dir", answer: "a.go"}
	var turns []turnFunc
	for i := 0; i <= maxToolSteps; i++ {
		turns = append(turns, asksFor(ToolCall{ID: fmt.Sprintf("c%d", i), Name: "list_dir"}))
	}
	turns = append(turns, says("finished"))
	wire := &scriptedWire{turns: turns}

	out := run(t, Options{Agent: "anthropic", Model: "claude-opus-5", Task: "work", Tools: []Tool{tool}},
		"/retry\n/exit\n", wire)

	if !strings.Contains(out, "stopping: the model has asked for tools") || !strings.Contains(out, "(/retry lets it carry on") {
		t.Errorf("the limit and the way on were not shown:\n%s", out)
	}
	if strings.Contains(out, "there is nothing to retry") || !strings.Contains(out, "finished") {
		t.Errorf("/retry did not let it go on:\n%s", out)
	}
}
