package chat

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/jmwri/flockdeck/internal/hooks"
	sess "github.com/jmwri/flockdeck/internal/session"
)

// The tests in this file are docs/status-matrix.md's section G: Flockdeck's
// own chat client reports its lifecycle under Claude Code's event names, and
// what each of its turns reports has to walk a pane through the same statuses
// a Claude pane's would.

// statusesOf replays what a pane reported through the same mapping the
// workspace applies, and returns each status it moved to in turn.
func statusesOf(evs []hooks.Event) []string {
	var out []string
	for _, ev := range evs {
		st, detail, ok := sess.StatusForEvent(ev.Event, ev.Tool, ev.NotificationType)
		if !ok {
			continue
		}
		name := st.String()
		if detail != "" {
			name += ":" + detail
		}
		if len(out) == 0 || out[len(out)-1] != name {
			out = append(out, name)
		}
	}
	return out
}

func (p *paneServer) all() []hooks.Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]hooks.Event(nil), p.events...)
}

func TestStatusMatrixChatTurns(t *testing.T) {
	call := ToolCall{ID: "c1", Name: "write_file", Args: json.RawMessage(`{"path":"a.txt"}`)}
	cases := []struct {
		row   string
		name  string
		tool  *fakeTool
		turns []turnFunc
		input string
		want  []string
	}{
		{row: "G1", name: "a turn with no tools is working, then idle",
			turns: []turnFunc{says("hello")},
			want:  []string{"working", "idle"}},
		{row: "G2", name: "a tool that asks nothing is working on it, then idle",
			tool:  &fakeTool{name: "write_file", answer: "wrote"},
			turns: []turnFunc{asksFor(call), says("done")},
			want:  []string{"working", "working:write_file", "working", "idle"}},
		{row: "G3", name: "a tool allowed at its question waits, then works, then idle",
			tool:  &fakeTool{name: "write_file", question: "write a.txt?", answer: "wrote"},
			turns: []turnFunc{asksFor(call), says("done")}, input: "y\n",
			want: []string{"working", "waiting:write_file", "working:write_file", "working", "idle"}},
		{row: "G4", name: "a tool refused at its question goes back to work, then idle",
			tool:  &fakeTool{name: "write_file", question: "write a.txt?", answer: "wrote"},
			turns: []turnFunc{asksFor(call), says("fine")}, input: "n\n",
			want: []string{"working", "waiting:write_file", "working", "idle"}},
	}
	for _, c := range cases {
		t.Run(c.row+"/"+c.name, func(t *testing.T) {
			srv := newPaneServer(t, "")
			var tools []Tool
			if c.tool != nil {
				tools = []Tool{c.tool}
			}
			run(t, Options{Agent: "anthropic", Tools: tools, Task: "do it",
				API: srv.srv.BaseURL(), Token: srv.srv.Token()}, c.input, &scriptedWire{turns: c.turns})
			if got := statusesOf(srv.all()); !slices.Equal(got, c.want) {
				t.Errorf("the pane went %q, want %q (events %q)", got, c.want, srv.names())
			}
		})
	}
}
