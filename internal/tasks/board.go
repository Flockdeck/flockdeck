package tasks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jmwri/flockdeck/internal/store"
)

// boardFile is where a project's board is kept, relative to its main checkout.
var boardFile = filepath.Join(".flockdeck", "team", "tasks.json")

// fileVersion is the version of the file's format.
const fileVersion = 1

type file struct {
	Version int    `json:"version"`
	Next    int    `json:"next"`
	Tasks   []Task `json:"tasks"`
}

// Board is one project's tasks and the file they are kept in. It is safe for
// concurrent use: every change reads the file, changes it and writes it back
// under one lock, so a change made by another process between two of this
// process's own is not lost either, as long as the two do not run at the very
// same moment.
type Board struct {
	// Path is the file.
	Path string

	mu  sync.Mutex
	now func() time.Time
	// Logf reports what the board did on its own: a damaged file it set aside.
	Logf func(format string, args ...any)
	// exclude is called once when the file is first created.
	exclude func(root string) (bool, error)
	root    string

	// cache is the parsed file as it was when it had cacheStamp, kept so that a
	// read of a file nobody has written does not parse it again. Every change
	// through the board's own methods drops it, and a change by another process
	// shows as a different stamp. It belongs to mu.
	cache      *file
	cacheStamp string
	// loads counts the times the file was read and parsed, for a test.
	loads atomic.Int64
}

// stampOf says what the file is now, cheaply: its size and modification time,
// or "none" for a file that is not there.
func (b *Board) stampOf() string {
	fi, err := os.Stat(b.Path)
	if err != nil {
		return "none"
	}
	return fmt.Sprintf("%d@%d", fi.Size(), fi.ModTime().UnixNano())
}

// Stamp is a string that changes when the board's file does. It costs a stat.
func (b *Board) Stamp() string { return b.stampOf() }

// Loads is how many times the file has been read and parsed.
func (b *Board) Loads() int64 { return b.loads.Load() }

// Open returns the board of the project whose directory is root. The board lives
// in the project's main checkout, so a root that is a linked worktree gets the
// board of the checkout it was cut from.
func Open(root string) *Board {
	main := MainCheckout(root)
	return &Board{
		Path:    filepath.Join(main, boardFile),
		root:    main,
		now:     time.Now,
		Logf:    log.Printf,
		exclude: EnsureExcluded,
	}
}

// load reads the file. A file that is missing is an empty board. One that does not
// parse is moved aside, under a name that keeps it, and the board starts empty:
// refusing to work until somebody fixes it by hand would stop a team that cannot
// stop to fix it. A file written by a newer Flockdeck is neither: it is an error,
// so an older one does not overwrite it.
func (b *Board) load() (f *file, existed bool, err error) {
	b.loads.Add(1)
	data, err := store.ReadState(b.Path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &file{Version: fileVersion, Next: 1}, false, nil
		}
		return nil, false, fmt.Errorf("read the task board: %w", err)
	}
	var out file
	if err := json.Unmarshal(data, &out); err != nil {
		aside := fmt.Sprintf("%s.corrupt-%d", b.Path, b.now().UnixNano())
		if rerr := os.Rename(b.Path, aside); rerr != nil {
			return nil, true, fmt.Errorf("the task board file is damaged (%v) and could not be moved aside: %w", err, rerr)
		}
		b.logf("flockdeck: the task board file %s could not be read (%v); it was moved to %s and the board starts empty", b.Path, err, aside)
		return &file{Version: fileVersion, Next: 1}, false, nil
	}
	if out.Version > fileVersion {
		return nil, true, fmt.Errorf("the task board at %s was written by a newer Flockdeck (format %d, this one reads %d)", b.Path, out.Version, fileVersion)
	}
	out.Version = fileVersion
	kept := out.Tasks[:0]
	seen := map[string]bool{}
	for _, t := range out.Tasks {
		if idNumber(t.ID) <= 0 || seen[t.ID] {
			continue
		}
		seen[t.ID] = true
		out.Next = max(out.Next, idNumber(t.ID)+1)
		kept = append(kept, t)
	}
	out.Tasks = kept
	out.Next = max(out.Next, 1)
	return &out, true, nil
}

func (b *Board) logf(format string, args ...any) {
	if b.Logf != nil {
		b.Logf(format, args...)
	}
}

// save writes the file. The first time it creates the file it also adds the
// directory to the repository's exclude file.
func (b *Board) save(f *file, existed bool) error {
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(b.Path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if !existed && b.exclude != nil {
		// Before the file is written, so a crash between the two leaves an
		// exclude line with no file, and never a file that git would offer to
		// commit.
		if _, err := b.exclude(b.root); err != nil {
			b.logf("flockdeck: could not add %s to the exclude file of %s: %v", ExcludeLine, b.root, err)
		}
	}
	if err := store.WriteAtomic(b.Path, data); err != nil {
		return fmt.Errorf("write the task board: %w", err)
	}
	return nil
}

// update reads the board, lets fn change it and writes it back. An error from fn
// leaves the file as it was.
func (b *Board) update(fn func(f *file) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Whatever this does to the file, what was kept of it is out of date.
	b.cache = nil
	f, existed, err := b.load()
	if err != nil {
		return err
	}
	if err := fn(f); err != nil {
		return err
	}
	return b.save(f, existed)
}

func (b *Board) view(fn func(f *file) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	// The stamp is taken before the read, so a write that lands between the two
	// leaves the cache holding an older file under an older stamp, and the next
	// read sees they differ.
	stamp := b.stampOf()
	if b.cache != nil && stamp != "none" && stamp == b.cacheStamp {
		return fn(b.cache)
	}
	f, _, err := b.load()
	if err != nil {
		return err
	}
	if stamp != "none" && b.stampOf() == stamp {
		b.cache, b.cacheStamp = f, stamp
	}
	return fn(f)
}

func find(f *file, id string) (*Task, error) {
	for i := range f.Tasks {
		if f.Tasks[i].ID == id {
			return &f.Tasks[i], nil
		}
	}
	return nil, fmt.Errorf("no task %s on this project's board; `flockdeck task list -state all` lists them", id)
}

// Kind says what a change did, which decides who is told of it.
type Kind string

// The kinds of change.
const (
	Added    Kind = "added"
	Assign   Kind = "assigned"
	Move     Kind = "moved"
	NoteKind Kind = "noted"
	Branch   Kind = "branch"
	Baton    Kind = "baton"
	Depends  Kind = "depends"
	Estimate Kind = "estimate"
)

// Change is what one operation did to a task.
type Change struct {
	Kind Kind
	// Task is the task after the change.
	Task Task
	// From is the state before a move.
	From State
	// Note is the text of the note a change added, if it added one.
	Note string
	// Claimed says a move gave the task to the pane that made it.
	Claimed bool
	// Overrode is who owned the task when an assign took it from them: the task
	// was in progress under a different owner. It is empty for any other change.
	Overrode string
}

// NewTask is what a task is made from.
type NewTask struct {
	Title, Body, Role, Branch string
}

// Add puts a task on the board in the planned state.
func (b *Board) Add(a Actor, in NewTask) (Change, error) {
	title, err := CleanTitle(in.Title)
	if err != nil {
		return Change{}, err
	}
	body, err := CleanBody(in.Body)
	if err != nil {
		return Change{}, err
	}
	var role, branch string
	if in.Role != "" {
		if role, err = CleanRole(in.Role); err != nil {
			return Change{}, err
		}
	}
	if in.Branch != "" {
		if branch, err = CleanBranch(in.Branch); err != nil {
			return Change{}, err
		}
	}
	var ch Change
	err = b.update(func(f *file) error {
		if len(f.Tasks) >= MaxTasks && !dropOldestFinished(f) {
			return fmt.Errorf("the board holds %d tasks, all of them open, which is the most it takes; finish or cancel some, then `flockdeck task prune` removes old finished ones", MaxTasks)
		}
		now := b.now()
		t := Task{
			ID: fmt.Sprintf("T%d", f.Next), Title: title, Body: body, OwnerRole: role,
			Creator: a, State: Planned, Branch: branch, Created: now, Updated: now,
		}
		t.addEvent(a, now, "added the task")
		f.Next++
		f.Tasks = append(f.Tasks, t)
		ch = Change{Kind: Added, Task: t}
		return nil
	})
	return ch, err
}

// dropOldestFinished removes the done or cancelled task that was changed least
// recently, to make room on a full board. It reports false when every task is
// open, and then removes nothing.
func dropOldestFinished(f *file) bool {
	at := -1
	for i, t := range f.Tasks {
		if t.State.Open() {
			continue
		}
		if at < 0 || t.Updated.Before(f.Tasks[at].Updated) ||
			t.Updated.Equal(f.Tasks[at].Updated) && idNumber(t.ID) < idNumber(f.Tasks[at].ID) {
			at = i
		}
	}
	if at < 0 {
		return false
	}
	f.Tasks = append(f.Tasks[:at], f.Tasks[at+1:]...)
	return true
}

// DefaultPruneDays is how old a finished task must be for `task prune` to
// remove it when no age is given.
const DefaultPruneDays = 30

// Prune removes the done and cancelled tasks that were last changed more than
// days days ago, and says how many it removed. Open tasks are never removed.
func (b *Board) Prune(a Actor, hasManager bool, days int) (int, error) {
	if err := CheckPrune(a, hasManager); err != nil {
		return 0, err
	}
	if days < 0 {
		return 0, fmt.Errorf("the age in days cannot be negative")
	}
	n := 0
	err := b.update(func(f *file) error {
		cut := b.now().Add(-time.Duration(days) * 24 * time.Hour)
		kept := f.Tasks[:0]
		for _, t := range f.Tasks {
			if !t.State.Open() && t.Updated.Before(cut) {
				n++
				continue
			}
			kept = append(kept, t)
		}
		f.Tasks = kept
		if n == 0 {
			return errNothingToWrite
		}
		return nil
	})
	if errors.Is(err, errNothingToWrite) {
		err = nil
	}
	return n, err
}

var errNothingToWrite = errors.New("nothing to write")

// Filter says which tasks List returns.
type Filter struct {
	// State limits the list to one state. Empty is every task that is not
	// finished, and All is every task.
	State State
	All   bool
	// Role limits it to tasks for a role.
	Role string
	// Mine limits it to tasks the actor Me owns.
	Mine bool
	Me   Actor
}

// List returns the tasks that match, oldest first, and how many finished tasks it
// left out because no state was asked for.
func (b *Board) List(flt Filter) (out []Task, hidden int, err error) {
	err = b.view(func(f *file) error {
		for _, t := range f.Tasks {
			switch {
			case flt.State != "" && t.State != flt.State:
				continue
			case flt.State == "" && !flt.All && !t.State.Open():
				hidden++
				continue
			case flt.Role != "" && t.OwnerRole != flt.Role:
				continue
			case flt.Mine && !IsOwner(t, flt.Me):
				continue
			}
			out = append(out, t)
		}
		return nil
	})
	sort.SliceStable(out, func(i, j int) bool { return idNumber(out[i].ID) < idNumber(out[j].ID) })
	return out, hidden, err
}

// Get returns one task.
func (b *Board) Get(id string) (Task, error) {
	var t Task
	err := b.view(func(f *file) error {
		p, err := find(f, id)
		if err != nil {
			return err
		}
		t = *p
		return nil
	})
	return t, err
}

// Assignee is who a task is assigned to.
type Assignee struct {
	Pane string
	Role string
	Name string
}

// Assign gives a task an owner. A planned task becomes assigned.
func (b *Board) Assign(a Actor, hasManager bool, id string, o Assignee) (Change, error) {
	var ch Change
	err := b.update(func(f *file) error {
		t, err := find(f, id)
		if err != nil {
			return err
		}
		if err := CheckAssign(*t, a, hasManager); err != nil {
			return err
		}
		from := t.State
		// An assign after the owner has started the work takes it from them. It is
		// allowed, since the manager may need to, but the caller is told.
		overrode := ""
		if t.State == InProgress && t.OwnerPane != "" && (o.Pane != t.OwnerPane) {
			overrode = OwnerLabel(*t) + " (pane " + t.OwnerPane + ")"
		}
		t.OwnerPane, t.OwnerRole, t.OwnerName = o.Pane, o.Role, o.Name
		if t.State == Planned {
			t.State = Assigned
		}
		t.Updated = b.now()
		t.addEvent(a, t.Updated, "assigned the task to "+OwnerLabel(*t))
		ch = Change{Kind: Assign, Task: *t, From: from, Overrode: overrode}
		return nil
	})
	return ch, err
}

// SetState moves a task to a state, if the rules let the actor.
func (b *Board) SetState(a Actor, hasManager bool, id string, to State, note string) (Change, error) {
	var text string
	if strings.TrimSpace(note) != "" {
		var err error
		if text, err = CleanNote(note); err != nil {
			return Change{}, err
		}
	}
	var ch Change
	err := b.update(func(f *file) error {
		t, err := find(f, id)
		if err != nil {
			return err
		}
		if err := CheckMove(*t, to, a, hasManager, text); err != nil {
			return err
		}
		from := t.State
		claimed := false
		// A task for a role that no pane has yet goes to the pane that starts it.
		if to == InProgress && t.OwnerPane == "" && t.OwnerRole != "" && IsOwner(*t, a) {
			t.OwnerPane, t.OwnerName = a.Pane, a.Name
			claimed = true
		}
		t.State = to
		now := b.now()
		t.Updated = now
		t.addEvent(a, now, fmt.Sprintf("moved the task from %s to %s", from, to))
		if text != "" {
			t.addNote(Note{Author: a, Time: now, Text: text})
		}
		ch = Change{Kind: Move, Task: *t, From: from, Note: text, Claimed: claimed}
		return nil
	})
	return ch, err
}

// AddNote adds a note to a task's thread. Anyone on the team may, and on a task
// of any state.
func (b *Board) AddNote(a Actor, id, text string) (Change, error) {
	text, err := CleanNote(text)
	if err != nil {
		return Change{}, err
	}
	var ch Change
	err = b.update(func(f *file) error {
		t, err := find(f, id)
		if err != nil {
			return err
		}
		now := b.now()
		t.addNote(Note{Author: a, Time: now, Text: text})
		t.Updated = now
		ch = Change{Kind: NoteKind, Task: *t, Note: text}
		return nil
	})
	return ch, err
}

func (t *Task) addNote(n Note) {
	if len(t.Notes) >= MaxNotes {
		t.Notes = append(t.Notes[:0:0], t.Notes[len(t.Notes)-MaxNotes+1:]...)
	}
	t.Notes = append(t.Notes, n)
}

// addEvent appends to the task's history, dropping the oldest entry when it is
// full.
func (t *Task) addEvent(a Actor, at time.Time, what string) {
	if len(t.History) >= MaxHistory {
		t.History = append(t.History[:0:0], t.History[len(t.History)-MaxHistory+1:]...)
	}
	t.History = append(t.History, Event{Actor: a, Time: at, What: what})
}

// SetBranch records the branch a task's work is on.
func (b *Board) SetBranch(a Actor, hasManager bool, id, branch string) (Change, error) {
	branch, err := CleanBranch(branch)
	if err != nil {
		return Change{}, err
	}
	var ch Change
	err = b.update(func(f *file) error {
		t, err := find(f, id)
		if err != nil {
			return err
		}
		if err := CheckEdit(*t, a, hasManager); err != nil {
			return err
		}
		t.Branch = branch
		t.Updated = b.now()
		t.addEvent(a, t.Updated, "set the branch to "+branch)
		ch = Change{Kind: Branch, Task: *t}
		return nil
	})
	return ch, err
}

// CleanDepends checks the ids a task is to wait on against the board: each must
// be a task there other than the task itself, none twice, at most MaxDepends,
// and the new edges must not close a loop. It returns the ids as the board
// spells them, in the order given. No ids clears the list.
func CleanDepends(f []Task, id string, ids []string) ([]string, error) {
	if len(ids) > MaxDepends {
		return nil, fmt.Errorf("%s may wait on at most %d tasks, %d were given", id, MaxDepends, len(ids))
	}
	byID := map[string]Task{}
	for _, t := range f {
		byID[t.ID] = t
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, raw := range ids {
		dep, err := ParseID(raw)
		if err != nil {
			return nil, err
		}
		switch {
		case dep == id:
			return nil, fmt.Errorf("%s cannot wait on itself", id)
		case seen[dep]:
			return nil, fmt.Errorf("%s is listed twice", dep)
		}
		if _, ok := byID[dep]; !ok {
			return nil, fmt.Errorf("no task %s on this project's board to wait on; `flockdeck task list -state all` lists them", dep)
		}
		seen[dep] = true
		out = append(out, dep)
	}
	// A loop is a path from a dependency back to this task through the tasks'
	// own lists, so walk from each new dependency and see whether id is reached.
	visited := map[string]bool{}
	var reaches func(from string) bool
	reaches = func(from string) bool {
		if from == id {
			return true
		}
		if visited[from] {
			return false
		}
		visited[from] = true
		for _, next := range byID[from].Depends {
			if reaches(next) {
				return true
			}
		}
		return false
	}
	for _, dep := range out {
		if reaches(dep) {
			return nil, fmt.Errorf("%s waiting on %s would make a loop: %s already waits on %s, directly or through other tasks", id, dep, dep, id)
		}
	}
	return out, nil
}

// SetDepends records which tasks a task waits on. An empty list clears it.
func (b *Board) SetDepends(a Actor, hasManager bool, id string, ids []string) (Change, error) {
	var ch Change
	err := b.update(func(f *file) error {
		t, err := find(f, id)
		if err != nil {
			return err
		}
		if err := CheckEdit(*t, a, hasManager); err != nil {
			return err
		}
		deps, err := CleanDepends(f.Tasks, t.ID, ids)
		if err != nil {
			return err
		}
		if len(deps) == 0 {
			deps = nil
		}
		t.Depends = deps
		t.Updated = b.now()
		what := "cleared what the task waits on"
		if len(deps) > 0 {
			what = "set the task to wait on " + strings.Join(deps, ", ")
		}
		t.addEvent(a, t.Updated, what)
		ch = Change{Kind: Depends, Task: *t}
		return nil
	})
	return ch, err
}

// SetEstimate records how long a task is thought to take, in minutes. Zero
// clears it.
func (b *Board) SetEstimate(a Actor, hasManager bool, id string, minutes int) (Change, error) {
	if minutes < 0 || minutes > MaxEstimateMin {
		return Change{}, fmt.Errorf("an estimate is between 0 and %d minutes, %d was given", MaxEstimateMin, minutes)
	}
	var ch Change
	err := b.update(func(f *file) error {
		t, err := find(f, id)
		if err != nil {
			return err
		}
		if err := CheckEdit(*t, a, hasManager); err != nil {
			return err
		}
		t.EstimateMin = minutes
		t.Updated = b.now()
		what := "cleared the estimate"
		if minutes > 0 {
			what = fmt.Sprintf("estimated the task at %d minutes", minutes)
		}
		t.addEvent(a, t.Updated, what)
		ch = Change{Kind: Estimate, Task: *t}
		return nil
	})
	return ch, err
}

// SetBaton points a task at the baton that gives it its context. The baton is
// not read or changed, and it is not replaced by the task: a baton is a handoff
// document that stays as it was written.
func (b *Board) SetBaton(a Actor, hasManager bool, id, baton string) (Change, error) {
	var ch Change
	err := b.update(func(f *file) error {
		t, err := find(f, id)
		if err != nil {
			return err
		}
		if err := CheckEdit(*t, a, hasManager); err != nil {
			return err
		}
		t.Baton = baton
		t.Updated = b.now()
		t.addEvent(a, t.Updated, "pointed the task at baton "+baton)
		ch = Change{Kind: Baton, Task: *t}
		return nil
	})
	return ch, err
}

// Counts is what the board holds, from the point of view of one pane.
type Counts struct {
	// Total is every task that is not finished.
	Total int
	// Mine is the open tasks the pane owns, and MineActive those of them that
	// are in progress. Unclaimed is the part of Mine that is assigned to the
	// pane's role and that no pane has taken yet; the rest are the pane's own.
	Mine, MineActive, Unclaimed int
	// InReview is the tasks in review that are not the pane's own.
	InReview int
	// Unassigned is the planned tasks that have no owner.
	Unassigned int
}

// Counts reads the board for a pane.
func (b *Board) Counts(me Actor) (Counts, error) {
	var c Counts
	err := b.view(func(f *file) error {
		for _, t := range f.Tasks {
			if !t.State.Open() {
				continue
			}
			c.Total++
			own := IsOwner(t, me)
			if own {
				c.Mine++
				if t.OwnerPane == "" {
					c.Unclaimed++
				}
				if t.State == InProgress {
					c.MineActive++
				}
			}
			if t.State == InReview && !own {
				c.InReview++
			}
			if t.State == Planned && !t.Owned() {
				c.Unassigned++
			}
		}
		return nil
	})
	return c, err
}

// Phrase says the counts in a sentence, or that the board has nothing open.
func (c Counts) Phrase() string {
	if c.Total == 0 {
		return "the board has no open tasks"
	}
	plural := func(n int, what string) string {
		if n == 1 {
			return fmt.Sprintf("1 task %s", what)
		}
		return fmt.Sprintf("%d tasks %s", n, what)
	}
	var parts []string
	if claimed := c.Mine - c.Unclaimed; claimed > 0 {
		s := plural(claimed, "assigned to you")
		if c.MineActive > 0 {
			s += fmt.Sprintf(" (%d in progress)", c.MineActive)
		}
		parts = append(parts, s)
	}
	if c.Unclaimed > 0 {
		// A task for a role is nobody's until a pane of the role starts it.
		if c.Mine == c.Unclaimed {
			parts = append(parts, plural(c.Unclaimed, "for your role, none claimed yet"))
		} else {
			parts = append(parts, plural(c.Unclaimed, "for your role, not claimed yet"))
		}
	}
	if c.InReview > 0 {
		parts = append(parts, fmt.Sprintf("%d in review", c.InReview))
	}
	if c.Unassigned > 0 {
		parts = append(parts, fmt.Sprintf("%d planned and not assigned", c.Unassigned))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("%d open, none assigned to you or in review", c.Total)
	}
	return strings.Join(parts, ", ")
}
