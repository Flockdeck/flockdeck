package radar

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jmwri/flockdeck/internal/gitx"
)

// TestMain keeps the tests that build real repositories away from the
// configuration of the person running them, as gitx's does: git is given an
// empty system configuration and a global one holding only an identity, and
// the home folder is a temporary one under every name git looks for it by.
func TestMain(m *testing.M) { os.Exit(runTests(m)) }

func runTests(m *testing.M) int {
	dir, err := os.MkdirTemp("", "radar-config-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "radar tests:", err)
		return 1
	}
	defer os.RemoveAll(dir)
	global := filepath.Join(dir, "gitconfig")
	config := "[user]\n\tname = Test\n\temail = test@example.com\n[commit]\n\tgpgsign = false\n"
	if err := os.WriteFile(global, []byte(config), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "radar tests:", err)
		return 1
	}
	os.Setenv("GIT_CONFIG_GLOBAL", global)
	os.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, name := range []string{"XDG_CONFIG_HOME", "HOME", "USERPROFILE"} {
		os.Setenv(name, dir)
	}
	for _, name := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_OBJECT_DIRECTORY", "GIT_COMMON_DIR"} {
		os.Unsetenv(name)
	}
	return m.Run()
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

func put(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Two worktrees edit neighbouring lines of one file, one committing and one
// not, and a third edits another file: the engine, with real snapshots and
// the real merge, shows the first two once they have conflicted twice, and
// merges only that pair.
func TestEngineAgainstARealRepository(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed")
	}
	if ok, why := gitx.CheckMergeTree(); !ok {
		t.Skip("merge-tree --write-tree is not available: " + why)
	}
	repo := t.TempDir()
	git(t, repo, "init", "-q", "--initial-branch=main")
	put(t, repo, "shared.txt", "1\n2\n3\n4\n5\n6\n7\n8\n")
	put(t, repo, "other.txt", "x\n")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "base")

	trees := map[string]string{}
	for _, name := range []string{"wa", "wb", "wc"} {
		dir := filepath.Join(t.TempDir(), name)
		if err := gitx.AddNewBranch(repo, dir, name); err != nil {
			t.Fatal(err)
		}
		trees[name] = dir
	}
	put(t, trees["wa"], "shared.txt", "1\n2\n3\n4\nA\n6\n7\n8\n")
	put(t, trees["wb"], "shared.txt", "1\n2\n3\n4\n5\nB\n7\n8\n")
	git(t, trees["wb"], "commit", "-q", "-a", "-m", "b")
	put(t, trees["wc"], "other.txt", "changed\n")

	common, err := gitx.CommonDir(repo)
	if err != nil {
		t.Fatal(err)
	}
	e := NewEngine()
	now := time.Unix(0, 0)
	e.now = func() time.Time { return now }

	var want []Conflict
	for cycle := 1; cycle <= 2; cycle++ {
		s, err := gitx.NewScratch(common)
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		base := gitx.BaseOf(ctx, repo)
		var in []Input
		for _, name := range []string{"wa", "wb", "wc"} {
			snap, err := gitx.Snapshot(ctx, trees[name], s, base, name != "wb")
			if err != nil {
				t.Fatal(err)
			}
			in = append(in, Input{ID: name, Ready: !snap.Empty, Commit: snap.Commit, Tree: snap.Tree, Paths: snap.Paths})
		}
		r := e.Update(ctx, repo, s, in)
		_ = s.Close()
		if r.Merges > 1 {
			t.Errorf("cycle %d ran %d merges, want at most the one pair that shares a file", cycle, r.Merges)
		}
		if cycle == 1 && len(r.Conflicts) != 0 {
			t.Errorf("the first sighting showed %v", r.Conflicts)
		}
		want = r.Conflicts
		now = now.Add(15 * time.Second)
	}
	if !reflect.DeepEqual(want, []Conflict{{A: "wa", B: "wb", Paths: []string{"shared.txt"}}}) {
		t.Errorf("conflicts = %+v, want wa and wb on shared.txt", want)
	}
}

// A pane that adds a file and one that adds a directory of the same name meet in
// the prefilter, and Predict names the path as it is in the repository, not as
// merge-tree spells it (foo~<commit>).
func TestPredictNamesADirectoryAgainstFileConflictByItsRealPath(t *testing.T) {
	if !gitx.Available() {
		t.Skip("git is not installed")
	}
	if ok, why := gitx.CheckMergeTree(); !ok {
		t.Skip(why)
	}
	repo := t.TempDir()
	git(t, repo, "init", "-q", "--initial-branch=main")
	put(t, repo, "keep.txt", "x\n")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "base")
	var dirs []string
	for _, name := range []string{"wa", "wb"} {
		dir := filepath.Join(t.TempDir(), name)
		if err := gitx.AddNewBranch(repo, dir, name); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dir)
	}
	put(t, dirs[0], "foo", "a file\n")
	if err := os.Mkdir(filepath.Join(dirs[1], "foo"), 0o700); err != nil {
		t.Fatal(err)
	}
	put(t, dirs[1], "foo/bar", "in a directory\n")

	common, _ := gitx.CommonDir(repo)
	s, err := gitx.NewScratch(common)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	base := gitx.BaseOf(ctx, repo)
	var in []Input
	for i, dir := range dirs {
		snap, err := gitx.Snapshot(ctx, dir, s, base, true)
		if err != nil {
			t.Fatal(err)
		}
		in = append(in, Input{ID: []string{"wa", "wb"}[i], Ready: true, Commit: snap.Commit, Tree: snap.Tree, Head: snap.Head, Paths: snap.Paths})
	}
	paths, clean, err := Predict(ctx, repo, s, in[0].Commit, in[1].Commit)
	if err != nil || clean {
		t.Fatalf("clean = %v, err = %v, want a conflict", clean, err)
	}
	if !reflect.DeepEqual(paths, []string{"foo"}) {
		t.Errorf("paths = %q, want [foo]", paths)
	}
	e := NewEngine()
	if r := e.Update(ctx, repo, s, in); r.Merges != 1 {
		t.Errorf("merges = %d, want the pair brought together by the file and the directory", r.Merges)
	}
}
