package gitx

import (
	"context"
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
	start := time.Now()
	_, _, err := runCapture(context.Background(), time.Second, t.TempDir(),
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
	if got := firstLines(withoutHints(pull), 5); got != "fatal: Not possible to fast-forward, aborting." {
		t.Errorf("pull failure reads %q", got)
	}
	if only := "hint: nothing but advice"; withoutHints(only) != only {
		t.Errorf("a message that is all advice should be kept whole, got %q", withoutHints(only))
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
