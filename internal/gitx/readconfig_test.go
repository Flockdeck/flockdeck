package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// editedRepo is a repository on main whose .gitattributes sends every file through
// the filter and diff driver named evil (neither is defined yet), with a branch feat
// to merge, and a tracked file edited in the working tree so that git has to compare
// it with the index.
func editedRepo(t *testing.T) (dir, feat string) {
	t.Helper()
	dir = newRepo(t)
	writeIn(t, dir, ".gitattributes", "* filter=evil diff=evil\n")
	writeIn(t, dir, "tracked.txt", "one\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "attributes")
	gitRun(t, dir, "checkout", "-q", "-b", "feat")
	feat = commitIn(t, dir, "feat.txt", "feat\n", "feat")
	gitRun(t, dir, "checkout", "-q", "main")
	// A different size and time, so that git cannot take the file as unchanged
	// without reading it.
	writeIn(t, dir, "tracked.txt", "one and a good deal more\n")
	return dir, feat
}

// readEntryPoints calls each way the program reads a checkout without being asked
// to change it. None of them may run a program the repository's configuration names.
func readEntryPoints(t *testing.T, dir, feat string) {
	t.Helper()
	ctx := context.Background()
	StatusOf(dir)
	_, _ = StatusWithin(dir, commandTimeout)
	_, _ = Changes(dir)
	_, _ = Diff(dir, "tracked.txt")
	_, _ = numstat(ctx, dir)
	_, _ = numstat(ctx, dir, "HEAD")
	common, err := CommonDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewScratch(common)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	_, _ = Snapshot(ctx, dir, s, BaseOf(ctx, dir), true)
	_, _ = MergeInto(ctx, dir, "main", feat, "merge")
}

func TestARepositoryThatConfiguresAProgramIsNotReadWithIt(t *testing.T) {
	for name, set := range map[string]func(t *testing.T, dir, m string){
		"clean filter": func(t *testing.T, dir, m string) {
			gitRun(t, dir, "config", "filter.evil.clean", "touch "+m+" && cat")
		},
		"process filter": func(t *testing.T, dir, m string) {
			gitRun(t, dir, "config", "filter.evil.process", "touch "+m)
		},
		"lfs extension clean": func(t *testing.T, dir, m string) {
			installLFSFilters(t, dir)
			gitRun(t, dir, "config", "lfs.extension.evil.clean", "touch "+m)
			gitRun(t, dir, "config", "lfs.extension.evil.priority", "0")
		},
		"an lfs key nobody listed": func(t *testing.T, dir, m string) {
			installLFSFilters(t, dir)
			gitRun(t, dir, "config", "lfs.somethingnew", "touch "+m)
		},
		"include": func(t *testing.T, dir, m string) {
			other := filepath.Join(t.TempDir(), "more.cfg")
			writeIn(t, filepath.Dir(other), "more.cfg", "[filter \"evil\"]\n\tclean = touch "+m+" && cat\n")
			gitRun(t, dir, "config", "include.path", filepath.ToSlash(other))
		},
		"filter that only starts as LFS": func(t *testing.T, dir, m string) {
			gitRun(t, dir, "config", "filter.evil.clean", "git-lfs clean -- %f && touch "+m)
		},
	} {
		dir, feat := editedRepo(t)
		m := marker(t)
		set(t, dir, m)

		var unsafe *UnsafeReadConfigError
		if err := UnsafeReadConfig(context.Background(), dir); !errors.As(err, &unsafe) {
			t.Errorf("%s: UnsafeReadConfig = %v", name, err)
		}
		readEntryPoints(t, dir, feat)
		if _, err := os.Stat(m); err == nil {
			t.Errorf("%s: the program ran", name)
		}
	}
}

// The standard filters of Git LFS, a program that is only a text conversion or a
// smudge filter, and a repository with none of these are read as before.
func TestARepositoryWithoutAProgramToRunOnAReadIsReadAsBefore(t *testing.T) {
	for name, set := range map[string]func(t *testing.T, dir, m string){
		"nothing": func(t *testing.T, dir, m string) {},
		"the standard filters of Git LFS and its data": func(t *testing.T, dir, m string) {
			installLFSFilters(t, dir)
			gitRun(t, dir, "config", "lfs.repositoryformatversion", "0")
			gitRun(t, dir, "config", "lfs.url", "https://example.com/x.git/info/lfs")
		},
		"a text conversion": func(t *testing.T, dir, m string) {
			gitRun(t, dir, "config", "diff.evil.textconv", "touch "+m+" && cat")
		},
		"an external diff": func(t *testing.T, dir, m string) {
			gitRun(t, dir, "config", "diff.external", "touch "+m)
		},
		"a smudge filter": func(t *testing.T, dir, m string) {
			gitRun(t, dir, "config", "filter.evil.smudge", "touch "+m+" && cat")
		},
	} {
		dir, _ := editedRepo(t)
		m := marker(t)
		set(t, dir, m)
		if err := UnsafeReadConfig(context.Background(), dir); err != nil {
			t.Errorf("%s: UnsafeReadConfig = %v", name, err)
		}
		st := StatusOf(dir)
		if st.Branch != "main" || st.Dirty != 1 {
			t.Errorf("%s: status = %+v, want main with one changed file", name, st)
		}
		if ch, err := Changes(dir); err != nil || len(ch) != 1 || ch[0].Path != "tracked.txt" {
			t.Errorf("%s: changes = %+v, %v", name, ch, err)
		}
		if d, err := Diff(dir, "tracked.txt"); err != nil || !strings.Contains(d, "a good deal more") {
			t.Errorf("%s: diff = %q, %v", name, d, err)
		}
		if _, err := os.Stat(m); err == nil {
			t.Errorf("%s: the program ran", name)
		}
	}
}

func TestTheKeyInAReadRefusalIsShownCleaned(t *testing.T) {
	text := (&UnsafeReadConfigError{Key: "filter.ev\x1b[31m\nil‮.clean"}).Error()
	for _, r := range text {
		if r < ' ' || r == 0x7f || r == '‮' {
			t.Fatalf("a control or override character reached the words: %q", text)
		}
	}
	if !strings.HasPrefix(text, "its config runs programs: filter.ev") {
		t.Errorf("text = %q", text)
	}
}
