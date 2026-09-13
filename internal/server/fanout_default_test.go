package server

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/workspace"
)

// TestFanoutPreviewKeepsItsProjectsDefault covers the fan-out dialog opened as
// the project changes. The agent a run starts on is the active project's
// default, and the preview read it on its own goroutine after the transcript --
// racing every project switch, and offering the run to whichever project had
// become active by then. The dialog's choice has to be the one of the project
// the preview was asked about.
func TestFanoutPreviewKeepsItsProjectsDefault(t *testing.T) {
	srv, ws := newTestServer(t)
	first := srv.activeRoot()
	second := t.TempDir()
	if _, ok := ws.Catalog().Find("anthropic"); !ok {
		t.Skip("no built-in anthropic agent to give the second project")
	}
	if err := setAgentDefault(second, agentChoice{Agent: "anthropic"}); err != nil {
		t.Fatal(err)
	}
	ws.ReloadAgents()
	if _, ok := ask(srv, func() bool {
		if err := ws.OpenProject(second); err != nil {
			t.Error(err)
		}
		ws.SelectProject(first)
		return true
	}); !ok {
		t.Fatal("server closed")
	}
	defaultOf := func(root string) string {
		d, _ := ask(srv, func() string {
			was := ws.ActiveRoot()
			ws.SelectProject(root)
			_, def := ws.Agents()
			ws.SelectProject(was)
			return def
		})
		return d
	}
	want := defaultOf(first)
	if want == defaultOf(second) {
		t.Skip("the two projects' defaults could not be made to differ")
	}

	// The preview is held once the workspace has answered it, and the
	// project switched underneath it before it carries on.
	held, release := make(chan struct{}), make(chan struct{})
	was := planTasks
	planTasks = func(workspace.PlanSource) ([]string, bool) {
		close(held)
		<-release
		return nil, false
	}
	t.Cleanup(func() { planTasks = was })

	c := &controlClient{out: make(chan []byte, 8)}
	srv.previewFanout(c, "")
	<-held
	ask(srv, func() bool { ws.SelectProject(second); return true })
	close(release)

	select {
	case raw := <-c.out:
		var msg fanoutPreviewMsg
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Agent != want {
			t.Fatalf("the preview offered the run to %q, the default of the project switched to; want %q, its own", msg.Agent, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no preview arrived")
	}
}
