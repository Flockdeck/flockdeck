package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/hooks"
	"github.com/jmwri/flockdeck/internal/tasks"
)

// recorder is a stand-in for the application: it keeps the requests it was
// sent and answers with res.
type recorder struct {
	reqs []hooks.TaskRequest
	res  hooks.TaskResult
	err  error
}

func (r *recorder) call(req hooks.TaskRequest) (hooks.TaskResult, error) {
	r.reqs = append(r.reqs, req)
	return r.res, r.err
}

func sampleTask() tasks.Task {
	at := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	return tasks.Task{
		ID: "T3", Title: "fix the parser", State: tasks.InProgress, OwnerPane: "w1", OwnerRole: "worker", OwnerName: "dev",
		Creator: tasks.Actor{Pane: "pm", Role: "manager", Name: "boss"}, Branch: "feat/parser", Created: at, Updated: at,
	}
}

func runTaskArgs(t *testing.T, r *recorder, stdin string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runTaskWith(r.call, args, strings.NewReader(stdin), &out)
	return out.String(), err
}

func TestTaskAddTakesFlagsBeforeOrAfterTheTitle(t *testing.T) {
	for _, args := range [][]string{
		{"add", "fix", "the", "parser", "-role", "worker", "-body", "details here", "-branch", "feat/p"},
		{"add", "-role", "worker", "fix the parser", "-body", "details here", "-branch", "feat/p"},
		{"add", "-body=details here", "-branch=feat/p", "-role=worker", "fix", "the parser"},
	} {
		r := &recorder{res: hooks.TaskResult{Task: ptr(sampleTask())}}
		out, err := runTaskArgs(t, r, "", args...)
		if err != nil {
			t.Fatalf("%q: %v", args, err)
		}
		if len(r.reqs) != 1 {
			t.Fatalf("%q: %d requests", args, len(r.reqs))
		}
		got := r.reqs[0]
		if got.Op != hooks.TaskAdd || got.Title != "fix the parser" || got.Body != "details here" || got.Role != "worker" || got.Branch != "feat/p" {
			t.Errorf("%q: request = %+v", args, got)
		}
		if !strings.HasPrefix(out, "added T3 [in-progress] fix the parser (worker dev, branch feat/parser)") {
			t.Errorf("output = %q", out)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func TestTaskAddReadsABodyFromStandardInputAndAfterADoubleDash(t *testing.T) {
	r := &recorder{res: hooks.TaskResult{Task: ptr(sampleTask())}}
	if _, err := runTaskArgs(t, r, "line one\nline two\n", "add", "-body", "-", "the title"); err != nil {
		t.Fatal(err)
	}
	if got := r.reqs[0]; got.Body != "line one\nline two\n" || got.Title != "the title" {
		t.Errorf("request = %+v", got)
	}
	// A title that starts with a dash goes after --.
	r = &recorder{res: hooks.TaskResult{Task: ptr(sampleTask())}}
	if _, err := runTaskArgs(t, r, "", "add", "-role", "docs", "--", "-v flag is ignored"); err != nil {
		t.Fatal(err)
	}
	if got := r.reqs[0]; got.Title != "-v flag is ignored" || got.Role != "docs" {
		t.Errorf("request = %+v", got)
	}
}

func TestTaskListRequestAndOutput(t *testing.T) {
	t2 := sampleTask()
	t2.ID, t2.State, t2.OwnerPane, t2.OwnerName, t2.OwnerRole, t2.Branch = "T4", tasks.Planned, "", "", "", ""
	r := &recorder{res: hooks.TaskResult{Tasks: []tasks.Task{sampleTask(), t2}, Hidden: 2}}
	out, err := runTaskArgs(t, r, "", "list", "-state", "in-progress", "-role", "worker", "-mine")
	if err != nil {
		t.Fatal(err)
	}
	got := r.reqs[0]
	if got.Op != hooks.TaskList || got.State != "in-progress" || got.Role != "worker" || !got.Mine {
		t.Errorf("request = %+v", got)
	}
	want := "T3 [in-progress] fix the parser (worker dev, branch feat/parser)\nT4 [planned] fix the parser (unassigned)\n(2 done or cancelled hidden; -state all lists them)\n"
	if out != want {
		t.Errorf("output =\n%s\nwant\n%s", out, want)
	}

	r = &recorder{}
	if out, _ := runTaskArgs(t, r, "", "list"); out != "no tasks match\n" {
		t.Errorf("empty output = %q", out)
	}
	if out, _ := runTaskArgs(t, r, "", "list", "-json"); strings.TrimSpace(out) != "[]" {
		t.Errorf("empty json = %q", out)
	}
	r = &recorder{res: hooks.TaskResult{Tasks: []tasks.Task{sampleTask()}}}
	out, _ = runTaskArgs(t, r, "", "list", "-json")
	var back []tasks.Task
	if err := json.Unmarshal([]byte(out), &back); err != nil || len(back) != 1 || back[0].ID != "T3" {
		t.Errorf("json = %s, %v", out, err)
	}
	if _, err := runTaskArgs(t, r, "", "list", "extra"); !errors.Is(err, errReported) {
		t.Errorf("list with a stray argument: %v", err)
	}
}

func TestTaskSubcommandsSendTheRightRequests(t *testing.T) {
	cases := []struct {
		args []string
		want hooks.TaskRequest
	}{
		{[]string{"show", "T3"}, hooks.TaskRequest{Op: hooks.TaskShow, ID: "T3"}},
		{[]string{"assign", "T3", "reviewer"}, hooks.TaskRequest{Op: hooks.TaskAssign, ID: "T3", Target: "reviewer"}},
		{[]string{"status", "T3", "in-review"}, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T3", State: "in-review"}},
		{[]string{"status", "T3", "in-progress", "-note", "needs a test"}, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "T3", State: "in-progress", Note: "needs a test"}},
		{[]string{"status", "-note", "why", "3", "blocked"}, hooks.TaskRequest{Op: hooks.TaskStatus, ID: "3", State: "blocked", Note: "why"}},
		{[]string{"note", "T3", "looks", "good", "-fix", "later"}, hooks.TaskRequest{Op: hooks.TaskNote, ID: "T3", Text: "looks good -fix later"}},
		{[]string{"branch", "T3", "feat/x"}, hooks.TaskRequest{Op: hooks.TaskBranch, ID: "T3", Branch: "feat/x"}},
		{[]string{"baton", "T3", "20260101-000000-abcdef"}, hooks.TaskRequest{Op: hooks.TaskBaton, ID: "T3", Baton: "20260101-000000-abcdef"}},
	}
	for _, c := range cases {
		r := &recorder{res: hooks.TaskResult{Task: ptr(sampleTask())}}
		if _, err := runTaskArgs(t, r, "", c.args...); err != nil {
			t.Errorf("%q: %v", c.args, err)
			continue
		}
		if len(r.reqs) != 1 || r.reqs[0] != c.want {
			t.Errorf("%q: requests = %+v, want %+v", c.args, r.reqs, c.want)
		}
	}
	// A note can come from standard input.
	r := &recorder{res: hooks.TaskResult{Task: ptr(sampleTask())}}
	if _, err := runTaskArgs(t, r, "from stdin", "note", "T3", "-"); err != nil || r.reqs[0].Text != "from stdin" {
		t.Errorf("stdin note = %+v, %v", r.reqs, err)
	}
}

func TestTaskCommandsRefuseBadArgumentsBeforeCallingTheApplication(t *testing.T) {
	for _, args := range [][]string{
		nil, {"bogus"}, {"add"}, {"show"}, {"show", "T1", "T2"}, {"assign", "T1"}, {"status", "T1"},
		{"status", "T1", "done", "extra"}, {"note", "T1"}, {"branch", "T1"}, {"baton"},
		{"add", "-nosuchflag", "x"},
	} {
		r := &recorder{}
		if _, err := runTaskArgs(t, r, "", args...); err == nil {
			t.Errorf("%q succeeded", args)
		}
		if len(r.reqs) != 0 {
			t.Errorf("%q reached the application: %+v", args, r.reqs)
		}
	}
	for _, args := range [][]string{{"-h"}, {"add", "-h"}, {"list", "-h"}, {"status", "-h"}, {"note", "-h"}} {
		r := &recorder{}
		if _, err := runTaskArgs(t, r, "", args...); err != nil {
			t.Errorf("%q: %v", args, err)
		}
	}
}

func TestTaskErrorsFromTheApplicationAreReturnedAsTheyAre(t *testing.T) {
	r := &recorder{err: errors.New("moving T3 from in-review to done is for the manager role, the owner")}
	_, err := runTaskArgs(t, r, "", "status", "T3", "done")
	if err == nil || !strings.Contains(err.Error(), "is for the manager role") {
		t.Errorf("err = %v", err)
	}
}

func TestTaskOutputIsSafeAgainstControlCharactersInWhatOthersWrote(t *testing.T) {
	bad := sampleTask()
	bad.Title = "title\x1b[2J\nT9 [done] forged line"
	bad.OwnerName = "dev\nowner: evil"
	bad.Branch = "feat\x07/x"
	bad.Body = "first\nowner: forged\n\x1b[31mred"
	bad.Notes = []tasks.Note{{Author: tasks.Actor{Pane: "w1", Name: "a\nb"}, Time: bad.Created, Text: "note\r\nstatus: done\x00"}}

	r := &recorder{res: hooks.TaskResult{Task: &bad, Tasks: []tasks.Task{bad}}}
	for _, args := range [][]string{{"list"}, {"show", "T3"}, {"status", "T3", "blocked"}, {"note", "T3", "x"}, {"add", "x"}, {"assign", "T3", "w1"}} {
		out, err := runTaskArgs(t, r, "", args...)
		if err != nil {
			t.Fatalf("%q: %v", args, err)
		}
		if strings.ContainsAny(out, "\x1b\x07\x00\r") {
			t.Errorf("%q printed a control character: %q", args, out)
		}
		for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			if strings.HasPrefix(l, "T9 ") || strings.HasPrefix(l, "owner: forged") || strings.HasPrefix(l, "status: done") || strings.HasPrefix(l, "owner: evil") {
				t.Errorf("%q printed a forged line %q in\n%s", args, l, out)
			}
		}
	}
}

func TestTaskWarnsWhenAnInReviewTaskHasNoBranchAndCountsWhoWasTold(t *testing.T) {
	tk := sampleTask()
	tk.State, tk.Branch = tasks.InReview, ""
	r := &recorder{res: hooks.TaskResult{Task: &tk, Told: 2}}
	out, err := runTaskArgs(t, r, "", "status", "T3", "in-review")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"moved T3 [in-review]", "warning: no branch is set on T3", "told 2 panes"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	r.res.Told = 1
	if out, _ := runTaskArgs(t, r, "", "status", "T3", "in-review"); !strings.Contains(out, "told 1 pane\n") {
		t.Errorf("output = %q", out)
	}
}

func TestTaskShowIsDetailAndJSON(t *testing.T) {
	tk := sampleTask()
	tk.Body = "the details"
	r := &recorder{res: hooks.TaskResult{Task: &tk}}
	out, err := runTaskArgs(t, r, "", "show", "T3")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"T3 [in-progress] fix the parser", "owner: worker dev (pane w1)", "creator: boss (manager) (pane pm)", "branch: feat/parser", "  the details", "not authenticated"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	out, _ = runTaskArgs(t, r, "", "show", "-json", "T3")
	var back tasks.Task
	if err := json.Unmarshal([]byte(out), &back); err != nil || back.ID != "T3" || back.Body != "the details" {
		t.Errorf("json = %s, %v", out, err)
	}
	r.res.Task = nil
	if _, err := runTaskArgs(t, r, "", "show", "T3"); err == nil {
		t.Error("a missing task in the answer was not reported")
	}
}

func TestTaskOverTheCallbackAPIAndOutsideAPane(t *testing.T) {
	srv := teamServer(t)
	srv.SetTaskHandler(func(r hooks.TaskRequest) (hooks.TaskResult, error) {
		if r.Pane != "p-me" || r.Op != hooks.TaskAdd || r.Title != "over the wire" {
			t.Errorf("request = %+v", r)
		}
		tk := sampleTask()
		return hooks.TaskResult{Task: &tk}, nil
	})
	var out bytes.Buffer
	if err := runTask([]string{"add", "over", "the", "wire"}, strings.NewReader(""), &out); err != nil || !strings.HasPrefix(out.String(), "added T3") {
		t.Errorf("out = %q, err = %v", out.String(), err)
	}
	t.Setenv("FLOCKDECK_PANE", "")
	t.Setenv("PERCH_PANE", "")
	if err := runTask([]string{"list"}, strings.NewReader(""), &out); !errors.Is(err, errNotInPane) {
		t.Errorf("err = %v, want the not-in-a-pane error", err)
	}
}

func TestTaskPruneSendsTheAgeAndSaysHowManyWereRemoved(t *testing.T) {
	r := &recorder{res: hooks.TaskResult{Pruned: 2}}
	out, err := runTaskArgs(t, r, "", "prune")
	if err != nil || len(r.reqs) != 1 || r.reqs[0].Op != hooks.TaskPrune || r.reqs[0].Days != tasks.DefaultPruneDays {
		t.Fatalf("prune = %+v, %v", r.reqs, err)
	}
	if out != "pruned 2 finished tasks older than 30 days\n" {
		t.Errorf("output = %q", out)
	}
	r = &recorder{res: hooks.TaskResult{Pruned: 1}}
	out, err = runTaskArgs(t, r, "", "prune", "-days", "7")
	if err != nil || r.reqs[0].Days != 7 || out != "pruned 1 finished task older than 7 days\n" {
		t.Errorf("prune -days 7 = %+v, %q, %v", r.reqs, out, err)
	}
	for _, args := range [][]string{{"prune", "-days", "0"}, {"prune", "-days", "-3"}} {
		r = &recorder{}
		if _, err := runTaskArgs(t, r, "", args...); err == nil || len(r.reqs) != 0 {
			t.Errorf("%q: err = %v, requests %d", args, err, len(r.reqs))
		}
	}
	r = &recorder{err: errors.New("only the manager role prunes the board")}
	if _, err := runTaskArgs(t, r, "", "prune"); err == nil || !strings.Contains(err.Error(), "only the manager") {
		t.Errorf("err = %v", err)
	}
}
