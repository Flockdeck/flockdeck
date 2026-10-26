package gitx

import (
	"context"
	"errors"
	"os"
	"os/exec"
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
func readEntryPoints(t *testing.T, dir, feat string, diffs ...string) {
	t.Helper()
	if len(diffs) == 0 {
		diffs = []string{"tracked.txt"}
	}
	ctx := context.Background()
	StatusOf(dir)
	_, _ = StatusWithin(dir, commandTimeout)
	_, _ = Changes(dir)
	for _, p := range diffs {
		_, _ = Diff(dir, p)
	}
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
		if st := StatusOf(dir); !st.Unreadable || st.Branch != "" {
			t.Errorf("%s: status = %+v, want an unreadable checkout and nothing else", name, st)
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
		if st.Unreadable || st.Branch != "main" || st.Dirty != 1 {
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

// A worktree status or diff of the superproject starts a status inside every
// initialised submodule, which reads that submodule's own configuration
// (.git/modules/<name>/config), and the check of the superproject's does not see it.
func TestASubmodulesOwnConfigIsNotRunByAReadOfTheRepositoryAroundIt(t *testing.T) {
	inner := newRepo(t)
	writeIn(t, inner, ".gitattributes", "* filter=evil\n")
	gitRun(t, inner, "add", "-A")
	gitRun(t, inner, "commit", "-q", "-m", "attributes")
	outer := newRepo(t)
	add := exec.Command("git", "-c", "protocol.file.allow=always", "submodule", "add", "--", filepath.ToSlash(inner), "mod")
	add.Dir = outer
	if out, err := add.CombinedOutput(); err != nil {
		t.Skipf("submodules are not usable here: %v: %s", err, out)
	}
	gitRun(t, outer, "commit", "-q", "-m", "add the submodule")
	gitRun(t, outer, "checkout", "-q", "-b", "feat")
	feat := commitIn(t, outer, "feat.txt", "feat\n", "feat")
	gitRun(t, outer, "checkout", "-q", "main")

	mod := filepath.Join(outer, "mod")
	m := marker(t)
	gitRun(t, mod, "config", "filter.evil.clean", "touch "+m+" && cat")
	writeIn(t, mod, "README.md", "hello and a good deal more\n")
	writeIn(t, outer, "tracked.txt", "x\n")

	if err := UnsafeReadConfig(context.Background(), outer); err != nil {
		t.Fatalf("the repository around it has nothing to refuse: %v", err)
	}
	readEntryPoints(t, outer, feat, "tracked.txt", "mod")
	if _, err := os.Stat(m); err == nil {
		t.Error("the submodule's clean filter ran")
	}
}

// A status that looks inside submodules goes on into theirs, so a submodule of a
// submodule is checked too (.git/modules/<a>/modules/<b>/config).
func TestANestedSubmodulesOwnConfigIsNotRun(t *testing.T) {
	leaf := newRepo(t)
	writeIn(t, leaf, ".gitattributes", "* filter=evil\n")
	gitRun(t, leaf, "add", "-A")
	gitRun(t, leaf, "commit", "-q", "-m", "attributes")
	middle := newRepo(t)
	file := []string{"-c", "protocol.file.allow=always"}
	if out, err := exec.Command("git", append(append([]string{"-C", middle}, file...), "submodule", "add", "--", filepath.ToSlash(leaf), "leaf")...).CombinedOutput(); err != nil {
		t.Skipf("submodules are not usable here: %v: %s", err, out)
	}
	gitRun(t, middle, "commit", "-q", "-m", "add the leaf")
	outer := newRepo(t)
	gitRun(t, outer, append(file, "submodule", "add", "-q", "--", filepath.ToSlash(middle), "mid")...)
	gitRun(t, outer, "-c", "protocol.file.allow=always", "submodule", "update", "--init", "--recursive", "-q")
	gitRun(t, outer, "commit", "-q", "-m", "add the middle")

	leafDir := filepath.Join(outer, "mid", "leaf")
	if _, err := os.Stat(filepath.Join(leafDir, ".git")); err != nil {
		t.Skipf("nested submodules are not usable here: %v", err)
	}
	m := marker(t)
	gitRun(t, leafDir, "config", "filter.evil.clean", "touch "+m+" && cat")
	writeIn(t, leafDir, "README.md", "hello and a good deal more\n")

	if submoduleWorkNames := submoduleWork(outer, "mid"); len(submoduleWorkNames) != 0 {
		t.Errorf("submoduleWork = %v, want nothing while a nested submodule's config names a program", submoduleWorkNames)
	}
	if !submoduleConfigUnsafe(outer, nil) {
		t.Error("the nested submodule's config was not found")
	}
	if _, err := os.Stat(m); err == nil {
		t.Error("the nested submodule's clean filter ran")
	}
	// And one that is fine is not refused.
	gitRun(t, leafDir, "config", "--unset", "filter.evil.clean")
	if submoduleConfigUnsafe(outer, nil) {
		t.Error("a nested submodule with nothing to run was refused")
	}
}

func TestACheckoutWhoseConfigCannotBeReadIsUnreadableNotEmpty(t *testing.T) {
	dir := newRepo(t)
	if err := os.WriteFile(filepath.Join(dir, ".git", "config"), []byte("[core\n\tbare = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var unread *ConfigUnreadableError
	if err := UnsafeReadConfig(context.Background(), dir); !errors.As(err, &unread) {
		t.Fatalf("UnsafeReadConfig = %v, want a ConfigUnreadableError", err)
	}
	if st := StatusOf(dir); !st.Unreadable {
		t.Errorf("status = %+v, want an unreadable checkout, not one with nothing changed", st)
	}
}
