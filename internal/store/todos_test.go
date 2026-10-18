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

// legacyTodos is a todos.json as the standalone Todo feature wrote it, before
// it was folded into Fan out, with a field this version does not know and a
// step missing its id.
const legacyTodos = `[
  {
    "id": "t1", "root": "C:/code/api", "title": "Ship health endpoint", "spec": "add a health endpoint",
    "steps": [
      {"id": "s1", "text": "add the route", "done": true,
       "attempts": [{"paneId": "p1", "startedAt": "2026-10-01T09:00:00Z", "endedAt": "2026-10-01T09:05:00Z", "outcome": "done"}]},
      {"text": "add a test", "futureField": 3}
    ],
    "futureField": {"a": 1},
    "createdAt": "2026-10-01T08:00:00Z", "updatedAt": "2026-10-01T09:05:00Z"
  },
  {"id": "t2", "root": "C:/code/web", "title": "", "spec": "", "steps": null,
   "createdAt": "2026-10-02T08:00:00Z", "updatedAt": "2026-10-02T08:00:00Z"}
]`

// TestLoadTodosReadsAFileWrittenBeforeTheMergeIntoFanOut checks a saved todo
// from the standalone feature loads whole, survives a save and a second load
// (what the first change made through Fan out does), and that nothing in it
// is lost or crashes on load: unknown fields, a todo with no steps, and a
// step with no id.
func TestLoadTodosReadsAFileWrittenBeforeTheMergeIntoFanOut(t *testing.T) {
	isolateConfig(t)
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, todosFile), []byte(legacyTodos), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := LoadTodos()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("loaded %d todos, want both", len(got))
	}
	first := got[0]
	if first.Title != "Ship health endpoint" || len(first.Steps) != 2 {
		t.Fatalf("first todo = %+v", first)
	}
	if !first.Steps[0].Done || len(first.Steps[0].Attempts) != 1 || first.Steps[0].Attempts[0].Outcome != "done" {
		t.Errorf("step 0 lost its done flag or attempt: %+v", first.Steps[0])
	}
	if first.Steps[1].ID == "" || first.Steps[1].Text != "add a test" {
		t.Errorf("step 1 = %+v, want it kept with an id given to it", first.Steps[1])
	}
	if len(got[1].Steps) != 0 {
		t.Errorf("second todo has steps %v, want none", got[1].Steps)
	}

	if err := SaveTodos(got); err != nil {
		t.Fatalf("save: %v", err)
	}
	again, err := LoadTodos()
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	if len(again) != 2 || again[0].Steps[1].ID != first.Steps[1].ID || !again[0].Steps[0].Done {
		t.Errorf("a save and reload changed the todos: %+v", again)
	}
}
