package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLoadTodosRoundTrip checks a saved todo, its steps and their attempt
// history all read back exactly as they were written, and that nothing ever
// saved reads back as none rather than as an error -- the ordinary case for
// a user who has never used the feature.
func TestLoadTodosRoundTrip(t *testing.T) {
	isolateConfig(t)

	if got, err := LoadTodos(); err != nil || len(got) != 0 {
		t.Fatalf("LoadTodos with nothing saved = %v, %v; want none, no error", got, err)
	}

	now := time.Now().Round(time.Second)
	want := []Todo{
		{
			ID: "t1", Root: "/code/api", Title: "add health endpoint", Spec: "add a health endpoint",
			Steps: []TodoStep{
				{ID: "s1", Text: "add the route", Done: true, Attempts: []TodoStepAttempt{
					{PaneID: "p1", StartedAt: now, EndedAt: now.Add(time.Minute), Outcome: "done"},
				}},
				{ID: "s2", Text: "add a test"},
			},
			CreatedAt: now, UpdatedAt: now,
		},
	}
	if err := SaveTodos(want); err != nil {
		t.Fatalf("save todos: %v", err)
	}
	got, err := LoadTodos()
	if err != nil {
		t.Fatalf("load todos: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("loaded %d todos, want 1", len(got))
	}
	if got[0].ID != "t1" || got[0].Title != "add health endpoint" || len(got[0].Steps) != 2 {
		t.Fatalf("todo = %+v, want %+v", got[0], want[0])
	}
	if len(got[0].Steps[0].Attempts) != 1 || got[0].Steps[0].Attempts[0].Outcome != "done" {
		t.Fatalf("step 0 attempts = %+v, want one attempt marked done", got[0].Steps[0].Attempts)
	}
	if !got[0].Steps[0].Done {
		t.Error("step 0 should have kept its Done flag")
	}
	if got[0].Steps[1].Done {
		t.Error("step 1 was never ticked and should not read back done")
	}
}

// TestLoadTodosDamagedFileIsKept checks a todos.json that does not parse is
// quarantined rather than silently dropped, and startup is not failed over
// it -- the same treatment groups.json and every other state file get.
func TestLoadTodosDamagedFileIsKept(t *testing.T) {
	isolateConfig(t)
	dir, err := Dir()
	if err != nil {
		t.Fatalf("dir: %v", err)
	}
	path := filepath.Join(dir, todosFile)
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatalf("write damaged file: %v", err)
	}

	got, err := LoadTodos()
	if err != nil {
		t.Fatalf("a damaged todos file should not fail startup: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v from a damaged file, want none", got)
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("the damaged file should have been moved aside, not left in place")
	}
}

// TestLoadTodosDropsEmptyEntries checks a todo with no id or no project root
// -- which a hand-edited file could hold -- is dropped rather than handed
// back to a caller that assumes every entry names something real.
func TestLoadTodosDropsEmptyEntries(t *testing.T) {
	isolateConfig(t)
	if err := SaveTodos([]Todo{
		{ID: "", Root: "/code/api", Steps: []TodoStep{{ID: "s1", Text: "x"}}},
		{ID: "t1", Root: "", Steps: []TodoStep{{ID: "s1", Text: "x"}}},
		{ID: "t2", Root: "/code/web", Steps: []TodoStep{{ID: "s1", Text: "x"}}},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadTodos()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 1 || got[0].ID != "t2" {
		t.Fatalf("got %v, want only t2", got)
	}
}
