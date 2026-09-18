package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"
)

// TodoStepAttempt is one time an agent was started for a todo's step.
type TodoStepAttempt struct {
	PaneID    string    `json:"paneId"`
	StartedAt time.Time `json:"startedAt"`
	// EndedAt and Outcome are filled in once the pane closes, read the same
	// way a fan-out's own job history reads a settled pane's outcome; see
	// captureFanoutHistory and captureTodoStepOutcome. Outcome is "done",
	// "needs" or "failed". Both are zero while the pane is still running,
	// or if the app was closed while it was.
	EndedAt time.Time `json:"endedAt,omitzero"`
	Outcome string    `json:"outcome,omitempty"`
}

// TodoStep is one item of a saved todo's checklist.
type TodoStep struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	// Done is ticked by hand from the checklist, or set automatically once
	// an attempt's pane closes with an outcome that reads as finished (see
	// captureTodoStepOutcome). Either way it is a plain flag the user can
	// always override, in either direction.
	Done bool `json:"done,omitempty"`
	// Attempts records every agent started for this step, oldest first, not
	// only the latest -- a step restarted after a failed attempt keeps the
	// history of why.
	Attempts []TodoStepAttempt `json:"attempts,omitempty"`
}

// Todo is a saved implementation plan: a spec the user wrote, and the steps
// drafted from it by an agent, checked off and started one at a time.
//
// Unlike a fan-out, which starts every task at once and forgets the run the
// moment its tab closes, a todo is meant to be come back to over days: it
// belongs to a project as a whole (Root), not to any one worktree, since a
// worktree is only where a given step's agent happens to run, and several
// may exist for one project.
type Todo struct {
	ID    string     `json:"id"`
	Root  string     `json:"root"`
	Title string     `json:"title"`
	Spec  string     `json:"spec"`
	Steps []TodoStep `json:"steps"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// todosFile holds every project's saved todos, kept apart from the
// per-root layouts and the recent list, the same way groups.json is.
// todosWhat is how the user is told of it.
const (
	todosFile = "todos.json"
	todosWhat = "the saved todo checklists"
)

// LoadTodos returns every saved todo, across every project, or nil when
// none has ever been saved -- the ordinary case for a fresh install.
func LoadTodos() ([]Todo, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, todosFile)
	data, err := readState(path)
	noteRead(path, err)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read todos: %w", err)
	}
	var todos []Todo
	if json.Unmarshal(data, &todos) != nil {
		// A damaged file is not worth failing startup over -- the checklists
		// simply come up empty, which is what a fresh install has anyway --
		// but it is kept rather than silently overwritten by the first save
		// that follows.
		quarantine(path, todosWhat, KeptDamaged)
		return nil, nil
	}
	out := make([]Todo, 0, len(todos))
	for _, t := range todos {
		if t.ID == "" || t.Root == "" {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

// SaveTodos records every todo, replacing whatever was there. A caller with
// none yet need not call this at all; an empty list is written just as
// readily as any other.
func SaveTodos(todos []Todo) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if todos == nil {
		todos = []Todo{}
	}
	data, err := json.MarshalIndent(todos, "", "  ")
	if err != nil {
		return fmt.Errorf("encode todos: %w", err)
	}
	path := filepath.Join(dir, todosFile)
	if err := keepUnread(path, todosWhat); err != nil {
		return fmt.Errorf("write todos: %w", err)
	}
	if err := writeAtomic(path, data); err != nil {
		return fmt.Errorf("write todos: %w", err)
	}
	return nil
}
