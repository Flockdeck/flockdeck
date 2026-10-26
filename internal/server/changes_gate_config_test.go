package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmwri/flockdeck/internal/changes"
)

// A push reads every branch with no click, so a merge driver or a filter that the
// repository's own configuration names must not run for it either.
func TestAPushReadDoesNotRunAProgramTheRepositoryConfigures(t *testing.T) {
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
	commit := func(msg string) {
		gitCmd(t, dir, "add", "-A")
		gitCmd(t, dir, "commit", "-q", "-m", msg)
	}
	put(".gitattributes", "* merge=evil filter=evil\n")
	put("a.txt", "base\n")
	commit("base")
	gitCmd(t, dir, "checkout", "-q", "-b", "feat")
	put("a.txt", "from feat\n")
	commit("feat")
	gitCmd(t, dir, "checkout", "-q", "main")
	put("a.txt", "from main\n")
	commit("main")
	gitCmd(t, dir, "checkout", "-q", "feat")

	m := filepath.ToSlash(filepath.Join(t.TempDir(), "ran"))
	gitCmd(t, dir, "config", "merge.evil.driver", "touch "+m)
	gitCmd(t, dir, "config", "filter.evil.smudge", "touch "+m+" && cat")

	b := changes.Read(context.Background(), "p1", dir, 0)
	if b.Error != "" {
		t.Fatalf("branch = %+v", b)
	}
	srv := &Server{}
	if f := srv.facts.Facts(context.Background(), b); f.Unmergeable {
		t.Errorf("a conflict was claimed although git was not asked: %+v", f)
	}
	if _, err := os.Stat(m); err == nil {
		t.Error("a program of the repository's configuration ran during a push read")
	}
}
