package sysproc

import (
	"errors"
	"reflect"
	"sort"
	"testing"
)

func ids(procs []StaleProc) []string {
	out := make([]string, len(procs))
	for i, p := range procs {
		out[i] = p.ID
	}
	sort.Strings(out)
	return out
}

// TestReapEndsOnlyAConfirmedProcess covers the one question Reap insists on
// an answer to before it ends anything: that the process still running under
// a record's id really is the one that made the record.
func TestReapEndsOnlyAConfirmedProcess(t *testing.T) {
	var terminated []int
	terminate := func(pid int) error { terminated = append(terminated, pid); return nil }

	procs := []StaleProc{
		{ID: "confirmed", PID: 101, Path: "/repo-wt"},
		{ID: "already-gone", PID: 103, Path: "/repo-wt2"},
		{ID: "bad-pid", PID: 0, Path: "/repo-wt3"},
	}
	same := func(p StaleProc) bool { return p.ID != "already-gone" }

	killed, settled := Reap(procs, same, terminate)

	if got, want := ids(killed), []string{"confirmed"}; !reflect.DeepEqual(got, want) {
		t.Errorf("killed = %v, want %v", got, want)
	}
	if got, want := terminated, []int{101}; !reflect.DeepEqual(got, want) {
		t.Errorf("terminate was called with %v, want %v", got, want)
	}
	// already-gone was never running under its own record at all, and
	// bad-pid names nothing to begin with -- both are settled, since neither
	// has anything left for a later launch to do about it, but neither was
	// ended.
	if got, want := ids(settled), []string{"already-gone", "bad-pid", "confirmed"}; !reflect.DeepEqual(got, want) {
		t.Errorf("settled = %v, want %v", got, want)
	}
}

// TestReapPassesThroughAFailedTerminate covers a process same confirms but
// terminate cannot end -- gone by the time it is asked, say, or one this
// process may not signal: settled, since there is nothing more this sweep
// can do about it, but not counted as killed.
func TestReapPassesThroughAFailedTerminate(t *testing.T) {
	terminate := func(int) error { return errors.New("terminate failed") }
	killed, settled := Reap([]StaleProc{{ID: "stubborn", PID: 5}}, func(StaleProc) bool { return true }, terminate)
	if len(killed) != 0 {
		t.Errorf("killed = %v, want none", killed)
	}
	if got, want := ids(settled), []string{"stubborn"}; !reflect.DeepEqual(got, want) {
		t.Errorf("settled = %v, want %v", got, want)
	}
}
