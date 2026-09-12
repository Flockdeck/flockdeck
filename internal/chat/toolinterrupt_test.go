package chat

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// slowTool runs until it is stopped, the way a long command does.
type slowTool struct {
	started chan struct{}
	runs    int
}

func (s *slowTool) Name() string                    { return "slow" }
func (s *slowTool) Describe() Schema                { return Schema{Description: "a slow tool"} }
func (s *slowTool) Approval(json.RawMessage) string { return "" }
func (s *slowTool) Run(ctx context.Context, _ json.RawMessage) (string, error) {
	s.runs++
	if s.runs == 1 {
		close(s.started)
	}
	<-ctx.Done()
	return "stopped part-way", nil
}

// Ctrl+C while a tool runs stops the turn as surely as Ctrl+C mid-answer, and
// the pane says so; /retry carries on from the calls' answers.
func TestATurnInterruptedAmongItsToolsCanBeRetried(t *testing.T) {
	tool := &slowTool{started: make(chan struct{})}
	wire := &scriptedWire{turns: []turnFunc{
		asksFor(ToolCall{ID: "c1", Name: "slow"}, ToolCall{ID: "c2", Name: "slow"}),
		says("carried on"),
	}}
	signals := make(chan os.Signal, 1)
	go func() {
		<-tool.started
		signals <- os.Interrupt
	}()

	out := run(t, Options{Agent: "anthropic", Model: "claude-opus-5", Task: "go", Tools: []Tool{tool}, Signals: signals},
		"/retry\n/exit\n", wire)

	if !strings.Contains(out, "(interrupted; /retry carries on)") {
		t.Errorf("the interruption was not shown:\n%s", out)
	}
	if strings.Contains(out, "there is nothing to retry") || !strings.Contains(out, "carried on") {
		t.Errorf("/retry did not carry on:\n%s", out)
	}
	if tool.runs != 1 {
		t.Errorf("the tool ran %d times; the call after the interruption should not have run", tool.runs)
	}
}
