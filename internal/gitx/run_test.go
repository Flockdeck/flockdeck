package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDeadlineHoldsWhenAChildKeepsTheOutputOpen is about what git starts
// rather than git itself. The deadline kills git; a hook or an ssh it left
// running still holds the pipes git wrote to, and waiting for those to close
// used to outlast the deadline by as long as the child cared to run.
func TestDeadlineHoldsWhenAChildKeepsTheOutputOpen(t *testing.T) {
	if !Available() {
		t.Skip("git is not installed")
	}
	// The child is left running on purpose, and Windows will not delete a
	// directory a process is working in, so this one is not t.TempDir, whose
	// cleanup failing fails the test. It goes when it can.
	dir, err := os.MkdirTemp("", "gitx-hang-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	start := time.Now()
	_, _, err = runCapture(context.Background(), time.Second, dir,
		"-c", "alias.hang=!sleep 15", "hang")
	if err == nil || !strings.Contains(err.Error(), "gave up") {
		t.Fatalf("err = %v, want the deadline reported", err)
	}
	if took := time.Since(start); took > 8*time.Second {
		t.Fatalf("returned after %s, long past a 1s deadline", took)
	}
}

// TestErrorsKeepTheLineThatSaysWhatHappened: git's advice comes first and
// runs long, and the few lines kept for a toast were all advice.
func TestErrorsKeepTheLineThatSaysWhatHappened(t *testing.T) {
	pull := "hint: Diverging branches can't be fast-forwarded, you need to either:\n" +
		"hint:\nhint: \tgit merge --no-ff\nhint:\nhint: or:\nhint:\nhint: \tgit rebase\n" +
		"hint:\nhint: Disable this message with \"git config set advice.diverging false\"\n" +
		"fatal: Not possible to fast-forward, aborting."
	if got := firstLines(withoutHints(pull), 5); got != "Not possible to fast-forward, aborting." {
		t.Errorf("pull failure reads %q", got)
	}
	if only := "hint: nothing but advice"; withoutHints(only) != only {
		t.Errorf("a message that is all advice should be kept whole, got %q", withoutHints(only))
	}
}

// TestARepositoryOfAnotherUsersKeepsTheWayPast: git refuses a repository that
// belongs to another user -- a drive formatted elsewhere, a clone made as an
// administrator -- and on Windows says who owns it and who is asking, a SID
// under each, before the command that lets it through. The lines kept for a
// toast were the report, and the command was cut off.
func TestARepositoryOfAnotherUsersKeepsTheWayPast(t *testing.T) {
	refusal := "fatal: detected dubious ownership in repository at 'D:/work/api'\n" +
		"'D:/work/api' is owned by:\n\t'S-1-5-32-544'\n" +
		"but the current user is:\n\t'S-1-5-21-1004336348-1177238915-682003330-1001'\n" +
		"To add an exception for this directory, call:\n\n" +
		"\tgit config --global --add safe.directory D:/work/api"
	got := firstLines(withoutHints(refusal), 5)
	if !strings.Contains(got, "git config --global --add safe.directory D:/work/api") {
		t.Errorf("the refusal reads %q, without the command that gets past it", got)
	}
	if strings.Contains(got, "S-1-5") {
		t.Errorf("the refusal still spends its lines on who owns what: %q", got)
	}
}

// TestErrorsSkipBlankLines: git spaces its longer answers out, and the blank
// lines used up the few a toast has room for.
func TestErrorsSkipBlankLines(t *testing.T) {
	identity := "Author identity unknown\n\n*** Please tell me who you are.\n\nRun\n\n" +
		"  git config --global user.email \"you@example.com\"\n" +
		"  git config --global user.name \"Your Name\"\n\n" +
		"to set your account's default identity.\n"
	if got := firstLines(identity, 5); !strings.Contains(got, "user.name") {
		t.Errorf("identity failure reads %q, want the commands that fix it", got)
	}
}

// TestHeadWriterKeepsOnlyWhatIsShown: a diff of tens of megabytes was held
// whole to be cut down to the few hundred kilobytes the panel is sent.
func TestHeadWriterKeepsOnlyWhatIsShown(t *testing.T) {
	w := &headWriter{limit: 10}
	for range 1000 {
		w.Write([]byte("0123456789abcdef"))
	}
	if w.String() != "0123456789" || w.total != 16000 || cap(w.head) > 64 {
		t.Errorf("kept %q (cap %d) of %d bytes; want the first 10 of 16000", w.String(), cap(w.head), w.total)
	}
}

// TestErrorsNameTheCommandNotItsArguments: a toast led with the whole
// argument list, absolute paths and all, before what git said was wrong.
func TestErrorsNameTheCommandNotItsArguments(t *testing.T) {
	repo := newRepo(t)
	err := AddFrom(repo, filepath.Join(t.TempDir(), "wt"), "topic", "no-such-base")
	if err == nil {
		t.Fatal("a base that does not exist should fail")
	}
	if got := err.Error(); got != "git worktree add: invalid reference: no-such-base" {
		t.Errorf("error = %q", got)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"commit", "-m", "a long message"}, "git commit"},
		{[]string{"worktree", "remove", "--force", "--", "/some/path"}, "git worktree remove"},
		{[]string{"push", "--set-upstream", "origin", "main"}, "git push"},
		{[]string{"-c", "k=v", "status"}, "git"},
	} {
		if got := gitLabel(tc.args); got != tc.want {
			t.Errorf("gitLabel(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}
