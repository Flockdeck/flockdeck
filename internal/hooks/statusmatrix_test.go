package hooks

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// The tests in this file are the transport's rows of docs/status-matrix.md:
// what reaches the application for each thing Claude Code reports, and in
// what order. The rows about what a pane shows for it are tested where that
// is decided, in internal/session and internal/workspace.

// TestStatusMatrixWhatEachPayloadIsReportedAs is rows A6, A11-A13, B11 and
// C16: which event, if any, the application is told about for each payload.
func TestStatusMatrixWhatEachPayloadIsReportedAs(t *testing.T) {
	cases := []struct {
		row, event, stdin string
		// want is the event the application receives; empty means none.
		want, wantTool, wantType string
	}{
		{"A6", "PostToolUseFailure", `{"tool_name":"Bash","is_interrupt":true}`, Interrupted, "Bash", ""},
		{"A5", "PostToolUseFailure", `{"tool_name":"Bash","is_interrupt":false}`, "PostToolUseFailure", "Bash", ""},
		{"A9", "Notification", `{"notification_type":"permission_prompt"}`, "Notification", "", "permission_prompt"},
		{"A10", "Notification", `{"notification_type":"idle_prompt"}`, "Notification", "", "idle_prompt"},
		{"A11", "Notification", `{"notification_type":"elicitation_dialog"}`, "Notification", "", "elicitation_dialog"},
		{"A11", "Notification", `{"notification_type":"agent_needs_input"}`, "Notification", "", "agent_needs_input"},
		{"A12", "Notification", `{}`, "Notification", "", ""},
		{"A13", "Notification", `{"notification_type":"auth_success"}`, "", "", ""},
		{"A13", "Notification", `{"notification_type":"elicitation_complete"}`, "", "", ""},
		{"A13", "Notification", `{"notification_type":"elicitation_response"}`, "", "", ""},
		{"A13", "Notification", `{"notification_type":"agent_completed"}`, "", "", ""},
		{"A13", "Notification", `{"notification_type":"computer_use_exit"}`, "", "", ""},
		{"A3", "PreToolUse", `{"tool_name":"AskUserQuestion"}`, "PreToolUse", "AskUserQuestion", ""},
		{"A14", "Stop", `{}`, "Stop", "", ""},
		{"A15", "StopFailure", `{}`, "StopFailure", "", ""},
		// C16: a payload the hook cannot read is still reported, as the
		// event alone. Losing the tool's name is better than losing the event.
		{"C16", "Stop", `not json`, "Stop", "", ""},
		{"C16", "PreToolUse", `{"tool_name":`, "PreToolUse", "", ""},
	}
	for _, c := range cases {
		t.Run(c.row+"/"+c.event+"/"+c.stdin, func(t *testing.T) {
			srv, r := newServer(t)
			if _, err := Emit(strings.NewReader(c.stdin), srv.Endpoint(), srv.Token(), "pane-m", c.event); err != nil {
				t.Fatalf("emit: %v", err)
			}
			if c.want == "" {
				select {
				case e := <-r.ch:
					t.Fatalf("reported %+v, want nothing", e)
				case <-time.After(150 * time.Millisecond):
				}
				return
			}
			got := r.next(t)
			if got.Event != c.want || got.Tool != c.wantTool || got.NotificationType != c.wantType {
				t.Errorf("reported %q tool %q type %q, want %q tool %q type %q",
					got.Event, got.Tool, got.NotificationType, c.want, c.wantTool, c.wantType)
			}
		})
	}
}

// TestStatusMatrixAwaitedHooksArriveInOrder is row C0: Claude Code waits for
// each hook before it goes on, and the server applies an event before it
// answers, so hooks that each finish in time are applied in the order they
// fired. This is why a late event needs a hook that gave up (C13).
func TestStatusMatrixAwaitedHooksArriveInOrder(t *testing.T) {
	srv, r := newServer(t)
	seq := []string{"UserPromptSubmit", "PreToolUse", "PostToolUse", "PreToolUse", "PostToolUse", "Stop"}
	for _, ev := range seq {
		if _, err := Emit(strings.NewReader(`{"tool_name":"Bash"}`), srv.Endpoint(), srv.Token(), "pane-o", ev); err != nil {
			t.Fatalf("emit %s: %v", ev, err)
		}
	}
	for i, want := range seq {
		if got := r.next(t); got.Event != want {
			t.Fatalf("event %d = %q, want %q", i, got.Event, want)
		}
	}
}

// TestStatusMatrixAHookThatGaveUpLandsAfterTheNextOne is row C13, the
// mechanism behind C1 and C2: a hook whose request outlives Emit's deadline
// returns an error, Claude Code goes on to its next event, and the one given
// up on is still applied -- after the one that followed it. The server does
// not know the hook gave up, and nothing says which of the two is newer.
func TestStatusMatrixAHookThatGaveUpLandsAfterTheNextOne(t *testing.T) {
	if testing.Short() {
		t.Skip("waits out Emit's three-second deadline")
	}
	release := make(chan struct{})
	var mu sync.Mutex
	var order []string
	srv, err := Serve(func(e Event) {
		if e.Event == "PostToolUse" {
			<-release // the application is slow to take this one
		}
		mu.Lock()
		order = append(order, e.Event)
		mu.Unlock()
	})
	if err != nil {
		t.Fatalf("serve: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	start := time.Now()
	if _, err := Emit(strings.NewReader(`{"tool_name":"Bash"}`), srv.Endpoint(), srv.Token(), "pane-late", "PostToolUse"); err == nil {
		t.Fatal("Emit reported success for a hook the application never answered")
	}
	if waited := time.Since(start); waited > 4*time.Second {
		t.Errorf("Emit waited %v; a hook must give up well inside Claude Code's own five seconds", waited)
	}
	if _, err := Emit(nil, srv.Endpoint(), srv.Token(), "pane-late", "Stop"); err != nil {
		t.Fatalf("emit Stop: %v", err)
	}
	close(release)

	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		got := append([]string(nil), order...)
		mu.Unlock()
		if len(got) == 2 {
			if got[0] != "Stop" || got[1] != "PostToolUse" {
				t.Fatalf("applied %q, want the Stop first and the given-up PostToolUse after it", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("applied %q, want both events, the given-up one last", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestStatusMatrixCompactionTriggerIsReported is row B19's transport half: a
// PreCompact or PostCompact carries what set it off as its source.
func TestStatusMatrixCompactionTriggerIsReported(t *testing.T) {
	for _, c := range []struct{ event, stdin, want string }{
		{"PreCompact", `{"trigger":"manual"}`, "manual"},
		{"PostCompact", `{"trigger":"auto"}`, "auto"},
		{"SessionStart", `{"source":"compact"}`, "compact"},
	} {
		srv, r := newServer(t)
		if _, err := Emit(strings.NewReader(c.stdin), srv.Endpoint(), srv.Token(), "pane-c", c.event); err != nil {
			t.Fatalf("emit: %v", err)
		}
		if got := r.next(t); got.Event != c.event || got.Source != c.want {
			t.Errorf("%s reported %q source %q, want source %q", c.event, got.Event, got.Source, c.want)
		}
	}
}
