package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jmwri/flockdeck/internal/session"
	"github.com/jmwri/flockdeck/internal/store"
)

// tasksFromReply proposes the tasks a fan-out would from a pane whose agent's
// last reply was reply, read from its transcript as a fan-out reads it. The
// pane runs Flockdeck's own chat client, which keeps its transcript where a
// test can write one.
func tasksFromReply(t *testing.T, reply string) []string {
	t.Helper()
	isolateConfig(t)
	base, err := store.Dir()
	if err != nil {
		t.Fatalf("state directory: %v", err)
	}
	chats := filepath.Join(base, "chats")
	if err := os.MkdirAll(chats, 0o700); err != nil {
		t.Fatal(err)
	}
	const id = "44444444-4444-4444-4444-444444444444"
	text, err := json.Marshal(reply)
	if err != nil {
		t.Fatal(err)
	}
	transcript := `{"type":"user","ts":1,"text":"plan it"}` + "\n" +
		`{"type":"assistant","ts":2,"text":` + string(text) + `}` + "\n"
	if err := os.WriteFile(filepath.Join(chats, id+".jsonl"), []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}

	w := &Workspace{panes: map[string]*Pane{
		id: {ID: id, Kind: session.KindClaude, Agent: "anthropic"},
	}}
	if _, ok := w.specFor("", "anthropic"); !ok {
		t.Skip("the catalog has no built-in anthropic agent")
	}
	tasks, fromReply := w.PlanSourceFor(id).Tasks()
	if !fromReply && len(tasks) > 0 {
		t.Fatalf("tasks = %q came from the screen, want the reply", tasks)
	}
	return tasks
}

// TestAReplyEndingInAnOpenCodeBlockKeepsItsPlan covers a reply whose last code
// block is never closed. A screen with an odd number of fences began inside a
// block, and reading a whole reply by that rule put the plan above the block
// inside it: the fan-out offered nothing.
func TestAReplyEndingInAnOpenCodeBlockKeepsItsPlan(t *testing.T) {
	reply := "Here's the plan:\n\n" +
		"1. Add a health endpoint to the HTTP server\n" +
		"2. Write tests for the config parser\n\n" +
		"The endpoint would look like this:\n\n" +
		"```go\n" +
		"func health(w http.ResponseWriter, r *http.Request) {}\n"
	want := []string{"Add a health endpoint to the HTTP server", "Write tests for the config parser"}
	if got := tasksFromReply(t, reply); !reflect.DeepEqual(got, want) {
		t.Errorf("tasks = %q, want %q", got, want)
	}
}
