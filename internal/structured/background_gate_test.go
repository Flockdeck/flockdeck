package structured

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/acp"
	"github.com/jmwri/flockdeck/internal/acp/bridge"
	"github.com/jmwri/flockdeck/internal/agent"
)

// updateOnly is the bridge as an acp.Handler that takes updates and refuses permissions.
type updateOnly struct{ b *bridge.Bridge }

func (u updateOnly) Update(x acp.Update)                                    { u.b.Update(x) }
func (updateOnly) Permission(context.Context, acp.PermissionRequest) string { return "" }

func runtimeWith(spec agent.Spec, queue ...queued) *Runtime {
	r := &Runtime{opts: Options{Spec: spec}, br: bridge.New(bridge.Config{}), phase: PhaseReady, queue: queue}
	return r
}

// An agent that is not Flockdeck's own Claude adapter cannot hold the queue or
// write under a tool row. A test's own command counts as not own.
func TestBusyAndSubagentTextFromAnotherAgentAreIgnored(t *testing.T) {
	r := runtimeWith(agent.Spec{ID: "fake"}, queued{id: 1, blocks: []string{"a"}})
	h := handler{Handler: updateOnly{r.br}, r: r}
	h.Update(acp.Update{Kind: acp.UpdateToolCall, ToolCallID: "t1", ToolKind: "other", Title: "Agent", Status: "pending"})
	h.Update(acp.Update{Kind: acp.UpdateBusy, Busy: true})
	h.Update(acp.Update{Kind: acp.UpdateSubagentText, ToolCallID: "t1", SubagentText: "hello"})
	if r.br.Background() {
		t.Error("another agent set busy")
	}
	for _, e := range r.br.Snapshot() {
		if e.Sub != "" {
			t.Errorf("another agent wrote under a tool row: %q", e.Sub)
		}
	}
	if q, ok := r.pop(); !ok || q.id != 1 {
		t.Errorf("pop = %+v %v, want the prompt", q, ok)
	}
}

func claudeSpec(t *testing.T) agent.Spec {
	t.Helper()
	s := agent.Spec{ID: "claude", Runner: agent.RunnerACP, ACP: agent.ACPSpec{Adapter: "claude"}}
	if !s.ACPOwnClaude() {
		t.Fatal("the spec is not Flockdeck's own Claude adapter")
	}
	return s
}

func TestBusyAndSubagentTextFromTheOwnClaudeAdapterAreTaken(t *testing.T) {
	r := runtimeWith(claudeSpec(t), queued{id: 1, blocks: []string{"a"}})
	h := handler{Handler: updateOnly{r.br}, r: r}
	h.Update(acp.Update{Kind: acp.UpdateToolCall, ToolCallID: "t1", ToolKind: "other", Title: "Agent", Status: "pending"})
	h.Update(acp.Update{Kind: acp.UpdateSubagentText, ToolCallID: "t1", SubagentText: "hello"})
	h.Update(acp.Update{Kind: acp.UpdateBusy, Busy: true})
	if !r.br.Background() {
		t.Fatal("busy was not taken")
	}
	got := ""
	for _, e := range r.br.Snapshot() {
		got += e.Sub
	}
	if got != "hello" {
		t.Errorf("sub = %q", got)
	}
	if _, ok := r.pop(); ok {
		t.Error("a prompt was handed out while busy")
	}
}

// The agent's own turn is not shown once the pane has left the ready phase.
func TestBackgroundIsNotReportedOutsideTheReadyPhase(t *testing.T) {
	r := runtimeWith(agent.Spec{ID: "fake"})
	r.br.Update(acp.Update{Kind: acp.UpdateBusy, Busy: true})
	r.phase = PhaseExited
	if r.Snapshot().Background {
		t.Error("Background reported for an exited pane")
	}
}

// tapSpy notes the lines sent to the agent.
type tapSpy struct {
	mu    sync.Mutex
	lines []string
}

func (t *tapSpy) Tap(dir string, raw []byte) {
	t.mu.Lock()
	t.lines = append(t.lines, dir+" "+string(raw))
	t.mu.Unlock()
}
func (t *tapSpy) Gap(int)      {}
func (t *tapSpy) Flush() error { return nil }
func (t *tapSpy) Close() error { return nil }
func (t *tapSpy) cancels() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := 0
	for _, l := range t.lines {
		if strings.Contains(l, "session/cancel") {
			n++
		}
	}
	return n
}

// Send now ends the agent's own turn, which is what holds the prompt.
func TestSendNowEndsTheAgentsOwnTurn(t *testing.T) {
	spy := &tapSpy{}
	o := fakeOptions(t)
	o.Recorder = spy
	r := begin(t, o)
	until(t, r, "the pane to be ready", func(s Snapshot) bool { return s.Phase == PhaseReady })
	r.br.Update(acp.Update{Kind: acp.UpdateBusy, Busy: true})
	if err := r.Send("later"); err != nil {
		t.Fatal(err)
	}
	q := r.Snapshot().Queue
	if len(q) != 1 {
		t.Fatalf("queue = %+v, the prompt should wait", q)
	}
	if err := r.SendNow(q[0].ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for spy.cancels() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if spy.cancels() == 0 {
		t.Error("no session/cancel was sent for the agent's own turn")
	}
}
