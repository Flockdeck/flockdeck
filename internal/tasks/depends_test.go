package tasks

import (
	"strings"
	"testing"
)

func TestDependsAreCheckedAgainstTheBoard(t *testing.T) {
	b, _ := testBoard(t)
	t1 := mustAdd(t, b, mgr, "parser")
	t2 := mustAdd(t, b, mgr, "lexer")
	t3 := mustAdd(t, b, mgr, "tests")

	ch, err := b.SetDepends(mgr, true, t3.ID, []string{t1.ID, "t2"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ch.Task.Depends, ","); got != "T1,T2" {
		t.Errorf("depends = %q, ids are read as the board spells them", got)
	}
	got, _ := b.Get(t3.ID)
	if len(got.Depends) != 2 || got.History[len(got.History)-1].What != "set the task to wait on T1, T2" {
		t.Errorf("stored task = %+v", got)
	}

	for name, tc := range map[string]struct {
		id   string
		deps []string
		want string
	}{
		"itself":        {t1.ID, []string{t1.ID}, "cannot wait on itself"},
		"unknown":       {t1.ID, []string{"T99"}, "no task T99"},
		"twice":         {t1.ID, []string{t2.ID, t2.ID}, "listed twice"},
		"not an id":     {t1.ID, []string{"parser"}, "not a task id"},
		"direct loop":   {t1.ID, []string{t3.ID}, "would make a loop"},
		"two-step loop": {t2.ID, []string{t3.ID}, "would make a loop"},
	} {
		if _, err := b.SetDepends(mgr, true, tc.id, tc.deps); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
	if _, err := b.SetDepends(mgr, true, t1.ID, make([]string, MaxDepends+1)); err == nil {
		t.Error("more than MaxDepends was accepted")
	}

	// No ids clears the list.
	ch, err = b.SetDepends(mgr, true, t3.ID, nil)
	if err != nil || len(ch.Task.Depends) != 0 {
		t.Errorf("cleared = %+v, %v", ch.Task.Depends, err)
	}
}

func TestOnlyThoseWhoMayEditSetDependsAndEstimates(t *testing.T) {
	b, _ := testBoard(t)
	t1 := mustAdd(t, b, mgr, "parser")
	t2 := mustAdd(t, b, mgr, "lexer")
	if _, err := b.SetDepends(w2, false, t2.ID, []string{t1.ID}); err == nil {
		t.Error("a pane that is not the owner, creator or manager set a dependency")
	}
	if _, err := b.SetEstimate(w2, false, t2.ID, 30); err == nil {
		t.Error("a pane that is not the owner, creator or manager set an estimate")
	}
}

func TestEstimateIsBoundedAndClears(t *testing.T) {
	b, _ := testBoard(t)
	t1 := mustAdd(t, b, mgr, "parser")
	ch, err := b.SetEstimate(mgr, true, t1.ID, 90)
	if err != nil || ch.Task.EstimateMin != 90 {
		t.Fatalf("estimate = %+v, %v", ch.Task, err)
	}
	for _, bad := range []int{-1, MaxEstimateMin + 1} {
		if _, err := b.SetEstimate(mgr, true, t1.ID, bad); err == nil {
			t.Errorf("%d minutes was accepted", bad)
		}
	}
	ch, err = b.SetEstimate(mgr, true, t1.ID, 0)
	if err != nil || ch.Task.EstimateMin != 0 {
		t.Errorf("cleared = %+v, %v", ch.Task, err)
	}
}

func TestDetailShowsWhatATaskWaitsOnAndItsEstimate(t *testing.T) {
	d := Detail(Task{ID: "T3", Title: "tests", State: Planned, Depends: []string{"T1", "T2"}, EstimateMin: 45})
	for _, want := range []string{"waits on: T1, T2\n", "estimate: 45 minutes\n"} {
		if !strings.Contains(d, want) {
			t.Errorf("detail lacks %q:\n%s", want, d)
		}
	}
}
