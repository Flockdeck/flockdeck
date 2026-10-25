package changes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testSha = "0123456789abcdef0123456789abcdef01234567"

func TestReviewStateIsCleanedWhenRead(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	p := filepath.Join(t.TempDir(), "s.json")
	body := `{"reviews":[
		{"branch":"feat","reviewer":"p2","state":"approved","note":"fine‮","at":"` + now + `","atSha":"` + testSha + `","stale":true,"extra":1},
		{"branch":"feat","reviewer":"p3","state":"merged","at":"` + now + `","atSha":"` + testSha + `"},
		{"branch":"feat","reviewer":"p4","state":"approved","at":"` + now + `","atSha":"not a sha"},
		{"branch":"feat","reviewer":"p5","state":"approved","at":"yesterday","atSha":"` + testSha + `"},
		{"branch":"","reviewer":"p6","state":"approved","at":"` + now + `","atSha":"` + testSha + `"}],"unknown":true}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := LoadReviews(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Reviews) != 1 || st.Reviews[0].Reviewer != "p2" || st.Reviews[0].Note != "fine" || st.Reviews[0].Stale {
		t.Fatalf("state = %+v", st)
	}
	if err := st.Save(p); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	for _, bad := range []string{"extra", "unknown", "stale"} {
		if strings.Contains(string(data), bad) {
			t.Errorf("saved state keeps %q: %s", bad, data)
		}
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(p), "*.tmp")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
	if got, err := LoadReviews(filepath.Join(t.TempDir(), "none.json")); err != nil || len(got.Reviews) != 0 {
		t.Errorf("a missing file: %+v %v, want an empty state and no error", got, err)
	}
	if err := os.WriteFile(p, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadReviews(p); err == nil || len(got.Reviews) != 0 {
		t.Errorf("a broken file: %+v %v, want an error", got, err)
	}
	if bad, err := os.ReadFile(p + ".bad"); err != nil || string(bad) != "{" {
		t.Errorf("the copy of the broken file: %q %v", bad, err)
	}
}

func TestSetReplacesTheSameReviewersAndForMarksStale(t *testing.T) {
	var st ReviewState
	at := time.Now().UTC().Format(time.RFC3339)
	if err := st.Set("feat", Review{Reviewer: "p2", State: "requested", At: at, AtSha: testSha}, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.Set("feat", Review{Reviewer: "p2", State: "approved", Note: strings.Repeat("n", MaxNote+10), At: at, AtSha: testSha}, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.Set("other", Review{Reviewer: "p2", State: "changes", At: at, AtSha: testSha}, nil); err != nil {
		t.Fatal(err)
	}
	if len(st.Reviews) != 2 {
		t.Fatalf("state = %+v, want one review for each branch", st.Reviews)
	}
	got := st.For("feat", testSha)
	if len(got) != 1 || got[0].State != "approved" || got[0].Stale || len([]rune(got[0].Note)) != MaxNote {
		t.Errorf("reviews = %+v", got)
	}
	if got := st.For("feat", "ffffffffffffffffffffffffffffffffffffffff"); len(got) != 1 || !got[0].Stale {
		t.Errorf("after a commit: %+v, want it stale", got)
	}
	if got := st.For("none", testSha); got == nil || len(got) != 0 {
		t.Errorf("a branch with no review: %v, want an empty list", got)
	}
}

func TestAFullStateDropsRequestsFirstAndNeverTheApprovals(t *testing.T) {
	at := time.Now().UTC().Format(time.RFC3339)
	name := func(i int) string { return "b" + string(rune('A'+i%26)) + string(rune('a'+i/26)) }
	var st ReviewState
	approved := 0
	for i := 0; i < MaxReviews; i++ {
		state := "requested"
		if i%2 == 0 {
			state = "approved"
			approved++
		}
		st.Reviews = append(st.Reviews, reviewRecord{Branch: name(i), Review: Review{Reviewer: "p", State: state, At: at, AtSha: testSha}})
	}
	if err := st.Set("new", Review{Reviewer: "the person", State: "changes", At: at, AtSha: testSha}, nil); err != nil {
		t.Fatalf("a request could not make room: %v", err)
	}
	kept := 0
	for _, r := range st.Reviews {
		if r.State == "approved" {
			kept++
		}
	}
	if len(st.Reviews) != MaxReviews || kept != approved || st.Reviews[0].Branch != "new" {
		t.Errorf("%d reviews, %d of %d approvals kept, first %q", len(st.Reviews), kept, approved, st.Reviews[0].Branch)
	}

	// With nothing left that may go, a new review is refused and the state is as it was.
	var full ReviewState
	for i := 0; i < MaxReviews; i++ {
		full.Reviews = append(full.Reviews, reviewRecord{Branch: name(i), Review: Review{Reviewer: "p", State: "approved", At: at, AtSha: testSha}})
	}
	if err := full.Set("new", Review{Reviewer: "p", State: "approved", At: at, AtSha: testSha}, nil); err != ErrReviewsFull || len(full.Reviews) != MaxReviews {
		t.Errorf("a full state of approvals: %v, %d reviews", err, len(full.Reviews))
	}
	// A branch that is gone gives its approval up.
	gone := func(b string) bool { return b == name(0) }
	if err := full.Set("new", Review{Reviewer: "p", State: "approved", At: at, AtSha: testSha}, gone); err != nil {
		t.Errorf("a gone branch kept its place: %v", err)
	}
}

func TestNotesAndLinesLoseInvisibleSeparators(t *testing.T) {
	in := "a b c⁠d⁣e​f‮g\x07h\ni\tj"
	if got := CleanNote(in); got != "abcdefgh\ni\tj" {
		t.Errorf("CleanNote = %q", got)
	}
	if got := cleanLine(in, 100); strings.ContainsAny(got, "⁠⁣​‮\x07") {
		t.Errorf("cleanLine = %q", got)
	}
}
