package changes

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func sampleComment(path string) Comment {
	return Comment{Pane: "p1", Path: path, Side: "new", Line: 3, AtSha: testSha, Text: "Add a test.", Author: "you", To: "p1",
		CreatedAt: time.Now().UTC().Format(time.RFC3339)}
}

func TestCommentsAreAddedOpenWithTheNextIdAndSurviveASave(t *testing.T) {
	var st CommentState
	a, err := st.Add("feat", sampleComment("a.go"), 7)
	if err != nil || a.ID != "c1" || a.State != "open" {
		t.Fatalf("first = %+v, %v", a, err)
	}
	b, _ := st.Add("feat", sampleComment("b.go"), 8)
	if b.ID != "c2" {
		t.Errorf("second id = %q", b.ID)
	}
	p := filepath.Join(t.TempDir(), "c.json")
	if err := st.Save(p); err != nil {
		t.Fatal(err)
	}
	got, err := LoadComments(p)
	if err != nil || len(got.Comments) != 2 || got.Next != 3 {
		t.Fatalf("loaded %+v, %v", got, err)
	}
	c, _ := got.Add("feat", sampleComment("c.go"), 9)
	if c.ID != "c3" {
		t.Errorf("an id was reused after a reload: %q", c.ID)
	}
	// Delivery is read from the inbox and never kept in the file.
	list := got.For("feat", "pNEW", func(to string, id int) string { return map[int]string{7: "read", 8: "queued"}[id] })
	if len(list) != 3 || list[0].Delivery != "read" || list[1].Delivery != "queued" || list[2].Delivery != "" || list[0].Pane != "pNEW" {
		t.Errorf("For = %+v", list)
	}
	if raw, _ := os.ReadFile(p); strings.Contains(string(raw), "delivery") {
		t.Errorf("delivery was written to the file: %s", raw)
	}
	if other := got.For("other", "p9", nil); len(other) != 0 {
		t.Errorf("comments of another branch: %+v", other)
	}
}

func TestResolveAndReopenMoveOnlyThatComment(t *testing.T) {
	var st CommentState
	st.Add("feat", sampleComment("a.go"), 1)
	st.Add("feat", sampleComment("b.go"), 2)
	if c, err := st.Resolve("feat", "c1"); err != nil || c.State != "resolved" {
		t.Fatalf("resolve = %+v, %v", c, err)
	}
	if st.Comments[1].State != "open" {
		t.Error("another comment was closed")
	}
	if c, err := st.Resolve("feat", "c1"); err != nil || c.State != "resolved" {
		t.Errorf("resolving twice = %+v, %v", c, err)
	}
	if c, err := st.Reopen("feat", "c1"); err != nil || c.State != "open" {
		t.Errorf("reopen = %+v, %v", c, err)
	}
	if _, err := st.Resolve("feat", "c9"); err != ErrNoComment {
		t.Errorf("an unknown id: %v", err)
	}
	if _, err := st.Resolve("other", "c1"); err != ErrNoComment {
		t.Errorf("a comment of another branch was resolved: %v", err)
	}
	// A fixed comment is closed by the person, and reopening drops the fix.
	st.Comments[1].State, st.Comments[1].FixedAtSha = "fixed", testSha
	if c, _ := st.Resolve("feat", "c2"); c.State != "resolved" || c.FixedAtSha != "" {
		t.Errorf("a fixed comment resolved = %+v", c)
	}
}

func TestABranchKeepsAtMost500AndAFullStoreDropsAClosedCommentFirst(t *testing.T) {
	var st CommentState
	for i := 0; i < MaxBranchComments; i++ {
		if _, err := st.Add("feat", sampleComment("a.go"), i+1); err != nil {
			t.Fatalf("comment %d: %v", i, err)
		}
	}
	if _, err := st.Add("feat", sampleComment("a.go"), 999); err != ErrTooManyComments {
		t.Errorf("the 501st comment: %v", err)
	}
	if _, err := st.Add("other", sampleComment("a.go"), 1000); err != nil {
		t.Errorf("another branch is refused: %v", err)
	}
	// A store at its limit makes room by dropping the oldest closed comment, and
	// refuses when every comment is open.
	var full CommentState
	for i := 0; i < MaxComments; i++ {
		c := sampleComment("a.go")
		full.Add("b"+strconv.Itoa(i/MaxBranchComments), c, i+1)
	}
	if _, err := full.Add("new", sampleComment("a.go"), 1); err != ErrTooManyComments {
		t.Fatalf("a full store of open comments: %v", err)
	}
	full.Comments[10].State = "resolved"
	gone := full.Comments[10].ID
	if _, err := full.Add("new", sampleComment("a.go"), 1); err != nil || len(full.Comments) != MaxComments {
		t.Fatalf("with a closed comment: %v, %d comments", err, len(full.Comments))
	}
	for _, r := range full.Comments {
		if r.ID == gone {
			t.Error("the closed comment was kept")
		}
	}
}

func TestLoadCommentsCleansWhatIsRead(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	p := filepath.Join(t.TempDir(), "c.json")
	good := `{"branch":"feat","msgId":4,"id":"c1","pane":"p1","path":"a.go","side":"new","line":2,"atSha":"` + testSha + `","text":"fine‮","author":"you","to":"p1","delivery":"read","state":"fixed","createdAt":"` + now + `","fixedAtSha":"` + testSha + `"}`
	body := `{"next":1,"comments":[` + good + `,
		{"branch":"feat","id":"c1","pane":"p1","path":"a.go","side":"new","line":2,"atSha":"` + testSha + `","text":"same id","author":"you","to":"p1","state":"open","createdAt":"` + now + `"},
		{"branch":"feat","id":"x","pane":"p1","path":"a.go","side":"new","line":2,"atSha":"` + testSha + `","text":"bad id","author":"you","to":"p1","state":"open","createdAt":"` + now + `"},
		{"branch":"feat","id":"c5","pane":"p1","path":"a.go","side":"middle","line":2,"atSha":"` + testSha + `","text":"bad side","author":"you","to":"p1","state":"open","createdAt":"` + now + `"},
		{"branch":"feat","id":"c6","pane":"p1","path":"a.go","side":"new","line":-1,"atSha":"` + testSha + `","text":"bad line","author":"you","to":"p1","state":"open","createdAt":"` + now + `"},
		{"branch":"feat","id":"c7","pane":"p1","path":"a.go","side":"new","line":1,"atSha":"nope","text":"bad sha","author":"you","to":"p1","state":"open","createdAt":"` + now + `"},
		{"branch":"feat","id":"c8","pane":"p1","path":"a.go","side":"new","line":1,"atSha":"` + testSha + `","text":"bad state","author":"you","to":"p1","state":"merged","createdAt":"` + now + `"},
		{"branch":"feat","id":"c9","pane":"p1","path":"a.go","side":"new","line":1,"atSha":"` + testSha + `","text":"","author":"you","to":"p1","state":"open","createdAt":"` + now + `"}]}`
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := LoadComments(p)
	if err != nil || len(st.Comments) != 1 {
		t.Fatalf("loaded %+v, %v", st, err)
	}
	c := st.Comments[0]
	if c.Text != "fine" || c.Delivery != "" || c.FixedAtSha != testSha || st.Next != 2 {
		t.Errorf("comment = %+v, next %d", c, st.Next)
	}
	if got, err := LoadComments(filepath.Join(t.TempDir(), "none.json")); err != nil || got.HasComments() {
		t.Errorf("a missing file: %+v, %v", got, err)
	}
	if err := os.WriteFile(p, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadComments(p); err == nil || !strings.Contains(err.Error(), ".bad") {
		t.Errorf("a damaged file: %v, want an error naming the copy", err)
	}
}

func TestSideLinesCountsTheLinesOfAFileAtACommit(t *testing.T) {
	dir := repo(t)
	head := strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))
	if n, ok, err := SideLines(context.Background(), dir, head, "a.txt"); err != nil || !ok || n != 3 {
		t.Errorf("a.txt = %d, %v, %v, want 3 lines", n, ok, err)
	}
	write(t, dir, "tail.txt", "no newline at the end")
	write(t, dir, "empty.txt", "")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "more")
	head = strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))
	for name, want := range map[string]int{"tail.txt": 1, "empty.txt": 0} {
		if n, ok, err := SideLines(context.Background(), dir, head, name); err != nil || !ok || n != want {
			t.Errorf("%s = %d, %v, %v, want %d", name, n, ok, err, want)
		}
	}
	if _, ok, err := SideLines(context.Background(), dir, head, "missing.txt"); err != nil || ok {
		t.Errorf("a file that is not there: %v, %v", ok, err)
	}
}
