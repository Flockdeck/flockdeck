package server

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jmwri/flockdeck/internal/changes"
)

// commentRepo makes a repository whose branch feat changes a.txt (3 lines on main,
// 4 on feat) and adds n.txt (2 lines), and returns the work a comment command
// would have, with a sender that records what it was asked to send.
func commentRepo(t *testing.T) (commentWork, *[]string) {
	t.Helper()
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	for _, kv := range [][2]string{{"user.email", "t@example.com"}, {"user.name", "t"}, {"commit.gpgsign", "false"}, {"core.autocrlf", "false"}} {
		gitCmd(t, dir, "config", kv[0], kv[1])
	}
	put := func(name, text string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("a.txt", "one\ntwo\nthree\n")
	put("same.txt", "unchanged\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-q", "-m", "first")
	gitCmd(t, dir, "checkout", "-q", "-b", "feat")
	put("a.txt", "one\ntwo\nthree\nfour\n")
	put("n.txt", "x\ny\n")
	gitCmd(t, dir, "add", "-A")
	gitCmd(t, dir, "commit", "-q", "-m", "second")
	b := changes.Read(context.Background(), "p1", dir, 0)
	if b.Error != "" || b.BaseSha == "" || b.HeadSha == "" {
		t.Fatalf("branch = %+v", b)
	}
	var sent []string
	w := commentWork{pane: "p1", cwd: dir, branch: b, store: filepath.Join(t.TempDir(), "x.comments.json"),
		send: func(to, text string) (int, error) {
			sent = append(sent, to+"\n"+text)
			return len(sent), nil
		}}
	return w, &sent
}

func commentCmd(w commentWork, file, side string, line int, text string) command {
	return command{Cmd: "commentAdd", ID: w.pane, File: file, Side: side, Line: line, Text: text, ExpectSha: w.branch.HeadSha}
}

func TestACommentIsSentToTheOwnerAndRecordedOpen(t *testing.T) {
	w, sent := commentRepo(t)
	got, ref := addComment(context.Background(), w, commentCmd(w, "a.txt", "new", 4, "Add a test for\nthe fourth line."))
	if ref != nil {
		t.Fatalf("refused: %+v", ref)
	}
	if got.ID != "c1" || got.State != "open" || got.To != "p1" || got.Author != "you" || got.Path != "a.txt" || got.AtSha != w.branch.HeadSha || got.Line != 4 || got.Side != "new" {
		t.Errorf("comment = %+v", got)
	}
	if len(*sent) != 1 {
		t.Fatalf("%d messages sent", len(*sent))
	}
	for _, want := range []string{"p1\n", "feat", "a.txt", "line 4 (new side)", "> Add a test for", "> the fourth line.", "from the person at the window"} {
		if !strings.Contains((*sent)[0], want) {
			t.Errorf("message lacks %q:\n%s", want, (*sent)[0])
		}
	}
	st, err := changes.LoadComments(w.store)
	if err != nil || len(st.Comments) != 1 {
		t.Fatalf("store = %+v, %v", st, err)
	}
	// A comment on the whole file, and on the old side's last line, are fine.
	if _, ref := addComment(context.Background(), w, commentCmd(w, "n.txt", "new", 0, "Why a new file?")); ref != nil {
		t.Errorf("whole file: %+v", ref)
	}
	if _, ref := addComment(context.Background(), w, commentCmd(w, "a.txt", "old", 3, "This line moved.")); ref != nil {
		t.Errorf("old side: %+v", ref)
	}
}

func TestACommentIsRefusedBeforeAnythingIsSent(t *testing.T) {
	w, sent := commentRepo(t)
	long := strings.Repeat("x", changes.MaxComment+1)
	moved := commentCmd(w, "a.txt", "new", 1, "hi")
	moved.ExpectSha = "0000000"
	for name, c := range map[string]struct {
		cmd  command
		code string
	}{
		"no file":              {commentCmd(w, "", "new", 1, "hi"), "badRequest"},
		"bad side":             {commentCmd(w, "a.txt", "middle", 1, "hi"), "badRequest"},
		"negative line":        {commentCmd(w, "a.txt", "new", -1, "hi"), "badRequest"},
		"no text":              {commentCmd(w, "a.txt", "new", 1, "  \u202e "), "badRequest"},
		"too long":             {commentCmd(w, "a.txt", "new", 1, long), "badRequest"},
		"head moved":           {moved, "headMoved"},
		"not a file of it":     {commentCmd(w, "same.txt", "new", 1, "hi"), "badRequest"},
		"outside the repo":     {commentCmd(w, "../x", "new", 1, "hi"), "badRequest"},
		"past the new side":    {commentCmd(w, "a.txt", "new", 5, "hi"), "badRequest"},
		"past the old side":    {commentCmd(w, "a.txt", "old", 4, "hi"), "badRequest"},
		"old side of new file": {commentCmd(w, "n.txt", "old", 1, "hi"), "badRequest"},
	} {
		noSha := c.cmd
		if name == "no sha" {
			noSha.ExpectSha = ""
		}
		if _, ref := addComment(context.Background(), w, noSha); ref == nil || ref.code != c.code {
			t.Errorf("%s: %+v, want %s", name, ref, c.code)
		}
	}
	missing := commentCmd(w, "a.txt", "new", 1, "hi")
	missing.ExpectSha = ""
	if _, ref := addComment(context.Background(), w, missing); ref == nil || ref.code != "badRequest" {
		t.Errorf("no expectSha: %+v", ref)
	}
	if len(*sent) != 0 {
		t.Errorf("%d messages were sent for refused comments", len(*sent))
	}
	if _, err := os.Stat(w.store); err == nil {
		t.Error("a store was written for refused comments")
	}
}

func TestACommentThatCannotBeSentOrRecordedIsNotHalfDone(t *testing.T) {
	w, _ := commentRepo(t)
	w.send = func(string, string) (int, error) { return 0, errors.New("the inbox of p1 is full") }
	if _, ref := addComment(context.Background(), w, commentCmd(w, "a.txt", "new", 1, "hi")); ref == nil || !strings.Contains(ref.msg, "full") {
		t.Errorf("a failed send: %+v", ref)
	}
	if _, err := os.Stat(w.store); err == nil {
		t.Error("a comment was recorded that was not sent")
	}
	// A damaged store stops the comment before the message goes out.
	w2, sent := commentRepo(t)
	if err := os.WriteFile(w2.store, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ref := addComment(context.Background(), w2, commentCmd(w2, "a.txt", "new", 1, "hi")); ref == nil || !strings.Contains(ref.msg, "damaged") {
		t.Errorf("a damaged store: %+v", ref)
	}
	if len(*sent) != 0 {
		t.Error("a message was sent although the comment could not be recorded")
	}
}

func TestResolveAndReopenChangeOnlyAnExistingCommentOfTheBranch(t *testing.T) {
	w, _ := commentRepo(t)
	added, _ := addComment(context.Background(), w, commentCmd(w, "a.txt", "new", 1, "hi"))
	resolve := command{Cmd: "commentResolve", ID: w.pane, CommentID: added.ID}
	got, ref := moveComment(w, resolve)
	if ref != nil || got.State != "resolved" || got.Pane != "p1" {
		t.Fatalf("resolve = %+v, %+v", got, ref)
	}
	reopen := resolve
	reopen.Cmd = "commentReopen"
	if got, ref := moveComment(w, reopen); ref != nil || got.State != "open" {
		t.Errorf("reopen = %+v, %+v", got, ref)
	}
	unknown := resolve
	unknown.CommentID = "c99"
	if _, ref := moveComment(w, unknown); ref == nil || ref.code != "notFound" {
		t.Errorf("unknown id: %+v", ref)
	}
	other := w
	other.branch.Branch = "another"
	if _, ref := moveComment(other, resolve); ref == nil || ref.code != "notFound" {
		t.Errorf("a comment of another branch: %+v", ref)
	}
	none := resolve
	none.CommentID = ""
	if _, ref := moveComment(w, none); ref == nil || ref.code != "badRequest" {
		t.Errorf("no commentId: %+v", ref)
	}
}

func TestCommentTextNamesWhereAndQuotesTheCommentCleaned(t *testing.T) {
	got := commentText("feat/x", "internal/a.go", "old", 12, "0123456789abcdef0123456789abcdef01234567", "line one\nline\u202e two")
	for _, want := range []string{"feat/x", "internal/a.go", "line 12 (old side)", "0123456789ab", "> line one", "> line two"} {
		if !strings.Contains(got, want) {
			t.Errorf("text lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\u202e") {
		t.Errorf("an override character reached the message: %q", got)
	}
	if whole := commentText("b", "f", "new", 0, "0123456789abcdef", "x"); !strings.Contains(whole, "on the whole file") {
		t.Errorf("whole file: %s", whole)
	}
}

func TestCommentCommandsAreRefusedToARelayWindowAndToAConnectionThatIsNotAPage(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, name := range []string{"commentAdd", "commentResolve", "commentReopen"} {
		cl := &controlClient{out: make(chan []byte, 8), remote: true, device: "d"}
		srv.handleCommand(cl, command{Cmd: name, Req: "r", ID: "p1"})
		if m := answerOf(t, cl); m.Code != "deskOnly" || m.Op != name {
			t.Errorf("%s from a relay window: %+v", name, m)
		}
		cl = &controlClient{out: make(chan []byte, 8)}
		srv.handleCommand(cl, command{Cmd: name, Req: "r", ID: "p1"})
		if m := answerOf(t, cl); m.Code != "deskOnly" || m.Op != name {
			t.Errorf("%s from a connection that is not a page: %+v", name, m)
		}
		cl = &controlClient{out: make(chan []byte, 8), page: true}
		srv.handleCommand(cl, command{Cmd: name, Req: "r", ID: "nope", CommentID: "c1"})
		if m := answerOf(t, cl); m.Code != "noBranch" && m.Code != "badRequest" {
			t.Errorf("%s for no pane: %+v", name, m)
		}
	}
}
