package tasks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	mgr     = Actor{Pane: "pm", Role: "manager", Name: "boss"}
	planner = Actor{Pane: "pp", Role: "planner", Name: "plan"}
	w1      = Actor{Pane: "w1", Role: "worker", Name: "dev-one"}
	w2      = Actor{Pane: "w2", Role: "worker", Name: "dev-two"}
	rev     = Actor{Pane: "pr", Role: "reviewer", Name: "eyes"}
)

// testBoard opens a board in a fresh directory with a clock that moves a minute
// at every reading, and a log that is kept.
func testBoard(t *testing.T) (*Board, *[]string) {
	t.Helper()
	return testBoardIn(t, t.TempDir())
}

func testBoardIn(t *testing.T, dir string) (*Board, *[]string) {
	t.Helper()
	b := Open(dir)
	var logs []string
	var mu sync.Mutex
	b.Logf = func(f string, a ...any) {
		mu.Lock()
		logs = append(logs, fmt.Sprintf(f, a...))
		mu.Unlock()
	}
	at := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	var cm sync.Mutex
	b.now = func() time.Time {
		cm.Lock()
		defer cm.Unlock()
		at = at.Add(time.Minute)
		return at
	}
	return b, &logs
}

func mustAdd(t *testing.T, b *Board, a Actor, title string) Task {
	t.Helper()
	ch, err := b.Add(a, NewTask{Title: title})
	if err != nil {
		t.Fatalf("add %q: %v", title, err)
	}
	return ch.Task
}

func TestAddWritesAPrivateFileAtomically(t *testing.T) {
	b, _ := testBoard(t)
	task := mustAdd(t, b, planner, "write the parser")
	if task.ID != "T1" || task.State != Planned || task.Creator != planner {
		t.Errorf("task = %+v", task)
	}
	data, err := os.ReadFile(b.Path)
	if err != nil {
		t.Fatal(err)
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil || len(f.Tasks) != 1 || f.Next != 2 || f.Version != fileVersion {
		t.Fatalf("file = %+v, %v", f, err)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(b.Path)
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
		}
	}
	// No temporary file is left beside it, after one write or many.
	for i := 0; i < 5; i++ {
		mustAdd(t, b, planner, fmt.Sprintf("task %d", i))
	}
	entries, _ := os.ReadDir(filepath.Dir(b.Path))
	if len(entries) != 1 || entries[0].Name() != "tasks.json" {
		t.Errorf("the directory holds %v, want tasks.json alone", entries)
	}
}

func TestAMissingFileIsAnEmptyBoard(t *testing.T) {
	b, logs := testBoard(t)
	list, hidden, err := b.List(Filter{})
	if err != nil || len(list) != 0 || hidden != 0 {
		t.Errorf("list = %v, %d, %v", list, hidden, err)
	}
	if _, err := os.Stat(b.Path); err == nil {
		t.Error("reading created the file")
	}
	if len(*logs) != 0 {
		t.Errorf("logged %v", *logs)
	}
}

func TestACorruptFileIsMovedAsideAndTheBoardStartsEmpty(t *testing.T) {
	b, logs := testBoard(t)
	if err := os.MkdirAll(filepath.Dir(b.Path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b.Path, []byte(`{"tasks": [ this is not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	task := mustAdd(t, b, mgr, "after the damage")
	if task.ID != "T1" {
		t.Errorf("id = %s, want the board to start again at T1", task.ID)
	}
	matches, _ := filepath.Glob(b.Path + ".corrupt-*")
	if len(matches) != 1 {
		t.Fatalf("set-aside files = %v, want one", matches)
	}
	if aside, _ := os.ReadFile(matches[0]); !strings.Contains(string(aside), "this is not json") {
		t.Errorf("the set-aside file lost the original: %q", aside)
	}
	if len(*logs) != 1 || !strings.Contains((*logs)[0], "moved to") {
		t.Errorf("log = %v, want one line saying where the file went", *logs)
	}
	if list, _, _ := b.List(Filter{}); len(list) != 1 {
		t.Errorf("list = %v", list)
	}
}

func TestAnEmptyFileCountsAsDamaged(t *testing.T) {
	b, logs := testBoard(t)
	_ = os.MkdirAll(filepath.Dir(b.Path), 0o700)
	_ = os.WriteFile(b.Path, nil, 0o600)
	mustAdd(t, b, mgr, "x")
	if len(*logs) != 1 {
		t.Errorf("log = %v", *logs)
	}
}

func TestAFileFromANewerVersionIsNotOverwritten(t *testing.T) {
	b, _ := testBoard(t)
	_ = os.MkdirAll(filepath.Dir(b.Path), 0o700)
	future := `{"version": 99, "next": 5, "tasks": []}`
	_ = os.WriteFile(b.Path, []byte(future), 0o600)
	if _, err := b.Add(mgr, NewTask{Title: "x"}); err == nil || !strings.Contains(err.Error(), "newer Flockdeck") {
		t.Errorf("err = %v", err)
	}
	if got, _ := os.ReadFile(b.Path); string(got) != future {
		t.Errorf("the file was changed: %s", got)
	}
}

func TestIDsContinueAcrossARestart(t *testing.T) {
	dir := t.TempDir()
	b, _ := testBoardIn(t, dir)
	for i := 1; i <= 3; i++ {
		if got := mustAdd(t, b, mgr, fmt.Sprintf("t%d", i)).ID; got != fmt.Sprintf("T%d", i) {
			t.Errorf("id = %s, want T%d", got, i)
		}
	}
	again, _ := testBoardIn(t, dir)
	if got := mustAdd(t, again, mgr, "after restart").ID; got != "T4" {
		t.Errorf("id after a restart = %s, want T4", got)
	}
	// An id is not reused when the newest task is cancelled either, and a file
	// that lost its counter still goes past the highest id in it.
	data, _ := os.ReadFile(b.Path)
	var f file
	_ = json.Unmarshal(data, &f)
	f.Next = 0
	data, _ = json.Marshal(f)
	_ = os.WriteFile(b.Path, data, 0o600)
	if got := mustAdd(t, again, mgr, "no counter").ID; got != "T5" {
		t.Errorf("id with no counter = %s, want T5", got)
	}
}

func TestCapsOnTitleBodyNoteAndBranch(t *testing.T) {
	b, _ := testBoard(t)
	if _, err := b.Add(mgr, NewTask{Title: strings.Repeat("é", MaxTitle)}); err != nil {
		t.Errorf("a title of %d characters was refused: %v", MaxTitle, err)
	}
	if _, err := b.Add(mgr, NewTask{Title: strings.Repeat("a", MaxTitle+1)}); err == nil {
		t.Error("a title over the limit was accepted")
	}
	if _, err := b.Add(mgr, NewTask{Title: "  \n\t "}); err == nil {
		t.Error("an empty title was accepted")
	}
	if _, err := b.Add(mgr, NewTask{Title: "t", Body: strings.Repeat("b", MaxBody)}); err != nil {
		t.Errorf("a body of %d bytes was refused: %v", MaxBody, err)
	}
	if _, err := b.Add(mgr, NewTask{Title: "t", Body: strings.Repeat("b", MaxBody+1)}); err == nil {
		t.Error("a body over the limit was accepted")
	}
	task := mustAdd(t, b, mgr, "notes")
	if _, err := b.AddNote(w1, task.ID, strings.Repeat("n", MaxNote)); err != nil {
		t.Errorf("a note of %d bytes was refused: %v", MaxNote, err)
	}
	if _, err := b.AddNote(w1, task.ID, strings.Repeat("n", MaxNote+1)); err == nil {
		t.Error("a note over the limit was accepted")
	}
	if _, err := b.AddNote(w1, task.ID, "   "); err == nil {
		t.Error("an empty note was accepted")
	}
	for _, bad := range []string{"has space", "new\nline", "tab\tbed", strings.Repeat("x", MaxBranch+1), ""} {
		if _, err := b.SetBranch(mgr, task.ID, bad); err == nil {
			t.Errorf("branch %q was accepted", bad)
		}
	}
	if _, err := b.SetBranch(mgr, task.ID, "feat/ünï"); err != nil {
		t.Errorf("a branch with accents was refused: %v", err)
	}
}

func TestTheNotesThreadKeepsTheNewestHundred(t *testing.T) {
	b, _ := testBoard(t)
	task := mustAdd(t, b, mgr, "chatty")
	for i := 1; i <= MaxNotes+7; i++ {
		if _, err := b.AddNote(w1, task.ID, fmt.Sprintf("note %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := b.Get(task.ID)
	if len(got.Notes) != MaxNotes {
		t.Fatalf("notes = %d, want %d", len(got.Notes), MaxNotes)
	}
	if got.Notes[0].Text != "note 8" || got.Notes[MaxNotes-1].Text != fmt.Sprintf("note %d", MaxNotes+7) {
		t.Errorf("thread runs from %q to %q", got.Notes[0].Text, got.Notes[MaxNotes-1].Text)
	}
}

func TestABoardThatIsFullRefusesNewTasks(t *testing.T) {
	b, _ := testBoard(t)
	f := file{Version: fileVersion, Next: MaxTasks + 1}
	for i := 1; i <= MaxTasks; i++ {
		f.Tasks = append(f.Tasks, Task{ID: fmt.Sprintf("T%d", i), Title: "x", State: Done})
	}
	data, _ := json.Marshal(f)
	_ = os.MkdirAll(filepath.Dir(b.Path), 0o700)
	_ = os.WriteFile(b.Path, data, 0o600)
	if _, err := b.Add(mgr, NewTask{Title: "one too many"}); err == nil || !strings.Contains(err.Error(), "most it takes") {
		t.Errorf("err = %v", err)
	}
}

func TestConcurrentWritersLoseNothing(t *testing.T) {
	b, _ := testBoard(t)
	const writers, each = 8, 6
	var wg sync.WaitGroup
	ids := make(chan string, writers*each)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				ch, err := b.Add(mgr, NewTask{Title: fmt.Sprintf("w%d-%d", w, i)})
				if err != nil {
					t.Error(err)
					return
				}
				ids <- ch.Task.ID
				if _, err := b.AddNote(w1, ch.Task.ID, "n"); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Errorf("id %s was given twice", id)
		}
		seen[id] = true
	}
	list, _, err := b.List(Filter{})
	if err != nil || len(list) != writers*each {
		t.Fatalf("tasks = %d, %v; want %d", len(list), err, writers*each)
	}
	for _, task := range list {
		if len(task.Notes) != 1 {
			t.Errorf("%s has %d notes, want 1", task.ID, len(task.Notes))
		}
	}
}

func TestListFiltersAndHidesFinishedTasks(t *testing.T) {
	b, _ := testBoard(t)
	a := mustAdd(t, b, mgr, "a")
	c := mustAdd(t, b, mgr, "b")
	d := mustAdd(t, b, mgr, "c")
	if _, err := b.Assign(mgr, a.ID, Assignee{Pane: w1.Pane, Role: "worker", Name: w1.Name}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Assign(mgr, c.ID, Assignee{Role: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetState(mgr, d.ID, Cancelled, ""); err != nil {
		t.Fatal(err)
	}
	ids := func(f Filter) string {
		list, _, err := b.List(f)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, task := range list {
			out = append(out, task.ID)
		}
		return strings.Join(out, ",")
	}
	if got := ids(Filter{}); got != "T1,T2" {
		t.Errorf("default = %s", got)
	}
	if _, hidden, _ := b.List(Filter{}); hidden != 1 {
		t.Errorf("hidden = %d", hidden)
	}
	if got := ids(Filter{All: true}); got != "T1,T2,T3" {
		t.Errorf("all = %s", got)
	}
	if got := ids(Filter{State: Cancelled}); got != "T3" {
		t.Errorf("cancelled = %s", got)
	}
	if got := ids(Filter{Role: "reviewer"}); got != "T2" {
		t.Errorf("role = %s", got)
	}
	if got := ids(Filter{Mine: true, Me: w1}); got != "T1" {
		t.Errorf("mine for w1 = %s", got)
	}
	if got := ids(Filter{Mine: true, Me: rev}); got != "T2" {
		t.Errorf("mine for a reviewer, by role = %s", got)
	}
	if got := ids(Filter{Mine: true, Me: w2}); got != "" {
		t.Errorf("mine for w2 = %s", got)
	}
}

func TestCountsFromOnePanesPointOfView(t *testing.T) {
	b, _ := testBoard(t)
	mine1 := mustAdd(t, b, mgr, "mine 1")
	mine2 := mustAdd(t, b, mgr, "mine 2")
	other := mustAdd(t, b, mgr, "other's")
	mustAdd(t, b, mgr, "nobody's")
	mustAdd(t, b, mgr, "nobody's either")
	closed := mustAdd(t, b, mgr, "cancelled")
	for _, id := range []string{mine1.ID, mine2.ID} {
		if _, err := b.Assign(mgr, id, Assignee{Pane: w1.Pane, Role: "worker", Name: w1.Name}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := b.Assign(mgr, other.ID, Assignee{Pane: w2.Pane, Role: "worker"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetState(w1, mine1.ID, InProgress, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetState(w2, other.ID, InProgress, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetState(w2, other.ID, InReview, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetState(mgr, closed.ID, Cancelled, ""); err != nil {
		t.Fatal(err)
	}
	c, err := b.Counts(w1)
	if err != nil {
		t.Fatal(err)
	}
	want := Counts{Total: 5, Mine: 2, MineActive: 1, InReview: 1, Unassigned: 2}
	if c != want {
		t.Errorf("counts = %+v, want %+v", c, want)
	}
	if got := c.Phrase(); got != "2 tasks assigned to you (1 in progress), 1 in review, 2 planned and not assigned" {
		t.Errorf("phrase = %q", got)
	}
	// Seen by the owner of the one in review, it is not counted as waiting.
	if c2, _ := b.Counts(w2); c2.InReview != 0 || c2.Mine != 1 {
		t.Errorf("counts for w2 = %+v", c2)
	}
	empty, _ := testBoard(t)
	if c3, _ := empty.Counts(w1); c3.Phrase() != "the board has no open tasks" {
		t.Errorf("empty phrase = %q", c3.Phrase())
	}
	if p := (Counts{Total: 3}).Phrase(); !strings.Contains(p, "3 open") {
		t.Errorf("phrase = %q", p)
	}
	if p := (Counts{Total: 1, Mine: 1}).Phrase(); p != "1 task assigned to you" {
		t.Errorf("phrase = %q", p)
	}
}

func TestParseIDAndState(t *testing.T) {
	for in, want := range map[string]string{"T3": "T3", "t3": "T3", "#3": "T3", " 12 ": "T12", "T#7": "T7"} {
		if got, err := ParseID(in); err != nil || got != want {
			t.Errorf("ParseID(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "T", "T0", "x3", "T3a", "T-1", "3\n4", "T99999999999"} {
		if _, err := ParseID(in); err == nil {
			t.Errorf("ParseID(%q) succeeded", in)
		}
	}
	for in, want := range map[string]State{
		"planned": Planned, "In-Progress": InProgress, "in_progress": InProgress, "in progress": InProgress,
		"inreview": InReview, "DONE": Done, "canceled": Cancelled, "blocked": Blocked,
	} {
		if got, err := ParseState(in); err != nil || got != want {
			t.Errorf("ParseState(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseState("finished"); err == nil || !strings.Contains(err.Error(), "in-progress") {
		t.Errorf("err = %v, want the list of states", err)
	}
}
