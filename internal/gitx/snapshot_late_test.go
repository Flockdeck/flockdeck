package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// snapTree lists the paths of the snapshot's tree, with the mode of each.
func snapTree(t *testing.T, s *Scratch, dir string, snap Snap) string {
	t.Helper()
	var out bytes.Buffer
	if _, err := runToEnv(context.Background(), commandTimeout, dir, s.env, nil, &out, "ls-tree", "-r", snap.Tree); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func committedRepoAt(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "init", "-q", "--initial-branch=main")
	write(t, dir, "x.txt", "x\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "inside")
}

func onceBeforeAdd(t *testing.T, f func()) {
	t.Helper()
	old := beforeAdd
	t.Cleanup(func() { beforeAdd = old })
	done := false
	beforeAdd = func() {
		if !done {
			done = true
			f()
		}
	}
}

// A repository with no commit that appears between the listing and the add makes
// git refuse the whole add: the listing is done again and the snapshot is made.
func TestANoCommitRepositoryThatAppearsInTheGapIsRetried(t *testing.T) {
	repo, wt := radarRepo(t, "wa")
	dir := wt["wa"]
	editLine(t, dir, "a.txt", 4, "edited")
	onceBeforeAdd(t, func() { gitRun(t, dir, "init", "-q", "--initial-branch=main", "late") })
	snap, s, err := snapOf(t, repo, dir, true)
	if err != nil {
		t.Fatalf("snapshot with an empty repository made in the gap: %v", err)
	}
	if got := sortedPaths(snap.Paths); got != "a.txt" {
		t.Errorf("paths = %s, want a.txt", got)
	}
	if tree := snapTree(t, s, dir, snap); strings.Contains(tree, "late") {
		t.Errorf("the tree holds the late repository: %s", tree)
	}
}

// One with a commit is staged by git as a gitlink, and said so in a warning: it is
// taken out again.
func TestACommittedRepositoryThatAppearsInTheGapIsNotStaged(t *testing.T) {
	repo, wt := radarRepo(t, "wa")
	dir := wt["wa"]
	editLine(t, dir, "a.txt", 4, "edited")
	onceBeforeAdd(t, func() { committedRepoAt(t, filepath.Join(dir, "late")) })
	snap, s, err := snapOf(t, repo, dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := sortedPaths(snap.Paths); got != "a.txt" {
		t.Errorf("paths = %s, want a.txt", got)
	}
	if tree := snapTree(t, s, dir, snap); strings.Contains(tree, "late") || strings.Contains(tree, "160000") {
		t.Errorf("the tree holds the late repository: %s", tree)
	}
}

// With the index unreadable the scratch index is built from HEAD, and a repository
// somebody staged and did not commit is still one of the checkout's changes.
func TestAStagedRepositoryIsKeptWhenTheIndexIsBuiltFromHead(t *testing.T) {
	repo, wt := radarRepo(t, "wa")
	dir := wt["wa"]
	committedRepoAt(t, filepath.Join(dir, "sub"))
	gitRun(t, dir, "add", "sub")
	editLine(t, dir, "a.txt", 4, "edited")
	old := copyIndexFn
	t.Cleanup(func() { copyIndexFn = old })
	copyIndexFn = func(from, to string) error { return os.ErrPermission }
	snap, s, err := snapOf(t, repo, dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if tree := snapTree(t, s, dir, snap); !strings.Contains(tree, "160000") || !strings.Contains(tree, "sub") {
		t.Errorf("the staged repository is not in the tree: %s", tree)
	}
}

// A user's GIT_LITERAL_PATHSPECS=1 would stop ":(exclude)" being understood, and
// the nested repositories would be staged or the add would fail.
func TestUsersPathspecVariablesDoNotChangeTheSnapshot(t *testing.T) {
	for _, v := range []string{"GIT_LITERAL_PATHSPECS", "GIT_GLOB_PATHSPECS", "GIT_NOGLOB_PATHSPECS", "GIT_ICASE_PATHSPECS"} {
		t.Run(v, func(t *testing.T) {
			repo, wt := radarRepo(t, "wa")
			dir := wt["wa"]
			nestedRepos(t, dir, 2)
			editLine(t, dir, "a.txt", 4, "edited")
			t.Setenv(v, "1")
			snap, _, err := snapOf(t, repo, dir, true)
			if err != nil {
				t.Fatalf("%s=1: %v", v, err)
			}
			if got := sortedPaths(snap.Paths); got != "a.txt" {
				t.Errorf("%s=1: paths = %s, want a.txt", v, got)
			}
		})
	}
}

// A user whose git warns of every file (core.autocrlf=true says so for each file with
// LF line endings) has the reason for a failure pushed out of the lines an error keeps.
// The retry for a repository with no commit that appeared in the gap does not depend
// on those lines: it is decided by listing the folders again.
func TestANoCommitRepositoryInTheGapIsRetriedWhenGitWarnsOfEveryFile(t *testing.T) {
	repo, wt := radarRepo(t, "wa")
	dir := wt["wa"]
	gitRun(t, dir, "config", "core.autocrlf", "true")
	for i := 0; i < 8; i++ {
		write(t, dir, fmt.Sprintf("lf%d.txt", i), "one\ntwo\n")
	}
	onceBeforeAdd(t, func() { gitRun(t, dir, "init", "-q", "--initial-branch=main", "late") })
	snap, s, err := snapOf(t, repo, dir, true)
	if err != nil {
		t.Fatalf("snapshot with 8 warning files and a repository made in the gap: %v", err)
	}
	if len(snap.Paths) != 8 {
		t.Errorf("paths = %v, want the 8 files", snap.Paths)
	}
	if tree := snapTree(t, s, dir, snap); strings.Contains(tree, "late") {
		t.Errorf("the tree holds the late repository: %s", tree)
	}
}

// What git says is not read: a git that says it in another language, or says
// something else, fails the add the same way and the retry is the same. The first add
// is failed by a stand-in with a message in German.
func TestTheRetryDoesNotDependOnWhatGitSays(t *testing.T) {
	repo, wt := radarRepo(t, "wa")
	dir := wt["wa"]
	editLine(t, dir, "a.txt", 4, "edited")
	onceBeforeAdd(t, func() { gitRun(t, dir, "init", "-q", "--initial-branch=main", "late") })
	var adds int
	withHook(t, func(ctx context.Context, args []string) error {
		if len(args) > 0 && args[0] == "add" {
			adds++
			if adds == 1 {
				return errors.New("fatal: 'late/' hat keinen ausgecheckten Commit")
			}
		}
		return nil
	})
	snap, _, err := snapOf(t, repo, dir, true)
	if err != nil {
		t.Fatalf("the retry did not happen for a message in German: %v", err)
	}
	if adds != 2 || sortedPaths(snap.Paths) != "a.txt" {
		t.Errorf("%d adds, paths %v, want a retry and a.txt", adds, snap.Paths)
	}

	// A failure that is not a repository that appeared stands, and is not retried.
	adds = 0
	withHook(t, func(ctx context.Context, args []string) error {
		if len(args) > 0 && args[0] == "add" {
			adds++
			return errors.New("fatal: eine ganz andere Sache")
		}
		return nil
	})
	if _, _, err := snapOf(t, repo, dir, true); err == nil || adds != 1 {
		t.Errorf("an add that failed for another reason: %v after %d adds, want the failure after one", err, adds)
	}
}

// The scratch commands have their messages in one language and never ask for a
// terminal, and leave the rest of the locale to the user.
func TestTheScratchEnvironmentPinsOnlyTheMessages(t *testing.T) {
	repo, _ := radarRepo(t, "wa")
	common, _ := CommonDir(repo)
	s, err := NewScratch(common)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	have := map[string]bool{}
	for _, kv := range s.env {
		have[kv] = true
	}
	for _, kv := range []string{"LANGUAGE=C", "LC_MESSAGES=C", "GIT_TERMINAL_PROMPT=0"} {
		if !have[kv] {
			t.Errorf("the scratch environment lacks %s", kv)
		}
	}
	for kv := range have {
		if strings.HasPrefix(kv, "LC_ALL=") || strings.HasPrefix(kv, "LC_CTYPE=") || strings.HasPrefix(kv, "LANG=") {
			t.Errorf("the scratch environment sets %s, which a clean filter may need as the user has it", kv)
		}
	}
}

// When git warned of a lot of files before it failed, the failure that is logged is
// the one that is not a warning.
func TestAnAddFailureIsLoggedWithoutItsWarnings(t *testing.T) {
	var warned strings.Builder
	for i := 0; i < 9; i++ {
		fmt.Fprintf(&warned, "warning: in the working copy of 'f%d.txt', LF will be replaced by CRLF the next time Git touches it\n", i)
	}
	warned.WriteString("fatal: 'late/' does not have a commit checked out\n")
	in := &commandError{msg: "git add: " + firstLines(warned.String(), 5), err: errors.New("exit status 128"), full: warned.String()}
	got := withoutWarnings(in).Error()
	if !strings.Contains(got, "does not have a commit checked out") || strings.Contains(got, "warning:") {
		t.Errorf("the logged failure is %q, want the fatal line without the warnings", got)
	}
}

// The snapshot's own error for a failed add says why it failed, with the warnings git
// printed first left out.
func TestAFailedAddIsReportedWithoutTheWarningsBeforeIt(t *testing.T) {
	repo, wt := radarRepo(t, "wa")
	dir := wt["wa"]
	editLine(t, dir, "a.txt", 4, "edited")
	var said strings.Builder
	for i := 0; i < 9; i++ {
		fmt.Fprintf(&said, "warning: LF will be replaced by CRLF in f%d.txt\n", i)
	}
	said.WriteString("fatal: unable to index file 'big.bin'")
	withHook(t, func(ctx context.Context, args []string) error {
		if len(args) > 0 && args[0] == "add" {
			return errors.New(said.String())
		}
		return nil
	})
	_, _, err := snapOf(t, repo, dir, true)
	if err == nil || !strings.Contains(err.Error(), "unable to index file") || strings.Contains(err.Error(), "warning:") {
		t.Errorf("the add's failure is %v, want the fatal line without the warnings", err)
	}
}

// A clean filter that needs the user's locale (here: LC_ALL as the user has it, which
// is UTF-8) still runs under git add -A in the scratch commands.
func TestACleanFilterSeesTheUsersLocale(t *testing.T) {
	t.Setenv("LC_ALL", "C.UTF-8")
	repo, wt := radarRepo(t, "wa")
	dir := wt["wa"]
	write(t, dir, "filter.sh", "case \"$LC_ALL\" in *UTF-8*) cat;; *) echo \"locale is $LC_ALL\" >&2; exit 1;; esac\n")
	write(t, dir, ".gitattributes", "*.loc filter=loc\n")
	gitRun(t, dir, "config", "filter.loc.clean", "sh ./filter.sh")
	gitRun(t, dir, "config", "filter.loc.required", "true")
	write(t, dir, "x.loc", "content\n")
	editLine(t, dir, "a.txt", 4, "edited")
	snap, _, err := snapOf(t, repo, dir, true)
	if err != nil {
		t.Fatalf("the clean filter did not get the user's locale: %v", err)
	}
	found := false
	for _, p := range snap.Paths {
		found = found || p == "x.loc"
	}
	if !found {
		t.Errorf("x.loc is not in the paths: %v", snap.Paths)
	}
}

// A gitlink somebody staged in the checkout's own index and has not committed is a
// change of the checkout's, and is kept when the index is copied: only the repositories
// that git add itself found are taken out.
func TestAStagedRepositoryAbsentFromHeadIsKeptWhenTheIndexIsCopied(t *testing.T) {
	repo, wt := radarRepo(t, "wa")
	dir := wt["wa"]
	committedRepoAt(t, filepath.Join(dir, "sub"))
	gitRun(t, dir, "add", "sub")
	editLine(t, dir, "a.txt", 4, "edited")
	snap, s, err := snapOf(t, repo, dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if tree := snapTree(t, s, dir, snap); !strings.Contains(tree, "160000") || !strings.Contains(tree, "sub") {
		t.Errorf("the staged repository is not in the tree: %s", tree)
	}
}
