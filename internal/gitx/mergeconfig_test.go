package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// conflictingRepo is a repository on main whose feat branch and main both edit
// README.md, with a .gitattributes that sends every file through the filter and the
// merge driver named evil. Neither is defined yet.
func conflictingRepo(t *testing.T) (dir, feat string) {
	t.Helper()
	dir = newRepo(t)
	writeIn(t, dir, ".gitattributes", "* merge=evil filter=evil\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "attributes")
	gitRun(t, dir, "checkout", "-q", "-b", "feat")
	// A pointer to an object that is not in this repository: Git LFS would fetch it
	// when the file is written.
	writeIn(t, dir, "obj.bin", "version https://git-lfs.github.com/spec/v1\noid sha256:"+strings.Repeat("a", 64)+"\nsize 12\n")
	feat = commitIn(t, dir, "README.md", "from feat\n", "feat")
	gitRun(t, dir, "checkout", "-q", "main")
	commitIn(t, dir, "README.md", "from main\n", "main")
	return dir, feat
}

func marker(t *testing.T) string {
	t.Helper()
	return filepath.ToSlash(filepath.Join(t.TempDir(), "ran"))
}

func TestARepositoryThatConfiguresAProgramIsNotMergedOrPredicted(t *testing.T) {
	for name, set := range map[string]func(t *testing.T, dir, m string){
		"merge driver": func(t *testing.T, dir, m string) {
			gitRun(t, dir, "config", "merge.evil.driver", "touch "+m)
		},
		"smudge filter": func(t *testing.T, dir, m string) {
			gitRun(t, dir, "config", "filter.evil.smudge", "touch "+m+" && cat")
		},
		"clean filter": func(t *testing.T, dir, m string) {
			gitRun(t, dir, "config", "filter.evil.clean", "touch "+m+" && cat")
		},
		"process filter": func(t *testing.T, dir, m string) {
			gitRun(t, dir, "config", "filter.evil.process", "touch "+m)
		},
		"signing program": func(t *testing.T, dir, m string) {
			gitRun(t, dir, "config", "commit.gpgsign", "true")
			gitRun(t, dir, "config", "gpg.program", "touch "+m)
		},
		"include": func(t *testing.T, dir, m string) {
			other := filepath.Join(t.TempDir(), "more.cfg")
			writeIn(t, filepath.Dir(other), "more.cfg", "[merge \"evil\"]\n\tdriver = touch "+m+"\n")
			gitRun(t, dir, "config", "include.path", filepath.ToSlash(other))
		},
		"lfs custom transfer agent": func(t *testing.T, dir, m string) {
			installLFSFilters(t, dir)
			gitRun(t, dir, "config", "lfs.customtransfer.evil.path", "touch "+m)
		},
		"lfs extension smudge": func(t *testing.T, dir, m string) {
			installLFSFilters(t, dir)
			gitRun(t, dir, "config", "lfs.extension.evil.smudge", "touch "+m)
		},
		"lfs extension clean": func(t *testing.T, dir, m string) {
			installLFSFilters(t, dir)
			gitRun(t, dir, "config", "lfs.extension.evil.clean", "touch "+m)
		},
		"an lfs key nobody listed": func(t *testing.T, dir, m string) {
			installLFSFilters(t, dir)
			gitRun(t, dir, "config", "lfs.somethingnew", "touch "+m)
		},
		"lfs access on something that is not a url": func(t *testing.T, dir, m string) {
			installLFSFilters(t, dir)
			gitRun(t, dir, "config", "lfs.extension.evil.access", "touch "+m)
			gitRun(t, dir, "config", "lfs.customtransfer.evil.locksverify", "touch "+m)
		},
		"ask pass": func(t *testing.T, dir, m string) {
			installLFSFilters(t, dir)
			gitRun(t, dir, "config", "core.askPass", "touch "+m)
		},
		"diff command": func(t *testing.T, dir, m string) {
			gitRun(t, dir, "config", "diff.evil.command", "touch "+m)
		},
		"lfs standalone transfer agent": func(t *testing.T, dir, m string) {
			installLFSFilters(t, dir)
			gitRun(t, dir, "config", "lfs.standalonetransferagent", "evil")
		},
		"credential helper": func(t *testing.T, dir, m string) {
			installLFSFilters(t, dir)
			gitRun(t, dir, "config", "credential.helper", "!touch "+m)
		},
		"ssh command": func(t *testing.T, dir, m string) {
			installLFSFilters(t, dir)
			gitRun(t, dir, "config", "core.sshCommand", "touch "+m)
		},
		"git proxy": func(t *testing.T, dir, m string) {
			installLFSFilters(t, dir)
			gitRun(t, dir, "config", "core.gitProxy", "touch "+m)
		},
		"diff textconv": func(t *testing.T, dir, m string) {
			gitRun(t, dir, "config", "diff.evil.textconv", "touch "+m)
		},
		"diff external": func(t *testing.T, dir, m string) {
			gitRun(t, dir, "config", "diff.external", "touch "+m)
		},
		"filter that only starts as LFS": func(t *testing.T, dir, m string) {
			gitRun(t, dir, "config", "filter.evil.smudge", "git-lfs smudge -- %f && touch "+m)
		},
	} {
		dir, feat := conflictingRepo(t)
		m := marker(t)
		set(t, dir, m)
		ctx := context.Background()

		var unsafe *UnsafeConfigError
		if err := UnsafeConfig(ctx, dir); !errors.As(err, &unsafe) {
			t.Errorf("%s: UnsafeConfig = %v", name, err)
		}
		if _, err := MergeInto(ctx, dir, "main", feat, "merge"); !errors.As(err, &unsafe) {
			t.Errorf("%s: MergeInto = %v", name, err)
		}
		if clean, known := MergesCleanly(ctx, dir, strings.TrimSpace(gitRun(t, dir, "rev-parse", "main")), feat); known || clean {
			t.Errorf("%s: MergesCleanly answered (%v, %v) instead of not knowing", name, clean, known)
		}
		if _, err := os.Stat(m); err == nil {
			t.Errorf("%s: the program ran", name)
		}
	}
}

// installLFSFilters sets the filters as Git LFS installs them.
func installLFSFilters(t *testing.T, dir string) {
	t.Helper()
	for k, v := range map[string]string{
		"filter.lfs.clean":   "git-lfs clean -- %f",
		"filter.lfs.smudge":  "git-lfs smudge -- %f",
		"filter.lfs.process": "git-lfs filter-process",
	} {
		gitRun(t, dir, "config", k, v)
	}
}

func TestTheDataKeysGitLFSWritesAreAllowed(t *testing.T) {
	dir := newRepo(t)
	installLFSFilters(t, dir)
	for k, v := range map[string]string{
		"lfs.repositoryformatversion": "0",
		"lfs.url":                     "https://example.com/x.git/info/lfs",
		"lfs.concurrenttransfers":     "3",
		"lfs.https://example.com/x.git/info/lfs.access":      "basic",
		"lfs.https://example.com/x.git/info/lfs.locksverify": "true",
	} {
		gitRun(t, dir, "config", k, v)
	}
	if err := UnsafeConfig(context.Background(), dir); err != nil {
		t.Errorf("data keys of Git LFS: %v", err)
	}
}

func TestLFSFiltersAndAnUnsignedMergeAreAllowed(t *testing.T) {
	dir := newRepo(t)
	installLFSFilters(t, dir)
	if err := UnsafeConfig(context.Background(), dir); err != nil {
		t.Fatalf("git-lfs filters: %v", err)
	}

	// commit.gpgsign alone is not a program, and the merge commit is made unsigned.
	gitRun(t, dir, "checkout", "-q", "-b", "feat")
	feat := commitIn(t, dir, "a.txt", "a\n", "a")
	gitRun(t, dir, "checkout", "-q", "main")
	commitIn(t, dir, "m.txt", "m\n", "m")
	gitRun(t, dir, "config", "commit.gpgsign", "true")
	tip, err := MergeInto(context.Background(), dir, "main", feat, "Merge feat")
	if err != nil || tip == feat {
		t.Fatalf("tip = %q, %v", tip, err)
	}
	if out := gitRun(t, dir, "cat-file", "-p", tip); strings.Contains(out, "gpgsig") {
		t.Errorf("the merge commit was signed:\n%s", out)
	}
}

func TestAFileGitIsToldNotToCompareStopsTheMerge(t *testing.T) {
	for name, flag := range map[string]string{"assume-unchanged": "--assume-unchanged", "skip-worktree": "--skip-worktree"} {
		dir := newRepo(t)
		gitRun(t, dir, "checkout", "-q", "-b", "feat")
		feat := commitIn(t, dir, "a.txt", "a\n", "a")
		gitRun(t, dir, "checkout", "-q", "main")
		gitRun(t, dir, "update-index", flag, "README.md")
		if _, err := MergeInto(context.Background(), dir, "main", feat, "m"); !errors.Is(err, ErrNotClean) {
			t.Errorf("%s: %v", name, err)
		}
		if got := strings.TrimSpace(gitRun(t, dir, "rev-parse", "HEAD")); got == feat {
			t.Errorf("%s: the merge was made", name)
		}
	}
}
